#!/usr/bin/env python3
"""Test Stage0 rebuild deadlines without invoking the compiler."""
from __future__ import annotations

import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parents[1]
RUNNER = ROOT / "scripts" / "run_with_timeout.py"


class RunWithTimeoutTests(unittest.TestCase):
    def test_timeout_terminates_owned_process_group(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            marker = Path(temporary) / "child-finished"
            child = (
                "import pathlib,signal,sys,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
                "time.sleep(0.6); "
                "pathlib.Path(sys.argv[1]).write_text('alive')"
            )
            parent = (
                "import subprocess,sys,time; "
                "subprocess.Popen([sys.executable,'-c',sys.argv[1],sys.argv[2]]); "
                "time.sleep(10)"
            )
            result = subprocess.run(
                [sys.executable, str(RUNNER), "0.1", "--", sys.executable, "-c", parent, child, str(marker)],
                capture_output=True,
                text=True,
                check=False,
                timeout=5,
            )
            self.assertEqual(result.returncode, 124, result.stderr)
            self.assertIn("timeout: exceeded 0.1s", result.stderr)
            time.sleep(0.7)
            self.assertFalse(marker.exists(), "a descendant survived the process-group timeout")

    def test_parent_signal_terminates_nested_process_group(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            marker = Path(temporary) / "nested-child-finished"
            child = (
                "import pathlib,signal,sys,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
                "time.sleep(0.6); "
                "pathlib.Path(sys.argv[1]).write_text('alive')"
            )
            parent = (
                "import subprocess,sys,time; "
                "subprocess.Popen([sys.executable,'-c',sys.argv[1],sys.argv[2]]); "
                "time.sleep(10)"
            )
            wrapper = subprocess.Popen(
                [sys.executable, str(RUNNER), "10", "--", sys.executable, "-c", parent, child, str(marker)],
                start_new_session=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            time.sleep(0.1)
            os.killpg(wrapper.pid, signal.SIGTERM)
            stdout, stderr = wrapper.communicate(timeout=5)
            self.assertEqual(wrapper.returncode, 128 + signal.SIGTERM, stderr or stdout)
            time.sleep(0.7)
            self.assertFalse(marker.exists(), "a nested descendant survived the parent signal")

    def test_exit_status_and_missing_command_are_preserved(self) -> None:
        result = subprocess.run(
            [sys.executable, str(RUNNER), "2", "--", sys.executable, "-c", "raise SystemExit(23)"],
            capture_output=True,
            text=True,
            check=False,
            timeout=5,
        )
        self.assertEqual(result.returncode, 23, result.stderr)
        missing = subprocess.run(
            [sys.executable, str(RUNNER), "2", "--", str(Path(tempfile.gettempdir()) / "not-a-command")],
            capture_output=True,
            text=True,
            check=False,
            timeout=5,
        )
        self.assertEqual(missing.returncode, 127, missing.stderr)

    def test_every_stage0_build_entrypoint_uses_fixed_30_second_runner(self) -> None:
        provenance = (ROOT / "scripts" / "stage0_provenance.py").read_text(encoding="utf-8")
        makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
        hook = (ROOT / "githooks" / "install-elisac.sh").read_text(encoding="utf-8")
        self.assertIn('            "30",\n            "--grace-seconds",\n            "0",\n            "--",\n            "go",\n            "build",', provenance)
        self.assertEqual(makefile.count('"$(PYTHON_BIN)" "$(ELISA_STAGE0_PROVENANCE_SCRIPT)" build'), 2)
        self.assertIn('"$(PYTHON_BIN)" "$(ELISA_STAGE0_TIMEOUT_SCRIPT)" 30 --grace-seconds 0 -- go build', makefile)
        self.assertIn("ELISA_STAGE0_TIMEOUT_SCRIPT ?= $(CURDIR)/scripts/run_with_timeout.py", makefile)
        self.assertIn('"$PYTHON_TOOL" "$PROVENANCE_SCRIPT" build', hook)


if __name__ == "__main__":
    unittest.main(verbosity=2)
