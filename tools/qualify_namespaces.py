#!/usr/bin/env python3
"""Actual multi-namespace batches and isolated migration of real new-app DCs.

Creates six labelled test namespaces, namespace-scoped RBAC and a private,
short-lived credential. No storage, nodes or cluster RBAC are provisioned.
"""

import argparse
import datetime
import json
import os
import pathlib
import re
import subprocess

from qualify_new_app import NewApp
from release_gate import dependency_digest, digest, fixture_digest


class Namespaces(NewApp):
    def select(self, key):
        self.ns = self.namespaces[key]

    def snapshot_all(self, label):
        previous = self.ns
        for key in self.namespaces:
            self.select(key)
            self.snapshot(label + "-" + key)
        self.ns = previous

    def sources_unchanged(self, keys=None):
        for key in keys or self.originals:
            self.select(key)
            after = self.get("dc")
            assert after["metadata"]["uid"] == self.originals[key]["metadata"]["uid"]
            assert after["spec"] == self.originals[key]["spec"], key

    def batch(self, mode, projects, expected=0, kubeconfig=None):
        out = self.root / f"{self.case}-{mode}-{self.sequence:04}"
        self.command(
            [
                self.args.binary,
                "--kubeconfig",
                kubeconfig or self.args.kubeconfig,
                "--context",
                self.args.context,
                "--mode",
                mode,
                "--projects",
                ",".join(projects),
                "--output-dir",
                str(out),
                "--json-report",
                str(out / "report.json"),
                "--report-path",
                "",
                "--operation-timeout",
                str(self.args.timeout + 60) + "s",
            ],
            expected=expected,
        )
        self.snapshot_all(mode)
        return out, json.loads((out / "report.json").read_text())

    def run_case(self, name, fn):
        self.case = name
        row = {
            "case": name,
            "result": "failed",
            "startedAt": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "namespaces": list(self.namespaces.values()),
        }
        try:
            row["observations"] = fn()
            row["result"] = "passed"
        except Exception as exc:
            row["error"] = str(exc)
            self.snapshot_all("failure")
        self.cases.append(row)
        (self.root / "cases.json").write_text(json.dumps(self.cases, indent=2))
        print(name, row["result"], row.get("error", ""), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for key in (
        "context",
        "kubeconfig",
        "binary",
        "image",
        "namespace-prefix",
        "output",
    ):
        parser.add_argument("--" + key, required=True)
    parser.add_argument("--timeout", type=int, default=120)
    parser.add_argument("--allow-test-mutations", action="store_true")
    args = parser.parse_args()
    if (
        not args.allow_test_mutations
        or not args.context.strip()
        or not re.fullmatch(r"dcmig-[a-z0-9-]{1,35}", args.namespace_prefix)
    ):
        parser.error("Explicit disposable target and mutation acknowledgement required")
    if "@sha256:" not in args.image or args.timeout <= 0:
        parser.error(
            "An immutable approved HTTP image and positive timeout are required"
        )
    args.binary = str(pathlib.Path(args.binary).resolve())
    args.kubeconfig = str(pathlib.Path(args.kubeconfig).resolve())
    live = Namespaces(args)
    live.namespaces, live.originals, health, bodies = {}, {}, {}, {}
    metadata = {
        "kind": "actual-openshift-multiple-namespaces",
        "testedAt": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "artifactSHA256": digest(args.binary),
        "revision": subprocess.check_output(
            ["git", "rev-parse", "HEAD"], text=True
        ).strip(),
        "fixtureSHA256": fixture_digest(),
        "dependencySHA256": dependency_digest(),
        "clusterVersion": live.get("clusterversion", "version"),
        "kubernetesVersion": json.loads(live.oc("get", "--raw=/version").stdout),
    }
    (live.root / "metadata.json").write_text(json.dumps(metadata, indent=2))
    for key, replicas in [("a", 1), ("b", 2), ("c", 0), ("sentinel", 1), ("manual", 1)]:
        body = f"namespace-{key}-{live.run}"
        live.app(key, options=("--env=RESPONSE=" + body,))
        live.namespaces[key] = live.ns
        if key == "manual":
            live.patch(lambda spec: spec.update(test=True), ready=False)
        elif replicas != 1:
            live.patch(lambda spec, n=replicas: spec.update(replicas=n))
        if key != "manual" and replicas:
            health[key] = live.route()
            live.http(health[key], body)
        live.originals[key] = live.get("dc")
        bodies[key] = body
    live.namespace("empty")
    live.namespaces["empty"] = live.ns
    (live.root / "namespaces.json").write_text(json.dumps(live.namespaces, indent=2))
    selected = [live.namespaces[k] for k in ("a", "b", "c")]
    batch_plans = {}

    def generation():
        projects = selected + [live.namespaces["empty"], selected[0], "kube-system"]
        out, report = live.batch("generate", projects)
        assert len(report["results"]) == 5
        assert report["totals"]["generated"] == 3
        assert report["totals"]["empty"] == report["totals"]["skipped"] == 1
        assert report["totals"]["failed"] == 0
        for key in ("a", "b", "c"):
            ns = live.namespaces[key]
            plan = json.loads((out / ns / "web.plan.json").read_text())
            assert (
                plan["source"]["metadata"]["uid"]
                == live.originals[key]["metadata"]["uid"]
            )
            assert plan["desired"]["metadata"]["namespace"] == ns
            assert bodies[key] in json.dumps(plan["desired"])
        assert not (out / live.namespaces["sentinel"]).exists()
        live.sources_unchanged()
        return {
            "singleInvocation": True,
            "duplicateProcessedOnce": True,
            "sameResourceName": "web",
            "namespacedArtifactDirectories": selected,
            "emptyNamespaces": 1,
            "reservedSkipped": 1,
            "totals": report["totals"],
        }

    def planning():
        out, report = live.batch("plan", selected + [live.namespaces["empty"]])
        assert report["totals"]["generated"] == 3 and report["totals"]["empty"] == 1
        identities = []
        for key in ("a", "b", "c"):
            ns = live.namespaces[key]
            path = out / ns / "web.plan.json"
            plan = json.loads(path.read_text())
            assert not plan.get("blockers") and plan["inventoryComplete"]
            assert (
                plan["source"]["metadata"]["namespace"]
                == plan["desired"]["metadata"]["namespace"]
                == ns
            )
            identities.append(plan["id"])
            batch_plans[key] = path
        assert len(set(identities)) == 3
        live.sources_unchanged()
        return {
            "singleInvocation": True,
            "independentMigrationIDs": identities,
            "planFiles": [str(p.relative_to(live.root)) for p in batch_plans.values()],
            "totals": report["totals"],
        }

    def dry_runs():
        assert len(batch_plans) == 3
        for key, plan in batch_plans.items():
            live.select(key)
            live.tool("dry-run", plan)
            assert not live.oc(
                "get", "deployment", "web", "--ignore-not-found", "-o", "name"
            ).stdout.strip()
        live.sources_unchanged()
        return {
            "allThreeBatchPlansServerValidated": True,
            "destinationsPersisted": 0,
            "sourceSpecsUnchanged": True,
        }

    def partial_manual():
        projects = [
            selected[0],
            live.namespaces["manual"],
            live.namespaces["empty"],
            selected[1],
            selected[2],
        ]
        totals = {}
        for mode in ("generate", "plan"):
            out, report = live.batch(mode, projects, expected=1)
            assert len(report["results"]) == 5
            by_ns = {r["source"]["namespace"]: r for r in report["results"]}
            assert by_ns[live.namespaces["manual"]]["outcome"] == "manual"
            assert by_ns[live.namespaces["empty"]]["outcome"] == "empty"
            assert all(by_ns[ns]["outcome"] == "generated" for ns in selected)
            assert (
                report["totals"]["manual"] == 1 and report["totals"]["generated"] == 4
            )
            assert all((out / ns / "web.yaml").is_file() for ns in selected)
            totals[mode] = report["totals"]
        live.sources_unchanged()
        return {
            "failureDoesNotStopLaterNamespaces": True,
            "aggregateExitNonzero": True,
            "manualSourceUnchanged": True,
            "totals": totals,
        }

    def restricted_batch():
        live.select("a")
        live.create("ServiceAccount", "v1", None, name="batch-reader")
        subject_ns = live.ns
        for key in ("a", "c"):
            live.select(key)
            live.create(
                "Role",
                "rbac.authorization.k8s.io/v1",
                None,
                name="batch-reader",
                rules=[
                    {
                        "apiGroups": ["apps.openshift.io"],
                        "resources": ["deploymentconfigs"],
                        "verbs": ["list"],
                    }
                ],
            )
            live.create(
                "RoleBinding",
                "rbac.authorization.k8s.io/v1",
                None,
                name="batch-reader",
                roleRef={
                    "apiGroup": "rbac.authorization.k8s.io",
                    "kind": "Role",
                    "name": "batch-reader",
                },
                subjects=[
                    {
                        "kind": "ServiceAccount",
                        "name": "batch-reader",
                        "namespace": subject_ns,
                    }
                ],
            )
        # Token-bearing output must never enter command evidence.
        token = subprocess.check_output(
            live.base
            + ["-n", subject_ns, "create", "token", "batch-reader", "--duration=15m"],
            text=True,
        ).strip()
        config = json.loads(
            subprocess.check_output(
                live.base
                + ["config", "view", "--minify", "--flatten", "--raw", "-o", "json"],
                text=True,
            )
        )
        config["users"] = [{"name": "batch-reader", "user": {"token": token}}]
        config["contexts"][0]["context"]["user"] = "batch-reader"
        credentials = live.root / "batch-reader-credentials.json"
        with open(
            credentials, "x", opener=lambda path, flags: os.open(path, flags, 0o600)
        ) as f:
            json.dump(config, f)
        _, report = live.batch(
            "generate", selected, expected=1, kubeconfig=str(credentials)
        )
        by_ns = {r["source"]["namespace"]: r for r in report["results"]}
        assert (
            by_ns[selected[0]]["outcome"]
            == by_ns[selected[2]]["outcome"]
            == "generated"
        )
        denied = by_ns[selected[1]]
        assert denied["outcome"] == "failed" and "denied" in denied["error"].lower()
        assert report["totals"]["generated"] == 2 and report["totals"]["failed"] == 1
        live.sources_unchanged()
        return {
            "allowedNamespaces": [selected[0], selected[2]],
            "deniedNamespace": selected[1],
            "laterAllowedNamespaceProcessed": True,
            "totals": report["totals"],
        }

    def isolation():
        plans, destination_uids, migration_ids = {}, [], []
        # HTTP health URLs are per application. Review a separate health-bound
        # plan for each, after proving the shared multi-project planning path.
        for key in ("a", "b", "c"):
            live.select(key)
            plans[key] = live.tool("plan", health=health.get(key))
            live.tool("stage", plans[key])
            live.tool("stage", plans[key])
            target = live.get("deployment")
            assert target["spec"]["replicas"] == 0 and target["spec"]["paused"]
            destination_uids.append(target["metadata"]["uid"])
            migration_ids.append(json.loads(plans[key].read_text())["id"])
        assert len(set(destination_uids)) == len(set(migration_ids)) == 3
        active = set()

        def verify():
            for key in ("a", "b", "c"):
                live.select(key)
                source, target = live.get("dc"), live.get("deployment")
                assert (
                    source["metadata"]["uid"] == live.originals[key]["metadata"]["uid"]
                )
                desired = live.originals[key]["spec"]["replicas"]
                if key in active:
                    assert source["spec"]["replicas"] == 0 and not source["spec"].get(
                        "triggers"
                    )
                    assert target["spec"]["replicas"] == desired
                else:
                    assert source["spec"] == live.originals[key]["spec"]
                    assert target["spec"]["replicas"] == 0 and target["spec"]["paused"]
                if desired:
                    live.http(health[key], bodies[key])
            live.sources_unchanged(["manual", "sentinel"])

        for key in ("a", "b", "c"):
            live.select(key)
            live.tool("cutover", plans[key])
            live.tool("cutover", plans[key])
            active.add(key)
            verify()
        for key in ("b", "c", "a"):
            live.select(key)
            live.tool("rollback", plans[key])
            live.tool("rollback", plans[key])
            active.remove(key)
            verify()
        live.snapshot_all("final-recovered")
        return {
            "sameNamedApplicationsMigrated": 3,
            "replicas": [1, 2, 0],
            "destinationUIDs": destination_uids,
            "migrationIDs": migration_ids,
            "cutoverOrder": ["a", "b", "c"],
            "recoveryOrder": ["b", "c", "a"],
            "otherNamespaceStateVerifiedAfterEveryStep": True,
            "distinctHTTPResponsesVerified": True,
            "sentinelAndManualSourcesUnchanged": True,
        }

    for name, fn in [
        ("batch-generation", generation),
        ("batch-planning", planning),
        ("batch-plan-dry-runs", dry_runs),
        ("batch-manual-partial-failure", partial_manual),
        ("batch-rbac-partial-failure", restricted_batch),
        ("cutover-recovery-namespace-isolation", isolation),
    ]:
        live.run_case(name, fn)
    return int(any(c["result"] != "passed" for c in live.cases))


if __name__ == "__main__":
    raise SystemExit(main())
