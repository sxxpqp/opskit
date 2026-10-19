#!/usr/bin/env python3
"""Black-box regression tests for the public E2E installer log stream."""
import os
import tempfile
from pathlib import Path
import subprocess
import sys
import unittest

SCRIPT = Path(__file__).with_name("stream-install-log.py")


class StreamInstallLogTests(unittest.TestCase):
    def run_filter(self, message: str) -> str:
        env = {**os.environ, "OPSKIT_DOCKER_E2E_PASSWORD": "ephemeral-password-54321"}
        result = subprocess.run(
            [sys.executable, str(SCRIPT)],
            input=message,
            text=True,
            capture_output=True,
            env=env,
            check=True,
        )
        return result.stdout

    def test_scrubs_join_token_credentials_and_key_material(self):
        message = (
            "Running kubeadm init\n"
            "kubeadm join 172.18.0.2:6443 --token abcdef.0123456789abcdef "
            "--discovery-token-ca-cert-hash sha256:bbbbbb\n"
            "password: ephemeral-password-54321\n"
            "client-key-data: cHJpdmF0ZQ==\n"
            '  "certificate-authority-data": "Y2VydA=="\n'
            "Authorization: Bearer my.service.token\n"
            "https://user:pass@registry.example/path\n"
            "-----BEGIN PRIVATE KEY-----\n"
            "sensitive-multiline-secret\n"
            "-----END PRIVATE KEY-----\n"
            "something went wrong: context deadline exceeded\n"
        )
        result = self.run_filter(message)
        for secret in (
            "abcdef.0123456789abcdef",
            "ephemeral-password-54321",
            "cHJpdmF0ZQ==",
            "Y2VydA==",
            "my.service.token",
            "user:pass@",
            "sensitive-multiline-secret",
        ):
            self.assertNotIn(secret, result)
        self.assertIn("kubeadm join 172.18.0.2:6443 --token <redacted>", result)
        self.assertIn("context deadline exceeded", result)
        self.assertIn("Running kubeadm init", result)
        self.assertIn("[PEM key/certificate redacted]", result)

    def test_separate_stdout_and_stderr_and_preserve_failure_exit_code(self):
        command = (
            "import sys;"
            "print('control plane initialized', flush=True);"
            "print('Calico failed: not a shared mount', file=sys.stderr, flush=True);"
            "print('kubeadm join 172.18.0.2:6443 --token abcdef.0123456789abcdef',"
            " file=sys.stderr, flush=True);"
            "sys.exit(17)"
        )
        with tempfile.TemporaryDirectory() as tmp:
            path = str(Path(tmp) / "install.log")
            result = subprocess.run(
                [
                    sys.executable,
                    str(SCRIPT),
                    "--run",
                    path,
                    sys.executable,
                    "-c",
                    command,
                ],
                text=True,
                capture_output=True,
            )
            self.assertEqual(result.returncode, 17)
            self.assertIn("[STDOUT] control plane initialized", result.stdout)
            self.assertIn("[STDERR] Calico failed: not a shared mount", result.stdout)
            self.assertNotIn("abcdef.0123456789abcdef", result.stdout)
            self.assertIn("[STDERR] kubeadm join", result.stdout)
            self.assertEqual(Path(path).read_text(), result.stdout)

    def test_successful_command_returns_zero(self):
        with tempfile.TemporaryDirectory() as tmp:
            result = subprocess.run(
                [
                    sys.executable, str(SCRIPT), "--run",
                    str(Path(tmp) / "install.log"),
                    sys.executable, "-c", "print('OK')",
                ],
                capture_output=True, text=True,
            )
            self.assertEqual(result.returncode, 0)
            self.assertIn("[STDOUT] OK", result.stdout)

    def test_actions_workflow_command_is_neutralized(self):
        result = self.run_filter("::error file=a,line=1::unsafe action\n[master/ready] 开始\n")
        self.assertTrue(result.startswith("[installer] ::error"))
        self.assertIn("[master/ready] 开始", result)

    def test_does_not_expose_long_lines_or_binary_escape_sequences(self):
        result = self.run_filter("\x1b[31mERROR\x1b[0m\n" + "x" * 5000 + "\n")
        self.assertIn("ERROR\n", result)
        self.assertNotIn("\x1b", result)
        self.assertIn("line omitted", result)
        self.assertNotIn("x" * 5000, result)


if __name__ == "__main__":
    unittest.main()
