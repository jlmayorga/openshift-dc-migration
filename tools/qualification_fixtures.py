"""Synthetic policy fixtures for local tests; never release evidence."""

import json
import pathlib
from release_gate import SCENARIOS, digest


def write_policy(root):
    root = pathlib.Path(root)

    def write(name, value):
        p = root / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(json.dumps(value))
        return p

    hashes = ("fixtures", "dependencies", "source")
    review = dict(
        kind="release-note-compatibility-review",
        result="assumed-compatible-with-prerequisites",
        reviewComplete=True,
        unresolvedApplicableFindings=[],
        assumedCompatibleReleases=["4." + str(n) for n in range(14, 21)],
        fixtureSHA256=hashes[0],
        dependencySHA256=hashes[1],
        productionSourceSHA256=hashes[2],
        scope="Synthetic only",
        prerequisites=["Synthetic prerequisites"],
        assessments=[],
    )
    for minor in review["assumedCompatibleReleases"]:
        name = "compatibility/reviews/" + minor + "-assessment.json"
        path = write(name, {"minor": minor})
        review["assessments"].append(dict(minor=minor, path=name, sha256=digest(path)))
    review_name = "compatibility/evidence/review.json"
    review_path = write(review_name, review)
    matrix = dict(
        newestGA="4.22",
        activeReleaseScope="compatibility/release-scope.json",
        targets=[
            dict(
                minor="4." + str(n),
                releaseRequirement="required",
                **(
                    dict(
                        qualificationPolicy="review-assumption",
                        reviewEvidence=review_name,
                        reviewEvidenceSHA256=digest(review_path),
                    )
                    if n < 21
                    else {}
                )
            )
            for n in range(14, 23)
        ],
    )
    scope = dict(
        schemaVersion="v1",
        kind="scoped-maintenance-release",
        name="maintenance-4.21",
        authorization={"instruction": "Synthetic test only"},
        requiredMinors=["4." + str(n) for n in range(14, 22)],
        actualMinor="4.21",
        revision="a" * 40,
        openshiftPatch="4.21.1",
        kubernetesVersion="v1.34.1",
        releaseImage="synthetic-image",
        fixtureSHA256=hashes[0],
        dependencySHA256=hashes[1],
        productionSourceSHA256=hashes[2],
        build=dict(goVersion="go1.27.2", ldflags="-X main.version=synthetic"),
        artifacts={},
    )
    rows = []
    for index, platform in enumerate(["linux/amd64", "darwin/arm64"]):
        sha = str(index + 1) * 64
        row = dict(
            kind="actual-openshift",
            result="passed",
            minor="4.21",
            revision=scope["revision"],
            artifactSHA256=sha,
            fixtureSHA256=hashes[0],
            dependencySHA256=hashes[1],
            productionSourceSHA256=hashes[2],
            openshiftPatch=scope["openshiftPatch"],
            kubernetesVersion=scope["kubernetesVersion"],
            releaseImage=scope["releaseImage"],
            dependencyVersions={
                k: "synthetic"
                for k in ["k8s.io/api", "k8s.io/apimachinery", "k8s.io/client-go"]
            },
            nativeExecutionPlatform=platform,
            scenarios=sorted(SCENARIOS),
            runner="synthetic",
            runURL="https://invalid.example/synthetic",
            testedAt="2026-01-01T00:00:00Z",
        )
        name = "compatibility/evidence/" + platform.replace("/", "-") + ".json"
        path = write(name, row)
        scope["artifacts"]["openshift-dc-converter-" + platform.replace("/", "-")] = (
            dict(
                platform=platform,
                sha256=sha,
                evidence=name,
                evidenceSHA256=digest(path),
            )
        )
        rows.append(row)
    write(matrix["activeReleaseScope"], scope)
    return matrix, scope, rows, hashes
