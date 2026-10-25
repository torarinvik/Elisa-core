#!/usr/bin/env python3
"""Build, record, and verify a content-bound Go Stage0 compiler receipt."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
from pathlib import Path
import re
import subprocess
import sys
import tempfile


SCHEMA = "elisa-stage0-provenance-v4"
INPUT_FILES = ("go.mod", "go.sum")
GO_ENV_CONTEXT_KEYS = (
    "GOVERSION", "GOOS", "GOARCH", "GOHOSTOS", "GOHOSTARCH", "CGO_ENABLED", "GOEXPERIMENT",
    "GOAMD64", "GOARM", "GO386", "GOMIPS", "GOMIPS64", "GOPPC64", "GORISCV64",
    "GOWASM", "GOTOOLCHAIN", "GO_EXTLINK_ENABLED", "CC", "CXX", "FC", "AR", "LD",
    "PKG_CONFIG", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_FFLAGS", "CGO_LDFLAGS",
)
GO_LIST_FILE_FIELDS = (
    "GoFiles", "CgoFiles", "CFiles", "CXXFiles", "MFiles", "HFiles", "FFiles",
    "SFiles", "SwigFiles", "SwigCXXFiles", "SysoFiles", "EmbedFiles",
)


def _go_environment(root: Path) -> dict[str, str]:
    try:
        result = subprocess.run(
            ["go", "env", "-json", "GOFLAGS", "GOWORK", "GOROOT", *GO_ENV_CONTEXT_KEYS],
            cwd=root,
            check=False,
            capture_output=True,
            text=True,
            timeout=10,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise RuntimeError(f"cannot inspect Go build environment: {error}") from error
    if result.returncode != 0:
        raise RuntimeError(f"cannot inspect Go build environment: {result.stderr.strip()}")
    try:
        environment = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError("Go returned invalid JSON while reporting its build environment") from error
    if not isinstance(environment, dict):
        raise RuntimeError("Go returned invalid build environment metadata")
    return {str(key): str(value or "") for key, value in environment.items()}


def _module_replacement_path(root: Path) -> str | None:
    go_mod = root / "go.mod"
    try:
        module_lines = go_mod.read_text(encoding="utf-8").splitlines()
    except (OSError, UnicodeDecodeError) as error:
        raise RuntimeError(f"cannot inspect Stage0 module replacements in {go_mod}: {error}") from error
    for line in module_lines:
        directive = line.split("//", 1)[0].strip()
        if "=>" not in directive:
            continue
        replacement_words = directive.split("=>", 1)[1].strip().split(maxsplit=1)
        if not replacement_words:
            continue
        replacement = replacement_words[0].strip('"`')
        if (
            replacement.startswith((".", os.sep))
            or Path(replacement).is_absolute()
            or re.match(r"^[A-Za-z]:[\\/]", replacement)
        ):
            return replacement
    return None


def _validate_go_build_context(root: Path) -> dict[str, str]:
    if os.environ.get("GOFLAGS", "").strip():
        raise RuntimeError("Stage0 provenance build rejects GOFLAGS overrides")
    if os.environ.get("GOWORK", "").strip() not in ("", "off"):
        raise RuntimeError("Stage0 provenance build rejects an explicit GOWORK workspace")
    environment = _go_environment(root)
    if str(environment.get("GOFLAGS", "")).strip():
        raise RuntimeError("Stage0 provenance build rejects GOFLAGS configured by the Go environment")
    workspace = str(environment.get("GOWORK", "")).strip()
    if workspace not in ("", "off"):
        raise RuntimeError(f"Stage0 provenance build rejects active Go workspace: {workspace}")
    replacement = _module_replacement_path(root)
    if replacement is not None:
        raise RuntimeError(f"Stage0 provenance build rejects local module replacement: {replacement}")

    target = (environment.get("GOOS", ""), environment.get("GOARCH", ""))
    host = (environment.get("GOHOSTOS", ""), environment.get("GOHOSTARCH", ""))
    if not all(target) or not all(host) or target != host:
        raise RuntimeError(
            f"Stage0 provenance build requires a native Go target (target={target}, host={host})"
        )
    if not environment.get("GOROOT"):
        raise RuntimeError("Go did not report GOROOT while checking Stage0 build inputs")
    return environment


def _source_candidates(root: Path, environment: dict[str, str]) -> list[tuple[str, Path]]:
    command = ["go", "list", "-deps", "-json", "./src"]
    try:
        result = subprocess.run(
            command,
            cwd=root,
            check=False,
            capture_output=True,
            text=True,
            timeout=30,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise RuntimeError(f"cannot enumerate Stage0 Go build inputs: {error}") from error
    if result.returncode != 0:
        raise RuntimeError(f"cannot enumerate Stage0 Go build inputs: {result.stderr.strip()}")

    decoder = json.JSONDecoder()
    packages: list[dict[str, object]] = []
    position = 0
    while position < len(result.stdout):
        while position < len(result.stdout) and result.stdout[position].isspace():
            position += 1
        if position == len(result.stdout):
            break
        try:
            package, position = decoder.raw_decode(result.stdout, position)
        except json.JSONDecodeError as error:
            raise RuntimeError("Go returned invalid package JSON while listing Stage0 inputs") from error
        if not isinstance(package, dict):
            raise RuntimeError("Go returned invalid package metadata while listing Stage0 inputs")
        if package.get("Error") or package.get("DepsErrors") or package.get("Incomplete"):
            detail = package.get("Error") or package.get("DepsErrors") or "incomplete package"
            raise RuntimeError(f"Go package input listing is incomplete: {detail}")
        packages.append(package)
    if not packages:
        raise RuntimeError("Go returned no packages while listing Stage0 inputs")

    root = root.resolve()
    goroot = Path(environment["GOROOT"]).resolve()
    selected: dict[str, Path] = {}
    for package in packages:
        directory_value = package.get("Dir")
        if not isinstance(directory_value, str) or not directory_value:
            raise RuntimeError(f"Go package has no source directory: {package.get('ImportPath', 'unknown')}")
        directory = Path(directory_value).resolve()
        module = package.get("Module")
        module = module if isinstance(module, dict) else {}
        module_directory_value = module.get("Dir")
        module_directory = Path(str(module_directory_value)).resolve() if module_directory_value else None
        if directory.is_relative_to(root):
            prefix = "module"
            package_relative = directory.relative_to(root)
        elif directory.is_relative_to(goroot):
            prefix = "goroot"
            package_relative = directory.relative_to(goroot)
        elif module_directory is not None and directory.is_relative_to(module_directory):
            module_path = str(module.get("Path", ""))
            module_version = str(module.get("Version", ""))
            if not module_path or not module_version:
                raise RuntimeError(f"Go dependency has no stable module identity: {package.get('ImportPath', 'unknown')}")
            replacement = module.get("Replace")
            replacement = replacement if isinstance(replacement, dict) else {}
            replacement_identity = ""
            if replacement:
                replacement_identity = (
                    "=>" + str(replacement.get("Path", "")) + "@" + str(replacement.get("Version", ""))
                )
            prefix = f"dependency/{module_path}@{module_version}{replacement_identity}"
            package_relative = directory.relative_to(module_directory)
        else:
            raise RuntimeError(
                f"unsupported Stage0 Go package source root: {package.get('ImportPath', 'unknown')} at {directory}"
            )
        if ".." in package_relative.parts:
            raise RuntimeError(f"unsupported Stage0 Go package path: {directory}")

        for field in GO_LIST_FILE_FIELDS:
            filenames = package.get(field, [])
            if not isinstance(filenames, list) or any(not isinstance(name, str) for name in filenames):
                raise RuntimeError(f"Go returned invalid {field} for {package.get('ImportPath', 'unknown')}")
            for filename in filenames:
                relative_input = Path(filename)
                if relative_input.is_absolute() or ".." in relative_input.parts:
                    raise RuntimeError(f"Go returned an unsafe {field} path: {filename}")
                path = directory / relative_input
                if not path.is_file() and not path.is_symlink():
                    raise RuntimeError(f"Go-selected Stage0 input is missing: {path}")
                identity = (Path(prefix) / package_relative / filename).as_posix()
                previous = selected.get(identity)
                if previous is not None and previous != path:
                    raise RuntimeError(f"ambiguous Stage0 Go input identity: {identity}")
                selected[identity] = path

    for entry in (*INPUT_FILES, "vendor/modules.txt"):
        path = root / entry
        if path.is_file() or path.is_symlink():
            selected[entry] = path
    if not any(identity.startswith("module/src/") and path.suffix == ".go" for identity, path in selected.items()):
        raise RuntimeError(f"Stage0 source tree has no Go files under {root / 'src'}")
    return sorted(selected.items(), key=lambda item: item[0])


def source_digest(root: Path, environment: dict[str, str] | None = None) -> str:
    root = root.resolve()
    if not (root / "go.mod").is_file():
        raise RuntimeError(f"Stage0 Go module file is missing: {root / 'go.mod'}")
    if environment is None:
        environment = _validate_go_build_context(root)
    files = _source_candidates(root, environment)

    digest = hashlib.sha256()
    for identity, path in files:
        logical_name = identity.encode("utf-8")
        file_digest = hashlib.sha256()
        size = 0
        if path.is_file():
            with path.open("rb") as stream:
                for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                    size += len(chunk)
                    file_digest.update(chunk)
        symlink = os.readlink(path).encode("utf-8", errors="surrogateescape") if path.is_symlink() else b""
        for value in (logical_name, str(size).encode("ascii"), file_digest.digest(), symlink):
            digest.update(len(value).to_bytes(8, "big"))
            digest.update(value)
    return digest.hexdigest()


def go_build_metadata(binary: Path) -> dict[str, object]:
    try:
        result = subprocess.run(
            ["go", "version", "-m", str(binary)],
            check=False,
            capture_output=True,
            text=True,
            timeout=10,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise RuntimeError(f"cannot read Go build metadata for {binary}: {error}") from error
    if result.returncode != 0:
        raise RuntimeError(f"cannot read Go build metadata for {binary}: {result.stderr.strip()}")

    lines = result.stdout.splitlines()
    if not lines or ": " not in lines[0]:
        raise RuntimeError(f"Go build metadata is missing for {binary}")
    go_version = lines[0].rsplit(": ", 1)[-1]
    package_path = ""
    module_path = ""
    metadata: list[str] = []
    for line in lines[1:]:
        normalized = line.strip()
        if not normalized:
            continue
        metadata.append(normalized)
        fields = normalized.split(maxsplit=1)
        if len(fields) != 2:
            continue
        if fields[0] == "path":
            package_path = fields[1]
        elif fields[0] == "mod":
            module_path = fields[1].split(maxsplit=1)[0]
    if package_path != "elisacore/src" or module_path != "elisacore":
        raise RuntimeError(
            f"{binary} is not an Elisa-core Stage0 compiler "
            f"(package={package_path or 'unknown'}, module={module_path or 'unknown'})"
        )
    return {"go_version": go_version, "metadata": metadata}


def manifest_path(binary: Path) -> Path:
    return binary.with_name(binary.name + ".provenance.json")


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def build_manifest(
    root: Path,
    binary: Path,
    source_sha256: str | None = None,
    environment: dict[str, str] | None = None,
) -> dict[str, object]:
    root = root.resolve()
    binary = binary.resolve()
    if not root.is_dir():
        raise RuntimeError(f"Stage0 compiler root does not exist: {root}")
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise RuntimeError(f"Stage0 compiler binary is missing or not executable: {binary}")
    if environment is None:
        environment = _validate_go_build_context(root)
    current_source_sha256 = source_digest(root, environment)
    if source_sha256 is not None and current_source_sha256 != source_sha256:
        raise RuntimeError("Stage0 source inputs changed while the compiler was being built")
    return {
        "schema": SCHEMA,
        "binary_sha256": sha256_file(binary),
        "source_sha256": current_source_sha256,
        "build_context": {key: environment.get(key, "") for key in GO_ENV_CONTEXT_KEYS},
        "go_build": go_build_metadata(binary),
    }


def _write_manifest(binary: Path, manifest: dict[str, object]) -> Path:
    destination = manifest_path(binary)
    temporary_name = ""
    try:
        with tempfile.NamedTemporaryFile(
            mode="w",
            encoding="utf-8",
            dir=destination.parent,
            prefix=destination.name + ".",
            suffix=".tmp",
            delete=False,
        ) as stream:
            temporary_name = stream.name
            json.dump(manifest, stream, indent=2, sort_keys=True)
            stream.write("\n")
        os.replace(temporary_name, destination)
    finally:
        if temporary_name and os.path.exists(temporary_name):
            os.unlink(temporary_name)
    return destination


def _publish_pair(staged_binary: Path, staged_receipt: Path, binary: Path) -> Path:
    receipt = manifest_path(binary)
    with tempfile.TemporaryDirectory(prefix=f".{binary.name}.rollback-", dir=binary.parent) as backup_dir:
        backup_root = Path(backup_dir)
        backup_binary = backup_root / "previous-binary"
        backup_receipt = backup_root / "previous-receipt"
        had_binary = binary.is_file()
        had_receipt = receipt.is_file()
        if had_binary:
            shutil.copy2(binary, backup_binary)
        if had_receipt:
            shutil.copy2(receipt, backup_receipt)
        try:
            os.replace(staged_binary, binary)
            os.replace(staged_receipt, receipt)
        except OSError as error:
            try:
                if had_binary:
                    os.replace(backup_binary, binary)
                else:
                    binary.unlink(missing_ok=True)
                if had_receipt:
                    os.replace(backup_receipt, receipt)
                else:
                    receipt.unlink(missing_ok=True)
            except OSError as rollback_error:
                raise RuntimeError(
                    f"Stage0 receipt publish failed ({error}); previous product rollback failed ({rollback_error})"
                ) from rollback_error
            raise RuntimeError(f"Stage0 receipt publish failed; previous product was restored: {error}") from error
    return receipt


def build(root: Path, binary: Path) -> Path:
    root = root.resolve()
    binary = binary.expanduser().resolve()
    if not root.is_dir():
        raise RuntimeError(f"Stage0 compiler root does not exist: {root}")
    binary.parent.mkdir(parents=True, exist_ok=True)
    environment = _validate_go_build_context(root)
    source_before = source_digest(root, environment)

    with tempfile.TemporaryDirectory(prefix=f".{binary.name}.stage0-", dir=binary.parent) as staging_dir:
        staged_binary = Path(staging_dir) / binary.name
        command = ["go", "build", "-buildvcs=false", "-o", str(staged_binary), "./src"]
        try:
            result = subprocess.run(command, cwd=root, check=False, capture_output=True, text=True)
        except OSError as error:
            raise RuntimeError(f"cannot build Stage0 compiler: {error}") from error
        if result.returncode != 0:
            raise RuntimeError(f"Stage0 build failed: {result.stderr.strip() or result.stdout.strip()}")
        if not staged_binary.is_file() or not os.access(staged_binary, os.X_OK):
            raise RuntimeError("Stage0 build returned success without producing a fresh executable")

        manifest = build_manifest(root, staged_binary, source_before, environment)
        if source_digest(root, environment) != source_before:
            raise RuntimeError("Stage0 source inputs changed before the provenance receipt was published")
        staged_receipt = _write_manifest(staged_binary, manifest)
        return _publish_pair(staged_binary, staged_receipt, binary)


def check(root: Path, binary: Path) -> None:
    binary = binary.expanduser().resolve()
    sidecar = manifest_path(binary)
    try:
        recorded = json.loads(sidecar.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise RuntimeError(f"Stage0 provenance receipt is missing or invalid: {sidecar}") from error
    if not isinstance(recorded, dict) or recorded.get("schema") != SCHEMA:
        raise RuntimeError(f"unsupported Stage0 provenance receipt: {sidecar}")
    environment = _validate_go_build_context(root)
    current = build_manifest(root, binary, environment=environment)
    if recorded != current:
        changed: list[str] = []
        for field, description in (
            ("binary_sha256", "binary contents"),
            ("source_sha256", "Go-selected compiler inputs or module metadata"),
            ("build_context", "Go compiler build context"),
            ("go_build", "embedded Go build or dependency metadata"),
        ):
            if recorded.get(field) != current[field]:
                changed.append(description)
        raise RuntimeError("Stage0 provenance mismatch: " + ", ".join(changed or ["receipt fields"]))


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    for command in ("build", "check"):
        subparser = subparsers.add_parser(command)
        subparser.add_argument("--root", required=True, type=Path, help="Elisa-core compiler module root")
        subparser.add_argument("--binary", required=True, type=Path, help="Go Stage0 compiler binary")
    args = parser.parse_args(argv)
    try:
        if args.command == "build":
            receipt = build(args.root, args.binary)
            print(f"built Stage0 with current provenance: {receipt}")
        else:
            check(args.root, args.binary)
            print(f"Stage0 provenance is current: {args.binary}")
    except (OSError, RuntimeError, ValueError) as error:
        print(error, file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
