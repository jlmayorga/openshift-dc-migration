#!/usr/bin/env python3
"""Run explicitly selected candidates on a configured disposable native runner."""

import hashlib
import io
import json
import os
import pathlib
import platform
import subprocess
import tarfile
import urllib.request

from qualify_openshift import validate_target
from release_gate import (
    REPO_ROOT,
    digest,
    dependency_digest,
    fixture_digest,
    production_source_digest,
)


def validate_candidate_source(provenance):
    expected = {
        "revision": subprocess.check_output(
            ["git", "rev-parse", "HEAD"], cwd=REPO_ROOT, text=True
        ).strip(),
        "fixtureSHA256": fixture_digest(),
        "dependencySHA256": dependency_digest(),
        "productionSourceSHA256": production_source_digest(),
    }
    if any(provenance.get(key) != value for key, value in expected.items()):
        raise ValueError(
            "Candidate provenance does not match the selected source checkout"
        )


def validate_configuration(target, selected_platform, provenance, candidates):
    validate_target(target.get("context"), target.get("namespace"), True)
    native = (
        platform.system().lower()
        + "/"
        + {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(
            platform.machine(), platform.machine()
        )
    )
    if selected_platform != native:
        raise ValueError("Selected candidate must execute on its native platform")
    if (
        not target.get("ocURL", "").startswith("https://")
        or len(target.get("ocSHA256", "")) != 64
    ):
        raise ValueError("Explicit HTTPS ocURL and ocSHA256 are required")
    name = "openshift-dc-converter-" + selected_platform.replace("/", "-")
    binary = pathlib.Path(candidates) / name
    if provenance.get("artifacts", {}).get(name) != digest(binary):
        raise ValueError("Candidate differs from build provenance")
    validate_candidate_source(provenance)
    return binary


def main():
    selected = os.environ["TARGET_PLATFORM"]
    target = json.loads(os.environ["TEST_TARGETS"])[os.environ["TARGET_MINOR"]][
        selected.replace("/", "-")
    ]
    provenance = json.loads(
        pathlib.Path("candidates/build-provenance.json").read_text()
    )
    binary = validate_configuration(
        target, selected, provenance, "candidates"
    ).resolve()
    if not os.environ.get("TEST_KUBECONFIG") or not os.environ.get("TEST_IMAGE"):
        raise ValueError(
            "Explicit test credentials and immutable test image are required"
        )
    # Install only the checksum-pinned client executable, never archive paths.
    raw = urllib.request.urlopen(target["ocURL"], timeout=60).read()
    if hashlib.sha256(raw).hexdigest() != target["ocSHA256"]:
        raise ValueError("oc archive checksum mismatch")
    work = pathlib.Path(os.environ["RUNNER_TEMP"])
    client = work / "oc"
    with tarfile.open(fileobj=io.BytesIO(raw)) as archive:
        members = [
            m
            for m in archive.getmembers()
            if pathlib.PurePosixPath(m.name).name == "oc" and m.isfile()
        ]
        if len(members) != 1:
            raise ValueError("oc archive must contain exactly one client")
        client.write_bytes(archive.extractfile(members[0]).read())
    client.chmod(0o700)
    config = work / "qualification-kubeconfig"
    config.touch(mode=0o600)
    config.write_text(os.environ["TEST_KUBECONFIG"])
    binary.chmod(0o700)
    argv = [
        "python3",
        "tools/qualify_openshift.py",
        "--context",
        target["context"],
        "--namespace",
        target["namespace"],
        "--kubeconfig",
        str(config),
        "--binary",
        str(binary),
        "--image",
        os.environ["TEST_IMAGE"],
        "--output",
        "evidence/actual.json",
        "--allow-test-mutations",
    ]
    if os.environ.get("SECOND_TEST_IMAGE"):
        argv += ["--second-image", os.environ["SECOND_TEST_IMAGE"]]
    if target.get("supplementalEvidence"):
        argv += ["--supplemental-evidence", target["supplementalEvidence"]]
    try:
        subprocess.run(
            argv,
            env=dict(os.environ, PATH=str(work) + os.pathsep + os.environ["PATH"]),
            check=True,
        )
    finally:
        config.unlink(missing_ok=True)


if __name__ == "__main__":
    main()
