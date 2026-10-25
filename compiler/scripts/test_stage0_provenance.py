"""Unit tests for the Stage0 source-content receipt."""
from __future__ import annotations

from contextlib import redirect_stderr
import io
import json
import os
from pathlib import Path
import platform
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


sys.path.insert(0, str(Path(__file__).resolve().parent))
import stage0_provenance


class Stage0ProvenanceTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name) / "compiler"
        (self.root / "src").mkdir(parents=True)
        (self.root / "go.mod").write_text("module elisacore\n", encoding="utf-8")
        (self.root / "src" / "main.go").write_text("package main\n", encoding="utf-8")
        self.binary = self.root / "bin" / "elisac"
        self.binary.parent.mkdir()
        self.binary.write_bytes(b"old executable")
        self.binary.chmod(0o755)
        self.dependency = "dep example.com/dependency v1.2.3 h1:checksum"
        self.go_environment = {
            "GOFLAGS": "",
            "GOWORK": "",
            "GOROOT": "/fake/go/root",
            "CGO_ENABLED": "0",
            "GOTOOLCHAIN": "local",
            "CC": "cc",
            "CXX": "c++",
            "AR": "ar",
            "PKG_CONFIG": "pkg-config",
        }
        self.go_files = ["main.go"]
        self.embed_files: list[str] = []
        self.extra_packages: list[dict[str, object]] = []
        self.host_os = {"Darwin": "darwin", "Linux": "linux", "Windows": "windows"}.get(
            platform.system(), platform.system().lower()
        )
        self.host_arch = {
            "x86_64": "amd64",
            "amd64": "amd64",
            "aarch64": "arm64",
            "arm64": "arm64",
        }.get(platform.machine().lower(), platform.machine().lower())

    def fake_go(self, args, **kwargs):
        if args[1] == "env":
            environment = dict(self.go_environment)
            environment.update({
                "GOOS": self.host_os,
                "GOARCH": self.host_arch,
                "GOHOSTOS": self.host_os,
                "GOHOSTARCH": self.host_arch,
            })
            return stage0_provenance.subprocess.CompletedProcess(
                args,
                0,
                stdout=json.dumps(environment),
                stderr="",
            )
        if args[1] == "list":
            packages = [{
                "ImportPath": "elisacore/src",
                "Dir": str(self.root / "src"),
                "Module": {"Path": "elisacore", "Dir": str(self.root)},
                "GoFiles": list(self.go_files),
                "EmbedFiles": list(self.embed_files),
            }, *self.extra_packages]
            return stage0_provenance.subprocess.CompletedProcess(
                args, 0, stdout="\n".join(json.dumps(package) for package in packages), stderr=""
            )
        if args[1] == "build":
            output = Path(args[args.index("-o") + 1])
            output.write_bytes(b"fresh fake executable")
            output.chmod(0o755)
            return stage0_provenance.subprocess.CompletedProcess(args, 0, stdout="", stderr="")
        binary = Path(args[-1])
        return stage0_provenance.subprocess.CompletedProcess(
            args,
            0,
            stdout=(
                f"{binary}: go1.25.0\n"
                " path elisacore/src\n"
                " mod elisacore (devel)\n"
                f" {self.dependency}\n"
                " build -buildmode=exe\n"
                " build -compiler=gc\n"
                " build CGO_ENABLED=0\n"
                " build GOARCH=arm64\n"
                " build GOOS=darwin\n"
            ),
            stderr="",
        )

    def build_current(self) -> Path:
        with patch.object(stage0_provenance.subprocess, "run", side_effect=self.fake_go):
            return stage0_provenance.build(self.root, self.binary)

    def test_build_and_check_accept_unchanged_inputs_and_keep_dependency_metadata(self) -> None:
        receipt = self.build_current()
        with patch.object(stage0_provenance.subprocess, "run", side_effect=self.fake_go):
            stage0_provenance.check(self.root, self.binary)
        self.assertEqual(receipt, stage0_provenance.manifest_path(self.binary.resolve()))
        manifest = json.loads(receipt.read_text(encoding="utf-8"))
        self.assertEqual(manifest["schema"], stage0_provenance.SCHEMA)
        self.assertIn(self.dependency, manifest["go_build"]["metadata"])

    def digest_current(self) -> str:
        with patch.object(stage0_provenance.subprocess, "run", side_effect=self.fake_go):
            return stage0_provenance.source_digest(self.root)

    def test_source_digest_tracks_go_selected_files_and_ignores_unselected_fixtures(self) -> None:
        source = self.root / "src" / "main.go"
        source.write_text("package main\n//go:embed assets/*\nvar files embed.FS\n", encoding="utf-8")
        assets = self.root / "src" / "assets"
        assets.mkdir()
        (assets / "schema.txt").write_text("schema v1\n", encoding="utf-8")
        (assets / ".hidden").write_text("hidden v1\n", encoding="utf-8")
        self.embed_files = ["assets/schema.txt"]
        original = self.digest_current()
        (self.root / "src" / "main_test.go").write_text("package main\n// test-only\n", encoding="utf-8")
        (self.root / "src" / "fixture.json").write_text("test fixture\n", encoding="utf-8")
        (self.root / "test").mkdir()
        (self.root / "test" / "fixture.go").write_text("package fixture\n", encoding="utf-8")
        self.assertEqual(self.digest_current(), original)

        # The Go package listing omits dotfiles from ordinary wildcard embeds.
        (assets / ".hidden").write_text("hidden v2\n", encoding="utf-8")
        self.assertEqual(self.digest_current(), original)

        # `go list` includes dotfiles when an all: embed pattern selects them.
        source.write_text("package main\n//go:embed all:assets/*\nvar files embed.FS\n", encoding="utf-8")
        self.embed_files = ["assets/schema.txt", "assets/.hidden"]
        all_baseline = self.digest_current()
        (assets / ".hidden").write_text("hidden v3\n", encoding="utf-8")
        self.assertNotEqual(self.digest_current(), all_baseline)

    def test_source_digest_tracks_selected_packages_under_previously_excluded_directories(self) -> None:
        helper = self.root / "testdata" / "helpers.go"
        helper.parent.mkdir()
        helper.write_text("package helpers\n", encoding="utf-8")
        self.extra_packages = [{
            "ImportPath": "elisacore/testdata/helpers",
            "Dir": str(helper.parent),
            "Module": {"Path": "elisacore", "Dir": str(self.root)},
            "GoFiles": ["helpers.go"],
        }]
        baseline = self.digest_current()
        helper.write_text("package helpers\n// changed\n", encoding="utf-8")
        self.assertNotEqual(self.digest_current(), baseline)

    def test_source_digest_rejects_unsafe_go_list_paths(self) -> None:
        self.go_files = ["../../outside.go"]
        with self.assertRaisesRegex(RuntimeError, "unsafe GoFiles path"):
            self.digest_current()

    def test_source_digest_tracks_selected_external_module_files(self) -> None:
        dependency_root = Path(self.temporary.name) / "module-cache" / "dependency"
        dependency_root.mkdir(parents=True)
        helper = dependency_root / "helper.go"
        helper.write_text("package dependency\n", encoding="utf-8")
        self.extra_packages = [{
            "ImportPath": "example.com/dependency",
            "Dir": str(dependency_root),
            "Module": {
                "Path": "example.com/dependency",
                "Version": "v1.2.3",
                "Dir": str(dependency_root),
            },
            "GoFiles": ["helper.go"],
        }]
        baseline = self.digest_current()
        helper.write_text("package dependency\n// changed\n", encoding="utf-8")
        self.assertNotEqual(self.digest_current(), baseline)

    def test_failed_build_preserves_previous_binary_and_receipt(self) -> None:
        receipt = self.build_current()
        previous_binary = self.binary.read_bytes()
        previous_receipt = receipt.read_bytes()

        def failed_build(args, **kwargs):
            if args[1] == "build":
                return stage0_provenance.subprocess.CompletedProcess(args, 1, stdout="", stderr="compile failed")
            return self.fake_go(args, **kwargs)

        with patch.object(stage0_provenance.subprocess, "run", side_effect=failed_build):
            with self.assertRaisesRegex(RuntimeError, "Stage0 build failed"):
                stage0_provenance.build(self.root, self.binary)
        self.assertEqual(self.binary.read_bytes(), previous_binary)
        self.assertEqual(receipt.read_bytes(), previous_receipt)

    def test_success_without_fresh_output_cannot_receipt_preexisting_binary(self) -> None:
        receipt = self.build_current()
        previous_binary = self.binary.read_bytes()
        previous_receipt = receipt.read_bytes()

        def no_output_build(args, **kwargs):
            if args[1] == "build":
                return stage0_provenance.subprocess.CompletedProcess(args, 0, stdout="", stderr="")
            return self.fake_go(args, **kwargs)

        with patch.object(stage0_provenance.subprocess, "run", side_effect=no_output_build):
            with self.assertRaisesRegex(RuntimeError, "without producing a fresh executable"):
                stage0_provenance.build(self.root, self.binary)
        self.assertEqual(self.binary.read_bytes(), previous_binary)
        self.assertEqual(receipt.read_bytes(), previous_receipt)

    def test_source_change_during_build_preserves_previous_pair(self) -> None:
        receipt = self.build_current()
        previous_binary = self.binary.read_bytes()
        previous_receipt = receipt.read_bytes()

        def mutate_during_build(args, **kwargs):
            if args[1] == "build":
                output = Path(args[args.index("-o") + 1])
                output.write_bytes(b"fresh but raced executable")
                output.chmod(0o755)
                (self.root / "src" / "main.go").write_text("package main\n// race\n", encoding="utf-8")
                return stage0_provenance.subprocess.CompletedProcess(args, 0, stdout="", stderr="")
            return self.fake_go(args, **kwargs)

        with patch.object(stage0_provenance.subprocess, "run", side_effect=mutate_during_build):
            with self.assertRaisesRegex(RuntimeError, "changed while"):
                stage0_provenance.build(self.root, self.binary)
        self.assertEqual(self.binary.read_bytes(), previous_binary)
        self.assertEqual(receipt.read_bytes(), previous_receipt)

    def test_receipt_publish_failure_rolls_back_existing_pair(self) -> None:
        receipt = self.build_current()
        previous_binary = self.binary.read_bytes()
        previous_receipt = receipt.read_bytes()
        real_replace = os.replace
        failed = False

        def fail_final_receipt_once(source, destination):
            nonlocal failed
            if Path(destination) == receipt and not failed:
                failed = True
                raise OSError("injected receipt publish failure")
            return real_replace(source, destination)

        with patch.object(stage0_provenance.subprocess, "run", side_effect=self.fake_go):
            with patch.object(stage0_provenance.os, "replace", side_effect=fail_final_receipt_once):
                with self.assertRaisesRegex(RuntimeError, "previous product was restored"):
                    stage0_provenance.build(self.root, self.binary)
        self.assertEqual(self.binary.read_bytes(), previous_binary)
        self.assertEqual(receipt.read_bytes(), previous_receipt)

    def test_source_change_after_build_invalidates_receipt(self) -> None:
        self.build_current()
        (self.root / "src" / "main.go").write_text("package main\n// changed\n", encoding="utf-8")
        with patch.object(stage0_provenance.subprocess, "run", side_effect=self.fake_go):
            with self.assertRaisesRegex(RuntimeError, "Go-selected compiler inputs"):
                stage0_provenance.check(self.root, self.binary)

    def test_binary_change_invalidates_receipt(self) -> None:
        self.build_current()
        self.binary.write_bytes(b"different executable")
        with patch.object(stage0_provenance.subprocess, "run", side_effect=self.fake_go):
            with self.assertRaisesRegex(RuntimeError, "binary contents"):
                stage0_provenance.check(self.root, self.binary)

    def test_dependency_metadata_change_invalidates_receipt(self) -> None:
        self.build_current()
        self.dependency = "dep example.com/dependency v1.2.4 h1:changed"
        with patch.object(stage0_provenance.subprocess, "run", side_effect=self.fake_go):
            with self.assertRaisesRegex(RuntimeError, "dependency metadata"):
                stage0_provenance.check(self.root, self.binary)

    def test_build_context_change_invalidates_receipt(self) -> None:
        self.build_current()
        self.go_environment["CC"] = "custom-cc"
        self.go_environment["PKG_CONFIG"] = "custom-pkg-config"
        with patch.object(stage0_provenance.subprocess, "run", side_effect=self.fake_go):
            with self.assertRaisesRegex(RuntimeError, "Go compiler build context"):
                stage0_provenance.check(self.root, self.binary)

    def test_rejects_wrong_package_or_module_identity(self) -> None:
        def wrong_identity(args, **kwargs):
            if args[1] in ("env", "list", "build"):
                return self.fake_go(args, **kwargs)
            binary = Path(args[-1])
            return stage0_provenance.subprocess.CompletedProcess(
                args,
                0,
                stdout=f"{binary}: go1.25.0\n path elisacore/other\n mod wrong-module (devel)\n",
                stderr="",
            )

        with patch.object(stage0_provenance.subprocess, "run", side_effect=wrong_identity):
            with self.assertRaisesRegex(RuntimeError, "not an Elisa-core Stage0 compiler"):
                stage0_provenance.build(self.root, self.binary)
        self.assertEqual(self.binary.read_bytes(), b"old executable")

    def test_rejects_go_flags_before_touching_existing_product(self) -> None:
        receipt = self.build_current()
        previous_binary = self.binary.read_bytes()
        previous_receipt = receipt.read_bytes()
        with patch.dict(os.environ, {"GOFLAGS": "-n"}), patch.object(stage0_provenance.subprocess, "run") as run:
            with self.assertRaisesRegex(RuntimeError, "rejects GOFLAGS"):
                stage0_provenance.build(self.root, self.binary)
        run.assert_not_called()
        self.assertEqual(self.binary.read_bytes(), previous_binary)
        self.assertEqual(receipt.read_bytes(), previous_receipt)

    def test_rejects_go_workspace_and_local_module_replacement(self) -> None:
        with patch.dict(os.environ, {"GOWORK": str(self.root / "go.work")}):
            with self.assertRaisesRegex(RuntimeError, "explicit GOWORK"):
                stage0_provenance.build(self.root, self.binary)
        (self.root / "go.mod").write_text(
            "module elisacore\nreplace example.com/local => ../local\n", encoding="utf-8"
        )
        with self.assertRaisesRegex(RuntimeError, "local module replacement"):
            stage0_provenance.build(self.root, self.binary)
        self.assertEqual(self.binary.read_bytes(), b"old executable")

    def test_rejects_workspace_detected_by_go_environment(self) -> None:
        def workspace_go(args, **kwargs):
            if args[1] == "env":
                return stage0_provenance.subprocess.CompletedProcess(
                    args,
                    0,
                    stdout=json.dumps({
                        "GOFLAGS": "",
                        "GOWORK": "/tmp/go.work",
                        "GOROOT": "/fake/go/root",
                        "GOOS": self.host_os,
                        "GOARCH": self.host_arch,
                        "GOHOSTOS": self.host_os,
                        "GOHOSTARCH": self.host_arch,
                    }),
                    stderr="",
                )
            return self.fake_go(args, **kwargs)

        with patch.object(stage0_provenance.subprocess, "run", side_effect=workspace_go):
            with self.assertRaisesRegex(RuntimeError, "active Go workspace"):
                stage0_provenance.build(self.root, self.binary)
        self.assertEqual(self.binary.read_bytes(), b"old executable")

    def test_rejects_cross_target_build(self) -> None:
        def cross_target_go(args, **kwargs):
            if args[1] == "env":
                return stage0_provenance.subprocess.CompletedProcess(
                    args,
                    0,
                    stdout=json.dumps({
                        "GOFLAGS": "",
                        "GOWORK": "",
                        "GOROOT": "/fake/go/root",
                        "GOOS": "linux" if self.host_os != "linux" else "darwin",
                        "GOARCH": self.host_arch,
                        "GOHOSTOS": self.host_os,
                        "GOHOSTARCH": self.host_arch,
                    }),
                    stderr="",
                )
            return self.fake_go(args, **kwargs)

        with patch.object(stage0_provenance.subprocess, "run", side_effect=cross_target_go):
            with self.assertRaisesRegex(RuntimeError, "requires a native Go target"):
                stage0_provenance.build(self.root, self.binary)
        self.assertEqual(self.binary.read_bytes(), b"old executable")

    def test_cli_does_not_accept_go_build_dry_run_flag(self) -> None:
        receipt = self.build_current()
        previous_binary = self.binary.read_bytes()
        previous_receipt = receipt.read_bytes()
        with patch.object(stage0_provenance.subprocess, "run") as run:
            with redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                stage0_provenance.main([
                    "build", "--root", str(self.root), "--binary", str(self.binary), "--build-arg=-n"
                ])
        run.assert_not_called()
        self.assertEqual(self.binary.read_bytes(), previous_binary)
        self.assertEqual(receipt.read_bytes(), previous_receipt)

    def test_check_rejects_missing_receipt(self) -> None:
        with self.assertRaisesRegex(RuntimeError, "receipt is missing or invalid"):
            stage0_provenance.check(self.root, self.binary)

    def test_external_receipt_requires_explicit_source_root(self) -> None:
        with tempfile.TemporaryDirectory() as external_directory:
            binary = Path(external_directory) / "pinned-elisac"
            binary.write_bytes(b"external compiler")
            binary.chmod(0o755)
            stage0_provenance.manifest_path(binary).write_text("{}\n", encoding="utf-8")
            environment = os.environ.copy()
            environment.pop("ELISA_STAGE0_SOURCE_ROOT", None)
            environment.pop("ELISA_ALLOW_STALE_STAGE0", None)
            checker = Path(__file__).resolve().parent / "assert_stage0_fresh.sh"
            if not checker.is_file():
                self.skipTest("consumer-shell integration belongs to the self-host checkout")
            result = subprocess.run(
                ["bash", str(checker), str(binary)],
                check=False,
                capture_output=True,
                text=True,
                env=environment,
            )
        self.assertEqual(result.returncode, 2)
        self.assertIn("ELISA_STAGE0_SOURCE_ROOT", result.stderr)


if __name__ == "__main__":
    unittest.main()
