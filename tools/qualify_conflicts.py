#!/usr/bin/env python3
"""Exercise source retries using real competing writes on a disposable OpenShift.

A credential-free loopback proxy issues actual status/spec/UID changes between
converter reads and updates. Conflict responses come from the real API server.
No synthetic409 response is used. Requires explicit disposable-cluster consent.
"""

import argparse
import copy
import hashlib
import http.client
import http.server
import json
import pathlib
import re
import signal
import socket
import subprocess
import threading
import time
from urllib.parse import parse_qs, urlsplit

from qualify_configurations import Live


def fingerprint(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True).encode()).hexdigest()


class ConflictProxy(http.server.ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, upstream):
        super().__init__(("127.0.0.1", 0), Handler)
        self.upstream = upstream
        self.plan = None
        self.namespace = None
        self.expected_uid = None
        self.stage = "dry-run"
        self.change = "status"
        self.limit = 0
        self.injections = []
        self.requests = []
        self.armed = False
        self.uncertain_sent = False

    def request(self, method, path, body=None, headers=None):
        connection = http.client.HTTPConnection("127.0.0.1", self.upstream, timeout=40)
        try:
            connection.request(
                method,
                path,
                body=body,
                headers=headers or {"Content-Type": "application/json"},
            )
            response = connection.getresponse()
            return response.status, response.getheaders(), response.read()
        finally:
            connection.close()

    def object_request(self, method, path, body=None):
        status, _, data = self.request(
            method, path, json.dumps(body).encode() if body is not None else None
        )
        assert 200 <= status < 300, (method, path, status, data.decode()[:1000])
        return json.loads(data)

    def interfere(self, path):
        before = self.object_request("GET", path)
        assert before["metadata"]["uid"] == self.expected_uid
        changed = copy.deepcopy(before)
        if self.change == "status":
            changed.setdefault("status", {}).setdefault("details", {})["message"] = (
                "qualification-conflict-" + str(len(self.injections))
            )
            after = self.object_request("PUT", path + "/status", changed)
            assert after["spec"] == before["spec"]
        elif self.change == "deep-spec":
            changed["spec"]["strategy"]["rollingParams"]["updatePeriodSeconds"] = 2
            after = self.object_request("PUT", path, changed)
        elif self.change == "migration-state":
            changed["spec"]["replicas"] = 0
            changed["spec"]["triggers"] = []
            changed["spec"].pop("paused", None)
            after = self.object_request("PUT", path, changed)
        elif self.change == "uid":
            assert before["spec"]["replicas"] == 0
            self.object_request(
                "DELETE",
                path,
                {
                    "apiVersion": "v1",
                    "kind": "DeleteOptions",
                    "preconditions": {"uid": self.expected_uid},
                    "propagationPolicy": "Background",
                },
            )
            deadline = time.monotonic() + 30
            while self.request("GET", path)[0] != 404:
                assert time.monotonic() < deadline
                time.sleep(0.1)
            replacement = {
                "apiVersion": before["apiVersion"],
                "kind": before["kind"],
                "metadata": {
                    "name": "web",
                    "namespace": self.namespace,
                    "labels": before["metadata"].get("labels", {}),
                },
                "spec": before["spec"],
            }
            after = self.object_request("POST", path.rsplit("/", 1)[0], replacement)
            assert after["metadata"]["uid"] != self.expected_uid
        else:
            raise AssertionError(self.change)
        assert (
            after["metadata"]["resourceVersion"]
            != before["metadata"]["resourceVersion"]
        )
        record = {
            "change": self.change,
            "before": before,
            "after": after,
            "beforeSpecSHA256": fingerprint(before["spec"]),
            "afterSpecSHA256": fingerprint(after["spec"]),
        }
        self.injections.append(record)
        return len(self.injections)


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def handle_request(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        parsed = urlsplit(self.path)
        dry = "dryRun" in parse_qs(parsed.query)
        injected = None
        try:
            if (
                self.server.armed
                and self.command == "PUT"
                and parsed.path
                == f"/apis/apps.openshift.io/v1/namespaces/{self.server.namespace}/deploymentconfigs/web"
                and dry == (self.server.stage == "dry-run")
                and len(self.server.injections) < self.server.limit
                and json.loads(self.server.plan.read_text())["progress"]["phase"]
                == "source-intent"
            ):
                if not self.server.change.startswith("uncertain-"):
                    injected = self.server.interfere(parsed.path)
            headers = {
                k: v
                for k, v in self.headers.items()
                if k.lower()
                not in {"host", "connection", "authorization", "accept-encoding"}
            }
            status, output_headers, data = self.server.request(
                self.command, self.path, body or None, headers
            )
            uncertain = (
                self.server.armed
                and self.server.change.startswith("uncertain-")
                and self.command == "PUT"
                and not dry
                and parsed.path
                == f"/apis/apps.openshift.io/v1/namespaces/{self.server.namespace}/deploymentconfigs/web"
                and 200 <= status < 300
            )
            self.server.requests.append(
                {
                    "method": self.command,
                    "path": self.path,
                    "status": status,
                    "injection": injected,
                    "afterUncertainResponse": self.server.uncertain_sent,
                    "responseWithheld": uncertain,
                    "requestSHA256": hashlib.sha256(body).hexdigest(),
                }
            )
            if uncertain:
                after = json.loads(data)
                self.server.injections.append(
                    {
                        "change": self.server.change,
                        "committedResponse": after,
                        "upstreamStatus": status,
                    }
                )
                self.server.armed = False
                self.server.uncertain_sent = True
                if self.server.change == "uncertain-eof":
                    self.close_connection = True
                    self.connection.shutdown(socket.SHUT_RDWR)
                    return
                status = 504
                data = json.dumps(
                    {
                        "apiVersion": "v1",
                        "kind": "Status",
                        "status": "Failure",
                        "reason": "Timeout",
                        "code": 504,
                        "message": "Qualification: committed source response withheld",
                    }
                ).encode()
                output_headers = [("Content-Type", "application/json")]
                if self.server.change == "uncertain-retry-after":
                    output_headers.append(("Retry-After", "0"))
            self.send_response(status)
            for key, value in output_headers:
                if key.lower() not in {
                    "content-length",
                    "connection",
                    "transfer-encoding",
                }:
                    self.send_header(key, value)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
        except Exception as exc:
            self.server.requests.append(
                {"method": self.command, "path": self.path, "injectionError": str(exc)}
            )
            self.send_error(500, "Qualification injection failed")

    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = handle_request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in [
        "kubeconfig",
        "context",
        "binary",
        "image",
        "namespace-prefix",
        "output",
    ]:
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--timeout", type=int, default=180)
    parser.add_argument("--allow-test-mutations", action="store_true")
    parser.add_argument("--cases")
    args = parser.parse_args()
    if (
        not args.allow_test_mutations
        or not re.fullmatch(r"dcmig-[a-z0-9-]+", args.namespace_prefix)
        or "@sha256:" not in args.image
    ):
        parser.error(
            "Explicit disposable target, mutation consent and immutable image required"
        )
    args.binary = str(pathlib.Path(args.binary).resolve())
    args.kubeconfig = str(pathlib.Path(args.kubeconfig).resolve())
    live = Live(args)
    upstream = subprocess.Popen(
        live.base
        + ["proxy", "--address=127.0.0.1", "--port=0", "--disable-filter=true"],
        stdout=subprocess.PIPE,
        stderr=(live.root / "oc-proxy.log").open("w"),
        text=True,
    )
    proxy = None
    try:
        match = re.search(r"127\.0\.0\.1:(\d+)", upstream.stdout.readline())
        assert match, "oc proxy failed to start"
        proxy = ConflictProxy(int(match[1]))
        threading.Thread(target=proxy.serve_forever, daemon=True).start()
        config = live.root / "loopback-kubeconfig.json"
        config.write_text(
            json.dumps(
                {
                    "apiVersion": "v1",
                    "kind": "Config",
                    "current-context": args.context,
                    "contexts": [
                        {
                            "name": args.context,
                            "context": {
                                "cluster": "loopback",
                                "user": "no-credentials",
                            },
                        }
                    ],
                    "clusters": [
                        {
                            "name": "loopback",
                            "cluster": {
                                "server": f"http://127.0.0.1:{proxy.server_port}"
                            },
                        }
                    ],
                    "users": [{"name": "no-credentials", "user": {}}],
                }
            )
        )
        live.args.kubeconfig = str(config)

        def run(stage, count, change, cancel=False):
            proxy.armed = False
            zero = change == "uid"
            health = live.setup(
                live.case, lambda spec: spec.update(replicas=0) if zero else None
            )
            source = live.get("dc")
            plan = live.tool("plan", health=health)
            live.tool("stage", plan)
            destination = live.get("deployment")
            proxy.plan, proxy.namespace, proxy.expected_uid = (
                plan,
                live.ns,
                source["metadata"]["uid"],
            )
            proxy.stage, proxy.limit, proxy.change = stage, count, change
            proxy.requests, proxy.injections, proxy.armed = [], [], True
            proxy.uncertain_sent = False
            success = change == "status" and count < 5 and not cancel
            try:
                if cancel:
                    folder = live.root / (live.case + "-cancelled")
                    folder.mkdir()
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
                    ]
                    with (folder / "stdout.log").open("w") as stdout, (
                        folder / "stderr.log"
                    ).open("w") as stderr:
                        process = subprocess.Popen(argv, stdout=stdout, stderr=stderr)
                        try:
                            deadline = time.monotonic() + 60
                            while not any(
                                "conflict; retry" in event
                                for event in json.loads(plan.read_text())["progress"][
                                    "events"
                                ]
                            ):
                                assert (
                                    process.poll() is None
                                    and time.monotonic() < deadline
                                )
                                time.sleep(0.005)
                            process.send_signal(signal.SIGTERM)
                            status = process.wait(timeout=30)
                            assert status != 0
                        finally:
                            if process.poll() is None:
                                process.terminate()
                                process.wait(timeout=30)
                    (folder / "command.json").write_text(
                        json.dumps({"argv": argv, "exit": status}, indent=2)
                    )
                    report = json.loads((folder / "report.json").read_text())
                else:
                    report = json.loads(
                        live.tool(
                            "cutover", plan, expected=0 if success else 1
                        ).read_text()
                    )
            finally:
                proxy.armed = False
                (live.root / (live.case + "-requests.json")).write_text(
                    json.dumps(proxy.requests, indent=2)
                )
                (live.root / (live.case + "-injections.json")).write_text(
                    json.dumps(proxy.injections, indent=2)
                )
                (live.root / (live.case + "-interrupted-plan.json")).write_text(
                    plan.read_text()
                )
            if change.startswith("uncertain-"):
                assert (
                    len(proxy.injections) == 1
                    and proxy.injections[0]["upstreamStatus"] == 200
                )
                assert not any(
                    item.get("afterUncertainResponse") for item in proxy.requests
                ), proxy.requests
                assert (
                    "state may be uncertain" in report["results"][0]["error"]
                    and not report["results"][0]["ready"]
                )
                assert (
                    json.loads(plan.read_text())["progress"]["phase"] == "source-intent"
                )
                assert (
                    live.get("dc")["spec"]
                    == proxy.injections[0]["committedResponse"]["spec"]
                )
                assert (
                    live.get("dc")["spec"]["triggers"] == []
                    and live.get("dc")["spec"]["replicas"] == 1
                )
                assert (
                    live.get("deployment")["spec"]["replicas"] == 0
                    and live.get("deployment")["spec"]["paused"]
                )
                live.tool("rollback", plan)
                live.tool("rollback", plan)
                assert live.get("dc")["spec"] == source["spec"]
                return {
                    "committedSourceWrite": True,
                    "withheldResponse": change,
                    "automaticRetryAfterUnknownResponse": False,
                    "explicitRollbackAndRerun": True,
                    "fullOriginalSpecRecovered": True,
                }
            injected = [item for item in proxy.requests if item.get("injection")]
            assert injected and len(injected) == len(proxy.injections)
            assert all(item["status"] == 409 for item in injected), injected
            assert all("injectionError" not in item for item in proxy.requests)
            if cancel:
                assert len(injected) < 5
            else:
                assert len(injected) == count
            journal = json.loads(plan.read_text())
            retries = [
                event
                for event in journal["progress"]["events"]
                if "conflict; retry" in event
            ]
            assert retries
            if success:
                assert (
                    report["results"][0]["ready"]
                    and journal["progress"]["phase"] == "complete"
                )
            else:
                assert (
                    not report["results"][0]["ready"]
                    and journal["progress"]["phase"] == "source-intent"
                )
                assert (
                    live.get("deployment")["metadata"]["uid"]
                    == destination["metadata"]["uid"]
                )
                assert (
                    live.get("deployment")["spec"]["paused"]
                    and live.get("deployment")["spec"]["replicas"] == 0
                )
                if change == "status":
                    assert live.get("dc")["spec"] == source["spec"]
                    if not cancel:
                        assert (
                            "5 attempts" in report["results"][0]["error"]
                            and len(retries) == 4
                        )
                else:
                    error = report["results"][0]["error"]
                    assert (
                        "source replaced"
                        if zero
                        else "spec changed during conflict retry"
                    ) in error, error
                    changed = live.get("dc")
                    assert changed["spec"] == proxy.injections[-1]["after"]["spec"]
                    live.tool(
                        "rollback",
                        plan,
                        expected=1 if change in {"uid", "deep-spec"} else 0,
                    )
                    if change in {"uid", "deep-spec"}:
                        assert (
                            live.get("dc")["metadata"]["uid"]
                            == changed["metadata"]["uid"]
                        )
                        assert live.get("dc")["spec"] == changed["spec"]
                    if zero:
                        return {
                            "actualAPIServerConflicts": len(injected),
                            "replacementUIDPreserved": changed["metadata"]["uid"],
                            "replacementReplicas": 0,
                            "automaticRecoveryRefused": True,
                            "retryEvents": retries,
                        }
                    # Explicit fixture repair after proving automatic recovery refuses foreign semantics.
                    if change == "deep-spec":
                        current = live.get("dc")
                        current["spec"] = copy.deepcopy(source["spec"])
                        live.oc("replace", "-f", "-", body=current)
            live.tool("rollback", plan)
            live.tool("rollback", plan)
            assert live.get("dc")["metadata"]["uid"] == source["metadata"]["uid"]
            assert live.get("dc")["spec"] == source["spec"]
            assert (
                live.get("deployment")["spec"]["paused"]
                and live.get("deployment")["spec"]["replicas"] == 0
            )
            return {
                "actualAPIServerConflicts": len(injected),
                "statusOnly": change == "status",
                "cutoverPassedWithoutCLIRestart": success,
                "retryEvents": retries,
                "explicitRollbackAndRerun": True,
                "fullOriginalSpecRecovered": True,
            }

        cases = [
            (f"{stage}-{count}", stage, count, "status", False)
            for stage in ["dry-run", "write"]
            for count in [1, 2, 5]
        ]
        cases += [
            (f"{stage}-{change}", stage, 1, change, False)
            for stage in ["dry-run", "write"]
            for change in ["deep-spec", "migration-state", "uid"]
        ]
        cases += [("cancel-backoff", "dry-run", 5, "status", True)]
        cases += [
            (change, "write", 1, change, False)
            for change in [
                "uncertain-timeout",
                "uncertain-eof",
                "uncertain-retry-after",
            ]
        ]
        for name, stage, count, change, cancel in cases:
            if args.cases and name not in args.cases.split(","):
                continue
            live.case = name
            live.run_case(
                name,
                lambda stage=stage, count=count, change=change, cancel=cancel: run(
                    stage, count, change, cancel
                ),
            )
        return (
            0
            if live.cases and all(item["result"] == "passed" for item in live.cases)
            else 1
        )
    finally:
        if proxy:
            proxy.shutdown()
            proxy.server_close()
        upstream.terminate()
        upstream.wait(timeout=15)


if __name__ == "__main__":
    raise SystemExit(main())
