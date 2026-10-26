package main

import (
	"fmt"
	"io"
)

// Durable incomplete work is a command status, never a semantic diagnostic or
// successful compile. No Result exists until all semantic postchecks complete.
const analysisIncompleteExitCode = 75

type analysisCommandStatus struct {
	io.Writer
	incomplete bool
}

func (s *analysisCommandStatus) analysisIncomplete() { s.incomplete = true }
func reportAnalysisIncomplete(stderr io.Writer, message string) {
	fmt.Fprintf(stderr, "build incomplete: %s\n", message)
	if status, ok := stderr.(interface{ analysisIncomplete() }); ok {
		status.analysisIncomplete()
	}
}
