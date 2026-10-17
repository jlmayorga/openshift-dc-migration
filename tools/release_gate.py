#!/usr/bin/env python3
"""Validate actual cluster evidence and explicitly approved source-review assumptions."""

import argparse
import datetime
import hashlib
import json
import pathlib
import re
import shutil
import subprocess
import sys

REPO_ROOT = pathlib.Path(__file__).resolve().parents[1]
FIXTURE_ROOT = REPO_ROOT / "internal/migration/testdata"

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


def dependency_digest(root=REPO_ROOT):
    return hashlib.sha256(
        (pathlib.Path(root) / "go.mod").read_bytes()
        + b"\0"
        + (pathlib.Path(root) / "go.sum").read_bytes()
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
    required_minors=None,
):
    if not source_hash:
        raise ValueError("Missing production source identity")
    required = {
        v["minor"]
        for v in matrix["targets"]
        if v["releaseRequirement"] != "development"
    }
    if required_minors is not None:
        if not set(required_minors).issubset(required):
            raise ValueError("Scoped targets are absent from the inventory")
        required = set(required_minors)
    if "4.14" not in required or (
        required_minors is None and matrix["newestGA"] not in required
    ):
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
                or row.get("productionSourceSHA256") != source_hash
            ):
                raise ValueError("Stale evidence: " + minor + " " + artifact)
            native = (
                artifact.removeprefix("openshift-dc-converter-")
                .removesuffix(".exe")
                .replace("-", "/")
            )
            if (
                native
                not in {
                    "linux/amd64",
                    "linux/arm64",
                    "darwin/amd64",
                    "darwin/arm64",
                    "windows/amd64",
                }
                or row.get("nativeExecutionPlatform") != native
            ):
                raise ValueError(
                    "Actual evidence execution platform mismatch: " + artifact
                )
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
    if not any(p.is_file() for p in pathlib.Path(root).rglob("*")):
        raise ValueError("Missing or empty fixture root: " + str(root))
    for p in sorted(pathlib.Path(root).rglob("*")):
        if p.is_file():
            h.update(p.relative_to(root).as_posix().encode() + b"\0" + p.read_bytes())
    return h.hexdigest()


def source_inventory_digest(root, paths):
    h = hashlib.sha256()
    for path in sorted(paths):
        h.update(path.relative_to(root).as_posix().encode() + b"\0" + path.read_bytes())
    return h.hexdigest()


def production_source_digest(root=REPO_ROOT):
    """Ship inventory: all cmd/internal Go except tests, plus embedded catalog."""
    root = pathlib.Path(root)
    paths = [
        p
        for directory in ["cmd", "internal"]
        for p in (root / directory).rglob("*.go")
        if not p.name.endswith("_test.go")
    ]
    if not paths:
        raise ValueError("Missing production Go inventory")
    paths.append(root / "internal/migration/compatibility/baseline-1.27.json")
    return source_inventory_digest(root, paths)


def historical_production_source_digest(root):
    """Original inventory, exclusively for an explicitly archived tested revision."""
    root = pathlib.Path(root)
    paths = [p for p in root.glob("*.go") if not p.name.endswith("_test.go")]
    if not paths:
        raise ValueError("Missing historical production source")
    paths.append(root / "compatibility/baseline-1.27.json")
    return source_inventory_digest(root, paths)


def fixture_digest():
    return tree_digest(FIXTURE_ROOT)


def evidence_path(root, name):
    """Resolve a declared evidence reference without allowing bundle escape."""
    if root is None:
        raise ValueError("An explicit --evidence-root is required")
    base = pathlib.Path(root).resolve()
    relative = pathlib.Path(name)
    if relative.is_absolute() or ".." in relative.parts:
        raise ValueError("Invalid evidence path")
    path = (base / relative).resolve()
    if not path.is_relative_to(base):
        raise ValueError("Evidence path escapes declared root")
    if not path.is_file():
        raise ValueError("Missing external evidence: " + name)
    return path


def evidence_directory(root, name):
    if root is None:
        raise ValueError("An explicit --evidence-root is required")
    base = pathlib.Path(root).resolve()
    relative = pathlib.Path(name)
    path = (base / relative).resolve()
    if (
        relative.is_absolute()
        or ".." in relative.parts
        or not path.is_relative_to(base)
    ):
        raise ValueError("Invalid evidence directory")
    if not path.is_dir():
        raise ValueError("Missing external evidence directory: " + name)
    return path


