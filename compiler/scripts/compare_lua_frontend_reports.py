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

from lua_report_comparison_data import (
    load_json,
    detect_report_kind,
    load_report,
    differential_case_key,
    benchmark_run_key,
    benchmark_aggregate_key,
    delta_percent,
    benchmark_change_is_significant,
    aggregate_change_metrics,
    diff_values,
    compare_metadata_reports,
    compare_differential_reports,
    input_labels,
    collect_configuration_mismatches,
    compare_benchmark_reports,
)
from lua_report_comparison_print import (
    print_differential_comparison,
    print_metadata_comparison,
    print_benchmark_comparison,
)



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