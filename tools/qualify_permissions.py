#!/usr/bin/env python3
"""Actual least-privilege, quota-admission and foreign-resource checks.

Creates namespace-scoped RBAC and short-lived service-account credentials.
Credentials are saved privately and never included in command evidence.
"""

import argparse
import json
import os
import pathlib
import re
import subprocess

from qualify_configurations import Live


def main():
    p = argparse.ArgumentParser(description=__doc__)
    for key in (
        "context",
        "kubeconfig",
        "binary",
        "image",
        "namespace-prefix",
        "output",
    ):
        p.add_argument("--" + key, required=True)
    p.add_argument("--allow-test-mutations", action="store_true")
    p.add_argument("--timeout", type=int, default=90)
    args = p.parse_args()
    if not args.allow_test_mutations or not re.fullmatch(
        r"dcmig-[a-z0-9-]+", args.namespace_prefix
    ):
        p.error("Explicit disposable target and mutation acknowledgement required")
    args.binary = str(pathlib.Path(args.binary).resolve())
    args.kubeconfig = str(pathlib.Path(args.kubeconfig).resolve())
    original = args.kubeconfig
    live = Live(args)

    def rules(read_only=False):
        return [
            {
                "apiGroups": ["apps.openshift.io"],
                "resources": ["deploymentconfigs"],
                "verbs": ["get", "list"],
            },
            {
                "apiGroups": ["apps"],
                "resources": [
                    "deployments",
                    "replicasets",
                    "statefulsets",
                    "daemonsets",
                ],
                "verbs": ["get", "list"],
            },
            {
                "apiGroups": [""],
                "resources": [
                    "services",
                    "replicationcontrollers",
                    "pods",
                    "endpoints",
                ],
                "verbs": ["get", "list"],
            },
            {"apiGroups": ["batch"], "resources": ["jobs"], "verbs": ["get", "list"]},
            {
                "apiGroups": ["autoscaling"],
                "resources": ["horizontalpodautoscalers"],
                "verbs": ["get", "list"],
            },
        ]

    def credential(role_rules):
        live.create("ServiceAccount", "v1", None, name="restricted")
        live.create("Role", "rbac.authorization.k8s.io/v1", None, rules=role_rules)
        live.create(
            "RoleBinding",
            "rbac.authorization.k8s.io/v1",
            None,
            roleRef={
                "apiGroup": "rbac.authorization.k8s.io",
                "kind": "Role",
                "name": "web",
            },
            subjects=[
                {"kind": "ServiceAccount", "name": "restricted", "namespace": live.ns}
            ],
        )
        # Never pass token-bearing stdout through the command-evidence logger.
        token = subprocess.check_output(
            live.base
            + ["-n", live.ns, "create", "token", "restricted", "--duration=15m"],
            text=True,
        ).strip()
        source = json.loads(
            subprocess.check_output(
                live.base
                + ["config", "view", "--minify", "--flatten", "--raw", "-o", "json"],
                text=True,
            )
        )
        source["users"] = [{"name": "restricted", "user": {"token": token}}]
        source["contexts"][0]["context"]["user"] = "restricted"
        source["contexts"][0]["context"]["namespace"] = live.ns
        target = live.root / (live.case + "-credentials.json")
        target.write_text(json.dumps(source))
        os.chmod(target, 0o600)
        return str(target)

    def rbac():
        health = live.setup("rbac", lambda s: None)
        source = live.get("dc")
        path = credential(
            [
                {
                    "apiGroups": ["apps.openshift.io"],
                    "resources": ["deploymentconfigs"],
                    "verbs": ["list"],
                }
            ]
        )
        live.args.kubeconfig = path
        live.command(
            [
                "oc",
                "--kubeconfig",
                path,
                "--context",
                args.context,
                "auth",
                "can-i",
                "get",
                "namespaces",
            ],
            expected=1,
        )
        report = live.tool("generate")
        assert json.loads(report.read_text())["totals"]["generated"] == 1
        live.oc("patch", "role", "web", "--type=merge", "-p", json.dumps({"rules": []}))
        denied = live.tool("generate", expected=1)
        assert "denied" in denied.read_text().lower()
        live.oc(
            "patch", "role", "web", "--type=merge", "-p", json.dumps({"rules": rules()})
        )
        live.args.kubeconfig = original
        plan = live.tool("plan", health=health)
        live.args.kubeconfig = path
        denied = live.tool("stage", plan, expected=1)
        assert "denied" in denied.read_text().lower()
        assert not live.oc(
            "get", "deploy", "web", "--ignore-not-found", "-o", "name"
        ).stdout.strip()
        live.args.kubeconfig = original
        live.tool("stage", plan)
        live.args.kubeconfig = path
        denied = live.tool("cutover", plan, expected=1)
        assert "denied" in denied.read_text().lower()
        assert live.get("dc")["spec"] == source["spec"]
        assert live.get("deploy")["spec"]["replicas"] == 0
        live.args.kubeconfig = original
        return {
            "generationWithoutNamespaceRead": "passed",
            "deniedDCList": "visible failure",
            "deniedDeploymentCreate": "no persistence",
            "deniedCutoverUpdate": "source unchanged",
        }

    def admission():
        live.args.kubeconfig = original
        health = live.setup("quota-admission", lambda s: None)
        source = live.get("dc")
        live.create("ResourceQuota", "v1", {"hard": {"count/deployments.apps": "0"}})
        plan = live.tool("plan", health=health)
        live.tool("dry-run", plan, expected=1)
        report = live.tool("stage", plan, expected=1)
        assert "quota" in report.read_text().lower()
        assert not live.oc(
            "get", "deploy", "web", "--ignore-not-found", "-o", "name"
        ).stdout.strip()
        assert live.get("dc")["spec"] == source["spec"]
        return {
            "dryRunQuotaDenied": True,
            "stageQuotaDenied": True,
            "destinationPersisted": False,
            "sourceUnchanged": True,
        }

    def foreign():
        live.args.kubeconfig = original
        health = live.setup("foreign", lambda s: None)
        live.create(
            "Deployment",
            "apps/v1",
            {
                "replicas": 0,
                "paused": True,
                "selector": {"matchLabels": {"unrelated": "true"}},
                "template": {
                    "metadata": {"labels": {"unrelated": "true"}},
                    "spec": {"containers": [{"name": "other", "image": args.image}]},
                },
            },
        )
        before = live.get("deploy")
        live.tool("plan", health=health, expected=1)
        assert live.get("deploy")["metadata"]["uid"] == before["metadata"]["uid"]
        assert live.get("deploy")["spec"] == before["spec"]
        return {"foreignDeploymentUID": before["metadata"]["uid"], "overwritten": False}

    for name, fn in [
        ("restricted-rbac", rbac),
        ("quota-admission", admission),
        ("foreign-destination", foreign),
    ]:
        live.run_case(name, fn)
        live.args.kubeconfig = original
    return 0 if live.cases and all(c["result"] == "passed" for c in live.cases) else 1


if __name__ == "__main__":
    raise SystemExit(main())
