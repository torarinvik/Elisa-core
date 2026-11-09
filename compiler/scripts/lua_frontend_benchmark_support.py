"""Runtime helpers and reporting for Lua frontend benchmarks."""

from __future__ import annotations

import json
import re
import statistics
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Iterable

import elisa_platform

MIB_PER_SECOND_RE = re.compile(r"MiB/s=([0-9]+(?:\.[0-9]+)?)")


def run(cmd: list[str], cwd: Path | None = None, capture_output: bool = False) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        cmd,
        cwd=cwd,
        check=True,
        text=True,
        capture_output=capture_output,
    )


def load_corpus_manifest(repo_root: Path, manifest_path: Path | None) -> list[tuple[str, Path]]:
    if manifest_path is None or not manifest_path.exists():
        return []
    inputs: list[tuple[str, Path]] = []
    for raw_line in manifest_path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        source_path = (repo_root / line).resolve()
        if not source_path.exists():
            raise RuntimeError(f"missing corpus file listed in {manifest_path}: {line}")
        label = source_path.stem.replace(".", "_")
        inputs.append((label, source_path))
    return inputs


def make_inline_control(lua_src_dir: Path, keep_temp: bool) -> tuple[Path | None, Path | None]:
    ast_path = lua_src_dir / "lua_ast.elisa"
    frontend_path = lua_src_dir / "lua_frontend.elisa"

    ast_text = ast_path.read_text(encoding="utf-8")
    marker = "    common:\n        @storage(side_table)\n        span: LuaSpan"
    if marker not in ast_text:
        if keep_temp:
            print("inline_control=disabled (lua_ast.elisa no longer uses the side-table span marker)")
        return None, None
    inline_ast_text = ast_text.replace(marker, "    common:\n        span: LuaSpan", 1)

    with tempfile.NamedTemporaryFile(
        mode="w",
        encoding="utf-8",
        dir=lua_src_dir,
        prefix=".lua_ast_inline_bench_",
        suffix=".elisa",
        delete=False,
    ) as ast_tmp:
        ast_tmp.write(inline_ast_text)
        inline_ast_path = Path(ast_tmp.name)

    frontend_text = frontend_path.read_text(encoding="utf-8")
    inline_frontend_text = frontend_text.replace('"lua_ast.elisa"', f'"{inline_ast_path.name}"', 1)
    if inline_frontend_text == frontend_text:
        raise RuntimeError("expected lua_frontend.elisa to include lua_ast.elisa")

    with tempfile.NamedTemporaryFile(
        mode="w",
        encoding="utf-8",
        dir=lua_src_dir,
        prefix=".lua_frontend_inline_bench_",
        suffix=".elisa",
        delete=False,
    ) as frontend_tmp:
        frontend_tmp.write(inline_frontend_text)
        inline_frontend_path = Path(frontend_tmp.name)

    if keep_temp:
        print(f"inline_ast={inline_ast_path}")
        print(f"inline_frontend={inline_frontend_path}")

    return inline_ast_path, inline_frontend_path


def build_benchmark_executable(
    compiler_dir: Path,
    source_path: Path,
    out_dir: Path,
    opt_level: str,
    harness_path: Path,
) -> Path:
    header_path = out_dir / "lua_frontend.h"
    object_path = out_dir / "lua_frontend.o"
    bench_path = out_dir / harness_path.stem
    benchmarks_dir = harness_path.parent
    runtime_shims_path = benchmarks_dir / "json_parser_runtime_shims.c"

    run(["go", "run", "./src", opt_level, "-emit", "header", "-o", str(header_path), str(source_path)], cwd=compiler_dir)
    run(["go", "run", "./src", opt_level, "-emit", "obj", "-o", str(object_path), str(source_path)], cwd=compiler_dir)
    run(
        [
            "clang",
            opt_level,
            "-pthread",
            *elisa_platform.LD_ALLOW_UNDEFINED,
            "-I",
            str(out_dir),
            str(harness_path),
            str(runtime_shims_path),
            str(object_path),
            "-o",
            str(bench_path),
        ],
        cwd=compiler_dir,
    )
    return bench_path


def format_run_failure(exc: subprocess.CalledProcessError) -> str:
    parts: list[str] = []
    if exc.stdout:
        parts.append(exc.stdout.strip())
    if exc.stderr:
        parts.append(exc.stderr.strip())
    message = " | ".join(part for part in parts if part)
    if message:
        return " ".join(message.split())
    return f"benchmark exited with status {exc.returncode}"


