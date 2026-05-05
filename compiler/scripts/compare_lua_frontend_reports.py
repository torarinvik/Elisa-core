#!/usr/bin/env python3
"""Compare structured Lua frontend report files or bundle directories.

Supports reports emitted by:
- run_lua_frontend_differential.py
- run_lua_frontend_storage_benchmark.py
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("baseline", help="Baseline JSON report path or bundle directory")
    parser.add_argument("candidate", help="Candidate JSON report path or bundle directory")
    parser.add_argument("--json-out", default=None, help="Optional path to write the structured comparison result")
    preset_group = parser.add_mutually_exclusive_group()
    preset_group.add_argument(
        "--ci-benchmark",
        action="store_true",
        help="Preset for CI benchmark comparisons: equivalent to --min-delta-pct 5 --fail-on-changes",
    )
    preset_group.add_argument(
        "--ci-bundle",
        action="store_true",
        help="Preset for CI bundle comparisons: equivalent to --min-delta-pct 5 --fail-on-changes (metadata drift stays non-failing unless --fail-on-metadata-change is also set)",
    )
    parser.add_argument(
        "--fail-on-changes",
        action="store_true",
        help="Exit non-zero when the comparison finds actionable changes (benchmark thresholds apply; metadata changes count only with --fail-on-metadata-change)",
    )
    parser.add_argument(
        "--fail-on-metadata-change",
        action="store_true",
        help="When evaluating failure, treat metadata.json field changes as actionable too",
    )
    parser.add_argument(
        "--min-delta-pct",
        type=float,
        default=None,
        help="For benchmark comparisons, only report run/aggregate throughput changes whose absolute percent delta meets this threshold",
    )
    parser.add_argument(
        "--min-delta-mib-s",
        type=float,
        default=None,
        help="For benchmark comparisons, only report run/aggregate throughput changes whose absolute MiB/s delta meets this threshold",
    )
    return parser.parse_args()


def resolve_comparison_policy(args: argparse.Namespace) -> dict[str, Any]:
    preset: str | None = None
    min_delta_pct: float | None = args.min_delta_pct
    min_delta_mib_s: float | None = args.min_delta_mib_s
    fail_on_changes = args.fail_on_changes
    fail_on_metadata_change = args.fail_on_metadata_change

    if args.ci_benchmark:
        preset = "ci-benchmark"
        if min_delta_pct is None:
            min_delta_pct = 5.0
        if min_delta_mib_s is None:
            min_delta_mib_s = 0.0
        fail_on_changes = True
    elif args.ci_bundle:
        preset = "ci-bundle"
        if min_delta_pct is None:
            min_delta_pct = 5.0
        if min_delta_mib_s is None:
            min_delta_mib_s = 0.0
        fail_on_changes = True

    if min_delta_pct is None:
        min_delta_pct = 0.0
    if min_delta_mib_s is None:
        min_delta_mib_s = 0.0

    return {
        "preset": preset,
        "min_delta_pct": min_delta_pct,
        "min_delta_mib_s": min_delta_mib_s,
        "fail_on_changes": fail_on_changes,
        "fail_on_metadata_change": fail_on_metadata_change,
    }


def comparison_overall_verdict(comparison: dict[str, Any]) -> str:
    tool = comparison["tool"]
    if tool == "run_lua_frontend_differential.py":
        if (
            comparison["configuration_mismatches"]
            or comparison["added_cases"]
            or comparison["removed_cases"]
            or comparison["changed_cases"]
        ):
            return "changed"
        return "clean"
    if tool == "run_lua_frontend_storage_benchmark.py":
        if (
            comparison["configuration_mismatches"]
            or comparison["path_key_mismatches"]
            or comparison["aggregate_configuration_mismatches"]
            or comparison["added_runs"]
            or comparison["removed_runs"]
            or comparison["changed_runs"]
            or comparison["added_aggregates"]
            or comparison["removed_aggregates"]
            or comparison["changed_aggregates"]
        ):
            return "changed"
        if comparison["filtered_out_runs"] > 0 or comparison["filtered_out_aggregates"] > 0:
            return "threshold-clean"
        return "clean"
    if tool == "metadata.json":
        if comparison["added_fields"] or comparison["removed_fields"] or comparison["changed_fields"]:
            return "metadata-drift"
        return "clean"
    if tool == "bundle_directory":
        if comparison["missing_components"]:
            return "changed"
        nested_verdicts = [
            nested.get("overall_verdict", comparison_overall_verdict(nested))
            for nested in comparison["components"].values()
        ]
        if any(verdict == "changed" for verdict in nested_verdicts):
            return "changed"
        if any(verdict == "metadata-drift" for verdict in nested_verdicts):
            return "metadata-drift"
        if any(verdict == "threshold-clean" for verdict in nested_verdicts):
            return "threshold-clean"
        return "clean"
    raise SystemExit(f"unsupported comparison tool: {tool!r}")


def annotate_verdicts(comparison: dict[str, Any]) -> None:
    if comparison["tool"] == "bundle_directory":
        for nested in comparison["components"].values():
            annotate_verdicts(nested)
    comparison["overall_verdict"] = comparison_overall_verdict(comparison)


def attach_bundle_summary(comparison: dict[str, Any]) -> None:
    if comparison["tool"] != "bundle_directory":
        return
    verdict_counts = {
        "clean": 0,
        "threshold-clean": 0,
        "changed": 0,
        "metadata-drift": 0,
    }
    components_by_verdict = {
        "clean": [],
        "threshold-clean": [],
        "changed": [],
        "metadata-drift": [],
    }
    for component_name, nested in comparison["components"].items():
        verdict = nested["overall_verdict"]
        verdict_counts[verdict] = verdict_counts.get(verdict, 0) + 1
        components_by_verdict.setdefault(verdict, []).append(component_name)
    comparison["component_summary"] = {
        "compared_components": len(comparison["components"]),
        "missing_components": len(comparison["missing_components"]),
        "verdict_counts": verdict_counts,
        "components_by_verdict": components_by_verdict,
    }


def attach_bundle_action_summary(
    comparison: dict[str, Any],
    *,
    fail_on_changes: bool,
    fail_on_metadata_change: bool,
) -> None:
    if comparison["tool"] != "bundle_directory":
        return

    actionable_components: list[str] = []
    non_actionable_changed_components: list[str] = []

    for component_name, nested in comparison["components"].items():
        if comparison_has_actionable_changes(nested, fail_on_metadata_change=fail_on_metadata_change):
            actionable_components.append(component_name)
        elif nested.get("overall_verdict") != "clean":
            non_actionable_changed_components.append(component_name)

    missing_component_names = [entry["component"] for entry in comparison["missing_components"]]
    failing_components = []
    if fail_on_changes:
        failing_components.extend(actionable_components)
        failing_components.extend(missing_component_names)

    comparison["action_summary"] = {
        "policy": {
            "fail_on_changes": fail_on_changes,
            "fail_on_metadata_change": fail_on_metadata_change,
        },
        "actionable_component_count": len(actionable_components),
        "actionable_components": actionable_components,
        "non_actionable_changed_component_count": len(non_actionable_changed_components),
        "non_actionable_changed_components": non_actionable_changed_components,
        "missing_component_count": len(missing_component_names),
        "missing_components": missing_component_names,
        "failing_component_count": len(failing_components),
        "failing_components": failing_components,
    }

def comparison_has_actionable_changes(comparison: dict[str, Any], *, fail_on_metadata_change: bool) -> bool:
    tool = comparison["tool"]
    if tool == "run_lua_frontend_differential.py":
        return bool(
            comparison["configuration_mismatches"]
            or comparison["added_cases"]
            or comparison["removed_cases"]
            or comparison["changed_cases"]
        )
    if tool == "run_lua_frontend_storage_benchmark.py":
        return bool(
            comparison["configuration_mismatches"]
            or comparison["path_key_mismatches"]
            or comparison["aggregate_configuration_mismatches"]
            or comparison["added_runs"]
            or comparison["removed_runs"]
            or comparison["changed_runs"]
            or comparison["added_aggregates"]
            or comparison["removed_aggregates"]
            or comparison["changed_aggregates"]
        )
    if tool == "metadata.json":
        if not fail_on_metadata_change:
            return False
        return bool(comparison["added_fields"] or comparison["removed_fields"] or comparison["changed_fields"])
    if tool == "bundle_directory":
        if comparison["missing_components"]:
            return True
        for nested in comparison["components"].values():
            if comparison_has_actionable_changes(nested, fail_on_metadata_change=fail_on_metadata_change):
                return True
        return False
    raise SystemExit(f"unsupported comparison tool: {tool!r}")


def attach_exit_evaluation(
    comparison: dict[str, Any],
    *,
    fail_on_changes: bool,
    fail_on_metadata_change: bool,
) -> None:
    actionable_changes = comparison_has_actionable_changes(
        comparison,
        fail_on_metadata_change=fail_on_metadata_change,
    )
    comparison["exit_evaluation"] = {
        "fail_on_changes": fail_on_changes,
        "fail_on_metadata_change": fail_on_metadata_change,
        "actionable_changes": actionable_changes,
        "would_fail": fail_on_changes and actionable_changes,
    }


def attach_comparison_policy(comparison: dict[str, Any], policy: dict[str, Any]) -> None:
    comparison["comparison_policy"] = policy


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


def print_differential_comparison(comparison: dict[str, Any]) -> None:
    print("REPORT type=differential")
    print(f"baseline={comparison['baseline_path']}")
    print(f"candidate={comparison['candidate_path']}")
    print(f"CONFIG_COUNTS changed={len(comparison['configuration_mismatches'])}")
    for entry in comparison["configuration_mismatches"][:10]:
        print(f"CONFIG field={entry['field']} baseline={entry['baseline']!r} candidate={entry['candidate']!r}")
    summary = comparison["summary_delta"]
    print(
        "SUMMARY_DELTA "
        f"cases={summary['cases']} "
        f"reference_mismatches={summary['reference_mismatches']} "
        f"expectation_mismatches={summary['expectation_mismatches']} "
        f"fingerprint_mismatches={summary['fingerprint_mismatches']}"
    )
    print(
        "CASE_COUNTS "
        f"added={len(comparison['added_cases'])} "
        f"removed={len(comparison['removed_cases'])} "
        f"changed={len(comparison['changed_cases'])}"
    )
    for case in comparison["changed_cases"][:10]:
        changed_fields = ", ".join(sorted(case["fields"].keys()))
        print(f"CHANGED family={case['family']} case={case['case']} fields={changed_fields}")


def print_metadata_comparison(comparison: dict[str, Any]) -> None:
    print("REPORT type=metadata")
    print(f"baseline={comparison['baseline_path']}")
    print(f"candidate={comparison['candidate_path']}")
    print(
        "FIELD_COUNTS "
        f"added={len(comparison['added_fields'])} "
        f"removed={len(comparison['removed_fields'])} "
        f"changed={len(comparison['changed_fields'])}"
    )
    for entry in comparison["changed_fields"][:10]:
        print(f"CHANGED field={entry['field']} baseline={entry['baseline']!r} candidate={entry['candidate']!r}")
    for entry in comparison["added_fields"][:10]:
        print(f"ADDED field={entry['field']} candidate={entry['candidate']!r}")
    for entry in comparison["removed_fields"][:10]:
        print(f"REMOVED field={entry['field']} baseline={entry['baseline']!r}")


def print_benchmark_comparison(comparison: dict[str, Any]) -> None:
    print("REPORT type=benchmark")
    print(f"baseline={comparison['baseline_path']}")
    print(f"candidate={comparison['candidate_path']}")
    print(
        "CONFIG_COUNTS "
        f"report={len(comparison['configuration_mismatches'])} "
        f"path_keys={len(comparison['path_key_mismatches'])} "
        f"aggregate={len(comparison['aggregate_configuration_mismatches'])}"
    )
    for entry in comparison["configuration_mismatches"][:10]:
        print(f"CONFIG field={entry['field']} baseline={entry['baseline']!r} candidate={entry['candidate']!r}")
    for entry in comparison["path_key_mismatches"][:10]:
        print(f"CONFIG field={entry['field']} baseline={entry['baseline']!r} candidate={entry['candidate']!r}")
    for entry in comparison["aggregate_configuration_mismatches"][:10]:
        print(f"CONFIG field={entry['field']} baseline={entry['baseline']!r} candidate={entry['candidate']!r}")
    thresholds = comparison["thresholds"]
    print(
        "THRESHOLDS "
        f"min_delta_pct={thresholds['min_delta_pct']:.2f} "
        f"min_delta_mib_s={thresholds['min_delta_mib_s']:.2f}"
    )
    summary = comparison["summary_delta"]
    print(
        "SUMMARY_DELTA "
        f"run_count={summary['run_count']} "
        f"aggregate_count={summary['aggregate_count']} "
        f"skipped_count={summary['skipped_count']} "
        f"opt_level_count={summary['opt_level_count']} "
        f"parallel_worker_count={summary['parallel_worker_count']} "
        f"parallel_mode_count={summary['parallel_mode_count']}"
    )
    print(
        "RUN_COUNTS "
        f"added={len(comparison['added_runs'])} "
        f"removed={len(comparison['removed_runs'])} "
        f"changed={len(comparison['changed_runs'])} "
        f"filtered_out={comparison['filtered_out_runs']} "
        f"total_changed={comparison['total_changed_runs']}"
    )
    for run in comparison["changed_runs"][:10]:
        delta_pct = "n/a" if run["delta_pct"] is None else f"{run['delta_pct']:+.2f}%"
        execution_text = f" execution={run['execution']}"
        if run["worker_count"]:
            execution_text += f" worker_count={run['worker_count']}"
        print(
            f"CHANGED variant={run['variant']}{execution_text} opt_level={run['opt_level']} input={run['input']} mode={run['mode']} "
            f"baseline_avg={run['baseline_avg_MiB_s']:.2f} candidate_avg={run['candidate_avg_MiB_s']:.2f} delta_pct={delta_pct}"
        )
    print(
        "AGGREGATE_COUNTS "
        f"added={len(comparison['added_aggregates'])} "
        f"removed={len(comparison['removed_aggregates'])} "
        f"changed={len(comparison['changed_aggregates'])} "
        f"filtered_out={comparison['filtered_out_aggregates']} "
        f"total_changed={comparison['total_changed_aggregates']}"
    )
    for entry in comparison["changed_aggregates"][:10]:
        delta_pct = entry.get("delta_pct")
        if delta_pct is None:
            changed_fields = ", ".join(sorted(entry["fields"].keys()))
            execution_text = f" execution={entry['execution']}"
            if entry["worker_count"]:
                execution_text += f" worker_count={entry['worker_count']}"
            print(
                f"CHANGED_AGGREGATE kind={entry['kind']}{execution_text} opt_level={entry['opt_level']} mode={entry['mode']} input={entry['input']} "
                f"fields={changed_fields}"
            )
        else:
            execution_text = f" execution={entry['execution']}"
            if entry["worker_count"]:
                execution_text += f" worker_count={entry['worker_count']}"
            print(
                f"CHANGED_AGGREGATE kind={entry['kind']}{execution_text} opt_level={entry['opt_level']} mode={entry['mode']} input={entry['input']} "
                f"delta_pct={delta_pct:+.2f}"
            )


def compare_loaded_reports(
    baseline: dict[str, Any],
    candidate: dict[str, Any],
    *,
    min_delta_pct: float,
    min_delta_mib_s: float,
) -> dict[str, Any]:
    baseline_tool = detect_report_kind(baseline)
    candidate_tool = detect_report_kind(candidate)
    if baseline_tool != candidate_tool:
        raise SystemExit(f"report tools do not match: {baseline_tool!r} vs {candidate_tool!r}")

    if baseline_tool == "run_lua_frontend_differential.py":
        return compare_differential_reports(baseline, candidate)
    if baseline_tool == "run_lua_frontend_storage_benchmark.py":
        return compare_benchmark_reports(
            baseline,
            candidate,
            min_delta_pct=min_delta_pct,
            min_delta_mib_s=min_delta_mib_s,
        )
    if baseline_tool == "metadata.json":
        return compare_metadata_reports(baseline, candidate)
    raise SystemExit(f"unsupported report tool: {baseline_tool!r}")


def print_comparison(comparison: dict[str, Any]) -> None:
    comparison_policy = comparison.get("comparison_policy")
    if comparison_policy is not None and comparison_policy.get("preset") is not None:
        print(
            "POLICY "
            f"preset={comparison_policy['preset']} "
            f"min_delta_pct={comparison_policy['min_delta_pct']:.2f} "
            f"min_delta_mib_s={comparison_policy['min_delta_mib_s']:.2f} "
            f"fail_on_changes={1 if comparison_policy['fail_on_changes'] else 0} "
            f"fail_on_metadata_change={1 if comparison_policy['fail_on_metadata_change'] else 0}"
        )

    tool = comparison["tool"]
    if tool == "run_lua_frontend_differential.py":
        print_differential_comparison(comparison)
    elif tool == "run_lua_frontend_storage_benchmark.py":
        print_benchmark_comparison(comparison)
    elif tool == "metadata.json":
        print_metadata_comparison(comparison)
    elif tool == "bundle_directory":
        print("REPORT type=bundle")
        print(f"baseline={comparison['baseline_path']}")
        print(f"candidate={comparison['candidate_path']}")
        print(
            "COMPONENT_COUNTS "
            f"compared={len(comparison['components'])} "
            f"missing={len(comparison['missing_components'])}"
        )
        component_summary = comparison.get("component_summary")
        if component_summary is not None:
            verdict_counts = component_summary["verdict_counts"]
            print(
                "BUNDLE_SUMMARY "
                f"clean={verdict_counts.get('clean', 0)} "
                f"threshold-clean={verdict_counts.get('threshold-clean', 0)} "
                f"changed={verdict_counts.get('changed', 0)} "
                f"metadata-drift={verdict_counts.get('metadata-drift', 0)}"
            )
        action_summary = comparison.get("action_summary")
        if action_summary is not None:
            print(
                "ACTION_SUMMARY "
                f"actionable={action_summary['actionable_component_count']} "
                f"ignored={action_summary['non_actionable_changed_component_count']} "
                f"missing={action_summary['missing_component_count']} "
                f"failing={action_summary['failing_component_count']}"
            )
        for entry in comparison["missing_components"]:
            print(
                f"MISSING component={entry['component']} "
                f"baseline_exists={entry['baseline_exists']} "
                f"candidate_exists={entry['candidate_exists']}"
            )
        for component_name in ("metadata", "benchmark", "differential"):
            nested = comparison["components"].get(component_name)
            if nested is not None:
                print(f"COMPONENT name={component_name}")
                print_comparison(nested)
    else:
        raise SystemExit(f"unsupported comparison tool: {tool!r}")

    overall_verdict = comparison.get("overall_verdict")
    if overall_verdict is not None:
        print(f"VERDICT overall={overall_verdict}")

    exit_evaluation = comparison.get("exit_evaluation")
    if exit_evaluation is not None:
        print(
            "EXIT_EVALUATION "
            f"fail_on_changes={1 if exit_evaluation['fail_on_changes'] else 0} "
            f"fail_on_metadata_change={1 if exit_evaluation['fail_on_metadata_change'] else 0} "
            f"actionable_changes={1 if exit_evaluation['actionable_changes'] else 0} "
            f"would_fail={1 if exit_evaluation['would_fail'] else 0}"
        )


def compare_bundle_directories(
    baseline_dir: Path,
    candidate_dir: Path,
    *,
    min_delta_pct: float,
    min_delta_mib_s: float,
) -> dict[str, Any]:
    components: dict[str, dict[str, Any]] = {}
    missing_components: list[dict[str, Any]] = []
    for component_name in ("metadata", "benchmark", "differential"):
        file_name = f"{component_name}.json"
        baseline_path = baseline_dir / file_name
        candidate_path = candidate_dir / file_name
        baseline_exists = baseline_path.exists()
        candidate_exists = candidate_path.exists()
        if baseline_exists and candidate_exists:
            components[component_name] = compare_loaded_reports(
                load_report(baseline_path),
                load_report(candidate_path),
                min_delta_pct=min_delta_pct,
                min_delta_mib_s=min_delta_mib_s,
            )
        elif baseline_exists or candidate_exists:
            missing_components.append(
                {
                    "component": component_name,
                    "baseline_exists": baseline_exists,
                    "candidate_exists": candidate_exists,
                }
            )
    return {
        "tool": "bundle_directory",
        "baseline_path": str(baseline_dir),
        "candidate_path": str(candidate_dir),
        "components": components,
        "missing_components": missing_components,
    }


def main() -> int:
    args = parse_args()
    policy = resolve_comparison_policy(args)
    baseline_path = Path(args.baseline)
    candidate_path = Path(args.candidate)
    if baseline_path.is_dir() or candidate_path.is_dir():
        if not baseline_path.is_dir() or not candidate_path.is_dir():
            raise SystemExit("baseline and candidate must both be directories when using bundle comparison")
        comparison = compare_bundle_directories(
            baseline_path,
            candidate_path,
            min_delta_pct=policy["min_delta_pct"],
            min_delta_mib_s=policy["min_delta_mib_s"],
        )
    else:
        comparison = compare_loaded_reports(
            load_report(baseline_path),
            load_report(candidate_path),
            min_delta_pct=policy["min_delta_pct"],
            min_delta_mib_s=policy["min_delta_mib_s"],
        )

    annotate_verdicts(comparison)
    attach_bundle_summary(comparison)
    attach_comparison_policy(comparison, policy)
    attach_bundle_action_summary(
        comparison,
        fail_on_changes=policy["fail_on_changes"],
        fail_on_metadata_change=policy["fail_on_metadata_change"],
    )
    attach_exit_evaluation(
        comparison,
        fail_on_changes=policy["fail_on_changes"],
        fail_on_metadata_change=policy["fail_on_metadata_change"],
    )

    print_comparison(comparison)

    if args.json_out:
        out_path = Path(args.json_out)
        out_path.parent.mkdir(parents=True, exist_ok=True)
        out_path.write_text(json.dumps(comparison, indent=2, sort_keys=True) + "\n", encoding="utf-8")

    return 1 if comparison["exit_evaluation"]["would_fail"] else 0


if __name__ == "__main__":
    raise SystemExit(main())