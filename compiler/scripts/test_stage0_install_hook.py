#!/usr/bin/env python3
"""Exercise Stage0 hook publication without real Git mutations or compiler builds.

Every scenario uses a temporary repository and HOME. Fake Git and Python commands
pass the hook's admission checks and produce recognizable binary/receipt bytes.
Copy/move shims inject failures, forwarding all other operations to /bin tools.
"""

import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


HOOK_PATH = Path(__file__).resolve().parent.parent / "githooks" / "install-elisac.sh"

# Each shim has an absolute Python shebang, so the fake PATH cannot recursively
# resolve the interpreter. The hook uses fake-python only for its build helper.
COMMAND_SHIM = r'''
import os
from pathlib import Path
import signal
import subprocess
import sys


name = Path(sys.argv[0]).name
root = Path(os.environ["CASE_ROOT"])
arguments = sys.argv[1:]
failure = os.environ.get("FAIL_KIND", "")

if name == "git":
    if "--absolute-git-dir" in arguments or "--git-common-dir" in arguments:
        print(root / ".git")
    elif "--show-toplevel" in arguments:
        print(root)
    # An empty status result represents a clean primary checkout.
    sys.exit(0)

if name == "fake-python":
    if failure == "build":
        sys.exit(1)
    output = Path(arguments[arguments.index("--binary") + 1])
    output.write_bytes(b"NEW-BINARY")
    output.chmod(0o755)
    output.with_name(output.name + ".provenance.json").write_bytes(b"NEW-RECEIPT")
    sys.exit(0)

destination = arguments[-1] if arguments else ""
phase = ""
if name == "cp":
    if "/.elisac-stage0-backup." in destination and destination.endswith("/elisac"):
        phase = "backup-bin"
    elif "/.elisac-stage0-backup." in destination and destination.endswith("/elisac.provenance.json"):
        phase = "backup-receipt"
    elif destination.endswith(".restore"):
        phase = "restore-bin-copy"
    elif destination.endswith(".restore.json"):
        phase = "restore-receipt-copy"
    elif ".tmp." in destination:
        phase = "stage-receipt" if destination.endswith(".json") else "stage-bin"
elif name == "mv":
    if arguments[-2].endswith(".restore"):
        phase = "restore-bin-move"
    elif arguments[-2].endswith(".restore.json"):
        phase = "restore-receipt-move"
    else:
        phase = "publish-receipt" if destination.endswith(".json") else "publish-bin"

with (root / "operations.log").open("a", encoding="utf-8") as stream:
    stream.write(f"{name} {phase} {arguments!r}\n")

if phase and phase in failure.split(","):
    sys.exit(23)
if phase == "publish-receipt" and failure == "signal":
    os.kill(os.getppid(), signal.SIGTERM)
    sys.exit(23)

sys.exit(subprocess.run(["/bin/" + name, *arguments], check=False).returncode)
'''


class Stage0InstallHookTests(unittest.TestCase):
    def run_case(self, failure="", previous_state="pair", restore_succeeds=True):
        with tempfile.TemporaryDirectory(prefix="elisac-hook-injection-") as temporary:
            # Resolve symlinks so fake --absolute-git-dir agrees with pwd -P.
            root = Path(temporary).resolve()
            (root / ".git").mkdir()
            binary_directory = root / "compiler" / "bin"
            binary_directory.mkdir(parents=True)
            scripts = root / "compiler" / "scripts"
            scripts.mkdir()
            (scripts / "stage0_provenance.py").write_text("# stub\n", encoding="utf-8")
            home = root / "home"
            home.mkdir()
            tools = root / "tools"
            tools.mkdir()
            for name in ("git", "fake-python", "cp", "mv"):
                command = tools / name
                command.write_text("#!" + sys.executable + "\n" + COMMAND_SHIM, encoding="utf-8")
                command.chmod(0o755)

            binary = binary_directory / "elisac"
            receipt = binary_directory / "elisac.provenance.json"
            if previous_state in ("pair", "bin", "symlink"):
                if previous_state == "symlink":
                    target = root / "old-target"
                    target.write_bytes(b"OLD-BINARY")
                    binary.symlink_to(target)
                else:
                    binary.write_bytes(b"OLD-BINARY")
                    binary.chmod(0o711)
            if previous_state in ("pair", "receipt", "symlink"):
                receipt.write_bytes(b"OLD-RECEIPT")

            def snapshot():
                pair = []
                for path in (binary, receipt):
                    if not path.exists() and not path.is_symlink():
                        pair.append(None)
                        continue
                    contents = os.readlink(path) if path.is_symlink() else path.read_bytes()
                    pair.append((path.is_symlink(), contents, path.stat().st_mode & 0o777))
                return pair

            previous_pair = snapshot()
            environment = os.environ.copy()
            environment.update(
                CASE_ROOT=str(root),
                HOME=str(home),
                PATH=str(tools) + ":/usr/bin:/bin",
                PYTHON_BIN=str(tools / "fake-python"),
                FAIL_KIND=failure,
                # Keep another shell session's helper override out of the case.
                ELISA_STAGE0_PROVENANCE_SCRIPT=str(scripts / "stage0_provenance.py"),
            )
            completed = subprocess.run(
                ["/bin/sh", str(HOOK_PATH)],
                cwd=root,
                env=environment,
                capture_output=True,
                text=True,
                timeout=5,
                check=False,
            )
            backups = list(binary_directory.glob(".elisac-stage0-backup.*"))
            if not failure:
                self.assertEqual(
                    [binary.read_bytes(), receipt.read_bytes()],
                    [b"NEW-BINARY", b"NEW-RECEIPT"],
                    completed.stderr,
                )
            elif restore_succeeds:
                self.assertEqual(snapshot(), previous_pair, completed.stderr)
            else:
                self.assertTrue(backups, completed.stderr)
                self.assertIn("recovery files preserved", completed.stderr)
                self.assertEqual((backups[0] / "elisac").read_bytes(), b"OLD-BINARY")
                self.assertEqual(
                    (backups[0] / "elisac.provenance.json").read_bytes(),
                    b"OLD-RECEIPT",
                )
            if restore_succeeds:
                self.assertFalse(backups, completed.stderr)
            self.assertFalse(list(binary_directory.glob("elisac.tmp.*")), completed.stderr)

    def test_success_publishes_binary_and_receipt(self):
        self.run_case()

    def test_publication_failures_keep_previous_pair(self):
        for phase in (
            "build",
            "stage-bin",
            "stage-receipt",
            "backup-bin",
            "backup-receipt",
            "publish-bin",
            "publish-receipt",
            "signal",
        ):
            with self.subTest(phase=phase):
                self.run_case(failure=phase)

    def test_rollback_restores_absent_partial_and_symlink_states(self):
        for state in ("none", "bin", "receipt", "symlink"):
            with self.subTest(previous_state=state):
                self.run_case(failure="publish-receipt", previous_state=state)

    def test_failed_restoration_preserves_recovery_files(self):
        for phase in (
            "restore-bin-copy",
            "restore-bin-move",
            "restore-receipt-copy",
            "restore-receipt-move",
        ):
            with self.subTest(phase=phase):
                self.run_case(
                    failure="publish-receipt," + phase,
                    restore_succeeds=False,
                )


if __name__ == "__main__":
    unittest.main(verbosity=2)
