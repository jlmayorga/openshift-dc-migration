import pathlib
import tempfile
import unittest
from unittest.mock import patch
from build_candidates import build


class CandidateTests(unittest.TestCase):
    def test_build_has_no_qualification_dependency_and_records_actual_bytes(self):
        def write(argv, **kwargs):
            pathlib.Path(argv[argv.index("-o") + 1]).write_bytes(b"new bytes")

        with tempfile.TemporaryDirectory() as tmp, patch(
            "build_candidates.subprocess.run", side_effect=write
        ), patch(
            "build_candidates.subprocess.check_output", return_value="a" * 40
        ), patch(
            "build_candidates.production_source_digest", return_value="current-source"
        ):
            result = build(tmp, ["linux/amd64"])
            self.assertEqual(result["kind"], "unqualified-candidates")
            self.assertEqual(result["productionSourceSHA256"], "current-source")
            self.assertEqual(len(result["artifacts"]), 1)
            self.assertTrue((pathlib.Path(tmp) / "SHA256SUMS").is_file())

    def test_help_and_asset_resolution_from_another_directory(self):
        import subprocess
        import sys
        from release_gate import fixture_digest, REPO_ROOT

        with tempfile.TemporaryDirectory() as tmp:
            proc = subprocess.run(
                [
                    sys.executable,
                    str(REPO_ROOT / "tools/build_candidates.py"),
                    "--help",
                ],
                cwd=tmp,
                capture_output=True,
                text=True,
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            before = pathlib.Path.cwd()
            try:
                import os

                expected = fixture_digest()
                os.chdir(tmp)
                self.assertEqual(expected, fixture_digest())
            finally:
                os.chdir(before)
