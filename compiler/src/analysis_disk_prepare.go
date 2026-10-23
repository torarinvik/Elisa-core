package main

import (
	"io"
	"time"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

type analysisScratchPreparation struct {
	file     *ast.File
	prepared *semantic.RetainedScratch
	elapsed  time.Duration
}

// Hydration only allocates and reconnects the checked graph; it does not run
// semantic analysis. A separate parse/declaration phase can therefore overlap
// it. Always join preparation before returning, including rejected snapshots,
// so fallback analysis and later requests never inherit background work.
func restoreAnalysisWithScratch(data []byte, program *loadedProgram, options semantic.AnalyzeOptions) (*semantic.RetainedAnalysis, analysisScratchPreparation, error) {
	done := make(chan analysisScratchPreparation, 1)
	go func() {
		started := time.Now()
		var preparation analysisScratchPreparation
		if file, ok := parseProgram(program.filename, program.source, io.Discard); ok {
			preparation.file = file
			preparation.prepared = semantic.PrepareRetainedScratch(file, options)
		}
		preparation.elapsed = time.Since(started)
		done <- preparation
	}()
	state, err := semantic.RestoreAnalysisState(data)
	preparation := <-done
	return state, preparation, err
}