def actual_evidence_paths(root, scope=None, directory=None):
    """Validate every selected record, including overrides, inside its bundle."""
    if directory is not None or scope is None:
        records = evidence_directory(root, directory or "compatibility/results")
        base = pathlib.Path(root).resolve()
        paths = [
            evidence_path(base, p.relative_to(base).as_posix())
            for p in sorted(records.glob("*.json"))
        ]
    else:
        paths = [
            evidence_path(root, item["evidence"])
            for item in scope["artifacts"].values()
        ]
    if scope is not None:
        pins = {item["evidenceSHA256"] for item in scope["artifacts"].values()}
        if len(paths) != len(scope["artifacts"]) or {digest(p) for p in paths} != pins:
            raise ValueError("Replacement records differ from pinned scoped evidence")
    return paths


def review_bindings(matrix, *, current_source=False):
    """Select current assumptions explicitly; frozen scope always keeps old pins."""
    if current_source and matrix.get("currentSourceReview"):
        item = matrix["currentSourceReview"]
        return [(item["evidence"], item["evidenceSHA256"])]
    targets = [
        t
        for t in matrix["targets"]
        if t.get("qualificationPolicy") == "review-assumption"
    ]
    bindings = []
    for name in sorted({t["reviewEvidence"] for t in targets}):
        pins = {
            t.get("reviewEvidenceSHA256")
            for t in targets
            if t["reviewEvidence"] == name
        }
        if len(pins) != 1 or None in pins:
            raise ValueError("Changed or unpinned review evidence")
        bindings.append((name, pins.pop()))
    return bindings


def load_reviews(matrix, evidence_root=None, *, current_source=False):
    reviews = []
    for name, pin in review_bindings(matrix, current_source=current_source):
        path = pathlib.Path(name)
        if (
            path.parent != pathlib.Path("compatibility/evidence")
            or path.suffix != ".json"
        ):
            raise ValueError("Invalid review evidence path")
        path = evidence_path(evidence_root, name)
        if digest(path) != pin:
            raise ValueError("Changed or unpinned review evidence")
        review = json.loads(path.read_text())
        current = review
        seen = {name}
        while current.get("baseReviewEvidence"):
            base = current["baseReviewEvidence"]
            if base in seen:
                raise ValueError("Cyclic review provenance")
            seen.add(base)
            base_path = evidence_path(evidence_root, base)
            expected = current.get("baseReviewSHA256") or matrix.get(
                "reviewProvenanceSHA256", {}
            ).get(base)
            if not expected or digest(base_path) != expected:
                raise ValueError("Changed or unpinned base review")
            current = json.loads(base_path.read_text())
        if current_source and matrix.get("currentSourceReview"):
            delta = review.get("sourceDeltaAssessment", {})
            reference = pathlib.Path(delta.get("path", ""))
            if (
                reference.parent != pathlib.Path("compatibility/reviews")
                or reference.suffix != ".json"
            ):
                raise ValueError("Invalid current source delta assessment path")
            delta_path = evidence_path(evidence_root, str(reference))
            if digest(delta_path) != delta.get("sha256"):
                raise ValueError("Changed current source delta assessment")
            content = json.loads(delta_path.read_text())
            if (
                content.get("kind") != "production-source-delta-assessment"
                or not isinstance(review.get("revision"), str)
                or not re.fullmatch(r"[a-f0-9]{40}", review["revision"])
                or content.get("reviewedRevision") != review.get("revision")
                or content.get("conclusion")
                != "no-new-applicable-breaking-change-identified"
                or content.get("unresolvedApplicableFindings") != []
            ):
                raise ValueError(
                    "Incomplete or mismatched current source delta assessment"
                )
        for assessment in review.get("assessments", []):
            p = pathlib.Path(assessment["path"])
            if p.parent != pathlib.Path("compatibility/reviews") or p.suffix != ".json":
                raise ValueError("Invalid assessment path")
            p = evidence_path(evidence_root, str(p))
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


