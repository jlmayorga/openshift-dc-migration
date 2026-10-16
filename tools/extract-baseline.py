#!/usr/bin/env python3
"""Derive baseline from Kubernetes v1.27.16 api/openapi-spec/swagger.json.
Input is an official downloaded swagger file; output is deterministic and trimmed.
"""

import hashlib
import json
import sys

raw = open(sys.argv[1], "rb").read()
schema = json.loads(raw)
defs = schema["definitions"]
result = {}
keys = {
    "type",
    "format",
    "properties",
    "items",
    "additionalProperties",
    "required",
    "$ref",
    "enum",
    "minimum",
    "maximum",
    "allOf",
}


def trim(v):
    if isinstance(v, dict):
        return {
            k: ({n: trim(p) for n, p in x.items()} if k == "properties" else trim(x))
            for k, x in v.items()
            if k in keys
        }
    if isinstance(v, list):
        return [trim(x) for x in v]
    return v


def visit(name):
    if name in result:
        return
    result[name] = trim(defs[name])

    def refs(v):
        if isinstance(v, dict):
            if "$ref" in v:
                visit(v["$ref"].split("/")[-1])
            for x in v.values():
                refs(x)
        elif isinstance(v, list):
            for x in v:
                refs(x)

    refs(result[name])


visit("io.k8s.api.apps.v1.Deployment")
visit("io.k8s.api.apps.v1.ReplicaSetSpec")
visit("io.k8s.api.autoscaling.v2.HorizontalPodAutoscalerSpec")
json.dump(
    {
        "source": "https://raw.githubusercontent.com/kubernetes/kubernetes/v1.27.16/api/openapi-spec/swagger.json",
        "sha256": hashlib.sha256(raw).hexdigest(),
        "definitions": result,
    },
    open("compatibility/baseline-1.27.json", "w"),
    sort_keys=True,
    indent=2,
)
