package main

import (
	"elisacore/src/backend"
	"elisacore/src/interpreter"
	"elisacore/src/semantic"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func semanticOptionsForCLI(options cliOptions) semantic.AnalyzeOptions {
	return semantic.AnalyzeOptions{
		TargetTriple:             options.targetTriple,
		TargetDebug:              effectiveOptimizationLevel(options) == backend.OptimizationLevel0,
		EnforceUnsafePermissions: options.strictPolicy,
		EnforceGlobalPermissions: options.globalsStrict,
		EnforceProgressSafety:    options.progressStrict || options.strictPolicy,
		EnforcePerfLints:         options.perfStrict,
		FlowLintMode:             options.flowLintMode,
		EnforceStrictConcurrency: options.concurrencyStrict,
		EnforceStrictProofs:      options.proofStrict,
		WarnDiscardedValues:      options.warnUnused,
		RequireExternContracts:   options.strictExterns,
		EnableSMT:                options.enableSMT,
		EmitProofHoleHints:       options.explainHole,
		RequiresReport:           options.requiresReport,
	}
}

func saveDebugTraceIfRequested(options cliOptions, debugger *interpreter.Debugger) error {
	if strings.TrimSpace(options.debugSaveTrace) == "" || debugger == nil {
		return nil
	}
	payload, err := interpreter.ExportDebugTrace(debugger)
	if err != nil {
		return err
	}
	return os.WriteFile(options.debugSaveTrace, payload, 0o644)
}

func writeOutputFile(path string, data []byte) error {
	if err := ensureOutputParentExists(path); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// printProofReport renders the refinement-discharge report under --explain (docs/85 observability):
// one line per obligation showing where it is, the subject and law, and how it was discharged
// (proven statically, refuted, or deferred to a runtime check). A trailing summary makes the
// static-vs-runtime split scannable.
func printProofReport(stderr io.Writer, report []semantic.ProofFact, explainHole bool) {
	fmt.Fprintln(stderr, "── refinement proof report (--explain) ──")
	if len(report) == 0 {
		fmt.Fprintln(stderr, "  (no refinement obligations)")
		return
	}
	var proven, assumed, refuted, runtime, measured int
	for _, f := range report {
		class := f.Class
		if class == "" {
			class = proofReportClass(f.Outcome)
		}
		fmt.Fprintf(stderr, "  %s: %s is %s — %s (%s)\n", f.Pos, f.Subject, f.Predicate, f.Outcome, class)
		if explainHole {
			fmt.Fprintf(stderr, "      goal: %s is %s\n", f.Subject, f.Predicate)
			fmt.Fprintln(stderr, "      known facts:")
			if len(f.KnownFacts) == 0 {
				fmt.Fprintln(stderr, "        - (none)")
			} else {
				for _, fact := range f.KnownFacts {
					fmt.Fprintf(stderr, "        - %s\n", fact)
				}
			}
		}
		if len(f.ClosedWorldFacts) > 0 {
			fmt.Fprintln(stderr, "      closed world:")
			for _, fact := range f.ClosedWorldFacts {
				fmt.Fprintf(stderr, "        - %s\n", fact)
			}
		}
		if f.Missing != "" {
			fmt.Fprintf(stderr, "      missing: %s\n", f.Missing)
		}
		switch f.Outcome {
		case semantic.ProofProvenFlow, semantic.ProofProvenLinear, semantic.ProofProvenConst, semantic.ProofProvenContract, semantic.ProofProvenSMT:
			proven++
		case semantic.ProofAssumedExtern:
			assumed++
		case semantic.ProofRefuted:
			refuted++
		case semantic.ProofRuntime:
			runtime++
		case semantic.ProofMeasured:
			measured++
		}
	}
	fmt.Fprintf(stderr, "  %d proven statically, %d assumed, %d runtime-checked, %d measured, %d refuted\n", proven, assumed, runtime, measured, refuted)
}

// printElisionSummary renders the per-category proof-elision telemetry under --explain (docs/85
// §elision-summary). One line per category shows how many checks were ELIDED by static proof vs
// how many fall back to a runtime guard, making the dogfooding payoff immediately scannable.
func printElisionSummary(stderr io.Writer, s semantic.ProofElisionSummary) {
	fmt.Fprintln(stderr, "── proof-elision summary (--explain) ──")
	printElisionLine(stderr, "return refinements ", s.ReturnRefinements)
	printElisionLine(stderr, "call-arg refinements", s.CallArgRefinements)
	printElisionLine(stderr, "array bounds        ", s.ArrayBounds)
	printElisionLine(stderr, "contract ensures    ", s.ContractEnsures)
}

func printElisionLine(stderr io.Writer, label string, c semantic.ProofElisionCounts) {
	total := c.Elided + c.Runtime
	if total == 0 {
		fmt.Fprintf(stderr, "  %s  —  (none)\n", label)
		return
	}
	pct := 0
	if total > 0 {
		pct = (c.Elided * 100) / total
	}
	fmt.Fprintf(stderr, "  %s  %d/%d elided (%d%% static)\n", label, c.Elided, total, pct)
}

// printRequiresReport renders the -requires-report blast-radius report (docs c3): one entry per
// (requires-bearing function, clause) showing how many direct call sites statically discharge the
// precondition vs fall back to a runtime check, listing the unprovable sites' locations. This
// surfaces, before committing, the new runtime-check obligations that adding a `requires` to a hot
// function would create.
func printRequiresReport(stderr io.Writer, report []semantic.RequiresReportEntry) {
	if len(report) == 0 {
		fmt.Fprintln(stderr, "requires-report: (no requires-bearing call sites)")
		return
	}
	for _, entry := range report {
		total := entry.Provable + entry.Unprovable
		fmt.Fprintf(stderr, "requires-report: %s  `%s`  — %d/%d call sites provable\n", entry.DeclName, entry.ClauseText, entry.Provable, total)
		if entry.Unprovable > 0 {
			fmt.Fprint(stderr, "  unprovable:")
			for _, pos := range entry.UnprovableSites {
				fmt.Fprintf(stderr, " %s", pos)
			}
			fmt.Fprintf(stderr, "  (%d)\n", entry.Unprovable)
		}
	}
}

func proofReportClass(outcome semantic.ProofOutcome) semantic.ProofDischargeClass {
	switch outcome {
	case semantic.ProofProvenFlow:
		return semantic.ProofClassFlow
	case semantic.ProofProvenLinear:
		return semantic.ProofClassLinear
	case semantic.ProofProvenConst:
		return semantic.ProofClassConst
	case semantic.ProofProvenSMT:
		return semantic.ProofClassSMT
	case semantic.ProofProvenContract:
		return semantic.ProofClassContract
	case semantic.ProofAssumedExtern:
		return semantic.ProofClassBoundary
	case semantic.ProofMeasured:
		return semantic.ProofClassMeasured
	default:
		return semantic.ProofClassRuntime
	}
}

// printPerfWarnings surfaces the backend's post-optimization performance-friction warnings (the
// auto-vectorization verifier) to the user, one per line on stderr.
func printPerfWarnings(stderr io.Writer, warnings []string) {
	for _, w := range warnings {
		fmt.Fprintln(stderr, w)
	}
}

func ensureOutputParentExists(path string) error {
	parent := filepath.Dir(path)
	if parent == "." || parent == "" {
		return nil
	}
	return os.MkdirAll(parent, 0o755)
}
