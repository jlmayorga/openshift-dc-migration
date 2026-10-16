#!/usr/bin/env python3
"""Opt-in actual OpenShift qualification. Never uses an implicit context.

Only run against an explicitly authorized disposable namespace. This suite creates
workloads and an unencrypted test Route, temporarily removes/recreates an HPA,
scales workloads, and deletes only objects bearing this run's ownership label.
"""

import argparse
import datetime
import json
import os
import pathlib
import re
import signal
import subprocess
import sys
import tempfile
import time
import uuid
from release_gate import SCENARIOS, digest, tree_digest, dependency_digest

OWNER = "migration.openshift-dc.io/qualification-run"


def validate_target(context, namespace, ack):
    if (
        not context
        or not namespace
        or not re.fullmatch(r"dcmig-[a-z0-9-]+", namespace)
        or not ack
    ):
        raise ValueError(
            "Explicit --context, disposable dcmig-* --namespace and --allow-test-mutations are required"
        )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", required=True)
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--kubeconfig", required=True)
    parser.add_argument("--binary", required=True)
    parser.add_argument(
        "--image", required=True, help="Approved HTTP image listening on port 8080"
    )
    parser.add_argument(
        "--second-image",
        help="Second approved HTTP image for image-trigger verification",
    )
    parser.add_argument("--health-path", default="/")
    parser.add_argument("--output", required=True)
    parser.add_argument("--timeout", type=int, default=300)
    parser.add_argument("--allow-test-mutations", action="store_true")
    parser.add_argument(
        "--supplemental-evidence",
        help="Actual variant records for the exact binary/cluster; see qualification runbook",
    )
    parser.add_argument("--smoke-only", action="store_true")
    args = parser.parse_args()
    validate_target(args.context, args.namespace, args.allow_test_mutations)
    if "@sha256:" not in args.image or (
        args.second_image and "@sha256:" not in args.second_image
    ):
        raise ValueError(
            "Use approved immutable image digests for reproducible recovery"
        )
    if not args.health_path.startswith("/") or "?" in args.health_path:
        raise ValueError("Health path must be an absolute path without a query")
    binary = str(pathlib.Path(args.binary).resolve())
    kubeconfig = str(pathlib.Path(args.kubeconfig).resolve())
    run_id = str(uuid.uuid4())
    ns = args.namespace
    passed = set()
    limitations = []
    resource_kinds = [
        "hpa",
        "deployment",
        "dc",
        "service",
        "route",
        "imagestream",
        "resourcequota",
    ]
    record = {
        "kind": "actual-openshift",
        "schemaVersion": "v1",
        "result": "failed",
        "minor": None,
        "artifactSHA256": digest(binary),
        "fixtureSHA256": tree_digest("testdata"),
        "dependencySHA256": dependency_digest(),
        "revision": subprocess.check_output(
            ["git", "rev-parse", "HEAD"], text=True
        ).strip(),
        "runner": os.environ.get(
            "RUNNER_NAME", os.uname().nodename if hasattr(os, "uname") else "local"
        ),
        "runURL": os.environ.get("QUALIFICATION_RUN_URL", "local:" + run_id),
        "testedAt": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "scenarios": [],
        "limitations": limitations,
    }
    work = pathlib.Path(tempfile.mkdtemp(prefix="dcmig-qualification-"))
    os.chmod(work, 0o700)

    def command(argv, input_object=None, expected=0, timeout=None):
        proc = subprocess.run(
            argv,
            input=json.dumps(input_object) if input_object is not None else None,
            text=True,
            capture_output=True,
            timeout=timeout or args.timeout + 60,
        )
        with (work / "commands.log").open("a") as f:
            f.write(
                json.dumps(
                    {
                        "argv": argv,
                        "inputObject": input_object,
                        "exit": proc.returncode,
                        "stdout": proc.stdout,
                        "stderr": proc.stderr,
                    }
                )
                + "\n"
            )
        os.chmod(work / "commands.log", 0o600)
        if expected is not None and (proc.returncode == 0) != (expected == 0):
            raise RuntimeError(
                "Unexpected exit for "
                + argv[0]
                + " "
                + str(argv[1:])
                + "; inspect private commands.log"
            )
        return proc

    def oc(*words, input_object=None, expected=0):
        return command(
            [
                "oc",
                "--kubeconfig",
                kubeconfig,
                "--context",
                args.context,
                "--namespace",
                ns,
                *words,
            ],
            input_object,
            expected,
        )

    def obj(kind, name):
        return json.loads(oc("get", kind, name, "-o", "json").stdout)

    def create(kind, version, name, spec):
        body = {
            "apiVersion": version,
            "kind": kind,
            "metadata": {"name": name, "namespace": ns, "labels": {OWNER: run_id}},
            "spec": spec,
        }
        oc("create", "-f", "-", input_object=body)
        return obj(kind, name)

    def wait(check):
        end = time.monotonic() + args.timeout
        while time.monotonic() < end:
            if check():
                return
            time.sleep(1)
        raise RuntimeError("Actual-cluster condition timed out")

    def cleanup():
        # Inspect each object before deleting; no namespace deletion and no broad delete --all.
        for kind in resource_kinds:
            result = oc(
                "get", kind, "-l", OWNER + "=" + run_id, "-o", "json", expected=None
            )
            if result.returncode:
                continue
            for item in json.loads(result.stdout).get("items", []):
                if item["metadata"].get("labels", {}).get(OWNER) != run_id:
                    raise RuntimeError("Cleanup ownership mismatch")
                oc(
                    "delete",
                    kind,
                    item["metadata"]["name"],
                    "--wait=true",
                    "--timeout=" + str(args.timeout) + "s",
                )

    def prepare(image_trigger=False, hpa=False):
        existing = oc(
            "get",
            "dc,deploy,svc,route,hpa,imagestream,resourcequota",
            "web",
            "--ignore-not-found",
            "-o",
            "json",
            expected=None,
        )
        if existing.returncode or json.loads(existing.stdout or '{"items": []}').get(
            "items"
        ):
            raise RuntimeError("Test resource name web must be unused")
        triggers = [{"type": "ConfigChange"}]
        if image_trigger:
            create("ImageStream", "image.openshift.io/v1", "web", {})
            oc("tag", "--source=docker", args.image, "web:current")
            triggers.append(
                {
                    "type": "ImageChange",
                    "imageChangeParams": {
                        "automatic": True,
                        "containerNames": ["web"],
                        "from": {"kind": "ImageStreamTag", "name": "web:current"},
                    },
                }
            )
        create(
            "DeploymentConfig",
            "apps.openshift.io/v1",
            "web",
            {
                "replicas": 1,
                "selector": {"deploymentconfig": "web"},
                "strategy": {
                    "type": "Rolling",
                    "rollingParams": {"maxSurge": "25%", "maxUnavailable": "25%"},
                },
                "triggers": triggers,
                "template": {
                    "metadata": {"labels": {"deploymentconfig": "web", OWNER: run_id}},
                    "spec": {
                        "containers": [
                            {
                                "name": "web",
                                "image": args.image,
                                "env": [
                                    {"name": "PORT", "value": "8080"},
                                    {"name": "SECOND_PORT", "value": "8888"},
                                ],
                                "ports": [{"name": "web", "containerPort": 8080}],
                                "resources": {
                                    "requests": {"cpu": "10m", "memory": "32Mi"}
                                },
                                "readinessProbe": {
                                    "httpGet": {"port": 8080, "path": args.health_path}
                                },
                            }
                        ]
                    },
                },
            },
        )
        create(
            "Service",
            "v1",
            "web",
            {
                "selector": {"deploymentconfig": "web"},
                "ports": [{"name": "web", "port": 8080, "targetPort": "web"}],
            },
        )
        route = create(
            "Route",
            "route.openshift.io/v1",
            "web",
            {"to": {"kind": "Service", "name": "web"}, "port": {"targetPort": "web"}},
        )
        wait(
            lambda: obj("dc", "web").get("status", {}).get("availableReplicas", 0) >= 1
        )
        pods = json.loads(
            oc("get", "pods", "-l", "deploymentconfig=web", "-o", "json").stdout
        )["items"]
        if not pods or any(
            not p["metadata"].get("annotations", {}).get("openshift.io/scc")
            for p in pods
        ):
            raise RuntimeError("SCC admission evidence missing")
        record["sccEvidence"] = [
            {
                "uid": p["metadata"]["uid"],
                "scc": p["metadata"]["annotations"]["openshift.io/scc"],
                "securityContext": p["spec"].get("securityContext", {}),
            }
            for p in pods
        ]
        if hpa:
            create(
                "HorizontalPodAutoscaler",
                "autoscaling/v2",
                "web",
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
                                "target": {
                                    "type": "Utilization",
                                    "averageUtilization": 80,
                                },
                            },
                        }
                    ],
                },
            )
        # Availability can precede the deployer/controller's final status update.
        # Plans deliberately reject changed source resource versions.
        last_version = None
        stable_since = time.monotonic()

        def settled():
            nonlocal last_version, stable_since
            current = obj("dc", "web")["metadata"]["resourceVersion"]
            if current != last_version:
                last_version, stable_since = current, time.monotonic()
            return time.monotonic() - stable_since >= 5

        wait(settled)
        host = route["spec"].get("host") or obj("route", "web")["spec"]["host"]
        return "http://" + host + args.health_path

    def tool(mode, plan=None, health=None, overlap=False, expected=0, popen=False):
        output = work / str(uuid.uuid4())
        output.mkdir(mode=0o700)
        argv = [
            binary,
            "--context",
            args.context,
            "--kubeconfig",
            kubeconfig,
            "--mode",
            mode,
            "--output-dir",
            str(output),
            "--json-report",
            str(output / "report.json"),
            "--report-path",
            "",
            "--operation-timeout",
            str(args.timeout * 3) + "s",
            "--readiness-timeout",
            str(args.timeout) + "s",
        ]
        if plan:
            argv += ["--plan-file", str(plan)]
        else:
            argv += ["--projects", ns]
        if health:
            argv += ["--health-url", health]
        if overlap:
            argv += [
                "--cutover-mode",
                "overlap",
                "--overlap-justification",
                "Approved disposable stateless HTTP test workload",
            ]
        if mode == "cutover":
            argv += ["--automation-quiesced"]
        if popen:
            log = (output / "process.log").open("w")
            os.chmod(output / "process.log", 0o600)
            proc = subprocess.Popen(argv, stdout=log, stderr=subprocess.STDOUT)
            return proc, log
        command(argv, expected=expected)
        snapshot = oc(
            "get", "dc,deploy,rs,rc,pods,svc,endpoints,hpa,imagestream", "-o", "json"
        )
        (output / "cluster-after.json").write_text(snapshot.stdout)
        return (
            output / ns / "web.plan.json" if mode == "plan" else output / "report.json"
        )

    try:
        namespace = obj("namespace", ns)
        if (
            namespace["metadata"]
            .get("labels", {})
            .get("migration.openshift-dc.io/test-namespace")
            != "true"
        ):
            raise RuntimeError("Namespace lacks explicit test-namespace=true label")
        cv = obj("clusterversion", "version")
        record["openshiftPatch"] = cv["status"]["desired"]["version"]
        record["releaseImage"] = cv["status"]["desired"]["image"]
        record["minor"] = ".".join(record["openshiftPatch"].split(".")[:2])
        info = subprocess.check_output(["go", "version", "-m", binary], text=True)
        record["dependencyVersions"] = {
            parts[1]: parts[2]
            for line in info.splitlines()
            if len(parts := line.split()) >= 3
            and parts[0] == "dep"
            and parts[1] in ["k8s.io/api", "k8s.io/apimachinery", "k8s.io/client-go"]
        }
        record["kubernetesVersion"] = json.loads(oc("get", "--raw", "/version").stdout)[
            "gitVersion"
        ]
        if record["minor"] == "4.14" and not record["kubernetesVersion"].startswith(
            "v1.27."
        ):
            raise RuntimeError("Floor Kubernetes metadata mismatch")
        modes = ["exclusive"] if args.smoke_only else ["exclusive", "overlap"]
        for mode in modes:
            health = prepare(hpa=not args.smoke_only)
            plan = tool("plan", health=health, overlap=mode == "overlap")
            tool("dry-run", plan)
            if oc(
                "get", "deployment", "web", "--ignore-not-found", "-o", "name"
            ).stdout.strip():
                raise RuntimeError("Dry run persisted destination")
            passed.update({"schema", "dry-run", "admission-scc"})
            tool("stage", plan)
            d = obj("deployment", "web")
            if d["spec"]["replicas"] != 0 or not d["spec"]["paused"]:
                raise RuntimeError("Staging started destination")
            tool("stage", plan)
            passed.add("safe-rerun")
            tool("cutover", plan)
            passed.update({"cutover-" + mode, "service-routing"})
            if not args.smoke_only:
                if obj("hpa", "web")["spec"]["scaleTargetRef"]["kind"] != "Deployment":
                    raise RuntimeError("HPA target not updated")
                passed.add("hpa-retarget")
            tool("cutover", plan)
            tool("rollback", plan)
            passed.add("rollback")
            cleanup()
        if not args.smoke_only:
            health = prepare()
            plan = tool("plan", health=health)
            oc("annotate", "dc", "web", "qualification-change=" + run_id)
            tool("stage", plan, expected=1)
            passed.add("stale-plan")
            cleanup()
            # Quota rejection must happen before any source/HPA mutation.
            # This is preflight evidence, not an actual failed rollout.
            health = prepare(hpa=True)
            before_source, before_hpa = obj("dc", "web"), obj("hpa", "web")
            create("ResourceQuota", "v1", "web", {"hard": {"pods": "1"}})
            plan = tool("plan", health=health, overlap=True)
            tool("stage", plan)
            tool("preflight", plan, expected=1)
            tool("cutover", plan, expected=1)
            after_source, after_hpa = obj("dc", "web"), obj("hpa", "web")
            if (
                after_source["spec"] != before_source["spec"]
                or after_hpa["metadata"]["uid"] != before_hpa["metadata"]["uid"]
                or after_hpa["spec"] != before_hpa["spec"]
            ):
                raise RuntimeError("Constraint refusal mutated source or HPA")
            if (
                json.loads(pathlib.Path(plan).read_text())["progress"]["phase"]
                != "staged"
            ):
                raise RuntimeError("Constraint refusal advanced the cutover journal")
            passed.add("constraint-preflight")
            cleanup()
            health = prepare()
            plan = tool("plan", health=health)
            tool("stage", plan)
            proc, log = tool("cutover", plan, popen=True)
            try:
                interrupted = False
                end = time.monotonic() + args.timeout
                while proc.poll() is None and time.monotonic() < end:
                    try:
                        phase = json.loads(pathlib.Path(plan).read_text())["progress"][
                            "phase"
                        ]
                    except (OSError, json.JSONDecodeError):
                        phase = ""
                    if phase in [
                        "source-intent",
                        "source-updated",
                        "destination-intent",
                    ]:
                        proc.send_signal(signal.SIGTERM)
                        interrupted = True
                        break
                    time.sleep(0.01)
                proc.wait(timeout=args.timeout)
                if not interrupted or proc.returncode == 0:
                    raise RuntimeError(
                        "Interruption was not exercised; do not count it as passed"
                    )
            finally:
                if proc.poll() is None:
                    proc.terminate()
                    proc.wait(timeout=args.timeout)
                log.close()
            tool("rollback", plan)
            passed.add("interruption")
            cleanup()
            if args.second_image:
                health = prepare(image_trigger=True)
                plan = tool("plan", health=health)
                tool("stage", plan)
                tool("cutover", plan)
                previous = obj("deployment", "web")["spec"]["template"]["spec"][
                    "containers"
                ][0]["image"]
                oc("tag", "--source=docker", args.second_image, "web:current")
                wait(
                    lambda: obj("deployment", "web")["spec"]["template"]["spec"][
                        "containers"
                    ][0]["image"]
                    != previous
                )
                oc(
                    "rollout",
                    "status",
                    "deployment/web",
                    "--timeout=" + str(args.timeout) + "s",
                )
                oc("tag", "--source=docker", args.image, "web:current")
                wait(
                    lambda: obj("imagestreamtag", "web:current")["image"][
                        "dockerImageReference"
                    ]
                    == previous
                )
                tool("rollback", plan)
                passed.add("image-triggers")
                cleanup()
            else:
                limitations.append(
                    "Image-controller update needs --second-image; not run"
                )
        if args.supplemental_evidence:
            extras = json.loads(pathlib.Path(args.supplemental_evidence).read_text())
            for extra in extras:
                if (
                    extra.get("kind") != "actual-openshift"
                    or extra.get("result") != "passed"
                ):
                    raise RuntimeError(
                        "Variant evidence is not a passing actual-cluster result"
                    )
                for key in [
                    "artifactSHA256",
                    "revision",
                    "fixtureSHA256",
                    "dependencySHA256",
                    "openshiftPatch",
                    "kubernetesVersion",
                    "releaseImage",
                ]:
                    if extra.get(key) != record.get(key):
                        raise RuntimeError(
                            "Stale or different-target variant evidence: " + key
                        )
                if not extra.get("runURL") or not extra.get("testedAt"):
                    raise RuntimeError("Variant evidence lacks provenance")
                if extra.get("scenario") not in SCENARIOS:
                    raise RuntimeError("Unknown variant scenario")
                passed.add(extra["scenario"])
            record["variantEvidence"] = extras
        missing = SCENARIOS - passed
        if missing:
            limitations.append(
                "Outstanding actual scenarios: " + ", ".join(sorted(missing))
            )
        record["result"] = "passed" if not missing else "partial"
    except Exception as exc:
        record["error"] = str(exc)
    finally:
        # Keep failed-run objects for inspection/recovery; never clean up an uncertain migration automatically.
        record["scenarios"] = sorted(passed)
        record["privateArtifacts"] = str(work)
        output = pathlib.Path(args.output)
        output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        with output.open("w") as f:
            json.dump(record, f, indent=2)
        os.chmod(output, 0o600)
    print("Qualification " + record["result"] + "; private evidence: " + args.output)
    return (
        0
        if record["result"] == "passed"
        or (
            args.smoke_only
            and {
                "schema",
                "dry-run",
                "admission-scc",
                "cutover-exclusive",
                "service-routing",
                "safe-rerun",
                "rollback",
            }.issubset(passed)
        )
        else 1
    )


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, OSError) as exc:
        sys.exit(str(exc))
