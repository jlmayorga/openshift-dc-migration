#!/usr/bin/env python3
"""Actual least-privilege, quota-admission and foreign-resource checks.

Creates namespace-scoped RBAC and short-lived service-account credentials.
Credentials are saved privately and never included in command evidence.
"""

import argparse
import base64
import json
import os
import pathlib
import re
import subprocess
import time

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
    p.add_argument("--cases", help="Comma-separated test names; omitted runs all")
    p.add_argument(
        "--expiry-wait-limit",
        type=int,
        default=720,
        help="Maximum seconds to wait for explicitly selected expired-credentials case",
    )
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

    def credential(role_rules, duration="15m"):
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
            + [
                "-n",
                live.ns,
                "create",
                "token",
                "restricted",
                "--duration=" + duration,
            ],
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

    def denied_hpa():
        live.args.kubeconfig = original
        health = live.setup("rbac-hpa", lambda s: None)
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
        source, hpa = live.get("dc"), live.get("hpa")
        allowed = rules()
        allowed[0]["verbs"] = ["get", "list", "update"]
        allowed[1]["verbs"] = ["get", "list", "create", "update"]
        allowed[-1]["verbs"] = ["get", "list", "create"]
        path = credential(allowed)
        plan = live.tool("plan", health=health)
        live.tool("stage", plan)
        live.args.kubeconfig = path
        report = live.tool("cutover", plan, expected=1)
        assert "denied delete horizontalpodautoscalers" in report.read_text()
        assert live.get("dc")["spec"] == source["spec"]
        assert live.get("hpa")["metadata"]["uid"] == hpa["metadata"]["uid"]
        assert live.get("deploy")["spec"]["replicas"] == 0
        live.args.kubeconfig = original
        return {
            "HPADeleteDenied": True,
            "HPAUnchanged": True,
            "sourceUnchanged": True,
            "destinationReplicas": 0,
        }

    def expired_credentials():
        live.args.kubeconfig = original
        health = live.setup("expired-credentials", lambda s: None)
        source = live.get("dc")
        path = credential(
            [
                {
                    "apiGroups": ["apps.openshift.io"],
                    "resources": ["deploymentconfigs"],
                    "verbs": ["list"],
                }
            ],
            duration="10m",
        )
        token = json.loads(pathlib.Path(path).read_text())["users"][0]["user"]["token"]
        payload = token.split(".")[1]
        claims = json.loads(
            base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4))
        )
        expires = float(claims["exp"])
        remaining = expires + 5 - time.time()
        if remaining <= 0 or remaining > args.expiry_wait_limit:
            raise RuntimeError(
                f"Issued token requires {remaining:.0f}s until expiry; "
                "outside --expiry-wait-limit. No expired-token result claimed."
            )
        live.args.kubeconfig = path
        before = live.tool("generate")
        assert json.loads(before.read_text())["totals"]["generated"] == 1
        # Wait for the server-issued signed token's real expiration. Do not
        # fake a token, revoke its account or change the test host's clock.
        (live.root / "credential-expiry-wait.json").write_text(
            json.dumps({"issuedAt": claims["iat"], "expiresAt": expires}, indent=2)
        )
        print(f"Waiting for actual signed-token expiry ({remaining:.0f}s)", flush=True)
        while time.time() < expires + 5:
            time.sleep(min(30, max(0.1, expires + 5 - time.time())))
        denied = live.tool("generate", expected=1)
        report = json.loads(denied.read_text())
        assert report["totals"]["failed"] >= 1
        assert report["totals"]["generated"] == 0
        diagnostic = denied.read_text().lower()
        assert "unauthorized" in diagnostic or "credentials" in diagnostic
        live.args.kubeconfig = original
        assert live.get("dc")["metadata"]["uid"] == source["metadata"]["uid"]
        assert live.get("dc")["spec"] == source["spec"]
        assert not live.oc(
            "get", "deploy", "web", "--ignore-not-found", "-o", "name"
        ).stdout.strip()
        response = live.command(
            ["curl", "-fsS", "--retry", "5", "--max-time", "10", health]
        ).stdout
        assert "qualified-expired-credentials" in response
        return {
            "validBeforeExpiry": True,
            "actualSignedTokenExpiration": expires,
            "observedAfterExpiry": time.time(),
            "expiredCredentialsProduceFailedReportAndExit": True,
            "sourceUIDSpecAndHTTPUnchanged": True,
            "destinationPersisted": False,
        }

    for name, fn in [
        ("restricted-rbac", rbac),
        ("quota-admission", admission),
        ("foreign-destination", foreign),
        ("denied-hpa-mutation", denied_hpa),
        ("expired-credentials", expired_credentials),
    ]:
        if name == "expired-credentials" and not args.cases:
            continue
        if args.cases and name not in args.cases.split(","):
            continue
        live.run_case(name, fn)
        live.args.kubeconfig = original
    return 0 if live.cases and all(c["result"] == "passed" for c in live.cases) else 1


if __name__ == "__main__":
    raise SystemExit(main())
