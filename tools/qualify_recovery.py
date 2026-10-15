#!/usr/bin/env python3
"""Lose API responses after actual migration mutations, then verify recovery.

Uses an authenticated oc proxy bound only to loopback. The converter receives a
credential-free loopback kubeconfig. No credentials enter request evidence.
"""

import argparse
import copy
import http.client
import http.server
import json
import pathlib
import re
import subprocess
import threading

from qualify_configurations import Live


class FaultProxy(http.server.ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, port, evidence):
        super().__init__(("127.0.0.1", 0), Handler)
        self.upstream = port
        self.evidence = evidence
        self.after = 0
        self.count = 0
        self.lost = False
        self.requests = []
        self.lock = threading.Lock()


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def handle_request(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        with self.server.lock:
            if self.server.lost:
                self.send_error(503, "Qualification: API connection unavailable")
                return
        connection = http.client.HTTPConnection(
            "127.0.0.1", self.server.upstream, timeout=30
        )
        try:
            headers = {
                k: v
                for k, v in self.headers.items()
                if k.lower()
                not in {"host", "connection", "authorization", "accept-encoding"}
            }
            connection.request(
                self.command, self.path, body=body or None, headers=headers
            )
            response = connection.getresponse()
            data = response.read()
            mutation = (
                self.command in {"PUT", "POST", "DELETE", "PATCH"}
                and "dryRun=" not in self.path
                and "/selfsubjectaccessreviews" not in self.path
            )
            with self.server.lock:
                if mutation and 200 <= response.status < 300:
                    self.server.count += 1
                self.server.requests.append(
                    dict(
                        method=self.command,
                        path=self.path,
                        status=response.status,
                        mutation=mutation,
                        mutationNumber=self.server.count,
                    )
                )
                lose = (
                    mutation
                    and self.server.after == self.server.count
                    and response.status < 300
                )
                if lose:
                    self.server.lost = True
            if lose:
                # The real API write committed. Deliberately withhold its success response.
                self.close_connection = True
                self.connection.shutdown(2)
                return
            self.send_response(response.status)
            for k, v in response.getheaders():
                if k.lower() not in {
                    "content-length",
                    "connection",
                    "transfer-encoding",
                }:
                    self.send_header(k, v)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
        finally:
            connection.close()

    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = handle_request


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
    if "@sha256:" not in args.image:
        p.error("Immutable image required")
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
        line = upstream.stdout.readline()
        match = re.search(r"127\.0\.0\.1:(\d+)", line)
        if not match:
            raise RuntimeError("oc proxy did not start: " + line)
        proxy = FaultProxy(int(match[1]), live.root)
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

        def recover(number):
            proxy.after, proxy.count, proxy.lost, proxy.requests = 0, 0, False, []
            health = live.setup(f"write-{number}", lambda s: None)
            source = live.get("dc")
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
                                "target": {
                                    "type": "Utilization",
                                    "averageUtilization": 80,
                                },
                            },
                        }
                    ],
                },
            )
            plan = live.tool("plan", health=health)
            live.tool("stage", plan)
            proxy.after, proxy.count = number, 0
            failed_report = live.tool("cutover", plan, expected=1)
            assert (
                proxy.lost and proxy.count == number
            ), "Fault did not reach intended mutation"
            before = json.loads(plan.read_text())
            assert before["progress"]["phase"] != "complete"
            assert not json.loads(failed_report.read_text())["results"][0]["ready"]
            writes = copy.deepcopy(proxy.requests)
            (live.root / f"write-{number}-fault-requests.json").write_text(
                json.dumps(writes, indent=2)
            )
            (live.root / f"write-{number}-interrupted-plan.json").write_text(
                json.dumps(before, indent=2)
            )
            proxy.after, proxy.lost = 0, False
            live.tool("rollback", plan)
            assert live.get("dc")["spec"] == source["spec"]
            assert live.get("deploy")["spec"]["replicas"] == 0
            assert (
                live.get("hpa")["spec"]["scaleTargetRef"]["kind"] == "DeploymentConfig"
            )
            assert json.loads(plan.read_text())["progress"]["phase"] == "rolled-back"
            live.tool("rollback", plan)
            successful = [
                r for r in writes if r["mutation"] and 200 <= r["status"] < 300
            ]
            return {
                "lostResponseAfterMutation": number,
                "mutation": successful[-1],
                "interruptedPhase": before["progress"]["phase"],
                "recovery": "verified and rerun",
            }

        for number in range(1, 7):
            live.run_case(f"write-{number}", lambda number=number: recover(number))
            proxy.after, proxy.lost = 0, False
        return 0 if all(c["result"] == "passed" for c in live.cases) else 1
    finally:
        if proxy:
            proxy.shutdown()
            proxy.server_close()
        upstream.terminate()
        upstream.wait(timeout=15)


if __name__ == "__main__":
    raise SystemExit(main())
