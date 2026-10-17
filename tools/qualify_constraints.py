#!/usr/bin/env python3
"""Actual pre-retirement constraints: quota, limits, placement and SCC failures.

Creates labelled disposable namespaces and one optional ClusterResourceQuota
whose selector matches only this run. All refused cutovers must retain a
healthy source and the original HPA, with a paused zero-replica destination.
"""

import argparse
import datetime
import json
import pathlib
import re
import subprocess

from qualify_new_app import NewApp
from release_gate import dependency_digest, digest, fixture_digest


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
    parser.add_argument("--cases", required=True)
    parser.add_argument("--timeout", type=int, default=120)
    parser.add_argument("--allow-test-mutations", action="store_true")
    args = parser.parse_args()
    known = {
        "positive",
        "pod-quota",
        "aggregate-quota",
        "replicaset-quota",
        "limit-range",
        "node-selector",
        "node-capacity",
        "network-policy",
        "runtime-policy",
        "pdb",
        "pdb-overlap",
        "service-account",
        "scc",
        "shared-cluster-quota",
        "changed-after-preflight",
        "zero",
    }
    selected = sorted(known) if args.cases == "all" else args.cases.split(",")
    if not selected or any(case not in known for case in selected):
        parser.error("Unknown or empty case selection")
    if (
        not args.allow_test_mutations
        or not args.context.strip()
        or not re.fullmatch(r"dcmig-[a-z0-9-]{1,34}", args.namespace_prefix)
    ):
        parser.error("Explicit disposable target and mutation acknowledgement required")
    if "@sha256:" not in args.image:
        parser.error("Use an approved immutable HTTP image")
    args.binary, args.kubeconfig = str(pathlib.Path(args.binary).resolve()), str(
        pathlib.Path(args.kubeconfig).resolve()
    )
    live = NewApp(args)
    (live.root / "metadata.json").write_text(
        json.dumps(
            {
                "kind": "actual-openshift-constraint-preflight",
                "testedAt": datetime.datetime.now(datetime.timezone.utc).isoformat(),
                "artifactSHA256": digest(args.binary),
                "revisionAtInvocation": subprocess.check_output(
                    ["git", "rev-parse", "HEAD"], text=True
                ).strip(),
                "fixtureSHA256": fixture_digest(),
                "dependencySHA256": dependency_digest(),
                "clusterVersion": live.get("clusterversion", "version"),
                "kubernetesVersion": json.loads(
                    live.oc("get", "--raw=/version").stdout
                ),
            },
            indent=2,
        )
    )

    def setup(case, replicas=1, resources=False):
        live.app(case)
        if replicas != 1 or resources:

            def patch(spec):
                spec["replicas"] = replicas
                if resources:
                    spec["template"]["spec"]["containers"][0]["resources"] = {
                        "requests": {"cpu": "100m"},
                        "limits": {"cpu": "100m"},
                    }

            live.patch(patch)
        health = live.route() if replicas else None
        if health:
            live.http(health)
        if replicas:
            live.create(
                "HorizontalPodAutoscaler",
                "autoscaling/v2",
                {
                    "scaleTargetRef": {
                        "apiVersion": "apps.openshift.io/v1",
                        "kind": "DeploymentConfig",
                        "name": "web",
                    },
                    "minReplicas": replicas,
                    "maxReplicas": replicas,
                    "metrics": [
                        {
                            "type": "Resource",
                            "resource": {
                                "name": "cpu",
                                "target": {
                                    "type": "Utilization",
                                    "averageUtilization": 80,
                                },
                            },
                        }
                    ],
                },
            )
        live.settled()
        source = live.get("dc")
        plan = live.tool("plan", health=health)
        live.tool("stage", plan)
        hpa = live.get("hpa") if replicas else None
        return source, plan, health, hpa

    def quota(hard):
        live.create("ResourceQuota", "v1", {"hard": hard}, name="constraints")

        def synced():
            q = live.get("resourcequota", "constraints")
            return q.get("status", {}).get("hard") == hard and all(
                k in q.get("status", {}).get("used", {}) for k in hard
            )

        live.wait(synced)

    def refusal(source, plan, health, hpa, expected_check=None):
        observations = []
        for mode in ("preflight", "cutover"):
            path = live.tool(mode, plan, expected=1)
            report = json.loads(path.read_text())
            item = report["results"][0]
            assert not item["ready"] and not item["applied"]
            assessment = item["constraints"]
            assert assessment["status"] == "blocked"
            blocked = [c for c in assessment["checks"] if c["status"] == "blocked"]
            assert blocked and (
                expected_check is None or blocked[0]["name"] == expected_check
            ), blocked
            observations.append(
                {"mode": mode, "blockedCheck": blocked[0], "totals": report["totals"]}
            )
            after = live.get("dc")
            assert (
                after["metadata"]["uid"] == source["metadata"]["uid"]
                and after["spec"] == source["spec"]
            )
            assert (
                after.get("status", {}).get("availableReplicas", 0)
                >= source["spec"]["replicas"]
            )
            target = live.get("deployment")
            assert target["spec"]["replicas"] == 0 and target["spec"]["paused"]
            current = live.get("hpa")
            assert (
                current["metadata"]["uid"] == hpa["metadata"]["uid"]
                and current["spec"] == hpa["spec"]
            )
            assert json.loads(plan.read_text())["progress"]["phase"] == "staged"
            live.http(health)
        live.snapshot("source-still-healthy")
        return {
            "sourceSpecAndAvailabilityUnchanged": True,
            "hpaUIDAndSpecUnchanged": True,
            "destinationPausedAtZero": True,
            "journalRemainsStaged": True,
            "assessments": observations,
        }

    def ordinary(case):
        replicas = 2 if case == "aggregate-quota" else 1
        source, plan, health, hpa = setup(case, replicas, case == "limit-range")
        expected = None
        if case in {"pod-quota", "aggregate-quota", "changed-after-preflight"}:
            if case == "changed-after-preflight":
                report = json.loads(live.tool("preflight", plan).read_text())
                assert report["results"][0]["constraints"]["status"] == "passed"
            quota({"pods": "3" if replicas == 2 else "1"})
            expected = (
                "namespace-and-cluster-quotas"
                if replicas == 2
                else "pod-admission-and-limits"
            )
        elif case == "replicaset-quota":
            quota({"count/replicasets.apps": "0"})
            expected = "replicaset-admission"
        elif case == "limit-range":
            live.create(
                "LimitRange",
                "v1",
                {
                    "limits": [
                        {
                            "type": "Container",
                            "min": {"cpu": "200m"},
                            "defaultRequest": {"cpu": "200m"},
                            "default": {"cpu": "500m"},
                        }
                    ]
                },
            )
            expected = "pod-admission-and-limits"
        elif case == "node-capacity":
            live.create(
                "LimitRange",
                "v1",
                {
                    "limits": [
                        {
                            "type": "Container",
                            "defaultRequest": {"memory": "1000Ti"},
                            "default": {"memory": "1000Ti"},
                        }
                    ]
                },
            )
            expected = "cluster-scheduling"
        elif case == "pdb":
            live.create(
                "PodDisruptionBudget",
                "policy/v1",
                {
                    "minAvailable": 1,
                    "selector": {"matchLabels": {"deploymentconfig": "web"}},
                },
            )
            expected = "availability-budget"
        elif case == "runtime-policy":
            pods = json.loads(
                live.oc(
                    "get", "pods", "-l", "deploymentconfig=web", "-o", "json"
                ).stdout
            )["items"]
            runtime_label = next(
                p["metadata"]["labels"]["deployment"]
                for p in pods
                if p.get("status", {}).get("phase") == "Running"
            )
            live.create(
                "NetworkPolicy",
                "networking.k8s.io/v1",
                {
                    "podSelector": {"matchLabels": {"deployment": runtime_label}},
                    "policyTypes": ["Ingress"],
                    "ingress": [{}],
                },
                name="source-rc",
            )
            live.create(
                "NetworkPolicy",
                "networking.k8s.io/v1",
                {"podSelector": {}, "policyTypes": ["Ingress"], "ingress": []},
                name="deny-all",
            )
            live.http(health)
            expected = "network-policy-selection"
        elif case == "network-policy":
            migration_id = json.loads(plan.read_text())["id"]
            live.create(
                "NetworkPolicy",
                "networking.k8s.io/v1",
                {
                    "podSelector": {
                        "matchLabels": {"migration.openshift-dc.io/id": migration_id}
                    },
                    "policyTypes": ["Ingress"],
                    "ingress": [],
                },
            )
            expected = "network-policy-selection"
        elif case == "node-selector":
            live.command(
                live.base
                + [
                    "annotate",
                    "namespace",
                    live.ns,
                    "openshift.io/node-selector=dcmig-impossible=" + live.run,
                ]
            )
            expected = "cluster-scheduling"
        elif case == "service-account":
            live.create("ServiceAccount", "v1", None, name="workload")
            # This changes only the template's SA; make a new settled plan.
            live.oc("delete", "deployment", "web")
            live.patch(
                lambda spec: spec["template"]["spec"].update(
                    serviceAccountName="workload"
                )
            )
            source = live.get("dc")
            plan = live.tool("plan", health=health)
            live.tool("stage", plan)
            live.oc("delete", "serviceaccount", "workload")
        return refusal(source, plan, health, hpa, expected)

    def positive(case="positive"):
        source, plan, health, hpa = setup(case, 0 if case == "zero" else 1)
        if case == "zero":
            quota({"pods": "0"})
        report = json.loads(live.tool("preflight", plan).read_text())
        assert report["results"][0]["constraints"]["status"] == "passed"
        assert live.get("dc")["spec"] == source["spec"]
        if hpa:
            assert live.get("hpa")["metadata"]["uid"] == hpa["metadata"]["uid"]
        live.tool("cutover", plan)
        live.tool("cutover", plan)
        assert (
            json.loads(plan.read_text())["progress"]["constraints"]["status"]
            == "passed"
        )
        live.tool("rollback", plan)
        assert live.get("dc")["spec"] == source["spec"]
        if health:
            live.http(health)
        return {
            "preflightPassedWithoutSourceMutation": True,
            "freshAssessmentAtCutover": True,
            "cutoverAndRecovery": "verified",
            "replicas": source["spec"]["replicas"],
        }

    def scc():
        live.app("scc")
        live.create("ServiceAccount", "v1", None, name="workload")
        live.create(
            "RoleBinding",
            "rbac.authorization.k8s.io/v1",
            None,
            name="temporary-scc",
            roleRef={
                "apiGroup": "rbac.authorization.k8s.io",
                "kind": "ClusterRole",
                "name": "system:openshift:scc:anyuid",
            },
            subjects=[
                {"kind": "ServiceAccount", "name": "workload", "namespace": live.ns}
            ],
        )

        def root_template(spec):
            spec["template"]["spec"]["serviceAccountName"] = "workload"
            spec["template"]["spec"]["containers"][0]["securityContext"] = {
                "runAsUser": 0
            }

        live.patch(root_template)
        health = live.route()
        live.http(health)
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
            },
        )
        source = live.get("dc")
        hpa = live.get("hpa")
        plan = live.tool("plan", health=health)
        live.tool("stage", plan)
        live.oc("delete", "rolebinding", "temporary-scc")
        # Existing source stays healthy; new Pods no longer have a usable SCC.
        return refusal(source, plan, health, hpa, "service-account-scc")

    def pdb_overlap():
        live.app("pdb-overlap")
        health = live.route()
        live.http(health)
        live.create(
            "PodDisruptionBudget",
            "policy/v1",
            {
                "minAvailable": 1,
                "selector": {"matchLabels": {"deploymentconfig": "web"}},
            },
        )
        live.wait(
            lambda: live.get("pdb").get("status", {}).get("observedGeneration")
            == live.get("pdb")["metadata"]["generation"]
        )
        original = live.get("dc")
        plan = live.tool(
            "plan",
            health=health,
            extra=(
                "--cutover-mode",
                "overlap",
                "--overlap-justification",
                "Stateless lab HTTP allows parallel replicas to preserve minAvailable=1",
            ),
        )
        live.tool("stage", plan)
        report = json.loads(live.tool("preflight", plan).read_text())
        assert report["results"][0]["constraints"]["status"] == "passed"
        live.tool("cutover", plan)
        live.tool("cutover", plan)
        live.http(health)
        assert (
            live.get("dc")["spec"]["replicas"] == 0
            and live.get("deployment")["status"]["availableReplicas"] >= 1
        )
        return {
            "sourceUID": original["metadata"]["uid"],
            "absoluteBudgetPreservedByOverlap": True,
            "destinationActiveAndHealthy": True,
            "retainedAt": "completed cutover; no automatic rollback that would interrupt this availability budget",
        }

    def shared():
        source, plan, health, hpa = setup("shared-cluster-quota", 2)
        ns = live.ns
        other = ns + "-other"
        selector = "migration.openshift-dc.io/constraint-scope"
        for namespace in [ns, other]:
            if namespace == other:
                live.command(live.base + ["create", "namespace", namespace])
                live.command(
                    live.base
                    + [
                        "label",
                        "namespace",
                        namespace,
                        "migration.openshift-dc.io/test-namespace=true",
                    ]
                )
            live.command(
                live.base + ["label", "namespace", namespace, selector + "=" + live.run]
            )
        live.command(
            live.base
            + [
                "-n",
                other,
                "new-app",
                "--as-deployment-config",
                "--name=web",
                "--image=" + args.image,
            ]
        )
        live.command(
            live.base + ["-n", other, "rollout", "status", "dc/web", "--timeout=120s"]
        )
        name = "dcmig-constraints-" + live.run
        obj = {
            "apiVersion": "quota.openshift.io/v1",
            "kind": "ClusterResourceQuota",
            "metadata": {
                "name": name,
                "labels": {"migration.openshift-dc.io/constraint-run": live.run},
            },
            "spec": {
                "selector": {"labels": {"matchLabels": {selector: live.run}}},
                "quota": {"hard": {"pods": "4"}},
            },
        }
        live.command(live.base + ["create", "-f", "-"], obj)

        def synced():
            p = live.oc(
                "get", "appliedclusterresourcequota", name, "-o", "json", expected=None
            )
            if p.returncode:
                return False
            q = json.loads(p.stdout).get("status", {}).get("total", {})
            return (
                q.get("hard", {}).get("pods") == "4"
                and int(q.get("used", {}).get("pods", "0")) == 3
            )

        live.wait(synced)
        result = refusal(source, plan, health, hpa, "namespace-and-cluster-quotas")
        live.command(live.base + ["delete", "clusterresourcequota", name])
        result.update(sharedQuotaNamespaces=[ns, other], clusterQuotaRemoved=True)
        return result

    for name in selected:
        if name in {"positive", "zero"}:
            fn = lambda name=name: positive(name)
        elif name == "pdb-overlap":
            fn = pdb_overlap
        elif name == "scc":
            fn = scc
        elif name == "shared-cluster-quota":
            fn = shared
        else:
            fn = lambda name=name: ordinary(name)
        live.run_case(name, fn)
    return int(any(c["result"] != "passed" for c in live.cases))


if __name__ == "__main__":
    raise SystemExit(main())