def load_scope(matrix, evidence_root=None, *, current_scope=False):
    """Load one explicit scope without changing the historical default or GA inventory."""
    if current_scope and not matrix.get("currentSourceReview"):
        raise ValueError("Current scope requires an explicit current source review")
    expected_path = pathlib.Path(
        "compatibility/current-release-scope.json"
        if current_scope
        else "compatibility/release-scope.json"
    )
    path = expected_path if current_scope else pathlib.Path(matrix["activeReleaseScope"])
    if path != expected_path:
        raise ValueError("Invalid release scope path")
    scope = json.loads((REPO_ROOT / path).read_text())
    if (
        scope.get("schemaVersion") != "v1"
        or scope.get("kind") != "scoped-maintenance-release"
        or scope.get("name")
        != ("current-maintenance-4.21" if current_scope else "maintenance-4.21")
        or not scope.get("authorization", {}).get("instruction")
        or scope.get("requiredMinors") != ["4." + str(n) for n in range(14, 22)]
        or scope.get("actualMinor") != "4.21"
        or not re.fullmatch(r"[a-f0-9]{40}", scope.get("revision", ""))
    ):
        raise ValueError("Invalid scoped maintenance policy")
    expected = {
        "openshift-dc-converter-linux-amd64": "linux/amd64",
        "openshift-dc-converter-darwin-arm64": "darwin/arm64",
    }
    if set(scope.get("artifacts", {})) != set(expected):
        raise ValueError("Scoped policy must contain only the two qualified platforms")
    for name, platform in expected.items():
        item = scope["artifacts"][name]
        path = pathlib.Path(item["evidence"])
        if (
            item["platform"] != platform
            or not re.fullmatch(r"[a-f0-9]{64}", item.get("sha256", ""))
            or path.parent != pathlib.Path("compatibility/evidence")
            or path.suffix != ".json"
        ):
            raise ValueError("Invalid scoped artifact or evidence path")
        path = evidence_path(evidence_root, str(path))
        if digest(path) != item["evidenceSHA256"]:
            raise ValueError("Changed scoped evidence: " + str(path))
    return scope


