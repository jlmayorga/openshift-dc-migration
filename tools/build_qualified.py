#!/usr/bin/env python3
"""Reproduce only the frozen binaries covered by the active maintenance scope."""

import argparse
import json
import os
import pathlib
import subprocess

from release_gate import (
    dependency_digest,
    digest,
    load_scope,
    production_source_digest,
    tree_digest,
    verify_scope,
)


def build(output):
    matrix = json.loads(pathlib.Path("compatibility/matrix.json").read_text())
    scope = load_scope(matrix)
    evidence = [
        json.loads(pathlib.Path(item["evidence"]).read_text())
        for item in scope["artifacts"].values()
    ]
    expected = {name: item["sha256"] for name, item in scope["artifacts"].items()}
    hashes = (tree_digest("testdata"), dependency_digest(), production_source_digest())
    # Validate scope and evidence before spending time building. Actual output
    # bytes are independently checked below, never relabelled as the old hash.
    verify_scope(matrix, scope, evidence, expected, *hashes)
    metadata = scope["build"]
    version = subprocess.check_output(["go", "env", "GOVERSION"], text=True).strip()
    if version != metadata["goVersion"]:
        raise ValueError("Frozen candidate requires " + metadata["goVersion"])
    out = pathlib.Path(output)
    out.mkdir(parents=True, exist_ok=True)
    for name, item in scope["artifacts"].items():
        goos, goarch = item["platform"].split("/")
        env = dict(os.environ, CGO_ENABLED="0", GOOS=goos, GOARCH=goarch, GOFLAGS="")
        subprocess.run(
            [
                "go",
                "build",
                "-mod=readonly",
                "-trimpath",
                "-buildvcs=false",
                "-ldflags=" + metadata["ldflags"],
                "-o",
                str(out / name),
                ".",
            ],
            env=env,
            check=True,
        )
        if digest(out / name) != item["sha256"]:
            raise ValueError("Build differs from live-qualified binary: " + name)
    actual = {
        p.name: digest(p) for p in out.glob("openshift-dc-converter-*") if p.is_file()
    }
    verify_scope(matrix, scope, evidence, actual, *hashes)
    (out / "SHA256SUMS").write_text(
        "".join(sha + "  " + name + "\n" for name, sha in sorted(actual.items()))
    )
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    (out / "build-provenance.json").write_text(
        json.dumps(
            {
                "revision": scope["revision"],
                "policyRevision": revision,
                "scope": scope["name"],
                "fixtureSHA256": hashes[0],
                "dependencySHA256": hashes[1],
                "productionSourceSHA256": hashes[2],
                "build": metadata,
                "artifacts": actual,
            },
            indent=2,
        )
        + "\n"
    )
    (out / "build-metadata.txt").write_text(
        subprocess.check_output(["go", "version"], text=True)
        + subprocess.check_output(["go", "list", "-m", "all"], text=True)
    )
    print("Reproduced both live-qualified maintenance binaries byte for byte")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", default="dist")
    args = parser.parse_args()
    build(args.output)
