import pathlib
import tempfile
import unittest
from unittest import mock

from build_qualified import build


class QualifiedBuildTests(unittest.TestCase):
    def test_wrong_toolchain_stops_before_build(self):
        with tempfile.TemporaryDirectory() as out:
            with mock.patch(
                "build_qualified.subprocess.check_output", return_value="go1.27.1\n"
            ):
                with mock.patch("build_qualified.subprocess.run") as run:
                    with self.assertRaisesRegex(
                        ValueError, "Frozen candidate requires"
                    ):
                        build(out)
                    run.assert_not_called()

    def test_different_build_bytes_cannot_inherit_live_evidence(self):
        def corrupt_output(argv, **kwargs):
            pathlib.Path(argv[argv.index("-o") + 1]).write_bytes(b"different candidate")

        with tempfile.TemporaryDirectory() as out:
            with mock.patch(
                "build_qualified.subprocess.check_output", return_value="go1.27.2\n"
            ):
                with mock.patch(
                    "build_qualified.subprocess.run", side_effect=corrupt_output
                ):
                    with self.assertRaisesRegex(
                        ValueError, "differs from live-qualified"
                    ):
                        build(out)
            self.assertFalse((pathlib.Path(out) / "build-provenance.json").exists())
            self.assertFalse((pathlib.Path(out) / "SHA256SUMS").exists())
