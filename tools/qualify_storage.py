#!/usr/bin/env python3
"""Live API refusal tests for removed/disabled volume plugins.

Render actual oc new-app objects, set replicas to zero before creation, and add
one legacy inline volume. No application Pod, PVC, PV or driver is provisioned. This tests
classification and staging refusal, not whether a legacy plugin can mount.
"""

import argparse
import copy
import datetime
import json
import pathlib
import re
import subprocess

from qualify_new_app import NewApp
from release_gate import dependency_digest, digest, tree_digest


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
        or not re.fullmatch(r"dcmig-[a-z0-9-]{1,34}", args.namespace_prefix)
        or "@sha256:" not in args.image
    ):
        parser.error(
            "Explicit disposable target, immutable image and acknowledgement required"
        )
    args.binary = str(pathlib.Path(args.binary).resolve())
    args.kubeconfig = str(pathlib.Path(args.kubeconfig).resolve())
    live = NewApp(args)
    (live.root / "metadata.json").write_text(
        json.dumps(
            {
                "kind": "actual-openshift-legacy-volume-refusal",
                "testedAt": datetime.datetime.now(datetime.timezone.utc).isoformat(),
                "artifactSHA256": digest(args.binary),
                "revisionAtInvocation": subprocess.check_output(
                    ["git", "rev-parse", "HEAD"], text=True
                ).strip(),
                "fixtureSHA256": tree_digest("testdata"),
                "dependencySHA256": dependency_digest(),
                "clusterVersion": live.get("clusterversion", "version"),
                "kubernetesVersion": json.loads(
                    live.oc("get", "--raw=/version").stdout
                ),
                "limitation": "Zero-replica API cases; no claim of legacy plugin mountability.",
            },
            indent=2,
        )
    )
    sources = {
        "cephfs": {"monitors": ["192.0.2.1:6789"], "path": "/data"},
        "rbd": {"monitors": ["192.0.2.1:6789"], "image": "fixture-only"},
        "gitRepo": {"repository": "https://example.test/repo.git", "revision": "main"},
    }

    def no_application_pods():
        pods = json.loads(live.oc("get", "pods", "-o", "json").stdout)["items"]
        # A zero-replica DC still launches and retains its deployer. It must
        # finish, must not be an application Pod, and must not mount test data.
        return all(
            pod.get("status", {}).get("phase") == "Succeeded"
            and pod["metadata"]
            .get("labels", {})
            .get("openshift.io/deployer-pod-for.name")
            and not any(
                plugin in volume
                for volume in pod["spec"].get("volumes", [])
                for plugin in sources
            )
            for pod in pods
        )

    for plugin, configuration in sources.items():

        def check(plugin=plugin, configuration=configuration):
            live.namespace(plugin.lower())
            rendered = json.loads(
                live.oc(
                    "new-app",
                    "--as-deployment-config",
                    "--name=web",
                    "--image=" + args.image,
                    "--dry-run",
                    "-o",
                    "json",
                ).stdout
            )
            dc = next(o for o in rendered["items"] if o["kind"] == "DeploymentConfig")
            dc["spec"]["replicas"] = 0
            volume = {"name": "legacy", plugin: copy.deepcopy(configuration)}
            dc["spec"]["template"]["spec"]["volumes"] = [volume]
            live.oc("create", "-f", "-", body=rendered)
            live.settled()
            source = live.get("dc")
            assert source["spec"]["replicas"] == 0
            live.wait(no_application_pods)
            # Compare with the admitted source, including server defaults such
            # as RBD pool/user/keyring, rather than the pre-admission request.
            volumes = source["spec"]["template"]["spec"]["volumes"]
            assert len(volumes) == 1 and all(
                volumes[0][plugin][key] == value for key, value in configuration.items()
            )
            outputs = []
            for mode in ("generate", "plan"):
                output = live.tool(mode, expected=1)
                report = (
                    output
                    if mode == "generate"
                    else output.parent.parent / "report.json"
                )
                result = json.loads(report.read_text())["results"][0]
                assert result["outcome"] == "manual" and result["generated"]
                assert plugin in " ".join(result["unsupported"])
                plan = (
                    output
                    if mode == "plan"
                    else output.parent / live.ns / "web.plan.json"
                )
                data = json.loads(plan.read_text())
                assert data["desired"]["spec"]["paused"]
                assert data["desired"]["spec"]["template"]["spec"]["volumes"] == volumes
                outputs.append({"mode": mode, "diagnostic": result["error"]})
            stage = json.loads(live.tool("stage", plan, expected=1).read_text())
            assert plugin in stage["results"][0]["error"]
            assert live.get("dc")["spec"] == source["spec"]
            assert live.get("dc")["metadata"]["uid"] == source["metadata"]["uid"]
            assert not json.loads(live.oc("get", "deployment", "-o", "json").stdout)[
                "items"
            ]
            assert no_application_pods()
            return {
                "plugin": plugin,
                "sourceUID": source["metadata"]["uid"],
                "sourceReplicas": 0,
                "sourceUnchanged": True,
                "pausedReviewPreservesVolume": True,
                "generationAndPlan": outputs,
                "stageRefusedWithoutDestination": True,
                "applicationPodsCreated": 0,
                "completedDeployerPodsAllowed": True,
                "admittedSourceVolumes": volumes,
            }

        live.run_case(plugin, check)
    raise SystemExit(any(c["result"] != "passed" for c in live.cases))


if __name__ == "__main__":
    main()
