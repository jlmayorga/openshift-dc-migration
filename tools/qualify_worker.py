#!/usr/bin/env python3
"""Exercise exclusive singleton ownership with a real Lease heartbeat.

Creates isolated namespace workloads and a service account allowed only to read
and update one test Lease. No storage, SCC grants or cluster roles are created.
"""

import argparse
import datetime
import json
import pathlib
import re
import signal
import subprocess
import threading
import time

from qualify_configurations import Live
from release_gate import dependency_digest, digest, fixture_digest

WORKER = r"""
set -eu
api="https://${KUBERNETES_SERVICE_HOST}:${KUBERNETES_SERVICE_PORT_HTTPS}"
sa=/var/run/secrets/kubernetes.io/serviceaccount
url="$api/apis/coordination.k8s.io/v1/namespaces/$POD_NAMESPACE/leases/worker"
token=$(cat "$sa/token")
get() { curl -fsS --connect-timeout 3 --max-time 5 --cacert "$sa/ca.crt" -H "Authorization: Bearer $token" "$url"; }
field() { printf '%s' "$current" | sed -n "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p"; }
write() {
  rv=$(field resourceVersion)
  stamp=$(date -u '+%Y-%m-%dT%H:%M:%S.000000Z')
  body="{\"apiVersion\":\"coordination.k8s.io/v1\",\"kind\":\"Lease\",\"metadata\":{\"name\":\"worker\",\"namespace\":\"$POD_NAMESPACE\",\"resourceVersion\":\"$rv\",\"labels\":{\"migration.openshift-dc.io/configuration-run\":\"$RUN_ID\"}},\"spec\":{\"holderIdentity\":\"$1\",\"leaseDurationSeconds\":20,\"renewTime\":\"$stamp\"}}"
  code=$(curl -sS --connect-timeout 3 --max-time 5 --cacert "$sa/ca.crt" -H "Authorization: Bearer $token" -H 'Content-Type: application/json' -X PUT --data-binary "$body" -o /tmp/lease-response -w '%{http_code}' "$url")
  case "$code" in
    200) return 0 ;;
    409) return 1 ;;
    *) cat /tmp/lease-response >&2; exit 45 ;;
  esac
}
release() {
  trap - TERM INT EXIT
  if [ -n "${server:-}" ]; then kill "$server" 2>/dev/null || true; fi
  for attempt in 1 2 3; do
    current=$(get) || continue
    owner=$(field holderIdentity)
    [ "$owner" = "$POD_UID" ] || break
    if write ''; then echo "released $POD_UID"; break; fi
  done
}
if [ "$FAIL_DESTINATION" = yes ] && [ -n "$MIGRATION_ID" ]; then
  echo 'injected singleton destination startup failure' >&2
  exit 42
fi
trap 'release; exit 0' TERM INT
trap release EXIT
server=''
while :; do
  current=$(get)
  owner=$(field holderIdentity)
  if [ -n "$owner" ] && [ "$owner" != "$POD_UID" ]; then
    echo "CONCURRENT_OWNER $owner $POD_UID" >&2
    exit 44
  fi
  if write "$POD_UID"; then
    if [ -z "$server" ]; then
      export RESPONSE="worker:$POD_UID"
      /hello_openshift &
      server=$!
    fi
    echo "heartbeat $POD_UID $stamp"
  fi
  sleep 1 & wait $! || true
done
"""


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
    parser.add_argument("--allow-test-mutations", action="store_true")
    parser.add_argument("--timeout", type=int, default=120)
    parser.add_argument("--cases", default="success,startup-failure,cancellation")
    args = parser.parse_args()
    selected = args.cases.split(",")
    if not args.allow_test_mutations or not re.fullmatch(
        r"dcmig-[a-z0-9-]+", args.namespace_prefix
    ):
        parser.error("Explicit disposable target and mutation acknowledgement required")
    if not selected or any(
        n not in {"success", "startup-failure", "cancellation"} for n in selected
    ):
        parser.error("Unknown or empty worker case selection")
    if "@sha256:" not in args.image:
        parser.error(
            "Immutable approved image with shell, curl and hello_openshift required"
        )
    args.binary = str(pathlib.Path(args.binary).resolve())
    args.kubeconfig = str(pathlib.Path(args.kubeconfig).resolve())
    live = Live(args)
    metadata = dict(
        kind="actual-openshift-singleton-worker",
        testedAt=datetime.datetime.now(datetime.timezone.utc).isoformat(),
        artifactSHA256=digest(args.binary),
        revisionAtInvocation=subprocess.check_output(
            ["git", "rev-parse", "HEAD"], text=True
        ).strip(),
        driverSHA256=digest(__file__),
        fixtureSHA256=fixture_digest(),
        dependencySHA256=dependency_digest(),
    )
    (live.root / "metadata.json").write_text(json.dumps(metadata, indent=2))

    def configure(spec, fail):
        live.create("ServiceAccount", "v1", None, name="worker")
        live.create(
            "Lease", "coordination.k8s.io/v1", {"holderIdentity": ""}, name="worker"
        )
        live.create(
            "Role",
            "rbac.authorization.k8s.io/v1",
            None,
            name="worker",
            rules=[
                {
                    "apiGroups": ["coordination.k8s.io"],
                    "resources": ["leases"],
                    "resourceNames": ["worker"],
                    "verbs": ["get", "update"],
                }
            ],
        )
        live.create(
            "RoleBinding",
            "rbac.authorization.k8s.io/v1",
            None,
            name="worker",
            roleRef={
                "apiGroup": "rbac.authorization.k8s.io",
                "kind": "Role",
                "name": "worker",
            },
            subjects=[
                {"kind": "ServiceAccount", "name": "worker", "namespace": live.ns}
            ],
        )
        pod = spec["template"]["spec"]
        pod.update(serviceAccountName="worker", terminationGracePeriodSeconds=15)
        container = pod["containers"][0]
        container["command"] = ["/bin/sh", "-ec", WORKER]
        container["env"] = [
            {
                "name": "POD_UID",
                "valueFrom": {"fieldRef": {"fieldPath": "metadata.uid"}},
            },
            {
                "name": "POD_NAMESPACE",
                "valueFrom": {"fieldRef": {"fieldPath": "metadata.namespace"}},
            },
            {
                "name": "MIGRATION_ID",
                "valueFrom": {
                    "fieldRef": {
                        "fieldPath": "metadata.labels['migration.openshift-dc.io/id']"
                    }
                },
            },
            {"name": "RUN_ID", "value": live.run},
            {"name": "FAIL_DESTINATION", "value": "yes" if fail else "no"},
        ]
        container["readinessProbe"]["initialDelaySeconds"] = 15

    def active(destination):
        observed = {}

        def check():
            lease = live.get("lease", "worker")["spec"]
            pods = json.loads(live.oc("get", "pods", "-o", "json").stdout)["items"]
            match = next(
                (
                    p
                    for p in pods
                    if p["metadata"]["uid"] == lease.get("holderIdentity")
                    and bool(
                        p["metadata"]
                        .get("labels", {})
                        .get("migration.openshift-dc.io/id")
                    )
                    == destination
                ),
                None,
            )
            if match and lease.get("renewTime"):
                if (
                    observed.get("uid") == match["metadata"]["uid"]
                    and observed["renewTime"] != lease["renewTime"]
                ):
                    return True
                observed.update(
                    uid=match["metadata"]["uid"],
                    renewTime=lease["renewTime"],
                    pod=match["metadata"]["name"],
                )
            return False

        live.wait(check)
        log = live.oc("logs", observed["pod"], "--tail=12").stdout
        assert "heartbeat " + observed["uid"] in log and "CONCURRENT_OWNER" not in log
        return observed

    def interrupt(plan, original_uid):
        folder = live.root / (live.case + "-interrupted-cutover")
        folder.mkdir(mode=0o700)
        argv = [
            args.binary,
            "--kubeconfig",
            args.kubeconfig,
            "--context",
            args.context,
            "--mode",
            "cutover",
            "--plan-file",
            str(plan),
            "--automation-quiesced",
            "--json-report",
            str(folder / "report.json"),
            "--report-path",
            "",
            "--readiness-timeout",
            str(args.timeout) + "s",
        ]
        with (folder / "stdout.log").open("w") as stdout, (folder / "stderr.log").open(
            "w"
        ) as stderr:
            process = subprocess.Popen(argv, stdout=stdout, stderr=stderr)
            try:
                destination = active(True)
                assert destination["uid"] != original_uid and process.poll() is None
                assert (
                    json.loads(plan.read_text())["progress"]["phase"]
                    == "destination-updated"
                )
                process.send_signal(signal.SIGTERM)
                code = process.wait(timeout=30)
            finally:
                if process.poll() is None:
                    process.terminate()
                    process.wait(timeout=30)
        (folder / "command.json").write_text(
            json.dumps({"argv": argv, "exit": code}, indent=2)
        )
        assert code != 0
        report = json.loads((folder / "report.json").read_text())
        assert not report["results"][0]["ready"]
        assert json.loads(plan.read_text())["progress"]["phase"] != "complete"
        live.snapshot("interrupted")
        return destination

    def run(name):
        health = live.setup(
            name, lambda spec: configure(spec, name == "startup-failure")
        )
        source = live.get("dc")
        initial = active(False)
        plan = live.tool("plan", health=health)
        live.tool("stage", plan)
        samples, errors, stop = [], [], threading.Event()
        ns = live.ns

        def observe():
            while not stop.is_set():
                try:
                    p = subprocess.run(
                        live.base + ["-n", ns, "get", "pods,leases", "-o", "json"],
                        capture_output=True,
                        text=True,
                        timeout=15,
                    )
                    if p.returncode:
                        errors.append(p.stderr)
                    else:
                        items = json.loads(p.stdout)["items"]
                        pods = [
                            x
                            for x in items
                            if x["kind"] == "Pod"
                            and x.get("status", {}).get("phase")
                            not in {"Succeeded", "Failed"}
                            and not x["metadata"]["name"].endswith("-deploy")
                        ]
                        lease = next(x for x in items if x["kind"] == "Lease")["spec"]
                        samples.append(
                            dict(
                                at=datetime.datetime.now(
                                    datetime.timezone.utc
                                ).isoformat(),
                                source=[
                                    x["metadata"]["uid"]
                                    for x in pods
                                    if "migration.openshift-dc.io/id"
                                    not in x["metadata"].get("labels", {})
                                ],
                                destination=[
                                    x["metadata"]["uid"]
                                    for x in pods
                                    if "migration.openshift-dc.io/id"
                                    in x["metadata"].get("labels", {})
                                ],
                                holder=lease.get("holderIdentity"),
                                renewTime=lease.get("renewTime"),
                            )
                        )
                except (subprocess.TimeoutExpired, ValueError, StopIteration) as exc:
                    errors.append(str(exc))
                stop.wait(0.3)

        thread = threading.Thread(target=observe, daemon=True)
        thread.start()
        destination = None
        try:
            if name == "cancellation":
                destination = interrupt(plan, initial["uid"])
            elif name == "startup-failure":
                report = json.loads(
                    live.tool(
                        "cutover",
                        plan,
                        expected=1,
                        extra=("--readiness-timeout", "20s"),
                    ).read_text()
                )
                assert report["results"][0]["constraints"]["status"] == "passed"
                assert not report["results"][0]["ready"]
                assert (
                    json.loads(plan.read_text())["progress"]["phase"]
                    == "destination-updated"
                )
                pods = json.loads(live.oc("get", "pods", "-o", "json").stdout)["items"]
                assert any(
                    s.get("terminated", {}).get("exitCode") == 42
                    for p in pods
                    for c in p.get("status", {}).get("containerStatuses", [])
                    for s in (c.get("state", {}), c.get("lastState", {}))
                )
            else:
                live.tool("cutover", plan)
                destination = active(True)
                response = live.command(
                    ["curl", "-fsS", "--max-time", "10", health]
                ).stdout
                assert "worker:" + destination["uid"] in response
            assert live.get("dc")["spec"]["replicas"] == 0
            live.tool("rollback", plan)
            restored = active(False)
            assert restored["uid"] != initial["uid"]
            assert live.get("dc")["spec"] == source["spec"]
            assert live.get("dc")["metadata"]["uid"] == source["metadata"]["uid"]
            assert live.get("deploy")["spec"]["replicas"] == 0
            response = live.command(["curl", "-fsS", "--max-time", "10", health]).stdout
            assert "worker:" + restored["uid"] in response
            live.tool("rollback", plan)
        finally:
            stop.set()
            thread.join(timeout=20)
            (live.root / (name + "-timeline.json")).write_text(
                json.dumps({"samples": samples, "readErrors": errors}, indent=2)
            )
        assert samples and not errors
        assert not any(s["source"] and s["destination"] for s in samples)
        assert any(s["holder"] == initial["uid"] for s in samples)
        assert any(s["holder"] == restored["uid"] for s in samples)
        if destination:
            assert any(s["holder"] == destination["uid"] for s in samples)
        return dict(
            sourceUID=source["metadata"]["uid"],
            initialWorker=initial,
            destinationWorker=destination,
            restoredWorker=restored,
            heartbeatSamples=len(samples),
            concurrentPodsObserved=0,
            readErrors=0,
            sourceSpecAndHTTPRecovered=True,
            recoveryRerun=True,
        )

    for name in selected:
        live.run_case(name, lambda name=name: run(name))
    return 0 if all(c["result"] == "passed" for c in live.cases) else 1


if __name__ == "__main__":
    raise SystemExit(main())
