"""Render Lua frontend report comparisons."""

from __future__ import annotations

from typing import Any


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


