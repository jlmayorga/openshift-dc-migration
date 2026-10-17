#!/usr/bin/env python3
"""Build current-source candidates without consulting qualification evidence."""

import argparse
import datetime
import json
import os
import pathlib
import subprocess

from release_gate import (
    REPO_ROOT,
    dependency_digest,
    digest,
    production_source_digest,
    fixture_digest,
)


def build(output, platforms=("linux/amd64", "darwin/arm64")):
    out = pathlib.Path(output).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if list(out.glob("openshift-dc-converter-*")):
        raise ValueError("Candidate output must not contain old binaries")
    revision = subprocess.check_output(
        ["git", "rev-parse", "HEAD"], cwd=REPO_ROOT, text=True
    ).strip()
    date = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    metadata = dict(version="dev", commit=revision, buildDate=date)
    flags = "-s -w " + " ".join("-X main." + k + "=" + v for k, v in metadata.items())
    artifacts = {}
    for target in platforms:
        if target not in {
            "linux/amd64",
            "linux/arm64",
            "darwin/amd64",
            "darwin/arm64",
            "windows/amd64",
        }:
            raise ValueError("Unsupported build platform: " + target)
        goos, goarch = target.split("/")
        name = (
            "openshift-dc-converter-"
            + target.replace("/", "-")
            + (".exe" if goos == "windows" else "")
        )
        subprocess.run(
            [
                "go",
                "build",
                "-mod=readonly",
                "-trimpath",
                "-buildvcs=false",
                "-ldflags=" + flags,
                "-o",
                str(out / name),
                "./cmd/openshift-dc-converter",
            ],
            cwd=REPO_ROOT,
            env=dict(os.environ, CGO_ENABLED="0", GOOS=goos, GOARCH=goarch, GOFLAGS=""),
            check=True,
        )
        artifacts[name] = digest(out / name)
    (out / "SHA256SUMS").write_text(
        "".join(sha + "  " + name + "\n" for name, sha in sorted(artifacts.items()))
    )
    provenance = dict(
        kind="unqualified-candidates",
        revision=revision,
        workingTreeDirty=bool(
            subprocess.check_output(
                ["git", "status", "--porcelain"], cwd=REPO_ROOT, text=True
            ).strip()
        ),
        fixtureSHA256=fixture_digest(),
        dependencySHA256=dependency_digest(),
        productionSourceSHA256=production_source_digest(),
        build=metadata,
        artifacts=artifacts,
    )
    (out / "build-provenance.json").write_text(json.dumps(provenance, indent=2) + "\n")
    return provenance


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", default="dist")
    parser.add_argument("--platform", action="append")
    args = parser.parse_args()
    build(args.output, args.platform or ("linux/amd64", "darwin/arm64"))
