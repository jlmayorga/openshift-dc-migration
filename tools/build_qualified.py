#!/usr/bin/env python3
"""Reproduce frozen binaries from immutable tested source and verified external inputs."""

import argparse
import contextlib
import io
import json
import os
import pathlib
import subprocess
import tarfile
import tempfile

from release_gate import (
    REPO_ROOT,
    dependency_digest,
    digest,
    evidence_path,
    load_scope,
    historical_production_source_digest,
    tree_digest,
    verify_scope,
)


@contextlib.contextmanager
def historical_source(revision):
    """Extract Git objects without changing the user's checkout or working tree."""
    with tempfile.TemporaryDirectory(prefix="dcmig-frozen-") as tmp:
        raw = subprocess.check_output(
            ["git", "archive", "--format=tar", revision], cwd=REPO_ROOT
        )
        with tarfile.open(fileobj=io.BytesIO(raw)) as archive:
            archive.extractall(tmp, filter="data")
        yield pathlib.Path(tmp)


@contextlib.contextmanager
def source_directory(source):
    previous = pathlib.Path.cwd()
    try:
        os.chdir(source)
        yield
    finally:
        os.chdir(previous)


def build_from_source(output, matrix, scope, evidence_root, source):
    evidence_root = pathlib.Path(evidence_root).resolve()
    evidence = [
        json.loads(evidence_path(evidence_root, item["evidence"]).read_text())
        for item in scope["artifacts"].values()
    ]
    expected = {name: item["sha256"] for name, item in scope["artifacts"].items()}
    out = pathlib.Path(output).resolve()
    with source_directory(source):
        hashes = (
            tree_digest(pathlib.Path(source) / "testdata"),
            dependency_digest(source),
            historical_production_source_digest(source),
        )
    verify_scope(
        matrix, scope, evidence, expected, *hashes, evidence_root=evidence_root
    )
    metadata = scope["build"]
    if (
        subprocess.check_output(["go", "env", "GOVERSION"], text=True).strip()
        != metadata["goVersion"]
    ):
        raise ValueError("Frozen candidate requires " + metadata["goVersion"])
    out.mkdir(parents=True, exist_ok=True)
    for name, item in scope["artifacts"].items():
        goos, goarch = item["platform"].split("/")
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
            cwd=source,
            env=dict(os.environ, CGO_ENABLED="0", GOOS=goos, GOARCH=goarch, GOFLAGS=""),
            check=True,
        )
        if digest(out / name) != item["sha256"]:
            raise ValueError("Build differs from live-qualified binary: " + name)
    actual = {
        p.name: digest(p) for p in out.glob("openshift-dc-converter-*") if p.is_file()
    }
    verify_scope(matrix, scope, evidence, actual, *hashes, evidence_root=evidence_root)
    (out / "SHA256SUMS").write_text(
        "".join(sha + "  " + name + "\n" for name, sha in sorted(actual.items()))
    )
    revision = subprocess.check_output(
        ["git", "rev-parse", "HEAD"], cwd=REPO_ROOT, text=True
    ).strip()
    (out / "build-provenance.json").write_text(
        json.dumps(
            dict(
                kind="frozen-reproduction",
                revision=scope["revision"],
                policyRevision=revision,
                scope=scope["name"],
                fixtureSHA256=hashes[0],
                dependencySHA256=hashes[1],
                productionSourceSHA256=hashes[2],
                build=metadata,
                artifacts=actual,
            ),
            indent=2,
        )
        + "\n"
    )
    (out / "build-metadata.txt").write_text(
        subprocess.check_output(["go", "version"], text=True)
        + subprocess.check_output(["go", "list", "-m", "all"], cwd=source, text=True)
    )


def build(output, evidence_root):
    matrix = json.loads((REPO_ROOT / "compatibility/matrix.json").read_text())
    scope = load_scope(matrix, evidence_root)
    with historical_source(scope["revision"]) as source:
        build_from_source(output, matrix, scope, evidence_root, source)
    print("Reproduced exact historical scoped binaries")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", default="dist")
    parser.add_argument("--evidence-root", required=True)
    args = parser.parse_args()
    build(args.output, args.evidence_root)