def verify_scope(
    matrix,
    scope,
    evidence,
    artifacts,
    fixture_hash,
    dependency_hash,
    source_hash,
    evidence_root=None,
    *,
    current_source=False,
):
    """Allow documentation/policy commits while retaining exact tested bytes."""
    for key, value in [
        ("fixtureSHA256", fixture_hash),
        ("dependencySHA256", dependency_hash),
        ("productionSourceSHA256", source_hash),
    ]:
        if scope[key] != value:
            raise ValueError("Stale release scope: " + key)
    expected = {name: item["sha256"] for name, item in scope["artifacts"].items()}
    if artifacts != expected:
        raise ValueError("Release binaries differ from the exact scoped artifact set")
    for item in scope["artifacts"].values():
        rows = [
            r
            for r in evidence
            if r.get("artifactSHA256") == item["sha256"]
            and r.get("minor") == scope["actualMinor"]
        ]
        if len(rows) != 1:
            raise ValueError("Missing or ambiguous scoped evidence")
        row = rows[0]
        for key in [
            "openshiftPatch",
            "kubernetesVersion",
            "releaseImage",
            "productionSourceSHA256",
        ]:
            if row.get(key) != scope[key]:
                raise ValueError("Scoped evidence mismatch: " + key)
        if row.get("nativeExecutionPlatform") != item["platform"]:
            raise ValueError("Scoped execution platform mismatch")
    return verify(
        matrix,
        evidence,
        artifacts,
        scope["revision"],
        fixture_hash,
        dependency_hash,
        source_hash,
        load_reviews(matrix, evidence_root, current_source=current_source),
        required_minors=scope["requiredMinors"],
    )


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--artifacts", required=True)
    p.add_argument(
        "--historical-source",
        action="store_true",
        help="Verify against isolated immutable tested source, never current candidates",
    )
    p.add_argument(
        "--evidence-root",
        required=True,
        help="External bundle root for actual records and pinned historical review overlays",
    )
    p.add_argument(
        "--evidence", help="Override the scoped canonical records with a directory"
    )
    p.add_argument(
        "--full-matrix", action="store_true", help="Require the entire GA inventory"
    )
    p.add_argument(
        "--current-scope",
        action="store_true",
        help="Verify the separately pinned current scope with refreshed source reviews",
    )
    p.add_argument(
        "--export-evidence", help="Export validated policy, records and gate result"
    )
    a = p.parse_args()
    if a.current_scope and (a.historical_source or a.full_matrix):
        p.error("--current-scope cannot be combined with --historical-source or --full-matrix")
    matrix = json.loads((REPO_ROOT / "compatibility/matrix.json").read_text())
    scope = (
        load_scope(matrix, a.evidence_root, current_scope=a.current_scope)
        if a.current_scope or (matrix.get("activeReleaseScope") and not a.full_matrix)
        else None
    )
    evidence_paths = actual_evidence_paths(a.evidence_root, scope, a.evidence)
    evidence = [json.loads(path.read_text()) for path in evidence_paths]
    artifacts = {
        p.name: digest(p)
        for p in pathlib.Path(a.artifacts).glob("openshift-dc-converter-*")
        if p.is_file()
    }
    if not artifacts:
        raise ValueError("No release binaries")
    revision = subprocess.check_output(
        ["git", "rev-parse", "HEAD"], cwd=REPO_ROOT, text=True
    ).strip()
    if a.historical_source:
        if scope is None:
            raise ValueError("Historical source requires an explicit frozen scope")
        from build_qualified import historical_source, source_directory

        with historical_source(scope["revision"]) as source, source_directory(source):
            hashes = (
                tree_digest(source / "testdata"),
                dependency_digest(source),
                historical_production_source_digest(source),
            )
    else:
        hashes = (fixture_digest(), dependency_digest(), production_source_digest())
    provenance = json.loads(
        (pathlib.Path(a.artifacts) / "build-provenance.json").read_text()
    )
    if provenance.get("artifacts") != artifacts or any(
        provenance.get(k) != v
        for k, v in zip(
            ["fixtureSHA256", "dependencySHA256", "productionSourceSHA256"], hashes
        )
    ):
        raise ValueError("Candidate provenance mismatch")
    tested_revision = scope["revision"] if scope else revision
    if provenance.get("revision") != tested_revision:
        raise ValueError("Candidate tested revision mismatch")
    current_source = a.current_scope or scope is None
    reviews = load_reviews(matrix, a.evidence_root, current_source=current_source)
    if scope:
        verify_scope(
            matrix, scope, evidence, artifacts, *hashes, evidence_root=a.evidence_root,
            current_source=current_source,
        )
    else:
        verify(matrix, evidence, artifacts, revision, *hashes, reviews)
    if a.export_evidence:
        out = pathlib.Path(a.export_evidence)
        out.mkdir(parents=True, exist_ok=True)
        paths = evidence_paths + [REPO_ROOT / "compatibility/matrix.json"]
        paths += [
            evidence_path(a.evidence_root, name)
            for name, _ in review_bindings(matrix, current_source=current_source)
        ]
        for review in reviews:
            paths += [
                evidence_path(a.evidence_root, r["path"]) for r in review["assessments"]
            ]
            if review.get("sourceDeltaAssessment"):
                paths.append(
                    evidence_path(
                        a.evidence_root, review["sourceDeltaAssessment"]["path"]
                    )
                )
            current = review
            while current.get("baseReviewEvidence"):
                base_path = evidence_path(
                    a.evidence_root, current["baseReviewEvidence"]
                )
                paths.append(base_path)
                current = json.loads(base_path.read_text())
        if scope:
            paths.append(
                REPO_ROOT / (
                    "compatibility/current-release-scope.json"
                    if a.current_scope else matrix["activeReleaseScope"]
                )
            )
        for path in set(paths):
            shutil.copyfile(path, out / path.name)
        (out / "gate-result.json").write_text(
            json.dumps(
                {
                    "kind": "release-gate-result",
                    "result": "passed",
                    "scope": scope["name"] if scope else "full-matrix",
                    "policyRevision": revision,
                    "testedRevision": scope["revision"] if scope else revision,
                    "artifacts": artifacts,
                    "fixtureSHA256": hashes[0],
                    "dependencySHA256": hashes[1],
                    "productionSourceSHA256": hashes[2],
                    "limitations": scope["limitations"] if scope else [],
                },
                indent=2,
            )
            + "\n"
        )
    print("Release gate passed: " + (scope["name"] if scope else "full-matrix"))


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError) as exc:
        sys.exit(str(exc))
