#!/usr/bin/env python3
"""Live ANP/BANP selector refusal before source retirement in one new-app.

Creates policies scoped to the new labelled test namespace, removes each by
verified ownership/UID, then verifies ordinary migration and recovery. It does
not edit any existing cluster policy, SCC, node or storage resource.
"""

import argparse
import datetime
import json
import pathlib
import re
import subprocess
import uuid

from qualify_new_app import NewApp
from release_gate import dependency_digest, digest, fixture_digest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in (
        "context",
        "kubeconfig",
        "binary",
        "image",
        "namespace-prefix",
        "output",
    ):
        parser.add_argument("--" + name, required=True)
    parser.add_argument(
        "--previous-binary", help="Known pre-fix binary; read-only preflight only"
    )
    parser.add_argument("--timeout", type=int, default=180)
    parser.add_argument("--allow-test-mutations", action="store_true")
    parser.add_argument("--allow-cluster-policy-fixture", action="store_true")
    args = parser.parse_args()
    if (
        not args.context.strip()
        or not re.fullmatch(r"dcmig-[a-z0-9-]{1,34}", args.namespace_prefix)
        or "@sha256:" not in args.image
        or not args.allow_test_mutations
        or not args.allow_cluster_policy_fixture
    ):
        parser.error(
            "Explicit disposable target, immutable image, mutation and cluster-policy acknowledgements required"
        )
    for field in ("binary", "kubeconfig", "previous_binary"):
        if getattr(args, field):
            setattr(args, field, str(pathlib.Path(getattr(args, field)).resolve()))
    live = NewApp(args)
    metadata = {
        "kind": "actual-openshift-admin-policy",
        "testedAt": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "artifactSHA256": digest(args.binary),
        "previousArtifactSHA256": (
            digest(args.previous_binary) if args.previous_binary else None
        ),
        "revisionAtInvocation": subprocess.check_output(
            ["git", "rev-parse", "HEAD"], text=True
        ).strip(),
        "driverSHA256": digest(__file__),
        "fixtureSHA256": fixture_digest(),
        "dependencySHA256": dependency_digest(),
        "clusterVersion": live.get("clusterversion", "version"),
        "kubernetesVersion": json.loads(live.oc("get", "--raw=/version").stdout),
    }
    (live.root / "metadata.json").write_text(json.dumps(metadata, indent=2))
    existing = json.loads(
        live.command(live.base + ["get", "anp,banp", "-o", "json"]).stdout
    )["items"]
    assert not any(
        p["kind"] == "BaselineAdminNetworkPolicy" for p in existing
    ), "Existing BANP must not be replaced by a test"
    used = {
        p["spec"]["priority"] for p in existing if p["kind"] == "AdminNetworkPolicy"
    }
    priority = next(i for i in range(1000, -1, -1) if i not in used)

    def source_pod():
        pods = json.loads(
            live.oc("get", "pods", "-l", "deploymentconfig=web", "-o", "json").stdout
        )["items"]
        active = [
            p
            for p in pods
            if p.get("status", {}).get("phase") not in {"Succeeded", "Failed"}
        ]
        assert len(active) == 1
        assert any(
            c["type"] == "Ready" and c["status"] == "True"
            for c in active[0]["status"]["conditions"]
        )
        return active[0]["metadata"]["uid"]

    def check():
        live.app("admin-policy")
        marker = "admin-policy-" + uuid.uuid4().hex[:8]

        def configure(spec):
            spec["template"]["spec"]["terminationGracePeriodSeconds"] = 1
            container = spec["template"]["spec"]["containers"][0]
            container["resources"] = {"requests": {"cpu": "100m", "memory": "24Mi"}}
            container.setdefault("env", []).append(
                {"name": "RESPONSE", "value": marker}
            )

        live.patch(configure)
        live.oc("autoscale", "dc/web", "--min=1", "--max=1", "--cpu-percent=80")
        health = live.route()
        live.http(health, marker)
        source, hpa, pod_uid = live.get("dc"), live.get("hpa"), source_pod()
        plan = live.tool("plan", health=health)
        live.tool("stage", plan)
        target = live.get("deployment")
        namespace_selector = {
            "matchLabels": {"migration.openshift-dc.io/new-app-run": live.run}
        }
        observations = []
        for kind, resource in (
            ("AdminNetworkPolicy", "adminnetworkpolicy"),
            ("BaselineAdminNetworkPolicy", "baselineadminnetworkpolicy"),
        ):
            for position in ("subject", "peer", "controller-hash"):
                name = (
                    "dcmig-" + live.run if kind == "AdminNetworkPolicy" else "default"
                )
                selected_pods = {
                    "namespaceSelector": namespace_selector,
                    "podSelector": {
                        "matchExpressions": [
                            {
                                "key": {
                                    "subject": "migration.openshift-dc.io/id",
                                    "peer": "deployment",
                                    "controller-hash": "pod-template-hash",
                                }[position],
                                "operator": "Exists",
                            }
                        ]
                    },
                }
                spec = {
                    "subject": {"pods": selected_pods},
                    "ingress": [{"action": "Deny", "from": [{"namespaces": {}}]}],
                }
                if position == "peer":
                    # Allow-only peer rule does not isolate the healthy source.
                    spec = {
                        "subject": {"namespaces": namespace_selector},
                        "ingress": [
                            {"action": "Allow", "from": [{"pods": selected_pods}]}
                        ],
                    }
                if kind == "AdminNetworkPolicy":
                    spec["priority"] = priority
                body = {
                    "apiVersion": "policy.networking.k8s.io/v1alpha1",
                    "kind": kind,
                    "metadata": {
                        "name": name,
                        "labels": {
                            "migration.openshift-dc.io/admin-policy-run": live.run
                        },
                    },
                    "spec": spec,
                }
                created = json.loads(
                    live.command(
                        live.base + ["create", "-f", "-", "-o", "json"], body
                    ).stdout
                )
                uid = created["metadata"]["uid"]
                try:
                    live.snapshot(kind + "-" + position)
                    previous_accepted = None
                    if args.previous_binary:
                        current = live.args.binary
                        try:
                            live.args.binary = args.previous_binary
                            old = json.loads(live.tool("preflight", plan).read_text())[
                                "results"
                            ][0]
                            assert old["constraints"]["status"] == "passed"
                            previous_accepted = True
                        finally:
                            live.args.binary = current
                    refusals = []
                    for mode in ("preflight", "cutover"):
                        result = json.loads(
                            live.tool(mode, plan, expected=1).read_text()
                        )["results"][0]
                        last = result["constraints"]["checks"][-1]
                        assert (
                            last["name"] == "admin-network-policy-selection"
                            and last["status"] == "blocked"
                        )
                        assert kind in last["detail"] and name in last["detail"]
                        assert not result["applied"] and not result["ready"]
                        for workload, original in (
                            ("dc", source),
                            ("hpa", hpa),
                            ("deployment", target),
                        ):
                            now = live.get(workload)
                            assert (
                                now["metadata"]["uid"] == original["metadata"]["uid"]
                                and now["spec"] == original["spec"]
                            )
                        assert source_pod() == pod_uid
                        assert (
                            json.loads(plan.read_text())["progress"]["phase"]
                            == "staged"
                        )
                        live.http(health, marker)
                        refusals.append({"mode": mode, "blockedCheck": last})
                    observations.append(
                        {
                            "kind": kind,
                            "position": position,
                            "policyUID": uid,
                            "previousReadOnlyPreflightAccepted": previous_accepted,
                            "refusals": refusals,
                        }
                    )
                finally:
                    current = json.loads(
                        live.command(
                            live.base + ["get", resource, name, "-o", "json"]
                        ).stdout
                    )
                    assert current["metadata"]["uid"] == uid
                    assert (
                        current["metadata"]["labels"][
                            "migration.openshift-dc.io/admin-policy-run"
                        ]
                        == live.run
                    )
                    live.command(live.base + ["delete", resource, name, "--wait=true"])
        after = json.loads(
            live.command(live.base + ["get", "anp,banp", "-o", "json"]).stdout
        )["items"]
        assert {p["metadata"]["uid"] for p in after} == {
            p["metadata"]["uid"] for p in existing
        }
        assert (
            json.loads(live.tool("preflight", plan).read_text())["results"][0][
                "constraints"
            ]["status"]
            == "passed"
        )
        live.tool("cutover", plan)
        live.http(health, marker)
        live.tool("rollback", plan)
        assert live.get("dc")["spec"] == source["spec"]
        assert live.get("hpa")["spec"] == hpa["spec"]
        assert live.get("deployment")["spec"]["replicas"] == 0
        live.http(health, marker)
        live.tool("rollback", plan)
        return {
            "sourceUID": source["metadata"]["uid"],
            "originalPodUID": pod_uid,
            "policyVariants": observations,
            "allTestPoliciesRemoved": True,
            "successfulCutoverAndRecoveryAfterRemoval": True,
            "marker": marker,
        }

    live.run_case("admin-policy-selector-refusal-and-recovery", check)
    return int(any(c["result"] != "passed" for c in live.cases))


if __name__ == "__main__":
    raise SystemExit(main())
