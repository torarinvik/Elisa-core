package main

import (
	"bytes"
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"strings"
	"testing"
)

func TestFrontendSMTTimingSurvivesAnalysisFailure(t *testing.T) {
	saved := frontendTimingEnabled
	defer func() { frontendTimingEnabled = saved }()
	for _, decoded := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			frontendTimingEnabled = enabled
			var output bytes.Buffer
			program := &loadedProgram{filename: "failed_timing.elisa", source: []byte("def work() -> i64:\n    return false\n")}
			if decoded {
				file, ok := parseProgram(program.filename, program.source, &output)
				if !ok {
					t.Fatal(output.String())
				}
				program.file = file
			}
			_, _, ok := analyzeLoadedProgramUsing(program, &output, semantic.AnalyzeOptions{}, func(file *ast.File, options semantic.AnalyzeOptions) *semantic.Result {
				result := semantic.AnalyzeWithOptions(file, options)
				result.SMTProfile = semantic.SMTStats{Enabled: true, Attempts: 3, Proven: 2, Declined: 1, CacheHits: 2, PersistentCacheHits: 2}
				return result
			})
			if ok {
				t.Fatal("invalid body accepted")
			}
			hasReport := strings.Contains(output.String(), "[front-smt] SMT tier: 3 obligations, 2 proven, 1 declined, 2 cached;")
			if hasReport != enabled {
				t.Fatalf("decoded=%v enabled=%v output=%s", decoded, enabled, output.String())
			}
			if enabled && !strings.Contains(output.String(), "2 persistent") {
				t.Fatal("persistent hits missing")
			}
		}
	}
}
