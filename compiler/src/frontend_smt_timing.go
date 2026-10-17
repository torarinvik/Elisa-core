package main

import (
	"elisacore/src/semantic"
	"fmt"
	"io"
)

// Emit before diagnostic handling: failed builds need the same cost evidence as
// successful ones. Keep this opt-in alongside existing frontend phase timing.
func frontendSMTTimingLog(stderr io.Writer, result *semantic.Result) {
	if !frontendTimingEnabled || result == nil {
		return
	}
	if report := result.SMTProfile.String(); report != "" {
		fmt.Fprintf(stderr, "[front-smt] %s\n", report)
	}
}
