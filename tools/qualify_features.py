#!/usr/bin/env python3
"""Live ImageStream and statically backed PVC migration variants.

Creates test namespaces, ImageStreams, a namespace-scoped SCC binding and one
static hostPath PV/PVC on the existing single-node throwaway cluster. It never
uses a dynamic StorageClass or provisions cloud storage.
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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for key in (
        "context",
        "kubeconfig",
        "binary",
        "image",
        "second-image",
        "namespace-prefix",
        "output",
    ):
        parser.add_argument("--" + key, required=True)
    parser.add_argument("--allow-test-mutations", action="store_true")
    parser.add_argument(
        "--static-volume-test",
        action="store_true",
        help="Requires separate authorization for node hostPath storage and a namespace SCC binding",
    )
    parser.add_argument("--timeout", type=int, default=90)
    parser.add_argument(
        "--cases", help="Comma-separated variant names; omitted runs image variants"
    )
    args = parser.parse_args()
    if not args.allow_test_mutations or not re.fullmatch(
        r"dcmig-[a-z0-9-]+", args.namespace_prefix
    ):
        parser.error("Explicit disposable target and mutation acknowledgement required")
    if any("@sha256:" not in image for image in (args.image, args.second_image)):
        parser.error("Immutable images required")
    args.binary = str(pathlib.Path(args.binary).resolve())
    args.kubeconfig = str(pathlib.Path(args.kubeconfig).resolve())
    live = Live(args)

    def images(name, cross=False, multi=False, disabled=False):
        def mutate(spec):
            images_ns = live.ns
            if cross:
                images_ns += "-images"
                live.command(
                    live.base + ["create", "-f", "-"],
                    {
                        "apiVersion": "v1",
                        "kind": "Namespace",
                        "metadata": {
                            "name": images_ns,
                            "labels": {
                                "migration.openshift-dc.io/test-namespace": "true",
                                "migration.openshift-dc.io/configuration-run": live.run,
                            },
                        },
                    },
                )
            live.command(live.base + ["-n", images_ns, "create", "imagestream", "web"])
            for tag in (["main", "sidecar"] if multi else ["main"]):
                live.command(
                    live.base
                    + [
                        "-n",
                        images_ns,
                        "tag",
                        "--source=docker",
                        args.image,
                        "web:" + tag,
                    ]
                )
            spec["triggers"].append(
                {
                    "type": "ImageChange",
                    "imageChangeParams": {
                        "automatic": True,
                        "containerNames": ["web"],
                        "from": {
                            "kind": "ImageStreamTag",
                            "namespace": images_ns,
                            "name": "web:main",
                        },
                    },
                }
            )
            if multi:
                sidecar = copy.deepcopy(spec["template"]["spec"]["containers"][0])
                sidecar.update(
                    name="sidecar",
                    ports=[{"name": "sidecar", "containerPort": 8081}],
                    env=[
                        {"name": "PORT", "value": "8081"},
                        {"name": "SECOND_PORT", "value": "8889"},
                    ],
                )
                sidecar["readinessProbe"]["httpGet"]["port"] = 8081
                spec["template"]["spec"]["containers"].append(sidecar)
                spec["triggers"].append(
                    {
                        "type": "ImageChange",
                        "imageChangeParams": {
                            "automatic": True,
                            "containerNames": ["sidecar"],
                            "from": {"kind": "ImageStreamTag", "name": "web:sidecar"},
                        },
                    }
                )

        health = live.setup(name, mutate)
        if disabled:
            # The API clears lastTriggeredImage on create. Resolve the initial
            # image/rollout first, then disable updates on the existing source.
            live.oc(
                "patch",
                "dc",
                "web",
                "--type=json",
                "-p",
                json.dumps(
                    [
                        {
                            "op": "replace",
                            "path": "/spec/triggers/1/imageChangeParams/automatic",
                            "value": False,
                        }
                    ]
                ),
            )
            time.sleep(5)
        source = live.get("dc")
        plan = live.tool("plan", health=health)
        live.tool("stage", plan)
        assert (
            not live.get("deploy")["metadata"]
            .get("annotations", {})
            .get("image.openshift.io/triggers")
        )
        live.tool("cutover", plan)
        previous = live.get("deploy")["spec"]["template"]["spec"]["containers"]
        images_ns = live.ns + "-images" if cross else live.ns
        tag = "sidecar" if multi else "main"
        live.command(
            live.base
            + [
                "-n",
                images_ns,
                "tag",
                "--source=docker",
                args.second_image,
                "web:" + tag,
            ]
        )

        def containers():
            return live.get("deploy")["spec"]["template"]["spec"]["containers"]

        if disabled:
            time.sleep(8)
            assert (
                containers() == previous
            ), "Paused image trigger changed the destination"
        else:
            position = 1 if multi else 0
            live.wait(
                lambda: containers()[position]["image"] != previous[position]["image"]
            )
            live.oc(
                "rollout",
                "status",
                "deployment/web",
                "--timeout=" + str(args.timeout) + "s",
            )
            if multi:
                assert (
                    containers()[0]["image"] == previous[0]["image"]
                ), "Sidecar tag changed the main container"
                sidecar_image = containers()[1]["image"]
                live.oc("tag", "--source=docker", args.second_image, "web:main")
                live.wait(lambda: containers()[0]["image"] != previous[0]["image"])
                assert containers()[1]["image"] == sidecar_image
                live.oc(
                    "rollout",
                    "status",
                    "deployment/web",
                    "--timeout=" + str(args.timeout) + "s",
                )
        live.snapshot("trigger-update")
        for tag in (["main", "sidecar"] if multi else ["main"]):
            live.command(
                live.base
                + ["-n", images_ns, "tag", "--source=docker", args.image, "web:" + tag]
            )
        if not disabled:
            live.wait(
                lambda: all(
                    c["image"] == p["image"] for c, p in zip(containers(), previous)
                )
            )
        live.tool("rollback", plan)
        assert live.get("dc")["spec"] == source["spec"]
        return dict(
            crossNamespace=cross,
            multiContainer=multi,
            disabled=disabled,
            triggerUpdatesVerified=True,
            rollback="verified",
        )

    def volume():
        nodes = json.loads(
            live.command(live.base + ["get", "nodes", "-o", "json"]).stdout
        )["items"]
        assert (
            len(nodes) == 1
        ), "Static hostPath test requires this disposable single-node cluster"
        marker = "persistent-marker-" + live.run
        pv_name = args.namespace_prefix + "-static-pv"

        def mutate(spec):
            live.create("ServiceAccount", "v1", None, name="volume-test")
            live.create(
                "RoleBinding",
                "rbac.authorization.k8s.io/v1",
                None,
                name="volume-test",
                roleRef={
                    "apiGroup": "rbac.authorization.k8s.io",
                    "kind": "ClusterRole",
                    "name": "system:openshift:scc:hostmount-anyuid",
                },
                subjects=[
                    {
                        "kind": "ServiceAccount",
                        "name": "volume-test",
                        "namespace": live.ns,
                    }
                ],
            )
            live.command(
                live.base + ["create", "-f", "-"],
                {
                    "apiVersion": "v1",
                    "kind": "PersistentVolume",
                    "metadata": {
                        "name": pv_name,
                        "labels": {
                            "migration.openshift-dc.io/configuration-run": live.run
                        },
                    },
                    "spec": {
                        "capacity": {"storage": "1Mi"},
                        "accessModes": ["ReadWriteOnce"],
                        "persistentVolumeReclaimPolicy": "Retain",
                        "storageClassName": "",
                        "hostPath": {
                            "path": "/var/tmp/dcmig-" + live.run,
                            "type": "DirectoryOrCreate",
                        },
                    },
                },
            )
            live.create(
                "PersistentVolumeClaim",
                "v1",
                {
                    "volumeName": pv_name,
                    "storageClassName": "",
                    "accessModes": ["ReadWriteOnce"],
                    "resources": {"requests": {"storage": "1Mi"}},
                },
            )
            pod = spec["template"]["spec"]
            pod.update(
                serviceAccountName="volume-test",
                nodeSelector={"kubernetes.io/hostname": nodes[0]["metadata"]["name"]},
                securityContext={"runAsUser": 0},
                volumes=[
                    {"name": "data", "persistentVolumeClaim": {"claimName": "web"}}
                ],
            )
            container = pod["containers"][0]
            container.update(
                volumeMounts=[{"name": "data", "mountPath": "/data"}],
                command=[
                    "/bin/sh",
                    "-c",
                    "test -f /data/marker || echo "
                    + marker
                    + " > /data/marker; export RESPONSE=$(cat /data/marker); exec /hello_openshift",
                ],
            )

        health = live.setup("persistent-volume", mutate)
        source = live.get("dc")
        live.wait(lambda: live.get("pvc").get("status", {}).get("phase") == "Bound")
        plan = live.tool("plan", health=health)
        live.tool("stage", plan)
        samples, stop = [], threading.Event()

        def observe():
            while not stop.is_set():
                p = subprocess.run(
                    live.base
                    + [
                        "-n",
                        live.ns,
                        "get",
                        "pods",
                        "-l",
                        "deploymentconfig=web",
                        "-o",
                        "json",
                    ],
                    capture_output=True,
                    text=True,
                    timeout=15,
                )
                if p.returncode == 0:
                    pods = [
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
                                for x in pods
                                if "migration.openshift-dc.io/id"
                                not in x["metadata"].get("labels", {})
                            ],
                            "destination": [
                                x["metadata"]["uid"]
                                for x in pods
                                if "migration.openshift-dc.io/id"
                                in x["metadata"].get("labels", {})
                            ],
                        }
                    )
                stop.wait(0.2)

        thread = threading.Thread(target=observe, daemon=True)
        thread.start()
        responses = []
        try:
            live.tool("cutover", plan)
            responses.append(
                live.command(
                    ["curl", "-fsS", "--retry", "5", "--retry-delay", "1", health]
                ).stdout
            )
            live.tool("rollback", plan)
            responses.append(
                live.command(
                    ["curl", "-fsS", "--retry", "5", "--retry-delay", "1", health]
                ).stdout
            )
        finally:
            stop.set()
            thread.join(timeout=20)
            (live.root / "persistent-volume-timeline.json").write_text(
                json.dumps(samples, indent=2)
            )
        assert samples and not any(
            s["source"] and s["destination"] for s in samples
        ), "Concurrent exclusive Pods observed"
        assert any(s["source"] for s in samples) and any(
            s["destination"] for s in samples
        )
        assert all(
            marker in response for response in responses
        ), "Persistent data marker lost"
        assert live.get("dc")["spec"] == source["spec"]
        return dict(
            pv=pv_name,
            storage="static hostPath, no cloud storage provisioned",
            dataMarker=marker,
            routeResponses=responses,
            timelineSamples=len(samples),
            simultaneousPods=0,
            limitation="Single-node static volume; does not qualify a cloud CSI backend",
        )

    for name, options in [
        ("cross-namespace-image", {"cross": True}),
        ("multi-image", {"multi": True}),
        ("disabled-image", {"disabled": True}),
    ]:
        if args.cases and name not in args.cases.split(","):
            continue
        live.run_case(name, lambda name=name, options=options: images(name, **options))
    if args.static_volume_test:
        live.run_case("persistent-volume", volume)
    return 0 if live.cases and all(c["result"] == "passed" for c in live.cases) else 1


if __name__ == "__main__":
    raise SystemExit(main())
