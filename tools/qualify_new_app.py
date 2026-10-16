#!/usr/bin/env python3
"""Actual oc new-app --as-deployment-config migration and edge-case tests.

Uses explicit test credentials and new dcmig-* namespaces. Apps are created by
oc, rather than reconstructed fixtures. Evidence is private; successful sources
and zero-replica destinations remain available for inspection. The regular-pvc
case creates a 1Gi PVC using the cluster's existing default StorageClass.
"""

import argparse
import copy
import datetime
import json
import pathlib
import re
import subprocess
import threading
import time

from qualify_configurations import Live
from release_gate import dependency_digest, digest, tree_digest


class NewApp(Live):
    def __init__(self, args):
        super().__init__(args)
        self.name = "web"
        self.flags = []

    def get(self, kind, name=None):
        return super().get(kind, name or self.name)

    def snapshot(self, label):
        p = self.oc(
            "get",
            "dc,deploy,rs,rc,pods,svc,endpoints,route,hpa,events,imagestream,buildconfig,build,pvc,networkpolicy",
            "-o",
            "json",
            expected=None,
        )
        (self.root / f"{self.case}-{label}.json").write_text(p.stdout)
        return json.loads(p.stdout) if p.returncode == 0 else {}

    def tool(self, mode, plan=None, health=None, expected=0, extra=()):
        result = super().tool(
            mode, plan, health, expected, tuple(self.flags) + tuple(extra)
        )
        return result.with_name(self.name + ".plan.json") if mode == "plan" else result

    def namespace(self, case, name="web"):
        self.case, self.name, self.flags = case, name, []
        self.ns = self.args.namespace_prefix + "-" + case
        self.command(
            self.base + ["create", "-f", "-"],
            {
                "apiVersion": "v1",
                "kind": "Namespace",
                "metadata": {
                    "name": self.ns,
                    "labels": {
                        "migration.openshift-dc.io/test-namespace": "true",
                        "migration.openshift-dc.io/new-app-run": self.run,
                    },
                },
            },
        )

    def app(self, case, style="external", options=(), name="web", wait=True):
        self.namespace(case, name)
        args = ["new-app", "--as-deployment-config", "--name=" + self.name]
        if style in {"local", "cross", "grouped"}:
            image_ns = self.ns
            if style == "cross":
                image_ns += "-images"
                self.command(
                    self.base + ["create", "-f", "-"],
                    {
                        "apiVersion": "v1",
                        "kind": "Namespace",
                        "metadata": {
                            "name": image_ns,
                            "labels": {
                                "migration.openshift-dc.io/test-namespace": "true",
                                "migration.openshift-dc.io/new-app-run": self.run,
                            },
                        },
                    },
                )
            for stream, image in [("origin", self.args.image)] + (
                [("sidecar", self.args.sidecar_image)] if style == "grouped" else []
            ):
                self.command(
                    self.base + ["-n", image_ns, "create", "imagestream", stream]
                )
                self.command(
                    self.base
                    + [
                        "-n",
                        image_ns,
                        "tag",
                        "--source=docker",
                        image,
                        stream + ":current",
                    ]
                )

                def imported():
                    p = self.command(
                        self.base
                        + [
                            "-n",
                            image_ns,
                            "get",
                            "imagestreamtag",
                            stream + ":current",
                            "-o",
                            "json",
                        ],
                        expected=None,
                    )
                    return p.returncode == 0 and bool(
                        json.loads(p.stdout)
                        .get("image", {})
                        .get("dockerImageReference")
                    )

                self.wait(imported)
                args += [
                    "--image-stream="
                    + (image_ns + "/" if style == "cross" else "")
                    + stream
                    + ":current"
                ]
            if style == "grouped":
                # Inspect real CLI output before creating resources: component
                # identifiers can differ between oc versions.
                grouped = None
                for group in [
                    "origin+sidecar",
                    "origin:current+sidecar:current",
                    self.ns + "/origin:current+" + self.ns + "/sidecar:current",
                ]:
                    p = self.oc(
                        *(args + ["--group=" + group, "--dry-run", "-o", "json"]),
                        expected=None,
                    )
                    if p.returncode == 0:
                        objects = json.loads(p.stdout).get("items", [])
                        dcs = [o for o in objects if o["kind"] == "DeploymentConfig"]
                        if (
                            len(dcs) == 1
                            and len(dcs[0]["spec"]["template"]["spec"]["containers"])
                            == 2
                        ):
                            grouped = group
                            break
                if not grouped:
                    raise RuntimeError(
                        "oc did not generate one grouped two-container DC"
                    )
                args += ["--group=" + grouped]
        elif style == "docker-build":
            args += ["--binary", "--strategy=docker"]
        elif style == "s2i-build":
            args += [
                "--binary",
                "--strategy=source",
                "--image-stream=" + self.args.builder_image_stream,
            ]
        else:
            args += ["--image=" + self.args.image]
        self.oc(*(args + list(options)))
        dcs = json.loads(self.oc("get", "dc", "-o", "json").stdout)["items"]
        assert len(dcs) == 1 and dcs[0]["metadata"]["name"] == self.name
        self.snapshot("new-app-created")
        if wait:
            self.ready()
        return self.get("dc")

    def settled(self):
        previous, since = None, time.monotonic()

        def stable():
            nonlocal previous, since
            version = self.get("dc")["metadata"]["resourceVersion"]
            if version != previous:
                previous, since = version, time.monotonic()
            return time.monotonic() - since >= 5

        self.wait(stable)

    def ready(self):
        self.oc(
            "rollout",
            "status",
            "dc/" + self.name,
            "--timeout=" + str(self.args.timeout) + "s",
        )
        self.settled()

    def patch(self, mutate, ready=True):
        source = self.get("dc")
        spec = copy.deepcopy(source["spec"])
        mutate(spec)
        self.oc(
            "patch", "dc", self.name, "--type=merge", "-p", json.dumps({"spec": spec})
        )
        if ready:
            self.ready()
        else:
            self.settled()

    def route(self, name="health", port=8080):
        svc = self.get("svc")
        selected = next(p for p in svc["spec"]["ports"] if p["port"] == port)
        self.oc(
            "expose",
            "service/" + self.name,
            "--name=" + name,
            "--port=" + str(selected.get("name") or port),
        )
        return "http://" + self.get("route", name)["spec"]["host"] + "/"

    def http(self, url, text=None):
        observed = []

        def healthy():
            p = self.command(["curl", "-fsS", "--max-time", "10", url], expected=None)
            if p.returncode == 0 and (text is None or text in p.stdout):
                observed.append(p.stdout)
                return True
            return False

        self.wait(healthy)
        return observed[-1]

    def protocol(self, health=None, activated=None, overlap=False):
        source = self.get("dc")
        services = json.loads(self.oc("get", "services", "-o", "json").stdout)["items"]
        builds = json.loads(self.oc("get", "buildconfig", "-o", "json").stdout)["items"]
        images = json.loads(self.oc("get", "imagestream", "-o", "json").stdout)["items"]
        plan = self.tool(
            "plan",
            health=health,
            extra=(
                (
                    "--cutover-mode",
                    "overlap",
                    "--overlap-justification",
                    "Authorized stateless new-app fixture",
                )
                if overlap
                else ()
            ),
        )
        data = json.loads(plan.read_text())
        for key, value in source["spec"]["template"]["metadata"]["labels"].items():
            assert (
                data["desired"]["spec"]["template"]["metadata"]["labels"][key] == value
            )
        assert data["desired"]["spec"]["replicas"] == source["spec"]["replicas"]
        self.tool("dry-run", plan)
        assert not self.oc(
            "get", "deployment", self.name, "--ignore-not-found", "-o", "name"
        ).stdout.strip()
        self.tool("stage", plan)
        staged = self.get("deployment")
        assert staged["spec"]["replicas"] == 0 and staged["spec"]["paused"]
        assert (
            not staged["metadata"]
            .get("annotations", {})
            .get("image.openshift.io/triggers")
        )
        self.tool("stage", plan)
        self.tool("cutover", plan)
        assert self.get("dc")["spec"]["replicas"] == 0
        assert json.loads(plan.read_text())["progress"]["phase"] == "complete"
        if source["spec"]["replicas"]:
            self.http(health)
        self.tool("cutover", plan)
        details = activated(plan, source) if activated else {}
        self.tool("rollback", plan)
        assert json.loads(plan.read_text())["progress"]["phase"] == "rolled-back"
        assert self.get("dc")["spec"] == source["spec"]
        assert self.get("deployment")["spec"]["replicas"] == 0
        self.tool("rollback", plan)
        for before in services:
            after = self.get("service", before["metadata"]["name"])
            assert (
                after["metadata"]["uid"] == before["metadata"]["uid"]
                and after["spec"] == before["spec"]
            )
        for before in builds:
            after = self.get("buildconfig", before["metadata"]["name"])
            assert (
                after["metadata"]["uid"] == before["metadata"]["uid"]
                and after["spec"] == before["spec"]
            )
        for before in images:
            assert (
                self.get("imagestream", before["metadata"]["name"])["metadata"]["uid"]
                == before["metadata"]["uid"]
            )
        return {
            "createdBy": "oc new-app --as-deployment-config",
            "sourceUID": source["metadata"]["uid"],
            "destinationUID": staged["metadata"]["uid"],
            "replicas": source["spec"]["replicas"],
            "servicesPreserved": len(services),
            "buildConfigsPreserved": len(builds),
            "imageStreamsPreserved": len(images),
            "protocol": "dry-run, stage, rerun, cutover, rerun, rollback, rerun",
            **(details or {}),
        }

    def image_update(self, plan, source):
        trigger = next(
            t for t in source["spec"]["triggers"] if t["type"] == "ImageChange"
        )
        ref = trigger["imageChangeParams"]["from"]
        ns = ref.get("namespace", self.ns)
        old = self.get("deployment")["spec"]["template"]["spec"]["containers"][0][
            "image"
        ]
        self.command(
            self.base
            + ["-n", ns, "tag", "--source=docker", self.args.second_image, ref["name"]]
        )
        self.wait(
            lambda: self.get("deployment")["spec"]["template"]["spec"]["containers"][0][
                "image"
            ]
            != old
        )
        self.oc(
            "rollout",
            "status",
            "deployment/" + self.name,
            "--timeout=" + str(self.args.timeout) + "s",
        )
        assert (
            self.get("dc")["spec"]["replicas"] == 0
            and self.get("dc")["spec"]["triggers"] == []
        )
        self.snapshot("imagestream-update")
        self.command(self.base + ["-n", ns, "tag", "--source=docker", old, ref["name"]])
        self.wait(
            lambda: self.get("deployment")["spec"]["template"]["spec"]["containers"][0][
                "image"
            ]
            == old
        )
        return {
            "newAppImageTriggerUpdatedDeployment": True,
            "sourceRemainedRetired": True,
        }


