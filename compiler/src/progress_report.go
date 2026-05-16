package main

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"elisacore/src/semantic"
)

func generateProgressReport(result *semantic.Result) string {
	var out bytes.Buffer
	out.WriteString("=== progress ===\n")
	summaries := collectProgressSummaries(result)
	warnings := progressWarnings(result)
	fmt.Fprintf(&out, "warnings: %d\n", len(warnings))
	fmt.Fprintf(&out, "functions: %d\n", len(summaries))
	if len(summaries) != 0 {
		out.WriteString("function summaries:\n")
		for _, summary := range summaries {
			fmt.Fprintf(&out, "  %s: obligations=%s evidence=%s unsafe_nonprogress=%t\n",
				summary.Name,
				strings.Join(summary.Obligations, ", "),
				summary.Evidence,
				summary.UnsafeNonProgress,
			)
		}
	}
	if len(warnings) != 0 {
		out.WriteString("diagnostics:\n")
		for _, warning := range warnings {
			fmt.Fprintf(&out, "  %s\n", warning)
		}
	}
	return out.String()
}

type progressReportSummary struct {
	Name              string
	Obligations       []string
	Evidence          string
	UnsafeNonProgress bool
}

func collectProgressSummaries(result *semantic.Result) []progressReportSummary {
	if result == nil || len(result.ProgressSummaries) == 0 {
		return nil
	}
	items := make([]progressReportSummary, 0, len(result.ProgressSummaries))
	for _, summary := range result.ProgressSummaries {
		if summary == nil {
			continue
		}
		item := progressReportSummary{
			Name:              summary.Name,
			UnsafeNonProgress: summary.HasUnsafeNonProgress,
			Evidence:          "none",
		}
		if summary.HasProgressEvidence {
			item.Evidence = "progress"
		}
		if summary.HasUnsafeNonProgress {
			if item.Evidence == "none" {
				item.Evidence = "unsafe-nonprogress"
			} else {
				item.Evidence += "+unsafe-nonprogress"
			}
		}
		counts := map[semantic.ProgressObligationKind]int{}
		for _, obligation := range summary.Obligations {
			counts[obligation.Kind]++
		}
		for kind, count := range counts {
			item.Obligations = append(item.Obligations, fmt.Sprintf("%s:%d", kind, count))
		}
		if len(item.Obligations) == 0 {
			item.Obligations = []string{"none"}
		}
		sort.Strings(item.Obligations)
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items
}

func progressWarnings(result *semantic.Result) []string {
	if result == nil {
		return nil
	}
	warnings := result.Warnings()
	out := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		if strings.Contains(warning, "progress warning:") {
			out = append(out, warning)
		}
	}
	sort.Strings(out)
	return out
}
