#!/usr/bin/env python3
"""Validate actual cluster evidence and explicitly approved source-review assumptions."""

import argparse
import datetime
import hashlib
import json
import pathlib
import re
import subprocess
import sys

SCENARIOS = {
    "schema",
    "dry-run",
    "admission-scc",
    "service-routing",
    "image-triggers",
    "cutover-exclusive",
    "cutover-overlap",
    "rollback",
    "failed-rollout",
    "constraint-preflight",
    "interruption",
    "stale-plan",
    "safe-rerun",
    "hpa-retarget",
    "exclusive-volume",
    "singleton-worker",
    "restricted-rbac",
    "admission-rejection",
    "cross-namespace-image-triggers",
    "multi-container-image-triggers",
    "per-mutation-recovery",
    "connectivity-loss",
}


def digest(path):
    return hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()


def dependency_digest():
    return hashlib.sha256(
        pathlib.Path("go.mod").read_bytes()
        + b"\0"
        + pathlib.Path("go.sum").read_bytes()
    ).hexdigest()


def verify(
    matrix,
    evidence,
    artifacts,
    revision,
    fixture_hash,
    dependency_hash,
    source_hash=None,
    reviews=(),
):
    required = {
        v["minor"]
        for v in matrix["targets"]
        if v["releaseRequirement"] != "development"
    }
    if "4.14" not in required or matrix["newestGA"] not in required:
        raise ValueError("Missing floor or newest GA target")
    rows = {}
    for item in evidence:
        if item.get("kind") != "actual-openshift":
            continue
        key = (item.get("minor"), item.get("artifactSHA256"))
        if key in rows:
            raise ValueError("Ambiguous evidence for " + str(key))
        rows[key] = item
    for minor in required:
        target = next(v for v in matrix["targets"] if v["minor"] == minor)
        if target.get("qualificationPolicy") == "review-assumption":
            # This exception is limited to the unavailable versions explicitly
            # authorized for source review. It never creates an integration pass.
            if (
                minor not in {"4." + str(n) for n in range(14, 21)}
                or minor == matrix["newestGA"]
            ):
                raise ValueError("Review assumption is not authorized for " + minor)
            matching = [
                r for r in reviews if minor in r.get("assumedCompatibleReleases", [])
            ]
            if len(matching) != 1:
                raise ValueError("Missing or ambiguous compatibility review: " + minor)
            review = matching[0]
            if (
                review.get("kind") != "release-note-compatibility-review"
                or review.get("result") != "assumed-compatible-with-prerequisites"
                or review.get("reviewComplete") is not True
                or review.get("unresolvedApplicableFindings") != []
                or not review.get("scope")
                or not review.get("prerequisites")
            ):
                raise ValueError("Incomplete compatibility review: " + minor)
            if (
                not source_hash
                or review.get("productionSourceSHA256") != source_hash
                or review.get("dependencySHA256") != dependency_hash
                or review.get("fixtureSHA256") != fixture_hash
            ):
                raise ValueError("Stale compatibility review: " + minor)
            if not any(a.get("minor") == minor for a in review.get("assessments", [])):
                raise ValueError("Missing per-release assessment: " + minor)
            continue
        if target.get("qualificationPolicy", "actual-cluster") != "actual-cluster":
            raise ValueError("Unknown qualification policy: " + minor)
        for artifact, sha in artifacts.items():
            row = rows.get((minor, sha), {})
            if row.get("result") != "passed":
                raise ValueError(
                    "Missing passing actual-cluster evidence: " + minor + " " + artifact
                )
            if (
                row.get("revision") != revision
                or row.get("fixtureSHA256") != fixture_hash
                or row.get("dependencySHA256") != dependency_hash
            ):
                raise ValueError("Stale evidence: " + minor + " " + artifact)
            if (
                not re.fullmatch(
                    re.escape(minor) + r"\.\d+", row.get("openshiftPatch", "")
                )
                or not re.match(r"v1\.\d+\.\d+", row.get("kubernetesVersion", ""))
                or not row.get("releaseImage")
            ):
                raise ValueError("Missing exact release metadata")
            versions = row.get("dependencyVersions", {})
            if (
                any(
                    not versions.get(k)
                    for k in ["k8s.io/api", "k8s.io/apimachinery", "k8s.io/client-go"]
                )
                or len(set(versions.values())) != 1
            ):
                raise ValueError("Missing or unaligned Kubernetes module versions")
            if minor == "4.14" and not row["kubernetesVersion"].startswith("v1.27."):
                raise ValueError("Floor must use actual Kubernetes 1.27")
            if not SCENARIOS.issubset(set(row.get("scenarios", []))):
                raise ValueError("Incomplete scenarios: " + minor)
            if (
                not row.get("runner")
                or not row.get("runURL")
                or not row.get("testedAt")
            ):
                raise ValueError("Missing provenance")
            datetime.datetime.fromisoformat(row["testedAt"].replace("Z", "+00:00"))
    return True