def main():
    p = argparse.ArgumentParser(description=__doc__)
    for key in (
        "context",
        "kubeconfig",
        "binary",
        "image",
        "second-image",
        "sidecar-image",
        "namespace-prefix",
        "output",
    ):
        p.add_argument("--" + key, required=True)
    p.add_argument("--builder-image-stream", default="openshift/httpd:2.4-ubi9")
    p.add_argument("--allow-test-mutations", action="store_true")
    p.add_argument(
        "--allow-regular-pvc",
        action="store_true",
        help="Explicit authorization to create a 1Gi regular PVC with the existing default StorageClass",
    )
    p.add_argument(
        "--cases", required=True, help="Comma-separated test case names or all"
    )
    p.add_argument("--timeout", type=int, default=120)
    args = p.parse_args()
    if not args.allow_test_mutations or not re.fullmatch(
        r"dcmig-[a-z0-9-]+", args.namespace_prefix
    ):
        p.error(
            "Explicit disposable namespace prefix/context and mutation acknowledgement required"
        )
    if any(
        "@sha256:" not in image
        for image in (args.image, args.second_image, args.sidecar_image)
    ):
        p.error("Immutable runtime images required")
    args.binary, args.kubeconfig = str(pathlib.Path(args.binary).resolve()), str(
        pathlib.Path(args.kubeconfig).resolve()
    )
    live = NewApp(args)

    def record_metadata():
        (live.root / "metadata.json").write_text(
            json.dumps(
                {
                    "kind": "actual-openshift-new-app",
                    "testedAt": datetime.datetime.now(
                        datetime.timezone.utc
                    ).isoformat(),
                    "artifactSHA256": digest(args.binary),
                    "revision": subprocess.check_output(
                        ["git", "rev-parse", "HEAD"], text=True
                    ).strip(),
                    "fixtureSHA256": tree_digest("testdata"),
                    "dependencySHA256": dependency_digest(),
                    "clusterVersion": live.get("clusterversion", "version"),
                    "kubernetesVersion": json.loads(
                        live.oc("get", "--raw", "/version").stdout
                    ),
                },
                indent=2,
            )
        )

    def simple(
        name,
        style="external",
        mutate=None,
        flags=(),
        overlap=False,
        options=(),
        app_name="web",
    ):
        live.app(name, style, options, app_name)
        if mutate:
            live.patch(mutate)
        live.flags = list(flags)
        health = live.route()
        if live.get("dc")["spec"]["replicas"] > 0:
            live.http(health)
        return live.protocol(health, overlap=overlap)

    def image_case(name, style):
        live.app(name, style)
        return live.protocol(live.route(), activated=live.image_update)

    def manual(
        name,
        mutate=None,
        options=(),
        phase="generate",
        diagnostic="manual",
        paused=False,
    ):
        live.app(name, options=options, wait=not options)
        if mutate:
            live.patch(mutate, ready=False)
        original = live.get("dc")
        if paused:
            plan = live.tool("plan", health=live.route())
            live.tool("stage", plan)
            report = live.tool("cutover", plan, expected=1)
        else:
            report = live.tool(phase, expected=1)
        assert diagnostic.lower() in report.read_text().lower()
        assert live.get("dc")["spec"] == original["spec"]
        assert not self_active_destination(live)
        return {
            "expectedFailure": diagnostic,
            "sourceUnchanged": True,
            "destinationActive": False,
        }

    def stale(name, change):
        live.app(name)
        plan = live.tool("plan", health=live.route())
        change()
        report = live.tool("stage", plan, expected=1)
        assert json.loads(report.read_text())["totals"]["failed"] > 0
        assert not self_active_destination(live)
        return {
            "staleDependencyRejected": True,
            "diagnostic": json.loads(report.read_text())["results"][0].get("error"),
        }

    def additional_service():
        live.create(
            "Service",
            "v1",
            {
                "selector": {"deploymentconfig": live.name},
                "clusterIP": "None",
                "ports": [{"port": 8080, "targetPort": 8080}],
            },
            name="headless",
        )
        live.create(
            "Service",
            "v1",
            {"selector": {"unrelated": "true"}, "ports": [{"port": 8080}]},
            name="unrelated",
        )

    def service_case():
        live.app("multiple-services")
        additional_service()
        first, second = live.route(), live.route("second-port", 8888)

        def active(plan, source):
            return {
                "bothExposedPortsHealthy": bool(live.http(first) and live.http(second))
            }

        return live.protocol(first, activated=active)

    def config_secret(spec):
        live.create(
            "ConfigMap", "v1", None, name="config", data={"SETTING": "dummy-config"}
        )
        live.create(
            "Secret",
            "v1",
            None,
            name="dummy",
            stringData={"TOKEN": "non-secret-fixture-value"},
        )
        c = spec["template"]["spec"]["containers"][0]
        c["envFrom"] = [{"configMapRef": {"name": "config"}}]
        c.setdefault("env", []).extend(
            [
                {
                    "name": "TOKEN",
                    "valueFrom": {"secretKeyRef": {"name": "dummy", "key": "TOKEN"}},
                },
                {
                    "name": "POD_NAME",
                    "valueFrom": {"fieldRef": {"fieldPath": "metadata.name"}},
                },
            ]
        )

    def init_container(spec):
        pod = spec["template"]["spec"]
        pod["terminationGracePeriodSeconds"] = 1
        pod["volumes"] = [{"name": "scratch", "emptyDir": {}}]
        pod["initContainers"] = [
            {
                "name": "initialize",
                "image": args.image,
                "command": ["/bin/sh", "-ec", "echo initialized > /scratch/marker"],
                "volumeMounts": [{"name": "scratch", "mountPath": "/scratch"}],
            }
        ]
        pod["containers"][0].update(
            volumeMounts=[{"name": "scratch", "mountPath": "/scratch"}],
            command=[
                "/bin/sh",
                "-ec",
                "test -f /scratch/marker; exec /hello_openshift",
            ],
        )

    cases = {
        "plain": lambda: simple("plain"),
        "env-labels": lambda: simple(
            "env-labels",
            options=(
                "--env=RESPONSE=new-app-labels",
                "--labels=app=retained,tier=frontend",
            ),
        ),
        "metadata-off": lambda: simple(
            "metadata-off",
            flags=("--preserve-labels=false", "--preserve-annotations=false"),
        ),
        "local-image": lambda: image_case("local-image", "local"),
        "cross-image": lambda: image_case("cross-image", "cross"),
        "grouped": lambda: simple("grouped", "grouped"),
        "replicas-three": lambda: simple(
            "replicas-three", mutate=lambda s: s.update(replicas=3)
        ),
        "scaled-zero": lambda: simple(
            "scaled-zero", mutate=lambda s: s.update(replicas=0)
        ),
        "recreate": lambda: simple(
            "recreate",
            mutate=lambda s: s.update(
                strategy={"type": "Recreate"}, minReadySeconds=2, revisionHistoryLimit=3
            ),
        ),
        "zero-surge": lambda: simple(
            "zero-surge",
            mutate=lambda s: s["strategy"]["rollingParams"].update(
                maxSurge=0, maxUnavailable=1
            ),
        ),
        "overlap": lambda: simple("overlap", overlap=True),
        "config-secret": lambda: simple("config-secret", mutate=config_secret),
        "init-container": lambda: simple("init-container", mutate=init_container),
        "long-name": lambda: simple("long-name", app_name="new-app-" + "x" * 40),
        "multiple-services": service_case,
        "test-app": lambda: manual(
            "test-app", options=("--as-test",), diagnostic="test"
        ),
        "paused": lambda: manual(
            "paused",
            mutate=lambda s: s.update(paused=True),
            paused=True,
            diagnostic="paused",
        ),
        "manual-triggers": lambda: manual(
            "manual-triggers",
            mutate=lambda s: s.update(triggers=[]),
            diagnostic="ConfigChange",
        ),
        "rolling-hook": lambda: manual(
            "rolling-hook",
            mutate=lambda s: s["strategy"]["rollingParams"].update(
                pre={
                    "failurePolicy": "Abort",
                    "execNewPod": {
                        "containerName": s["template"]["spec"]["containers"][0]["name"],
                        "command": ["/bin/true"],
                    },
                }
            ),
            diagnostic="hook",
        ),
        "new-pod-field": lambda: manual(
            "new-pod-field",
            mutate=lambda s: s["template"]["spec"].update(
                resources={"requests": {"cpu": "10m"}}
            ),
            diagnostic="resources",
        ),
        "stale-source": lambda: stale(
            "stale-source",
            lambda: live.oc("annotate", "dc", live.name, "changed-after-plan=true"),
        ),
        "stale-service": lambda: stale(
            "stale-service",
            lambda: live.oc(
                "patch",
                "service",
                live.name,
                "--type=merge",
                "-p",
                json.dumps({"spec": {"selector": {"wrong": "true"}}}),
            ),
        ),
    }
    # More complex scenarios are registered below; each retains its own namespace.
    add_advanced_cases(cases, live, args)
    selected = list(cases) if args.cases == "all" else args.cases.split(",")
    unknown = set(selected) - cases.keys()
    if unknown or not selected:
        p.error("Unknown or empty new-app cases: " + str(sorted(unknown)))
    if "regular-pvc" in selected and not args.allow_regular_pvc:
        p.error("regular-pvc requires explicit --allow-regular-pvc")
    record_metadata()
    for name in selected:
        live.run_case(name, cases[name])
    return (
        0 if live.cases and all(row["result"] == "passed" for row in live.cases) else 1
    )


