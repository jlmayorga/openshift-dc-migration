import copy
import json
import os
import pathlib
import tempfile
import unittest
from release_gate import verify, SCENARIOS, digest, load_reviews


class GateTests(unittest.TestCase):
    def setUp(self):
        self.matrix = {
            "newestGA": "4.22",
            "targets": [
                {"minor": x, "releaseRequirement": "required"} for x in ["4.14", "4.22"]
            ],
        }
        self.rows = [
            dict(
                kind="actual-openshift",
                minor=x,
                artifactSHA256="sha",
                result="passed",
                revision="rev",
                fixtureSHA256="fixtures",
                dependencySHA256="deps",
                openshiftPatch=x + ".1",
                kubernetesVersion="v1.27.16" if x == "4.14" else "v1.35.1",
                dependencyVersions={
                    k: "v0.31.3"
                    for k in ["k8s.io/api", "k8s.io/apimachinery", "k8s.io/client-go"]
                },
                releaseImage="image@sha256:123",
                scenarios=list(SCENARIOS),
                runner="runner",
                runURL="https://ci.example/run",
                testedAt="2026-10-15T00:00:00Z",
            )
            for x in ["4.14", "4.22"]
        ]

    def check(self, rows):
        return verify(
            self.matrix, rows, {"linux-amd64": "sha"}, "rev", "fixtures", "deps"
        )

    def test_valid_contract(self):
        self.assertTrue(self.check(self.rows))

    def test_missing_and_simulated(self):
        for rows in [[], [dict(r, kind="simulated") for r in self.rows]]:
            with self.assertRaises(ValueError):
                self.check(rows)

    def test_stale_or_incomplete(self):
        for field, value in [
            ("revision", "old"),
            ("dependencySHA256", "old"),
            ("scenarios", ["schema"]),
            ("result", "skipped"),
            ("openshiftPatch", ""),
            ("kubernetesVersion", "v1.28.0"),
        ]:
            with self.subTest(field=field):
                rows = copy.deepcopy(self.rows)
                rows[0][field] = value
                with self.assertRaises(ValueError):
                    self.check(rows)

    def reviewed_floor(self):
        self.matrix["targets"][0]["qualificationPolicy"] = "review-assumption"
        return dict(
            kind="release-note-compatibility-review",
            result="assumed-compatible-with-prerequisites",
            reviewComplete=True,
            unresolvedApplicableFindings=[],
            assumedCompatibleReleases=["4.14"],
            productionSourceSHA256="source",
            dependencySHA256="deps",
            fixtureSHA256="fixtures",
            scope="Conventional baseline workload semantics",
            prerequisites=["Reviewed platform patches and working CSI/runtime"],
            assessments=[dict(minor="4.14")],
        )

    def review_check(self, review, rows=None):
        return verify(
            self.matrix,
            self.rows[1:] if rows is None else rows,
            {"linux-amd64": "sha"},
            "rev",
            "fixtures",
            "deps",
            "source",
            [review],
        )

    def test_explicit_review_policy_preserves_actual_newest_gate(self):
        review = self.reviewed_floor()
        self.assertTrue(self.review_check(review))
        with self.assertRaisesRegex(ValueError, "actual-cluster"):
            self.review_check(review, [])
        self.matrix["targets"][1]["qualificationPolicy"] = "review-assumption"
        with self.assertRaisesRegex(ValueError, "not authorized"):
            self.review_check(review)

    def test_stale_incomplete_or_unresolved_review_cannot_replace_actual(self):
        review = self.reviewed_floor()
        for field, value in [
            ("reviewComplete", False),
            ("productionSourceSHA256", "old"),
            ("dependencySHA256", "old"),
            ("fixtureSHA256", "old"),
            ("unresolvedApplicableFindings", ["unsafe-placement"]),
            ("assessments", []),
            ("assumedCompatibleReleases", []),
            ("result", "passed"),
            ("scope", ""),
            ("prerequisites", []),
        ]:
            with self.subTest(field=field):
                with self.assertRaises(ValueError):
                    self.review_check(dict(review, **{field: value}))

    def test_review_record_does_not_enable_an_implicit_exception(self):
        review = self.reviewed_floor()
        del self.matrix["targets"][0]["qualificationPolicy"]
        with self.assertRaisesRegex(ValueError, "actual-cluster"):
            self.review_check(review)

    def test_assessment_tampering_and_path_escape_block_review_loading(self):
        original_cwd = os.getcwd()
        with tempfile.TemporaryDirectory() as directory:
            try:
                os.chdir(directory)
                pathlib.Path("compatibility/evidence").mkdir(parents=True)
                pathlib.Path("compatibility/reviews").mkdir(parents=True)
                assessment = pathlib.Path("compatibility/reviews/4.14-assessment.json")
                assessment.write_text('{"minor": "4.14"}\n')
                record = {
                    "assessments": [
                        {
                            "minor": "4.14",
                            "path": str(assessment),
                            "sha256": digest(assessment),
                        }
                    ]
                }
                path = pathlib.Path("compatibility/evidence/review.json")
                path.write_text(json.dumps(record))
                matrix = {
                    "targets": [
                        {
                            "qualificationPolicy": "review-assumption",
                            "reviewEvidence": str(path),
                        }
                    ]
                }
                self.assertEqual(len(load_reviews(matrix)), 1)
                assessment.write_text('{"changed": true}\n')
                with self.assertRaisesRegex(ValueError, "Changed release assessment"):
                    load_reviews(matrix)
                # A correctly hashed assessment for a different release cannot
                # be relabelled to satisfy the unavailable floor.
                assessment.write_text('{"openshift": "4.21"}\n')
                record["assessments"][0]["sha256"] = digest(assessment)
                path.write_text(json.dumps(record))
                with self.assertRaisesRegex(ValueError, "identity mismatch"):
                    load_reviews(matrix)
                record["assessments"][0]["path"] = "../../outside.json"
                path.write_text(json.dumps(record))
                with self.assertRaisesRegex(ValueError, "Invalid assessment path"):
                    load_reviews(matrix)
            finally:
                os.chdir(original_cwd)
