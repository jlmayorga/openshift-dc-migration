import unittest
import pathlib
import subprocess
import sys
from qualify_openshift import validate_target


class StaticVolumeAuthorization(unittest.TestCase):
    def test_requires_explicit_scc_before_access(self):
        script = pathlib.Path(__file__).with_name("qualify_features.py")
        args = [
            sys.executable,
            str(script),
            "--context",
            "test",
            "--kubeconfig",
            "/nonexistent",
            "--binary",
            "/nonexistent",
            "--image",
            "example@sha256:00",
            "--second-image",
            "example@sha256:01",
            "--namespace-prefix",
            "dcmig-test",
            "--output",
            "/nonexistent",
            "--allow-test-mutations",
            "--static-volume-test",
        ]
        proc = subprocess.run(args, capture_output=True, text=True, timeout=10)
        self.assertEqual(proc.returncode, 2)
        self.assertIn("explicitly authorized --volume-scc", proc.stderr)


class QualificationGuardTests(unittest.TestCase):
    def test_explicit_disposable_target(self):
        validate_target("lab", "dcmig-test", True)

    def test_no_ambient_or_production_target(self):
        for ctx, ns, ack in [
            ("", "dcmig-test", True),
            ("lab", "production", True),
            ("lab", "dcmig-test", False),
            ("lab", "dcmig-test;bad", True),
        ]:
            with self.assertRaises(ValueError):
                validate_target(ctx, ns, ack)