def self_active_destination(live):
    p = live.oc("get", "deployment", live.name, "--ignore-not-found", "-o", "json")
    return bool(
        p.stdout.strip() and json.loads(p.stdout)["spec"].get("replicas", 0) > 0
    )


def add_advanced_cases(cases, live, args):
    def observe(execute):
        samples, errors, stop = [], [], threading.Event()
        ns, name = live.ns, live.name

        def monitor():
            while not stop.is_set():
                try:
                    p = subprocess.run(
                        live.base
                        + [
                            "-n",
                            ns,
                            "get",
                            "pods",
                            "-l",
                            "deploymentconfig=" + name,
                            "-o",
                            "json",
                        ],
                        capture_output=True,
                        text=True,
                        timeout=15,
                    )
                    if p.returncode == 0:
                        active = [
                            x
                            for x in json.loads(p.stdout)["items"]
                            if x.get("status", {}).get("phase")
                            not in {"Succeeded", "Failed"}
                            and not x["metadata"]["name"].endswith("-deploy")
                        ]
                        samples.append(
                            {
                                "at": datetime.datetime.now(
                                    datetime.timezone.utc
                                ).isoformat(),
                                "source": [
                                    x["metadata"]["uid"]
                                    for x in active
                                    if "migration.openshift-dc.io/id"
                                    not in x["metadata"].get("labels", {})
                                ],
                                "destination": [
                                    x["metadata"]["uid"]
                                    for x in active
                                    if "migration.openshift-dc.io/id"
                                    in x["metadata"].get("labels", {})
                                ],
                            }
                        )
                    else:
                        errors.append(p.stderr)
                except (subprocess.TimeoutExpired, ValueError) as exc:
                    errors.append(str(exc))
                stop.wait(0.2)

        thread = threading.Thread(target=monitor, daemon=True)
        thread.start()
        try:
            result = execute()
        finally:
            stop.set()
            thread.join(20)
            (live.root / (live.case + "-ownership-timeline.json")).write_text(
                json.dumps({"samples": samples, "readErrors": errors}, indent=2)
            )
        assert (
            samples
            and any(s["source"] for s in samples)
            and any(s["destination"] for s in samples)
        )
        assert not any(
            s["source"] and s["destination"] for s in samples
        ), "Exclusive source/destination Pods overlap"
        return {
            **result,
            "timelineSamples": len(samples),
            "concurrentSourceAndDestinationObserved": 0,
            "observationReadErrors": len(errors),
        }

    def volume():
        live.app("regular-pvc")
        live.create(
            "PersistentVolumeClaim",
            "v1",
            {
                "accessModes": ["ReadWriteOnce"],
                "resources": {"requests": {"storage": "1Gi"}},
            },
            name="data",
        )
        marker = "new-app-pvc-" + live.run

        def mount(spec):
            pod = spec["template"]["spec"]
            pod["terminationGracePeriodSeconds"] = 1
            pod["volumes"] = [
                {"name": "data", "persistentVolumeClaim": {"claimName": "data"}}
            ]
            pod["containers"][0].update(
                volumeMounts=[{"name": "data", "mountPath": "/data"}],
                command=[
                    "/bin/sh",
                    "-ec",
                    "test -f /data/marker || echo "
                    + marker
                    + " > /data/marker; export RESPONSE=$(cat /data/marker); exec /hello_openshift",
                ],
            )

        live.patch(mount)
        claim = live.get("pvc", "data")
        assert claim["status"]["phase"] == "Bound"
        pv = live.get("pv", claim["spec"]["volumeName"])
        assert "csi" in pv["spec"] and "hostPath" not in pv["spec"]
        health = live.route()
        before_response = live.http(health, marker)

        def active(plan, source):
            return {"destinationMarkerResponse": live.http(health, marker)}

        result = observe(lambda: live.protocol(health, activated=active))
        restored_response = live.http(health, marker)
        assert live.get("pvc", "data")["metadata"]["uid"] == claim["metadata"]["uid"]
        assert (
            live.get("pv", pv["metadata"]["name"])["metadata"]["uid"]
            == pv["metadata"]["uid"]
        )
        pods = json.loads(
            live.oc(
                "get", "pods", "-l", "deploymentconfig=" + live.name, "-o", "json"
            ).stdout
        )["items"]
        scc = [
            p["metadata"].get("annotations", {}).get("openshift.io/scc")
            for p in pods
            if p["status"].get("phase") == "Running"
        ]
        return {
            **result,
            "pvcUID": claim["metadata"]["uid"],
            "pvUID": pv["metadata"]["uid"],
            "storageClass": claim["spec"]["storageClassName"],
            "csiDriver": pv["spec"]["csi"]["driver"],
            "markerBefore": before_response,
            "markerAfterRecovery": restored_response,
            "admittedSCCs": scc,
            "customSCCBindingCreated": False,
        }

    def build_case(style):
        name = "docker-build" if style == "docker-build" else "s2i-build"
        live.app(name, style, wait=False)
        context = live.root / (name + "-context")
        context.mkdir(mode=0o700)

        def content(marker):
            if style == "docker-build":
                data = "FROM " + args.image + "\nENV RESPONSE=" + marker + "\n"
                (context / "Dockerfile").write_text(data)
            else:
                data = "<html><body>" + marker + "</body></html>\n"
                (context / "index.html").write_text(data)
            (live.root / (name + "-" + marker + "-input.txt")).write_text(data)

        def build(marker):
            content(marker)
            live.oc(
                "start-build",
                live.name,
                "--from-dir=" + str(context),
                "--wait",
                "--follow",
            )
            bc = live.get("buildconfig")
            latest = live.get(
                "build", live.name + "-" + str(bc["status"]["lastVersion"])
            )
            assert latest["status"]["phase"] == "Complete"
            return latest["metadata"]["uid"]

        first_marker, second_marker = (
            name + "-first-" + live.run,
            name + "-second-" + live.run,
        )
        first_build = build(first_marker)
        if not live.oc(
            "get", "svc", live.name, "--ignore-not-found", "-o", "name"
        ).stdout.strip():
            live.oc(
                "expose",
                "dc/" + live.name,
                "--port=8080",
                "--target-port=8080",
                "--name=" + live.name,
            )
        live.ready()
        health = live.route()
        live.http(health, first_marker)

        def rebuild(plan, source):
            old = source["spec"]["template"]["spec"]["containers"][0]["image"]
            second_build = build(second_marker)
            live.wait(
                lambda: live.get("deployment")["spec"]["template"]["spec"][
                    "containers"
                ][0]["image"]
                != old
            )
            live.oc(
                "rollout",
                "status",
                "deployment/" + live.name,
                "--timeout=" + str(args.timeout) + "s",
            )
            response = live.http(health, second_marker)
            assert (
                live.get("dc")["spec"]["replicas"] == 0
                and live.get("dc")["spec"]["triggers"] == []
            )
            live.snapshot("build-after-cutover")
            trigger = next(
                t for t in source["spec"]["triggers"] if t["type"] == "ImageChange"
            )
            ref = trigger["imageChangeParams"]["from"]
            stream = ref["name"].rsplit(":", 1)[0]
            live.oc("tag", stream + "@" + old.rsplit("@", 1)[1], ref["name"])
            live.wait(
                lambda: live.get("deployment")["spec"]["template"]["spec"][
                    "containers"
                ][0]["image"]
                == old
            )
            return {
                "initialBuildUID": first_build,
                "postCutoverBuildUID": second_build,
                "postCutoverBuildResponse": response,
                "buildOutputContinuedUpdatingDeployment": True,
                "sourceRemainedRetired": True,
            }

        result = live.protocol(health, activated=rebuild)
        result["restoredSourceResponse"] = live.http(health, first_marker)
        return result

    def pending_build():
        live.app("unbuilt-binary", "docker-build", wait=False)
        live.settled()
        source = live.get("dc")
        generated = live.tool("generate")
        assert json.loads(generated.read_text())["totals"]["generated"] == 1
        plan = live.tool("plan", expected=1)
        assert any("unresolved" in b for b in json.loads(plan.read_text())["blockers"])
        report = live.tool("stage", plan, expected=1)
        assert "unresolved" in report.read_text()
        assert not self_active_destination(live)
        assert live.get("dc")["spec"] == source["spec"]
        return {
            "readOnlyReviewGenerationAllowed": True,
            "unresolvedImageLivePlanAndStageRejected": True,
            "sourceUnchanged": True,
            "buildConfigRetained": bool(live.get("buildconfig")),
        }

    def template():
        live.namespace("template")
        p = live.oc(
            "new-app",
            "--as-deployment-config",
            "--name=web",
            "--image=" + args.image,
            "--dry-run",
            "-o",
            "json",
        )
        objects = json.loads(p.stdout)["items"]
        objects = json.loads(json.dumps(objects).replace("web", "${NAME}"))
        dc = next(o for o in objects if o["kind"] == "DeploymentConfig")
        dc["spec"]["template"]["spec"]["containers"][0]["env"] = [
            {"name": "RESPONSE", "value": "${MESSAGE}"}
        ]
        body = {
            "apiVersion": "template.openshift.io/v1",
            "kind": "Template",
            "metadata": {"name": "new-app-template"},
            "parameters": [
                {"name": "NAME", "required": True},
                {"name": "MESSAGE", "required": True},
            ],
            "objects": objects,
        }
        path = live.root / "new-app-template.json"
        path.write_text(json.dumps(body, indent=2))
        live.oc(
            "new-app",
            "--file=" + str(path),
            "--param=NAME=web",
            "--param=MESSAGE=template-parameter-retained",
            "--as-deployment-config",
        )
        live.ready()
        health = live.route()
        live.http(health, "template-parameter-retained")
        return live.protocol(
            health,
            activated=lambda p, s: {
                "templateResponse": live.http(health, "template-parameter-retained")
            },
        )

    def target_tamper():
        live.app("changed-target")
        source = live.get("dc")
        health = live.route()
        plan = live.tool("plan", health=health)
        live.tool("stage", plan)
        live.oc(
            "patch",
            "deployment",
            live.name,
            "--type=merge",
            "-p",
            json.dumps(
                {
                    "spec": {
                        "template": {
                            "metadata": {"annotations": {"changed": "after-stage"}}
                        }
                    }
                }
            ),
        )
        report = live.tool("cutover", plan, expected=1)
        assert "changed" in report.read_text().lower()
        assert live.get("dc")["spec"] == source["spec"] and not self_active_destination(
            live
        )
        live.tool("rollback", plan)
        return {
            "changedStagedSpecRejected": True,
            "sourceUnchanged": True,
            "explicitRecovery": "verified",
        }

    def collision(overlap=False):
        case = "foreign-controller-overlap" if overlap else "foreign-name"
        live.app(case)
        name = "other" if overlap else live.name
        match = {"deploymentconfig": live.name} if overlap else {"foreign": "true"}
        live.create(
            "Deployment",
            "apps/v1",
            {
                "replicas": 0,
                "paused": True,
                "selector": {"matchLabels": match},
                "template": {
                    "metadata": {"labels": match},
                    "spec": {"containers": [{"name": "other", "image": args.image}]},
                },
            },
            name=name,
        )
        original = live.get("deployment", name)
        live.tool("plan", health=live.route(), expected=1)
        assert (
            live.get("deployment", name)["metadata"]["uid"]
            == original["metadata"]["uid"]
        )
        assert live.get("deployment", name)["spec"] == original["spec"]
        return {
            "foreignUID": original["metadata"]["uid"],
            "foreignSpecUnchanged": True,
            "overlap": overlap,
        }

    def health_failure():
        live.app("blocked-health")
        health = live.route()
        live.http(health)
        plan = live.tool("plan", health=health)
        plan_data = json.loads(plan.read_text())
        live.create(
            "NetworkPolicy",
            "networking.k8s.io/v1",
            {
                "podSelector": {
                    "matchLabels": {"migration.openshift-dc.io/id": plan_data["id"]}
                },
                "policyTypes": ["Ingress"],
                "ingress": [],
            },
            name="block-destination",
        )
        live.tool("stage", plan)
        report = live.tool("cutover", plan, expected=1)
        assert not json.loads(report.read_text())["results"][0]["ready"]
        assert json.loads(plan.read_text())["progress"]["phase"] != "complete"
        live.oc("delete", "networkpolicy", "block-destination", "--wait=true")
        live.tool("rollback", plan)
        live.http(health)
        return {
            "blockedTargetTrafficNotReportedReady": True,
            "recoveryAfterPolicyRemoval": "verified",
        }

    def multi_app():
        live.app("two-apps", options=("--labels=app=shared",))
        live.oc(
            "new-app",
            "--as-deployment-config",
            "--name=other",
            "--image=" + args.image,
            "--labels=app=shared",
        )
        live.oc("rollout", "status", "dc/other", "--timeout=" + str(args.timeout) + "s")
        health = live.route()
        live.settled()
        web, other = live.get("dc"), live.get("dc", "other")
        plan = live.tool("plan", health=health)
        other_plan = plan.with_name("other.plan.json")
        assert other_plan.exists()
        for path in (plan, other_plan):
            live.tool("stage", path)
        live.tool("cutover", plan)
        assert live.get("dc", "other")["spec"] == other["spec"]
        live.tool("rollback", plan)
        assert live.get("dc")["spec"] == web["spec"]
        # Both plans deliberately share an operator health URL. The next
        # cutover verifies its own Service endpoints as well as that URL.
        live.tool("cutover", other_plan)
        live.tool("rollback", other_plan)
        assert live.get("dc", "other")["spec"] == other["spec"]
        return {
            "twoNewAppSourcesMigratedIndependently": True,
            "sharedAppLabel": "shared",
            "distinctServiceSelectors": True,
        }

    def paused_image():
        live.app("disabled-image")

        def disable(spec):
            for t in spec["triggers"]:
                if t["type"] == "ImageChange":
                    t["imageChangeParams"]["automatic"] = False

        live.patch(disable)

        def update(plan, source):
            trigger = next(
                t for t in source["spec"]["triggers"] if t["type"] == "ImageChange"
            )
            ref = trigger["imageChangeParams"]["from"]
            old = live.get("deployment")["spec"]["template"]["spec"]["containers"][0][
                "image"
            ]
            annotation = json.loads(
                live.get("deployment")["metadata"]["annotations"][
                    "image.openshift.io/triggers"
                ]
            )
            assert annotation and all(t["paused"] for t in annotation)
            live.oc("tag", "--source=docker", args.second_image, ref["name"])
            live.wait(
                lambda: live.get("imagestreamtag", ref["name"])["image"][
                    "dockerImageReference"
                ]
                != old
            )
            time.sleep(5)
            assert (
                live.get("deployment")["spec"]["template"]["spec"]["containers"][0][
                    "image"
                ]
                == old
            )
            live.oc("tag", "--source=docker", old, ref["name"])
            live.wait(
                lambda: live.get("imagestreamtag", ref["name"])["image"][
                    "dockerImageReference"
                ]
                == old
            )
            return {
                "disabledNewAppImageTriggerPreserved": True,
                "importedTagChangeIgnored": True,
            }

        return live.protocol(live.route(), activated=update)

    def added_hpa():
        live.app("added-hpa")
        source = live.get("dc")
        plan = live.tool("plan", health=live.route())
        live.create(
            "HorizontalPodAutoscaler",
            "autoscaling/v2",
            {
                "scaleTargetRef": {
                    "apiVersion": "apps.openshift.io/v1",
                    "kind": "DeploymentConfig",
                    "name": live.name,
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
        report = live.tool("stage", plan, expected=1)
        assert not self_active_destination(live)
        assert live.get("dc")["spec"] == source["spec"]
        return {
            "newAutoscalerAfterPlanRejected": True,
            "diagnostic": json.loads(report.read_text())["results"][0]["error"],
        }

    def volume_overlap():
        live.app("volume-overlap")
        live.patch(
            lambda s: s["template"]["spec"].update(
                volumes=[{"name": "scratch", "emptyDir": {}}]
            )
        )
        original = live.get("dc")
        plan = live.tool(
            "plan",
            health=live.route(),
            expected=1,
            extra=(
                "--cutover-mode",
                "overlap",
                "--overlap-justification",
                "Deliberately unsafe volume fixture",
            ),
        )
        assert any("volume" in b for b in json.loads(plan.read_text())["blockers"])
        live.tool("stage", plan, expected=1)
        assert live.get("dc")["spec"] == original[
            "spec"
        ] and not self_active_destination(live)
        return {"unsafeVolumeOverlapRejected": True, "sourceUnchanged": True}

    def failed_initial_rollout(orphan=False):
        live.app("orphan-recovery" if orphan else "failed-initial-rollout")

        def inject(spec):
            pod = spec["template"]["spec"]
            pod["terminationGracePeriodSeconds"] = 1
            container = pod["containers"][0]
            container.setdefault("env", []).append(
                {
                    "name": "QUALIFICATION_MIGRATION_ID",
                    "valueFrom": {
                        "fieldRef": {
                            "fieldPath": "metadata.labels['migration.openshift-dc.io/id']"
                        }
                    },
                }
            )
            # The source has no migration label and remains healthy. Admission
            # accepts both templates; only actual destination startup fails.
            container["command"] = [
                "/bin/sh",
                "-ec",
                'if [ -n "$QUALIFICATION_MIGRATION_ID" ]; then '
                "echo 'injected destination startup failure' >&2; exit 42; fi; "
                "exec /hello_openshift",
            ]
            container["readinessProbe"] = {
                "httpGet": {"port": 8080, "path": "/"},
                "periodSeconds": 1,
            }

        live.patch(inject)
        health = live.route()
        original_response = live.http(health)
        source = live.get("dc")
        plan = live.tool("plan", health=health)
        live.tool("stage", plan)
        report = live.tool(
            "cutover", plan, expected=1, extra=("--readiness-timeout", "20s")
        )
        result = json.loads(report.read_text())["results"][0]
        assert result["constraints"]["status"] == "passed"
        assert not result["ready"]
        phase = json.loads(plan.read_text())["progress"]["phase"]
        assert phase not in {"staged", "complete"}
        assert live.get("dc")["spec"]["replicas"] == 0
        assert live.get("deployment")["spec"]["replicas"] == 1
        pods = json.loads(
            live.oc(
                "get", "pods", "-l", "migration.openshift-dc.io/id", "-o", "json"
            ).stdout
        )["items"]
        exit_codes = [
            state["terminated"]["exitCode"]
            for pod in pods
            for status in pod.get("status", {}).get("containerStatuses", [])
            for state in (status.get("state", {}), status.get("lastState", {}))
            if "terminated" in state
        ]
        assert 42 in exit_codes, "Injected startup failure was not observed"
        live.snapshot("failed-destination-startup")
        if orphan:
            target = live.get("deployment")
            migration_id = json.loads(plan.read_text())["id"]
            assert (
                target["metadata"]["annotations"][
                    "migration.openshift-dc.io/source-uid"
                ]
                == source["metadata"]["uid"]
            )
            live.oc(
                "delete", "deployment", live.name, "--cascade=foreground", "--wait=true"
            )
            live.oc(
                "create",
                "-f",
                "-",
                body={
                    "apiVersion": "v1",
                    "kind": "Pod",
                    "metadata": {
                        "name": "orphan",
                        "namespace": live.ns,
                        "labels": {
                            "migration.openshift-dc.io/id": migration_id,
                            "migration.openshift-dc.io/configuration-run": live.run,
                        },
                    },
                    "spec": {
                        "containers": [
                            {
                                "name": "idle",
                                "image": args.image,
                                "command": ["/bin/sh", "-ec", "exec sleep 86400"],
                            }
                        ],
                        "terminationGracePeriodSeconds": 1,
                    },
                },
            )
            assert not live.get("pod", "orphan")["metadata"].get("ownerReferences")
            blocked = live.tool(
                "rollback", plan, expected=1, extra=("--readiness-timeout", "5s")
            )
            assert not json.loads(blocked.read_text())["results"][0]["ready"]
            assert live.get("dc")["spec"]["replicas"] == 0
            assert json.loads(plan.read_text())["progress"]["phase"] != "rolled-back"
            live.snapshot("orphan-blocked-source-restoration")
            live.oc("delete", "pod", "orphan", "--wait=true")
        live.tool("rollback", plan)
        assert live.get("dc")["spec"] == source["spec"]
        if not orphan:
            assert live.get("deployment")["spec"]["replicas"] == 0
            assert live.get("deployment")["spec"]["paused"]
        assert live.http(health) == original_response
        live.tool("rollback", plan)
        return {
            "preRetirementAssessmentPassed": True,
            "destinationStartupExitCode": 42,
            "failedJournalPhase": phase,
            "failureNotReportedReady": True,
            "sourceRemainedRetiredUntilExplicitRecovery": True,
            "sourceSpecAndHTTPRecovered": True,
            "recoveryRerun": "verified",
            "orphanBlockedRestorationUntilRemoved": orphan,
        }

    def failed_image_rollout():
        live.app("failed-image-rollout")

        def probe(spec):
            spec["template"]["spec"]["containers"][0]["readinessProbe"] = {
                "httpGet": {"port": 8080, "path": "/"},
                "periodSeconds": 1,
            }

        live.patch(probe)
        health = live.route()
        source = live.get("dc")
        plan = live.tool("plan", health=health)
        live.tool("stage", plan)
        live.tool("cutover", plan)
        ref = next(t for t in source["spec"]["triggers"] if t["type"] == "ImageChange")[
            "imageChangeParams"
        ]["from"]
        old = source["spec"]["template"]["spec"]["containers"][0]["image"]
        # The real alternate HTTP image defaults to 8081; this existing probe
        # requires 8080. Its imported image is valid but rollout cannot be ready.
        live.oc("tag", "--source=docker", args.sidecar_image, ref["name"])
        live.wait(
            lambda: live.get("deployment")["spec"]["template"]["spec"]["containers"][0][
                "image"
            ]
            != old
        )
        report = live.tool("cutover", plan, expected=1)
        assert not json.loads(report.read_text())["results"][0]["ready"]
        assert live.get("dc")["spec"]["replicas"] == 0
        live.oc("tag", "--source=docker", old, ref["name"])
        live.wait(
            lambda: live.get("deployment")["spec"]["template"]["spec"]["containers"][0][
                "image"
            ]
            == old
        )
        live.tool("rollback", plan)
        assert live.get("dc")["spec"] == source["spec"]
        live.http(health)
        return {
            "validImageWithFailedReadinessRejected": True,
            "sourceRemainedRetiredUntilRecovery": True,
            "rollbackAfterRestoringTag": "verified",
        }

    def rejected_strategy(name, strategy):
        live.app(name)
        live.patch(lambda s: s.update(strategy=strategy), ready=False)
        source = live.get("dc")
        report = live.tool("generate", expected=1)
        assert json.loads(report.read_text())["totals"]["manual"] > 0
        assert live.get("dc")["spec"] == source["spec"] and not self_active_destination(
            live
        )
        return {"unsupportedStrategyOrHookRejected": True, "sourceUnchanged": True}

    cases.update(
        {
            "regular-pvc": volume,
            "docker-build": lambda: build_case("docker-build"),
            "s2i-build": lambda: build_case("s2i-build"),
            "unbuilt-binary": pending_build,
            "template": template,
            "changed-target": target_tamper,
            "foreign-name": collision,
            "foreign-controller-overlap": lambda: collision(True),
            "blocked-health": health_failure,
            "two-apps": multi_app,
            "disabled-image": paused_image,
            "added-hpa": added_hpa,
            "volume-overlap": volume_overlap,
            "failed-image-rollout": failed_image_rollout,
            "failed-initial-rollout": failed_initial_rollout,
            "orphan-recovery": lambda: failed_initial_rollout(orphan=True),
            "recreate-hook": lambda: rejected_strategy(
                "recreate-hook",
                {
                    "type": "Recreate",
                    "recreateParams": {
                        "post": {
                            "failurePolicy": "Abort",
                            "execNewPod": {
                                "containerName": "web",
                                "command": ["/bin/true"],
                            },
                        }
                    },
                },
            ),
            "custom-deployer": lambda: rejected_strategy(
                "custom-deployer",
                {
                    "type": "Custom",
                    "customParams": {"image": args.image, "command": ["/bin/true"]},
                },
            ),
        }
    )


if __name__ == "__main__":
    raise SystemExit(main())
