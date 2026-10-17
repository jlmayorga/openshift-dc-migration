#!/usr/bin/env python3
"""Verify raw Deployment fields cannot disappear before migration preflight.

Creates one real oc new-app in a fresh labelled disposable namespace. Adds a
post-baseline SELinux field only to its paused zero-replica destination, proves
refused operations preserve the healthy source, then recovers while retaining
the unfamiliar destination field. No SCC or storage is provisioned.
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
        "--previous-binary", help="Optional pre-fix binary; read-only preflight only"
    )
    parser.add_argument("--timeout", type=int, default=180)
    parser.add_argument("--allow-test-mutations", action="store_true")
    args = parser.parse_args()
    if (
        not args.context.strip()
        or not re.fullmatch(r"dcmig-[a-z0-9-]{1,34}", args.namespace_prefix)
        or "@sha256:" not in args.image
        or not args.allow_test_mutations
    ):
        parser.error(
            "Explicit disposable target, immutable image and acknowledgement required"
        )
    for field in ("binary", "kubeconfig", "previous_binary"):
        if getattr(args, field):
            setattr(args, field, str(pathlib.Path(getattr(args, field)).resolve()))
    live = NewApp(args)
    metadata = {
        "kind": "actual-openshift-raw-deployment-field",
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

    def active_source_pods():
        items = json.loads(
            live.oc("get", "pods", "-l", "deploymentconfig=web", "-o", "json").stdout
        )["items"]
        active = [
            p
            for p in items
            if p.get("status", {}).get("phase") not in {"Succeeded", "Failed"}
        ]
        assert len(active) == 1
        assert any(
            c["type"] == "Ready" and c["status"] == "True"
            for c in active[0]["status"]["conditions"]
        )
        return active[0]["metadata"]["uid"]

    def check():
        live.app("raw-field")
        marker = "raw-field-" + uuid.uuid4().hex[:8]

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
        source, hpa, pod_uid = live.get("dc"), live.get("hpa"), active_source_pods()
        plan = live.tool("plan", health=health)
        live.tool("stage", plan)
        live.oc(
            "patch",
            "deployment",
            "web",
            "--type=merge",
            "-p",
            json.dumps(
                {
                    "spec": {
                        "template": {
                            "spec": {
                                "securityContext": {"seLinuxChangePolicy": "Recursive"}
                            }
                        }
                    }
                }
            ),
        )
        destination = live.get("deployment")
        assert destination["spec"]["replicas"] == 0 and destination["spec"]["paused"]
        assert (
            destination["spec"]["template"]["spec"]["securityContext"][
                "seLinuxChangePolicy"
            ]
            == "Recursive"
        ), "Server did not retain the fixture field; this case cannot qualify"
        live.snapshot("post-baseline-field-present")
        previous_accepted = None
        if args.previous_binary:
            current = live.args.binary
            try:
                live.args.binary = args.previous_binary
                previous = json.loads(live.tool("preflight", plan).read_text())[
                    "results"
                ][0]
                assert previous["constraints"]["status"] == "passed"
                previous_accepted = True
            finally:
                live.args.binary = current
        refusals = []
        for mode in ("preflight", "cutover"):
            item = json.loads(live.tool(mode, plan, expected=1).read_text())["results"][
                0
            ]
            assert (
                "baseline" in item["error"] and "seLinuxChangePolicy" in item["error"]
            )
            assert not item["applied"] and not item["ready"]
            for kind, original in (
                ("dc", source),
                ("hpa", hpa),
                ("deployment", destination),
            ):
                now = live.get(kind)
                assert (
                    now["metadata"]["uid"] == original["metadata"]["uid"]
                    and now["spec"] == original["spec"]
                )
            assert active_source_pods() == pod_uid
            assert json.loads(plan.read_text())["progress"]["phase"] == "staged"
            live.http(health, marker)
            refusals.append({"mode": mode, "error": item["error"]})
        live.tool("rollback", plan)
        stopped = live.get("deployment")
        assert stopped["metadata"]["uid"] == destination["metadata"]["uid"]
        assert stopped["spec"] == destination["spec"]
        assert live.get("dc")["spec"] == source["spec"]
        assert live.get("hpa")["metadata"]["uid"] == hpa["metadata"]["uid"]
        assert live.get("hpa")["spec"] == hpa["spec"]
        assert active_source_pods() == pod_uid
        live.http(health, marker)
        live.tool("rollback", plan)
        live.snapshot("recovered-with-field-preserved")
        return {
            "sourceUID": source["metadata"]["uid"],
            "originalPodUID": pod_uid,
            "destinationUID": destination["metadata"]["uid"],
            "field": "spec.template.spec.securityContext.seLinuxChangePolicy",
            "value": "Recursive",
            "previousReadOnlyPreflightAccepted": previous_accepted,
            "refusals": refusals,
            "recoveredWithRawSpecPreserved": True,
            "sourcePodAndSpecHPAUIDAndSpecUnchanged": True,
            "marker": marker,
        }

    live.run_case("raw-deployment-field", check)
    return int(any(c["result"] != "passed" for c in live.cases))


if __name__ == "__main__":
    raise SystemExit(main())
