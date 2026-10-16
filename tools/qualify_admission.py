#!/usr/bin/env python3
"""Live required-SCC authorization, refusal and recovery for a real new-app.

Uses one fresh labelled namespace and a service account with namespace-only
permission for the existing nonroot-v2 SCC. Revokes that grant after staging,
verifies a healthy source survives refused cutover, restores the grant, then
verifies successful migration/recovery. No SCC or storage is provisioned.
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
        "--previous-binary",
        help="Optional known pre-fix binary; only its read-only preflight is exercised",
    )
    parser.add_argument("--timeout", type=int, default=180)
    parser.add_argument("--allow-test-mutations", action="store_true")
    parser.add_argument("--allow-scc-binding", action="store_true")
    args = parser.parse_args()
    if (
        not args.context.strip()
        or not re.fullmatch(r"dcmig-[a-z0-9-]{1,34}", args.namespace_prefix)
        or not args.allow_test_mutations
        or not args.allow_scc_binding
        or "@sha256:" not in args.image
    ):
        parser.error(
            "Explicit disposable target, immutable image, mutation and SCC-binding acknowledgements required"
        )
    args.binary = str(pathlib.Path(args.binary).resolve())
    args.kubeconfig = str(pathlib.Path(args.kubeconfig).resolve())
    if args.previous_binary:
        args.previous_binary = str(pathlib.Path(args.previous_binary).resolve())
    live = NewApp(args)
    scc_name, sa_name, role_name = (
        "nonroot-v2",
        "required-scc-worker",
        "required-scc-use",
    )
    scc = live.get("scc", scc_name)
    assert scc["runAsUser"]["type"] == "MustRunAsNonRoot"
    assert all(
        not scc.get(flag, False)
        for flag in (
            "allowPrivilegedContainer",
            "allowHostNetwork",
            "allowHostPID",
            "allowHostIPC",
            "allowHostDirVolumePlugin",
        )
    )
    metadata = {
        "kind": "actual-openshift-required-scc",
        "testedAt": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "artifactSHA256": digest(args.binary),
        "previousArtifactSHA256": (
            digest(args.previous_binary) if args.previous_binary else None
        ),
        "revisionAtInvocation": subprocess.check_output(
            ["git", "rev-parse", "HEAD"], text=True
        ).strip(),
        "fixtureSHA256": tree_digest("testdata"),
        "dependencySHA256": dependency_digest(),
        "clusterVersion": live.get("clusterversion", "version"),
        "kubernetesVersion": json.loads(live.oc("get", "--raw=/version").stdout),
        "existingSCCUID": scc["metadata"]["uid"],
        "existingSCC": scc_name,
    }
    (live.root / "metadata.json").write_text(json.dumps(metadata, indent=2))

    def authorization():
        review = {
            "apiVersion": "authorization.k8s.io/v1",
            "kind": "LocalSubjectAccessReview",
            "metadata": {"namespace": live.ns},
            "spec": {
                "user": f"system:serviceaccount:{live.ns}:{sa_name}",
                "groups": [
                    "system:serviceaccounts",
                    f"system:serviceaccounts:{live.ns}",
                    "system:authenticated",
                ],
                "resourceAttributes": {
                    "namespace": live.ns,
                    "group": "security.openshift.io",
                    "version": "v1",
                    "resource": "securitycontextconstraints",
                    "name": scc_name,
                    "verb": "use",
                },
            },
        }
        result = json.loads(
            live.oc("create", "-f", "-", "-o", "json", body=review).stdout
        )["status"]
        assert not result.get("evaluationError"), result
        return bool(result["allowed"]) and not result.get("denied", False)

    def check_pod_scc():
        pods = json.loads(
            live.oc("get", "pods", "-l", "deploymentconfig=web", "-o", "json").stdout
        )["items"]
        active = [
            p
            for p in pods
            if p.get("status", {}).get("phase") not in {"Succeeded", "Failed"}
        ]
        assert len(active) == 1
        assert (
            active[0]["metadata"].get("annotations", {}).get("openshift.io/scc")
            == scc_name
        )
        assert active[0]["spec"]["serviceAccountName"] == sa_name
        return active[0]["metadata"]["uid"]

    def check():
        live.app("required-scc")
        subject = f"system:serviceaccount:{live.ns}:{sa_name}"
        subject_groups = {
            "system:serviceaccounts",
            f"system:serviceaccounts:{live.ns}",
            "system:authenticated",
        }
        assert subject not in scc.get("users", [])
        assert not subject_groups.intersection(
            scc.get("groups", [])
        ), "Fixture SCC has a direct grant that revocation of test RBAC cannot remove"
        namespace = live.get("namespace", live.ns)
        uid = int(
            namespace["metadata"]["annotations"]["openshift.io/sa.scc.uid-range"].split(
                "/"
            )[0]
        )
        assert uid > 0
        live.create("ServiceAccount", "v1", None, name=sa_name)
        live.create(
            "Role",
            "rbac.authorization.k8s.io/v1",
            None,
            name=role_name,
            rules=[
                {
                    "apiGroups": ["security.openshift.io"],
                    "resources": ["securitycontextconstraints"],
                    "resourceNames": [scc_name],
                    "verbs": ["use"],
                }
            ],
        )
        binding = {
            "apiVersion": "rbac.authorization.k8s.io/v1",
            "kind": "RoleBinding",
            "metadata": {
                "name": role_name,
                "namespace": live.ns,
                "labels": {"migration.openshift-dc.io/configuration-run": live.run},
            },
            "roleRef": {
                "apiGroup": "rbac.authorization.k8s.io",
                "kind": "Role",
                "name": role_name,
            },
            "subjects": [
                {"kind": "ServiceAccount", "name": sa_name, "namespace": live.ns}
            ],
        }
        live.oc("create", "-f", "-", body=binding)
        live.wait(authorization)
        marker = "required-scc-" + live.run

        def patch(spec):
            pod = spec["template"]["spec"]
            pod["serviceAccountName"] = sa_name
            pod["securityContext"] = {"runAsUser": uid}
            pod["containers"][0]["resources"] = {
                "requests": {"cpu": "100m"},
                "limits": {"cpu": "100m"},
            }
            pod["containers"][0].setdefault("env", []).append(
                {"name": "RESPONSE", "value": marker}
            )
            spec["template"]["metadata"].setdefault("annotations", {})[
                "openshift.io/required-scc"
            ] = scc_name

        live.patch(patch)
        health = live.route()
        live.http(health, marker)
        original_pod = check_pod_scc()
        live.create(
            "HorizontalPodAutoscaler",
            "autoscaling/v2",
            {
                "scaleTargetRef": {
                    "apiVersion": "apps.openshift.io/v1",
                    "kind": "DeploymentConfig",
                    "name": "web",
                },
                "minReplicas": 1,
                "maxReplicas": 1,
                "metrics": [
                    {
                        "type": "Resource",
                        "resource": {
                            "name": "cpu",
                            "target": {"type": "Utilization", "averageUtilization": 80},
                        },
                    }
                ],
            },
        )
        live.settled()
        source, hpa = live.get("dc"), live.get("hpa")
        plan = live.tool("plan", health=health)
        live.tool("stage", plan)
        destination = live.get("deployment")
        created_binding = live.get("rolebinding", role_name)
        assert (
            created_binding["metadata"]["labels"][
                "migration.openshift-dc.io/configuration-run"
            ]
            == live.run
        )
        live.oc("delete", "rolebinding", role_name)
        live.wait(lambda: not authorization())
        template = copy.deepcopy(source["spec"]["template"])
        review = {
            "apiVersion": "security.openshift.io/v1",
            "kind": "PodSecurityPolicySubjectReview",
            "metadata": {"namespace": live.ns},
            "spec": {
                "template": template,
                "user": f"system:serviceaccount:{live.ns}:{sa_name}",
                "groups": [
                    "system:serviceaccounts",
                    f"system:serviceaccounts:{live.ns}",
                    "system:authenticated",
                ],
            },
        }
        selected = json.loads(
            live.oc("create", "-f", "-", "-o", "json", body=review).stdout
        )["status"]["allowedBy"]["name"]
        assert (
            selected != scc_name
        ), "Fixture must exercise a mismatched subject-review SCC"
        old_pass = None
        if args.previous_binary:
            current = live.args.binary
            try:
                live.args.binary = args.previous_binary
                previous = json.loads(live.tool("preflight", plan).read_text())[
                    "results"
                ][0]
                assert previous["constraints"]["status"] == "passed"
                old_pass = True
            finally:
                live.args.binary = current
        refusals = []
        for mode in ("preflight", "cutover"):
            item = json.loads(live.tool(mode, plan, expected=1).read_text())["results"][
                0
            ]
            blocked = item["constraints"]["checks"][-1]
            assert (
                blocked["name"] == "service-account-scc"
                and blocked["status"] == "blocked"
            )
            assert scc_name in blocked["detail"] and sa_name in blocked["detail"]
            assert not item["applied"] and not item["ready"]
            now, now_hpa, now_destination = (
                live.get("dc"),
                live.get("hpa"),
                live.get("deployment"),
            )
            assert (
                now["metadata"]["uid"] == source["metadata"]["uid"]
                and now["spec"] == source["spec"]
            )
            assert (
                now_hpa["metadata"]["uid"] == hpa["metadata"]["uid"]
                and now_hpa["spec"] == hpa["spec"]
            )
            assert (
                now_destination["metadata"]["uid"] == destination["metadata"]["uid"]
                and now_destination["spec"] == destination["spec"]
            )
            assert json.loads(plan.read_text())["progress"]["phase"] == "staged"
            assert check_pod_scc() == original_pod
            live.http(health, marker)
            refusals.append({"mode": mode, "blockedCheck": blocked})
        live.oc("create", "-f", "-", body=binding)
        live.wait(authorization)
        passed = json.loads(live.tool("preflight", plan).read_text())["results"][0]
        assert passed["constraints"]["status"] == "passed"
        live.tool("cutover", plan)
        live.http(health, marker)
        destination_pod = check_pod_scc()
        assert destination_pod != original_pod
        live.tool("rollback", plan)
        live.tool("rollback", plan)
        assert (
            live.get("dc")["spec"] == source["spec"]
            and live.get("hpa")["spec"] == hpa["spec"]
        )
        restored_pod = check_pod_scc()
        live.http(health, marker)
        target = live.get("deployment")
        assert target["spec"]["replicas"] == 0 and target["spec"]["paused"]
        assert (
            live.get("scc", scc_name)["metadata"]["uid"] == metadata["existingSCCUID"]
        )
        live.snapshot("restored")
        return {
            "requiredSCC": scc_name,
            "subjectReviewSelectedAfterRevocation": selected,
            "serviceAccount": sa_name,
            "sourceUID": source["metadata"]["uid"],
            "originalPodUID": original_pod,
            "previousBinaryPreflightAcceptedWithoutSCCUse": old_pass,
            "refusals": refusals,
            "grantRestoredPreflight": "passed",
            "destinationPodUID": destination_pod,
            "restoredPodUID": restored_pod,
            "sourceSpecHTTPAndHPASpecRecovered": True,
            "destinationPausedAtZero": True,
            "namespaceOnlySCCBindingRetained": True,
            "newSCCOrClusterRoleOrStorage": False,
            "marker": marker,
        }

    live.run_case("required-scc-authorization", check)
    raise SystemExit(any(c["result"] != "passed" for c in live.cases))


if __name__ == "__main__":
    main()
