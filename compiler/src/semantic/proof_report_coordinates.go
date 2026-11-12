package semantic

import (
	"sync"

	"elisacore/src/ast"
)

type proofReportBindingRenderer = func(proofContribution, ProofFact) (ProofFact, string)

// Both inputs only read the authored syntax. Join the witness walk before
// returning, including if binding preparation panics, so retained analysis
// cannot mutate that syntax while a background walk is still using it.
func prepareProofReportInputs(capture func() proofReportCoordinateWitnesses, prepareBindings func() proofReportBindingRenderer) (proofReportCoordinateWitnesses, proofReportBindingRenderer) {
	var witnesses proofReportCoordinateWitnesses
	var witnessPanic any
	var witnessPanicked bool
	var pending sync.WaitGroup
	pending.Add(1)
	go func() {
		defer pending.Done()
		returned := false
		defer func() {
			if !returned {
				witnessPanic = recover()
				witnessPanicked = true
			}
		}()
		witnesses = capture()
		returned = true
	}()
	defer func() {
		pending.Wait()
		// The old sequential order captured witnesses before preparing
		// bindings. Preserve that panic precedence even if both fail.
		if witnessPanicked {
			panic(witnessPanic)
		}
	}()
	bindings := prepareBindings()
	pending.Wait()
	return witnesses, bindings
}

// prepareProofReportCoordinates builds a source-coordinate rendering adapter.
// Contribution ownership is already recorded by the producers; coordinates
// never select an owner. A rendered span must exist uniquely in the independently
// parsed edited syntax. This deliberately does not guess byte shifts from line
// counts, rewrite arbitrary strings, or copy any scratch semantic evidence.
func (r *RetainedAnalysis) prepareProofReportCoordinates(file *ast.File, marks map[string][][2]int) (func(*Analyzer) string, string) {
	old := make(map[proofContributionKey]proofContribution, len(r.a.proofLifecycle.Segments))
	for key, row := range r.a.proofLifecycle.Segments {
		old[key] = row
	}
	sameFacts := func(left, right []ProofFact) bool {
		return len(left) != 0 && len(left) == len(right) && &left[0] == &right[0]
	}
	witnesses, renderBindings := prepareProofReportInputs(
		func() proofReportCoordinateWitnesses { return captureProofReportCoordinateWitnesses(file) },
		func() proofReportBindingRenderer { return r.prepareProofReportBindings(file, marks) },
	)

	return func(a *Analyzer) string {
		requests := collectProofReportCoordinateRequests(a.proofLifecycle.Segments, old)
		render := witnesses.requestedRenderer(marks, requests)
		replacements := make(map[proofContributionKey]proofContribution, len(a.proofLifecycle.Segments))
		for key, row := range a.proofLifecycle.Segments {
			baseline, exists := old[key]
			unchanged := exists && sameFacts(baseline.Facts, row.Facts)
			facts := make([]ProofFact, len(row.Facts))
			for i, fact := range row.Facts {
				next, why := render(fact.Pos, unchanged)
				if why != "" {
					return why
				}
				fact, why = renderBindings(row, fact)
				if why != "" {
					return why
				}
				fact.Pos = next
				facts[i] = fact
			}
			row.Facts = facts
			replacements[key] = row
		}
		// No row is changed until every retained and newly emitted span is bound.
		a.proofLifecycle.Segments = replacements
		return ""
	}, ""
}
