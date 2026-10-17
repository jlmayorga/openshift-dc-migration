import pathlib
import tempfile
import unittest
from unittest import mock
from build_qualified import build_from_source
from qualification_fixtures import write_policy


class QualifiedBuildTests(unittest.TestCase):
    def check_failure(self, version, message, side_effect=None):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            matrix, scope, rows, hashes = write_policy(root)
            # The unit fixture supplies synthetic source hashes, never canonical records.
            with mock.patch(
                "build_qualified.tree_digest", return_value=hashes[0]
            ), mock.patch(
                "build_qualified.dependency_digest", return_value=hashes[1]
            ), mock.patch(
                "build_qualified.historical_production_source_digest",
                return_value=hashes[2],
            ), mock.patch(
                "build_qualified.subprocess.check_output", return_value=version
            ), mock.patch(
                "build_qualified.subprocess.run", side_effect=side_effect
            ) as run:
                with self.assertRaisesRegex(ValueError, message):
                    build_from_source(root / "out", matrix, scope, root, root)
                if side_effect is None:
                    run.assert_not_called()
            self.assertFalse((root / "out/build-provenance.json").exists())
            self.assertFalse((root / "out/SHA256SUMS").exists())

    def test_wrong_toolchain_stops_before_build(self):
        self.check_failure("go1.27.1\n", "Frozen candidate requires")

    def test_different_build_bytes_cannot_inherit_live_evidence(self):
        def corrupt(argv, **kwargs):
            pathlib.Path(argv[argv.index("-o") + 1]).write_bytes(b"different candidate")

        self.check_failure("go1.27.2\n", "differs from live-qualified", corrupt)