def tree_digest(root):
    h = hashlib.sha256()
    for p in sorted(pathlib.Path(root).rglob("*")):
        if p.is_file():
            h.update(p.relative_to(root).as_posix().encode() + b"\0" + p.read_bytes())
    return h.hexdigest()


def production_source_digest():
    """Bind assumptions to all shipped Go code and the embedded field catalog."""
    paths = [
        p for p in pathlib.Path(".").glob("*.go") if not p.name.endswith("_test.go")
    ]
    paths.append(pathlib.Path("compatibility/baseline-1.27.json"))
    h = hashlib.sha256()
    for p in sorted(paths):
        h.update(p.as_posix().encode() + b"\0" + p.read_bytes())
    return h.hexdigest()


def load_reviews(matrix):
    reviews = []
    for name in sorted(
        {
            v["reviewEvidence"]
            for v in matrix["targets"]
            if v.get("qualificationPolicy") == "review-assumption"
        }
    ):
        path = pathlib.Path(name)
        if (
            path.parent != pathlib.Path("compatibility/evidence")
            or path.suffix != ".json"
        ):
            raise ValueError("Invalid review evidence path")
        review = json.loads(path.read_text())
        for assessment in review.get("assessments", []):
            p = pathlib.Path(assessment["path"])
            if p.parent != pathlib.Path("compatibility/reviews") or p.suffix != ".json":
                raise ValueError("Invalid assessment path")
            if digest(p) != assessment["sha256"]:
                raise ValueError("Changed release assessment: " + str(p))
            content = json.loads(p.read_text())
            minor = assessment["minor"]
            identity = content.get(
                "openshiftMinor", content.get("minor", content.get("openshift"))
            )
            if (
                not re.fullmatch(r"4\.\d+", minor)
                or p.name != minor + "-assessment.json"
                or identity != minor
            ):
                raise ValueError("Release assessment identity mismatch: " + str(p))
        reviews.append(review)
    return reviews


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--artifacts", required=True)
    p.add_argument("--evidence", default="compatibility/results")
    a = p.parse_args()
    matrix = json.loads(pathlib.Path("compatibility/matrix.json").read_text())
    evidence = [
        json.loads(p.read_text()) for p in pathlib.Path(a.evidence).glob("*.json")
    ]
    artifacts = {
        p.name: digest(p)
        for p in pathlib.Path(a.artifacts).glob("openshift-dc-converter-*")
        if p.is_file()
    }
    if not artifacts:
        raise ValueError("No release binaries")
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    verify(
        matrix,
        evidence,
        artifacts,
        revision,
        tree_digest("testdata"),
        dependency_digest(),
        production_source_digest(),
        load_reviews(matrix),
    )
    print(
        "Required actual-cluster evidence and explicit source-review assumptions validated"
    )


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError) as exc:
        sys.exit(str(exc))
