import pathlib
import tempfile
import unittest
from unittest.mock import patch
from dispatch_qualification import validate_configuration, validate_candidate_source
from release_gate import digest


class DispatchTests(unittest.TestCase):
    def setUp(self):
        self.identity = dict(
            revision="a" * 40,
            fixtureSHA256="fixtures",
            dependencySHA256="dependencies",
            productionSourceSHA256="source",
        )
        for name, value in [
            ("subprocess.check_output", self.identity["revision"]),
            ("fixture_digest", "fixtures"),
            ("dependency_digest", "dependencies"),
            ("production_source_digest", "source"),
        ]:
            self.enterContext(
                patch("dispatch_qualification." + name, return_value=value)
            )

    def test_stale_or_missing_candidate_source_identity_is_refused(self):
        validate_candidate_source(self.identity)
        for key in self.identity:
            for value in ["old", None]:
                changed = dict(self.identity, **{key: value})
                with self.subTest(key=key, value=value), self.assertRaisesRegex(
                    ValueError, "selected source checkout"
                ):
                    validate_candidate_source(changed)

    def test_rejects_implicit_target_foreign_platform_and_changed_candidate(self):
        with tempfile.TemporaryDirectory() as tmp:
            binary = pathlib.Path(tmp) / "openshift-dc-converter-linux-amd64"
            binary.write_bytes(b"candidate")
            provenance = dict(self.identity, artifacts={binary.name: digest(binary)})
            target = dict(
                context="test",
                namespace="dcmig-test",
                ocURL="https://example.test/oc.tar.gz",
                ocSHA256="0" * 64,
            )
            with patch(
                "dispatch_qualification.platform.system", return_value="Linux"
            ), patch("dispatch_qualification.platform.machine", return_value="x86_64"):
                self.assertEqual(
                    validate_configuration(target, "linux/amd64", provenance, tmp),
                    binary,
                )
                for t, p, meta in [
                    ({}, "linux/amd64", provenance),
                    (target, "darwin/arm64", provenance),
                    (target, "linux/amd64", {"artifacts": {}}),
                ]:
                    with self.assertRaises(ValueError):
                        validate_configuration(t, p, meta, tmp)

    def test_workflow_emits_a_valid_checkout_revision(self):
        import json
        import os
        import subprocess
        import sys
        import textwrap
        from release_gate import REPO_ROOT

        workflow = (REPO_ROOT / ".github/workflows/compatibility.yaml").read_text()
        script = textwrap.dedent(
            workflow.split("python3 - <<'PYCODE'\n")[1].split("          PYCODE")[0]
        )
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            (root / "candidates").mkdir()
            revision = "a" * 40
            (root / "candidates/build-provenance.json").write_text(
                json.dumps({"revision": revision})
            )
            output = root / "github-output"
            proc = subprocess.run(
                [sys.executable, "-c", script],
                cwd=root,
                env=dict(os.environ, GITHUB_OUTPUT=str(output)),
                capture_output=True,
                text=True,
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertEqual(output.read_text(), "revision=" + revision + "\n")
