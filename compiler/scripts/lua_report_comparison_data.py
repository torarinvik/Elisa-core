"""Load and compare Lua frontend report data."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any


def load_json(path: Path) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def detect_report_kind(report: dict[str, Any]) -> str:
    if "tool" in report:
        return str(report["tool"])
    if "bundle_type" in report:
        return "metadata.json"
    raise SystemExit(f"unsupported report format in {report.get('_path', '<unknown path>')}")


def load_report(path: Path) -> dict[str, Any]:
    report = load_json(path)
    report["_path"] = str(path)
    return report


def differential_case_key(case: dict[str, Any]) -> tuple[str, str]:
    return str(case.get("family", "")), str(case.get("case", ""))


def benchmark_run_key(entry: dict[str, Any]) -> tuple[str, str, int, str, str, str]:
    return (
        str(entry.get("variant", "")),
        str(entry.get("execution", "serial")),
        int(entry.get("worker_count", 0) or 0),
        str(entry.get("opt_level", "")),
        str(entry.get("input", "")),
        str(entry.get("mode", "")),
    )


def benchmark_aggregate_key(entry: dict[str, Any]) -> tuple[str, str, int, str, str, str]:
    return (
        str(entry.get("kind", "")),
        str(entry.get("execution", "serial")),
        int(entry.get("worker_count", 0) or 0),
        str(entry.get("opt_level", "")),
        str(entry.get("mode", "")),
        str(entry.get("input", "")),
    )


def delta_percent(baseline: float, candidate: float) -> float | None:
    if baseline == 0.0:
        return None
    return ((candidate - baseline) / baseline) * 100.0


def benchmark_change_is_significant(
    delta_mib_s: float | None,
    delta_pct: float | None,
    *,
    min_delta_pct: float,
    min_delta_mib_s: float,
) -> bool:
    if delta_mib_s is None and delta_pct is None:
        return True
    if min_delta_mib_s > 0.0:
        if delta_mib_s is None or abs(delta_mib_s) < min_delta_mib_s:
            return False
    if min_delta_pct > 0.0:
        if delta_pct is None or abs(delta_pct) < min_delta_pct:
            return False
    return True


def aggregate_change_metrics(entry: dict[str, Any]) -> tuple[float | None, float | None]:
    fields = entry.get("fields", {})
    if "avg_MiB_s" in fields:
        baseline = float(fields["avg_MiB_s"]["baseline"])
        candidate = float(fields["avg_MiB_s"]["candidate"])
        return candidate - baseline, delta_percent(baseline, candidate)
    if "current_avg_MiB_s" in fields:
        baseline = float(fields["current_avg_MiB_s"]["baseline"])
        candidate = float(fields["current_avg_MiB_s"]["candidate"])
        return candidate - baseline, delta_percent(baseline, candidate)
    if "current_vs_inline_pct" in fields:
        baseline = float(fields["current_vs_inline_pct"]["baseline"])
        candidate = float(fields["current_vs_inline_pct"]["candidate"])
        return None, candidate - baseline
    return None, None


def diff_values(baseline: Any, candidate: Any, prefix: str = "") -> tuple[list[dict[str, Any]], list[dict[str, Any]], list[dict[str, Any]]]:
    added: list[dict[str, Any]] = []
    removed: list[dict[str, Any]] = []
    changed: list[dict[str, Any]] = []

    if isinstance(baseline, dict) and isinstance(candidate, dict):
        for key in sorted(set(baseline) | set(candidate)):
            field = f"{prefix}.{key}" if prefix else str(key)
            if key not in baseline:
                added.append({"field": field, "candidate": candidate[key]})
            elif key not in candidate:
                removed.append({"field": field, "baseline": baseline[key]})
            else:
                child_added, child_removed, child_changed = diff_values(baseline[key], candidate[key], field)
                added.extend(child_added)
                removed.extend(child_removed)
                changed.extend(child_changed)
        return added, removed, changed

    if baseline != candidate:
        changed.append({"field": prefix or "<root>", "baseline": baseline, "candidate": candidate})
    return added, removed, changed


def compare_metadata_reports(baseline: dict[str, Any], candidate: dict[str, Any]) -> dict[str, Any]:
    baseline_payload = {key: value for key, value in baseline.items() if not key.startswith("_")}
    candidate_payload = {key: value for key, value in candidate.items() if not key.startswith("_")}
    added, removed, changed = diff_values(baseline_payload, candidate_payload)
    return {
        "tool": "metadata.json",
        "baseline_path": baseline.get("_path"),
        "candidate_path": candidate.get("_path"),
        "added_fields": added,
        "removed_fields": removed,
        "changed_fields": changed,
    }


def compare_differential_reports(baseline: dict[str, Any], candidate: dict[str, Any]) -> dict[str, Any]:
    baseline_cases = {differential_case_key(case): case for case in baseline.get("cases", [])}
    candidate_cases = {differential_case_key(case): case for case in candidate.get("cases", [])}
    configuration_mismatches: list[dict[str, Any]] = []

    for field in ("opt_level", "strict"):
        if baseline.get(field) != candidate.get(field):
            configuration_mismatches.append(
                {
                    "field": field,
                    "baseline": baseline.get(field),
                    "candidate": candidate.get(field),
                }
            )

    added = sorted(candidate_cases.keys() - baseline_cases.keys())
    removed = sorted(baseline_cases.keys() - candidate_cases.keys())
    changed: list[dict[str, Any]] = []

    for key in sorted(baseline_cases.keys() & candidate_cases.keys()):
        before = baseline_cases[key]
        after = candidate_cases[key]
        fields: dict[str, dict[str, Any]] = {}
        for field in (
            "expected_status",
            "elisacore_status",
            "reference_status",
            "ll_vs_ref_match",
            "expectation_match",
            "fingerprint_match",
            "expected_fingerprints",
            "elisacore_fingerprints",
            "fingerprint_mismatches",
        ):
            if before.get(field) != after.get(field):
                fields[field] = {"baseline": before.get(field), "candidate": after.get(field)}
        if fields:
            changed.append({"family": key[0], "case": key[1], "fields": fields})

    summary_delta: dict[str, int] = {}
    baseline_summary = baseline.get("summary", {})
    candidate_summary = candidate.get("summary", {})
    for field in ("cases", "reference_mismatches", "expectation_mismatches", "fingerprint_mismatches"):
        summary_delta[field] = int(candidate_summary.get(field, 0)) - int(baseline_summary.get(field, 0))

    return {
        "tool": "run_lua_frontend_differential.py",
        "baseline_path": baseline.get("_path"),
        "candidate_path": candidate.get("_path"),
        "configuration_mismatches": configuration_mismatches,
        "summary_delta": summary_delta,
        "added_cases": [{"family": family, "case": case} for family, case in added],
        "removed_cases": [{"family": family, "case": case} for family, case in removed],
        "changed_cases": changed,
    }


def input_labels(report: dict[str, Any]) -> list[str]:
    return [str(entry.get("label", "")) for entry in report.get("inputs", [])]


def collect_configuration_mismatches(
    baseline: dict[str, Any],
    candidate: dict[str, Any],
    *,
    fields: tuple[str, ...],
) -> list[dict[str, Any]]:
    mismatches: list[dict[str, Any]] = []
    for field in fields:
        if baseline.get(field) != candidate.get(field):
            mismatches.append(
                {
                    "field": field,
                    "baseline": baseline.get(field),
                    "candidate": candidate.get(field),
                }
            )

    baseline_inputs = input_labels(baseline)
    candidate_inputs = input_labels(candidate)
    if baseline_inputs != candidate_inputs:
        mismatches.append(
            {
                "field": "inputs.labels",
                "baseline": baseline_inputs,
                "candidate": candidate_inputs,
            }
        )
    return mismatches


def compare_benchmark_reports(
    baseline: dict[str, Any],
    candidate: dict[str, Any],
    *,
    min_delta_pct: float,
    min_delta_mib_s: float,
) -> dict[str, Any]:
    configuration_mismatches = collect_configuration_mismatches(
        baseline,
        candidate,
        fields=(
            "modes",
            "parallel_workers",
            "parallel_modes",
            "opt_level",
            "opt_levels",
            "stmt_count",
            "parse_iterations",
            "sample_iterations",
            "repeats",
            "skip_real_corpus",
        ),
    )
    baseline_runs = {benchmark_run_key(entry): entry for entry in baseline.get("runs", [])}
    candidate_runs = {benchmark_run_key(entry): entry for entry in candidate.get("runs", [])}
    baseline_aggregates = {benchmark_aggregate_key(entry): entry for entry in baseline.get("aggregate_summaries", [])}
    candidate_aggregates = {benchmark_aggregate_key(entry): entry for entry in candidate.get("aggregate_summaries", [])}

    added_runs = sorted(candidate_runs.keys() - baseline_runs.keys())
    removed_runs = sorted(baseline_runs.keys() - candidate_runs.keys())
    changed_runs: list[dict[str, Any]] = []
    total_changed_runs = 0

    for key in sorted(baseline_runs.keys() & candidate_runs.keys()):
        before = baseline_runs[key]
        after = candidate_runs[key]
        before_avg = float(before.get("avg_MiB_s", 0.0))
        after_avg = float(after.get("avg_MiB_s", 0.0))
        if before_avg != after_avg:
            total_changed_runs += 1
            entry = {
                "variant": key[0],
                "execution": key[1],
                "worker_count": key[2],
                "opt_level": key[3],
                "input": key[4],
                "mode": key[5],
                "baseline_avg_MiB_s": before_avg,
                "candidate_avg_MiB_s": after_avg,
                "delta_MiB_s": after_avg - before_avg,
                "delta_pct": delta_percent(before_avg, after_avg),
            }
            if benchmark_change_is_significant(
                entry["delta_MiB_s"],
                entry["delta_pct"],
                min_delta_pct=min_delta_pct,
                min_delta_mib_s=min_delta_mib_s,
            ):
                changed_runs.append(entry)

    added_aggregates = sorted(candidate_aggregates.keys() - baseline_aggregates.keys())
    removed_aggregates = sorted(baseline_aggregates.keys() - candidate_aggregates.keys())
    changed_aggregates: list[dict[str, Any]] = []
    total_changed_aggregates = 0

    for key in sorted(baseline_aggregates.keys() & candidate_aggregates.keys()):
        before = baseline_aggregates[key]
        after = candidate_aggregates[key]
        if before == after:
            continue
        entry: dict[str, Any] = {
            "kind": key[0],
            "execution": key[1],
            "worker_count": key[2],
            "opt_level": key[3],
            "mode": key[4],
            "input": key[5],
            "fields": {},
        }
        for field in (
            "avg_MiB_s",
            "min_MiB_s",
            "max_MiB_s",
            "input_count",
            "current_avg_MiB_s",
            "inline_avg_MiB_s",
            "current_vs_inline_pct",
            "skipped",
            "no_successful_inputs",
        ):
            if before.get(field) != after.get(field):
                entry["fields"][field] = {"baseline": before.get(field), "candidate": after.get(field)}
        if entry["fields"]:
            total_changed_aggregates += 1
            delta_mib_s, delta_pct = aggregate_change_metrics(entry)
            entry["delta_MiB_s"] = delta_mib_s
            entry["delta_pct"] = delta_pct
            if benchmark_change_is_significant(
                delta_mib_s,
                delta_pct,
                min_delta_pct=min_delta_pct,
                min_delta_mib_s=min_delta_mib_s,
            ):
                changed_aggregates.append(entry)

    summary_delta: dict[str, int] = {}
    baseline_summary = baseline.get("summary", {})
    candidate_summary = candidate.get("summary", {})
    for field in ("run_count", "aggregate_count", "skipped_count", "opt_level_count", "parallel_worker_count", "parallel_mode_count"):
        summary_delta[field] = int(candidate_summary.get(field, 0)) - int(baseline_summary.get(field, 0))

    path_key_mismatches: list[dict[str, Any]] = []
    baseline_current_keys = sorted(baseline.get("current_benches_by_opt_level", {}).keys())
    candidate_current_keys = sorted(candidate.get("current_benches_by_opt_level", {}).keys())
    if baseline_current_keys != candidate_current_keys:
        path_key_mismatches.append(
            {
                "field": "current_benches_by_opt_level.keys",
                "baseline": baseline_current_keys,
                "candidate": candidate_current_keys,
            }
        )
    baseline_inline_keys = sorted(baseline.get("inline_benches_by_opt_level", {}).keys())
    candidate_inline_keys = sorted(candidate.get("inline_benches_by_opt_level", {}).keys())
    if baseline_inline_keys != candidate_inline_keys:
        path_key_mismatches.append(
            {
                "field": "inline_benches_by_opt_level.keys",
                "baseline": baseline_inline_keys,
                "candidate": candidate_inline_keys,
            }
        )
    baseline_current_parallel_keys = sorted(baseline.get("current_parallel_benches_by_opt_level", {}).keys())
    candidate_current_parallel_keys = sorted(candidate.get("current_parallel_benches_by_opt_level", {}).keys())
    if baseline_current_parallel_keys != candidate_current_parallel_keys:
        path_key_mismatches.append(
            {
                "field": "current_parallel_benches_by_opt_level.keys",
                "baseline": baseline_current_parallel_keys,
                "candidate": candidate_current_parallel_keys,
            }
        )
    baseline_inline_parallel_keys = sorted(baseline.get("inline_parallel_benches_by_opt_level", {}).keys())
    candidate_inline_parallel_keys = sorted(candidate.get("inline_parallel_benches_by_opt_level", {}).keys())
    if baseline_inline_parallel_keys != candidate_inline_parallel_keys:
        path_key_mismatches.append(
            {
                "field": "inline_parallel_benches_by_opt_level.keys",
                "baseline": baseline_inline_parallel_keys,
                "candidate": candidate_inline_parallel_keys,
            }
        )

    aggregate_configuration_mismatches: list[dict[str, Any]] = []
    if baseline_summary.get("inline_available") != candidate_summary.get("inline_available"):
        aggregate_configuration_mismatches.append(
            {
                "field": "summary.inline_available",
                "baseline": baseline_summary.get("inline_available"),
                "candidate": candidate_summary.get("inline_available"),
            }
        )
    if baseline_summary.get("parallel_enabled", False) != candidate_summary.get("parallel_enabled", False):
        aggregate_configuration_mismatches.append(
            {
                "field": "summary.parallel_enabled",
                "baseline": baseline_summary.get("parallel_enabled", False),
                "candidate": candidate_summary.get("parallel_enabled", False),
            }
        )

    changed_runs.sort(key=lambda entry: abs(entry["delta_pct"] if entry["delta_pct"] is not None else 0.0), reverse=True)
    changed_aggregates.sort(key=lambda entry: abs(entry["delta_pct"] if entry["delta_pct"] is not None else 0.0), reverse=True)

    return {
        "tool": "run_lua_frontend_storage_benchmark.py",
        "baseline_path": baseline.get("_path"),
        "candidate_path": candidate.get("_path"),
        "configuration_mismatches": configuration_mismatches,
        "path_key_mismatches": path_key_mismatches,
        "aggregate_configuration_mismatches": aggregate_configuration_mismatches,
        "thresholds": {
            "min_delta_pct": min_delta_pct,
            "min_delta_mib_s": min_delta_mib_s,
        },
        "summary_delta": summary_delta,
        "total_changed_runs": total_changed_runs,
        "filtered_out_runs": total_changed_runs - len(changed_runs),
        "added_runs": [
            {
                "variant": key[0],
                "execution": key[1],
                "worker_count": key[2],
                "opt_level": key[3],
                "input": key[4],
                "mode": key[5],
            }
            for key in added_runs
        ],
        "removed_runs": [
            {
                "variant": key[0],
                "execution": key[1],
                "worker_count": key[2],
                "opt_level": key[3],
                "input": key[4],
                "mode": key[5],
            }
            for key in removed_runs
        ],
        "changed_runs": changed_runs,
        "total_changed_aggregates": total_changed_aggregates,
        "filtered_out_aggregates": total_changed_aggregates - len(changed_aggregates),
        "added_aggregates": [
            {
                "kind": key[0],
                "execution": key[1],
                "worker_count": key[2],
                "opt_level": key[3],
                "mode": key[4],
                "input": key[5],
            }
            for key in added_aggregates
        ],
        "removed_aggregates": [
            {
                "kind": key[0],
                "execution": key[1],
                "worker_count": key[2],
                "opt_level": key[3],
                "mode": key[4],
                "input": key[5],
            }
            for key in removed_aggregates
        ],
        "changed_aggregates": changed_aggregates,
    }