def run_suite(
    exe: Path,
    input_path: Path,
    iterations: int,
    repeats: int,
    mode: str,
    label: str,
    *,
    worker_count: int = 0,
) -> list[float]:
    print(label)
    mib_values: list[float] = []
    for _ in range(repeats):
        cmd = [str(exe), str(input_path), str(iterations)]
        if worker_count > 0:
            cmd.append(str(worker_count))
        cmd.append(mode)
        try:
            completed = run(cmd, capture_output=True)
        except subprocess.CalledProcessError as exc:
            raise RuntimeError(format_run_failure(exc)) from exc
        output = completed.stdout.strip()
        if output:
            print(output)
        if completed.stderr:
            print(completed.stderr.strip(), file=sys.stderr)
        match = MIB_PER_SECOND_RE.search(output)
        if not match:
            raise RuntimeError(f"missing MiB/s metric in benchmark output: {output!r}")
        mib_values.append(float(match.group(1)))
    return mib_values


def summarize(label: str, values: Iterable[float]) -> float:
    values = list(values)
    avg = statistics.mean(values)
    print(f"SUMMARY label={label} avg_MiB_s={avg:.2f} min_MiB_s={min(values):.2f} max_MiB_s={max(values):.2f}")
    return avg


def delta_percent(current: float, inline: float) -> float:
    if inline == 0.0:
        return 0.0
    return ((current - inline) / inline) * 100.0


def run_label(variant: str, opt_level: str, input_label: str, mode: str, execution: str, worker_count: int) -> str:
    label = f"{variant}_{opt_level}_{input_label}_{mode}"
    if execution == "parallel":
        label = f"{label}_parallel_w{worker_count}"
    return label.upper()


def write_json_report(
    out_path: Path,
    *,
    repo_root: Path,
    manifest_path: Path,
    modes: list[str],
    parallel_workers: list[int],
    parallel_modes: list[str],
    opt_levels: list[str],
    temp_root: Path,
    keep_temp: bool,
    default_opt_level: str,
    stmt_count: int,
    parse_iterations: int,
    sample_iterations: int,
    repeats: int,
    skip_real_corpus: bool,
    current_benches_by_opt: dict[str, Path],
    inline_benches_by_opt: dict[str, Path],
    current_parallel_benches_by_opt: dict[str, Path],
    inline_parallel_benches_by_opt: dict[str, Path],
    inline_ast_path: Path | None,
    inline_frontend_path: Path | None,
    synthetic_input: dict[str, object],
    inputs: list[tuple[str, Path]],
    runs: list[dict[str, object]],
    aggregate_summaries: list[dict[str, object]],
    skipped: list[dict[str, object]],
) -> None:
    report = {
        "tool": "run_lua_frontend_storage_benchmark.py",
        "repo_root": str(repo_root),
        "manifest_path": str(manifest_path),
        "modes": modes,
        "parallel_workers": parallel_workers,
        "parallel_modes": parallel_modes,
        "opt_levels": opt_levels,
        "temp_root": str(temp_root),
        "keep_temp": keep_temp,
        "opt_level": default_opt_level,
        "stmt_count": stmt_count,
        "parse_iterations": parse_iterations,
        "sample_iterations": sample_iterations,
        "repeats": repeats,
        "skip_real_corpus": skip_real_corpus,
        "current_bench": str(current_benches_by_opt[opt_levels[0]]),
        "inline_bench": None if opt_levels[0] not in inline_benches_by_opt else str(inline_benches_by_opt[opt_levels[0]]),
        "current_parallel_bench": None if opt_levels[0] not in current_parallel_benches_by_opt else str(current_parallel_benches_by_opt[opt_levels[0]]),
        "inline_parallel_bench": None if opt_levels[0] not in inline_parallel_benches_by_opt else str(inline_parallel_benches_by_opt[opt_levels[0]]),
        "current_benches_by_opt_level": {
            opt_level: str(path) for opt_level, path in sorted(current_benches_by_opt.items())
        },
        "inline_benches_by_opt_level": {
            opt_level: str(path) for opt_level, path in sorted(inline_benches_by_opt.items())
        },
        "current_parallel_benches_by_opt_level": {
            opt_level: str(path) for opt_level, path in sorted(current_parallel_benches_by_opt.items())
        },
        "inline_parallel_benches_by_opt_level": {
            opt_level: str(path) for opt_level, path in sorted(inline_parallel_benches_by_opt.items())
        },
        "inline_ast": None if inline_ast_path is None else str(inline_ast_path),
        "inline_frontend": None if inline_frontend_path is None else str(inline_frontend_path),
        "synthetic_input": synthetic_input,
        "inputs": [{"label": label, "path": str(path)} for label, path in inputs],
        "runs": runs,
        "aggregate_summaries": aggregate_summaries,
        "skipped": skipped,
        "summary": {
            "run_count": len(runs),
            "aggregate_count": len(aggregate_summaries),
            "skipped_count": len(skipped),
            "inline_available": len(inline_benches_by_opt) > 0,
            "parallel_enabled": len(parallel_workers) > 0 and len(parallel_modes) > 0,
            "parallel_worker_count": len(parallel_workers),
            "parallel_mode_count": len(parallel_modes),
            "opt_level_count": len(opt_levels),
        },
    }
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")


