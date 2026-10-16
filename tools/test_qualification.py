import unittest
import pathlib
import subprocess
import sys
import tempfile
from qualify_openshift import validate_target


class NewAppAuthorization(unittest.TestCase):
    def invoke(self, output, cases):
        script = pathlib.Path(__file__).with_name("qualify_new_app.py")
        return subprocess.run(
            [
                sys.executable,
                str(script),
                "--context",
                "test",
                "--kubeconfig",
                "/nonexistent",
                "--binary",
                "/nonexistent",
                "--image",
                "test@sha256:00",
                "--second-image",
                "test@sha256:01",
                "--sidecar-image",
                "test@sha256:02",
                "--namespace-prefix",
                "dcmig-test",
                "--output",
                output,
                "--cases",
                cases,
                "--allow-test-mutations",
            ],
            capture_output=True,
            text=True,
            timeout=10,
        )

    def test_regular_volume_requires_explicit_authorization_before_access(self):
        with tempfile.TemporaryDirectory() as output:
            proc = self.invoke(output, "regular-pvc")
        self.assertEqual(proc.returncode, 2)
        self.assertIn("explicit --allow-regular-pvc", proc.stderr)

    def test_empty_case_selection_fails_before_access(self):
        with tempfile.TemporaryDirectory() as output:
            proc = self.invoke(output, "")
        self.assertEqual(proc.returncode, 2)
        self.assertIn("Unknown or empty", proc.stderr)


class NamespaceAuthorization(unittest.TestCase):
    def test_invalid_target_or_missing_ack_fails_before_access(self):
        script = pathlib.Path(__file__).with_name("qualify_namespaces.py")
        with tempfile.TemporaryDirectory() as output:
            for prefix, ack in [("dcmig-test", False), ("production", True)]:
                command = [
                    sys.executable,
                    str(script),
                    "--context",
                    "test",
                    "--kubeconfig",
                    "/nonexistent",
                    "--binary",
                    "/nonexistent",
                    "--image",
                    "test@sha256:00",
                    "--namespace-prefix",
                    prefix,
                    "--output",
                    output,
                ]
                if ack:
                    command.append("--allow-test-mutations")
                proc = subprocess.run(
                    command, capture_output=True, text=True, timeout=10
                )
                self.assertEqual(proc.returncode, 2)
                self.assertIn("Explicit disposable target", proc.stderr)


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
