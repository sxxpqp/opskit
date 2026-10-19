#!/usr/bin/env python3
"""Make sure CI reports original Calico failures instead of only timeout tails."""
import subprocess
import sys
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("summarize-install-diagnostics.py")


class SummaryTests(unittest.TestCase):
    def summarize(self, text):
        result = subprocess.run(
            [sys.executable, str(SCRIPT)],
            input=text,
            capture_output=True,
            text=True,
            check=True,
        )
        return result.stdout

    def test_first_calico_error_is_kept_when_journal_tail_is_noisy(self):
        lines = [
            "=== Kubernetes node and system pod state ===",
            "NAME READY STATUS",
            "calico-node-abc 0/1 Init:CreateContainerError",
            "coredns-123 0/1 ContainerCreating",
            "13m Warning Failed pod/calico-node-abc Error: failed to generate container "
            "aa4839eaa6301d87d95995ae6ff427214f8c7a7e2e74e6d72fd35a588978c938 "
            "spec: path \"/sys/fs/\" is mounted on \"/sys\" but it is not a shared mount",
        ]
        lines.extend(
            "Oct 19 master kubelet: Failed to create sandbox for pod coredns "
            "plugin type=calico failed (add): /var/lib/calico/nodename missing"
            for _ in range(250)
        )
        result = self.summarize("\n".join(lines))
        self.assertIn("Init:CreateContainerError", result)
        self.assertIn("not a shared mount", result)
        self.assertIn("-- Calico Pod warning events --", result)
        self.assertNotIn("aa4839eaa6301d87d95995ae6ff427214f8c7a7e2e74e6d72fd35a588978c938", result)
        self.assertLess(len(result.splitlines()), 32)

    def test_missing_section_is_reported(self):
        result = self.summarize("some unrelated text\n")
        self.assertIn("No specific container/runtime failure found", result)
        self.assertIn("node-diagnostics.log", result)


if __name__ == "__main__":
    unittest.main()
