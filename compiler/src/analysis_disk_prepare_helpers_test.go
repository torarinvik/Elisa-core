package main

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"io"
)

func restoreAnalysisWithScratch(data []byte, program *loadedProgram, options semantic.AnalyzeOptions, lazy bool) (*semantic.RetainedAnalysis, analysisScratchPreparation, error) {
	if lazy {
		filename, source, tokens := program.filename, program.source, program.tokenCache
		prepared := semantic.NewLazyParsedRetainedScratch(func() (*ast.File, error) {
			file, ok := parseProgramWithTokenCache(filename, source, io.Discard, tokens)
			if !ok {
				return nil, fmt.Errorf("independent source parse rejected")
			}
			return file, nil
		}, options)
		state, err := semantic.RestoreAnalysisState(data)
		return state, analysisScratchPreparation{prepared: prepared}, err
	}
	done := startAnalysisScratchPreparation(program, options)
	state, err := semantic.RestoreAnalysisState(data)
	return state, <-done, err
}
