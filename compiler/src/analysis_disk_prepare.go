package main

import (
	"io"
	"os"
	"time"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// This future belongs to one invocation. Its owner always joins it, including
// parse failures. Nothing survives to answer another compiler request.
type analysisDiskRestoration struct {
	artifact     analysisDiskArtifact
	started      time.Time
	done         chan struct{}
	state        *semantic.RetainedAnalysis
	preparation  analysisScratchPreparation
	err          error
	loaded       bool
	familyReplay bool
	bytes        int
}

func startAnalysisDiskRestoration(program *loadedProgram, options semantic.AnalyzeOptions) *analysisDiskRestoration {
	artifact, eligible := analysisDiskArtifactFor(program, options)
	if !eligible {
		return nil
	}
	// Established before either parser starts. Sharing tokens does not share AST
	// identity or semantic mutations, and does not retain a server/session cache.
	if program.tokenCache == nil {
		program.tokenCache = newRequestSourceTokenCache()
	}
	pending := &analysisDiskRestoration{artifact: artifact, started: time.Now(), done: make(chan struct{})}
	go func() {
		defer close(pending.done)
		data, err := artifact.read()
		// A family hint never changes the exact artifact's identity or header.
		// Only a missing exact artifact may try a validated added-file baseline.
		if os.IsNotExist(err) {
			if candidate, ok := analysisDiskAddedFilesBaseline(program, options); ok {
				data, err = candidate.read()
				pending.familyReplay = err == nil
			}
		}
		if err != nil {
			pending.err = err
			return
		}
		pending.loaded, pending.bytes = true, len(data)
		pending.state, pending.preparation, pending.err = restoreAnalysisWithScratch(data, program, options)
	}()
	return pending
}

func (p *analysisDiskRestoration) join() {
	if p != nil {
		<-p.done
	}
}

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
		if file, ok := parseProgramWithTokenCache(program.filename, program.source, io.Discard, program.tokenCache); ok {
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
