#!/usr/bin/env python3
"""Actual-cluster configuration matrix; creates isolated, labelled namespaces.

Requires explicit disposable-cluster authorization. No nodes, volumes or cloud
infrastructure are provisioned. Successful cases retain their DC and paused
Deployment for inspection. Raw evidence must remain private.
"""

import argparse
import copy
import datetime
import json
import pathlib
import re
import subprocess
import time
import uuid

from release_gate import dependency_digest, digest, fixture_digest


class Live:
    def __init__(self, args):
        self.args = args
        self.root = pathlib.Path(args.output).resolve()
        self.root.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.run = uuid.uuid4().hex[:8]
        self.sequence = 0
        self.ns = None
        self.case = None
        self.cases = []
        self.last_binary_exit = None
        self.base = ["oc", "--kubeconfig", args.kubeconfig, "--context", args.context]

    def command(self, argv, body=None, expected=0):
        started = datetime.datetime.now(datetime.timezone.utc).isoformat()
        p = subprocess.run(
            argv,
            input=json.dumps(body) if body else None,
            capture_output=True,
            text=True,
            timeout=self.args.timeout + 90,
        )
        self.sequence += 1
        if argv[0] == self.args.binary:
            self.last_binary_exit = p.returncode
        record = dict(
            startedAt=started,
            argv=argv,
            inputObject=body,
            exit=p.returncode,
            stdout=p.stdout,
            stderr=p.stderr,
        )
        (self.root / f"command-{self.sequence:04}.json").write_text(
            json.dumps(record, indent=2)
        )
        if expected is not None and (p.returncode == 0) != (expected == 0):
            raise RuntimeError(
                f"Unexpected exit {p.returncode}: {argv[-6:]}; command-{self.sequence:04}.json"
            )
        return p

    def oc(self, *words, body=None, expected=0):
        return self.command(
            self.base + (["-n", self.ns] if self.ns else []) + list(words),
            body,
            expected,
        )

    def get(self, kind, name="web"):
        return json.loads(self.oc("get", kind, name, "-o", "json").stdout)

    def create(self, kind, api, spec, name="web", **extra):
        body = dict(
            apiVersion=api,
            kind=kind,
            metadata=dict(
                name=name,
                namespace=self.ns,
                labels={"migration.openshift-dc.io/configuration-run": self.run},
            ),
            **extra,
        )
        if spec is not None:
            body["spec"] = spec
        self.oc("create", "-f", "-", body=body)

    def wait(self, check):
        deadline = time.monotonic() + self.args.timeout
        while time.monotonic() < deadline:
            if check():
                return
            time.sleep(1)
        raise RuntimeError("Live condition did not converge")

    def snapshot(self, label):
        p = self.oc(
            "get",
            "dc,deploy,rs,rc,pods,svc,endpoints,route,hpa,events",
            "-o",
            "json",
            expected=None,
        )
        (self.root / f"{self.case}-{label}.json").write_text(p.stdout)
        return json.loads(p.stdout) if p.returncode == 0 else {}

    def tool(self, mode, plan=None, health=None, expected=0, extra=()):
        out = self.root / f"{self.case}-{mode}-{self.sequence:04}"
        argv = [
            self.args.binary,
            "--kubeconfig",
            self.args.kubeconfig,
            "--context",
            self.args.context,
            "--mode",
            mode,
            "--output-dir",
            str(out),
            "--json-report",
            str(out / "report.json"),
            "--report-path",
            "",
            "--readiness-timeout",
            f"{self.args.timeout}s",
            "--operation-timeout",
            f"{self.args.timeout + 60}s",
        ]
        argv += ["--plan-file", str(plan)] if plan else ["--projects", self.ns]
        if health:
            argv += ["--health-url", health]
        if mode == "cutover":
            argv += ["--automation-quiesced"]
        self.command(argv + list(extra), expected=expected)
        self.snapshot(mode)
        return (
            out / self.ns / "web.plan.json" if mode == "plan" else out / "report.json"
        )

    def setup(self, name, mutate, start=True):
        self.case = name
        self.ns = f"{self.args.namespace_prefix}-{name}"
        ns = dict(
            apiVersion="v1",
            kind="Namespace",
            metadata=dict(
                name=self.ns,
                labels={
                    "migration.openshift-dc.io/test-namespace": "true",
                    "migration.openshift-dc.io/configuration-run": self.run,
                },
            ),
        )
        self.command(self.base + ["create", "-f", "-"], ns)
        self.create("ConfigMap", "v1", None, data={"VALUE": "retained-config"})
        spec = dict(
            replicas=1,
            selector={"deploymentconfig": "web"},
            strategy={
                "type": "Rolling",
                "rollingParams": {"maxSurge": 1, "maxUnavailable": 0},
            },
            triggers=[{"type": "ConfigChange"}],
            template={
                "metadata": {
                    "labels": {"deploymentconfig": "web", "app": "web", "keep": name}
                },
                "spec": {
                    "terminationGracePeriodSeconds": 1,
                    "containers": [
                        {
                            "name": "web",
                            "image": self.args.image,
                            "ports": [{"name": "web", "containerPort": 8080}],
                            "env": [
                                {"name": "PORT", "value": "8080"},
                                {"name": "SECOND_PORT", "value": "8888"},
                                {"name": "RESPONSE", "value": "qualified-" + name},
                            ],
                            "resources": {"requests": {"cpu": "10m", "memory": "24Mi"}},
                            "readinessProbe": {
                                "httpGet": {"port": 8080, "path": "/"},
                                "periodSeconds": 1,
                            },
                        }
                    ],
                },
            },
        )
        mutate(spec)
        self.create("DeploymentConfig", "apps.openshift.io/v1", spec)
        self.create(
            "Service",
            "v1",
            {
                "selector": {"deploymentconfig": "web"},
                "ports": [{"name": "web", "port": 8080, "targetPort": "web"}],
            },
        )
        self.create(
            "Route",
            "route.openshift.io/v1",
            {"to": {"kind": "Service", "name": "web"}, "port": {"targetPort": "web"}},
        )
        if start:
            wanted = self.get("dc")["spec"].get("replicas", 0)
            self.wait(
                lambda: self.get("dc").get("status", {}).get("availableReplicas", 0)
                >= wanted
            )
        version, since = None, time.monotonic()

        def settled():
            nonlocal version, since
            current = self.get("dc")["metadata"]["resourceVersion"]
            if current != version:
                version, since = current, time.monotonic()
            return time.monotonic() - since >= 5

        self.wait(settled)
        self.snapshot("source")
        return "http://" + self.get("route")["spec"]["host"] + "/"

    def migrate(self, name, mutate):
        health = self.setup(name, mutate)
        source = self.get("dc")
        if name == "api-defaults":
            omitted = copy.deepcopy(source)
            omitted["spec"].pop("replicas", None)
            input_file = self.root / "api-defaults-omitted.json"
            input_file.write_text(json.dumps(omitted, indent=2))
            offline = self.root / "api-defaults-offline"
            self.command(
                [
                    self.args.binary,
                    "--input",
                    str(input_file),
                    "--output-dir",
                    str(offline),
                    "--json-report",
                    str(offline / "report.json"),
                    "--report-path",
                    "",
                ]
            )
            offline_plan = json.loads((offline / self.ns / "web.plan.json").read_text())
            assert (
                offline_plan["desired"]["spec"]["replicas"]
                == source["spec"]["replicas"]
                == 0
            )
        plan = self.tool("plan", health=health)
        p = json.loads(plan.read_text())
        desired = p["desired"]["spec"]
        assert desired["replicas"] == source["spec"]["replicas"]
        for key in ("minReadySeconds", "revisionHistoryLimit"):
            if key in source["spec"]:
                assert desired[key] == source["spec"][key]
        for label, value in source["spec"]["template"]["metadata"]["labels"].items():
            assert desired["template"]["metadata"]["labels"][label] == value
        for key in ("containers", "volumes", "terminationGracePeriodSeconds"):
            if key in source["spec"]["template"]["spec"]:
                assert (
                    desired["template"]["spec"][key]
                    == source["spec"]["template"]["spec"][key]
                ), key
        self.tool("dry-run", plan)
        assert not self.oc(
            "get", "deploy", "web", "--ignore-not-found", "-o", "name"
        ).stdout.strip()
        self.tool("stage", plan)
        staged = self.get("deploy")
        assert staged["spec"]["replicas"] == 0 and staged["spec"]["paused"]
        self.tool("stage", plan)
        self.tool("cutover", plan)
        assert self.get("dc")["spec"]["replicas"] == 0
        assert (
            self.get("deploy")["status"].get("availableReplicas", 0)
            >= desired["replicas"]
        )
        assert json.loads(plan.read_text())["progress"]["phase"] == "complete"
        self.tool("cutover", plan)
        self.tool("rollback", plan)
        assert self.get("dc")["spec"] == source["spec"]
        assert self.get("deploy")["spec"]["replicas"] == 0
        assert json.loads(plan.read_text())["progress"]["phase"] == "rolled-back"
        self.tool("rollback", plan)
        return {
            "plan": str(plan.relative_to(self.root)),
            "replicas": desired["replicas"],
            "sourceUID": source["metadata"]["uid"],
            "destinationUID": staged["metadata"]["uid"],
        }

    def rejection(self, name, mutate, expected_text, paused=False):
        health = self.setup(name, mutate, start=False)
        source = self.get("dc")
        if paused:
            plan = self.tool("plan", health=health)
            self.tool("stage", plan)
            report_path = self.tool("cutover", plan, expected=1)
            assert self.get("dc")["spec"] == source["spec"]
            assert self.get("deploy")["spec"]["replicas"] == 0
        else:
            report_path = self.tool("generate", expected=1)
        report = json.loads(report_path.read_text())
        assert expected_text.lower() in json.dumps(report).lower(), report
        if not paused:
            assert not self.oc(
                "get", "deploy", "web", "--ignore-not-found", "-o", "name"
            ).stdout.strip()
        return {
            "diagnostic": report["results"][0],
            "sourceUnmodified": self.get("dc")["spec"] == source["spec"],
        }

    def run_case(self, name, fn):
        row = {
            "case": name,
            "result": "failed",
            "startedAt": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        }
        try:
            row["observations"] = fn()
            row["result"] = "passed"
        except Exception as exc:
            row["error"] = str(exc)
            if self.ns:
                self.snapshot("failure")
        row["namespace"] = self.ns
        self.cases.append(row)
        (self.root / "cases.json").write_text(json.dumps(self.cases, indent=2))
        print(name, row["result"], row.get("error", ""), flush=True)


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
    p.add_argument(
        "--cases", help="Comma-separated configuration names; omitted runs all"
    )
    args = p.parse_args()
    if not args.allow_test_mutations or not re.fullmatch(
        r"dcmig-[a-z0-9-]+", args.namespace_prefix
    ):
        p.error(
            "Explicit disposable dcmig-* namespace prefix and mutation acknowledgement required"
        )
    if "@sha256:" not in args.image:
        p.error("An immutable test image is required")
    args.binary = str(pathlib.Path(args.binary).resolve())
    args.kubeconfig = str(pathlib.Path(args.kubeconfig).resolve())
    live = Live(args)
    metadata = dict(
        kind="actual-openshift-configurations",
        artifactSHA256=digest(args.binary),
        revision=subprocess.check_output(
            ["git", "rev-parse", "HEAD"], text=True
        ).strip(),
        fixtureSHA256=fixture_digest(),
        dependencySHA256=dependency_digest(),
        clusterVersion=live.get("clusterversion", "version"),
        kubernetesVersion=json.loads(live.oc("get", "--raw", "/version").stdout),
    )
    (live.root / "metadata.json").write_text(json.dumps(metadata, indent=2))

    def rolling(s):
        s.update(replicas=2, minReadySeconds=2, revisionHistoryLimit=4)

    def percentages(s):
        s["strategy"]["rollingParams"].update(maxSurge="150%", maxUnavailable="0%")

    def volumes(s):
        pod = s["template"]["spec"]
        pod["volumes"] = [{"name": "scratch", "emptyDir": {}}]
        pod["containers"][0]["volumeMounts"] = [
            {"name": "scratch", "mountPath": "/scratch"}
        ]
        pod["containers"][0]["envFrom"] = [{"configMapRef": {"name": "web"}}]

    def multi(s):
        second = copy.deepcopy(s["template"]["spec"]["containers"][0])
        second.update(
            name="sidecar",
            ports=[{"containerPort": 8081, "name": "sidecar"}],
            env=[
                {"name": "PORT", "value": "8081"},
                {"name": "SECOND_PORT", "value": "8889"},
            ],
        )
        second["readinessProbe"]["httpGet"]["port"] = 8081
        s["template"]["spec"]["containers"].append(second)

    supported = [
        ("rolling-integer", rolling),
        ("rolling-percent", percentages),
        ("recreate", lambda s: s.update(strategy={"type": "Recreate"})),
        ("zero", lambda s: s.update(replicas=0)),
        ("env-volume", volumes),
        ("multi-container", multi),
        ("api-defaults", lambda s: s.pop("replicas")),
    ]
    for name, mutate in supported:
        if args.cases and name not in args.cases.split(","):
            continue
        live.run_case(name, lambda name=name, mutate=mutate: live.migrate(name, mutate))
    negative = [
        ("paused", lambda s: s.update(paused=True), "paused", True),
        ("manual-triggers", lambda s: s.update(triggers=[]), "configchange", False),
        ("test-dc", lambda s: s.update(test=True), "test", False),
        (
            "custom",
            lambda s: s.update(
                strategy={
                    "type": "Custom",
                    "customParams": {"image": args.image, "command": ["/bin/true"]},
                }
            ),
            "custom",
            False,
        ),
        (
            "rolling-hook",
            lambda s: s["strategy"]["rollingParams"].update(
                pre={
                    "failurePolicy": "Abort",
                    "execNewPod": {"containerName": "web", "command": ["/bin/true"]},
                }
            ),
            "hook",
            False,
        ),
        (
            "recreate-hook",
            lambda s: s.update(
                strategy={
                    "type": "Recreate",
                    "recreateParams": {
                        "mid": {
                            "failurePolicy": "Abort",
                            "execNewPod": {
                                "containerName": "web",
                                "command": ["/bin/true"],
                            },
                        }
                    },
                }
            ),
            "hook",
            False,
        ),
    ]
    for name, mutate, diagnostic, paused in negative:
        if args.cases and name not in args.cases.split(","):
            continue
        live.run_case(
            name,
            lambda name=name, mutate=mutate, diagnostic=diagnostic, paused=paused: live.rejection(
                name, mutate, diagnostic, paused
            ),
        )
    return 0 if live.cases and all(c["result"] == "passed" for c in live.cases) else 1


if __name__ == "__main__":
    raise SystemExit(main())
