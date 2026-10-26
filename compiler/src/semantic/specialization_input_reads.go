package semantic

import "elisacore/src/ast"

// SpecializationInputRead is a diagnostic consultation, never a complete dependency
// manifest. Summary queries include inherited, negative and provisional outcomes;
// their provenance is deliberately unresolved by this trace.
type SpecializationInputRead struct {
	Declaration *ast.FuncDecl
	Use         string
}
type specializationInputTrace struct {
	reads []SpecializationInputRead
	seen  map[SpecializationInputRead]bool
}

// InputReadObservations returns an independent copy. Observed means tracing was
// enabled, not that dependency coverage is complete or reuse is authorized.
func (p *SpecializedFunction) InputReadObservations() ([]SpecializationInputRead, bool) {
	if p == nil {
		return nil, false
	}
	return append([]SpecializationInputRead(nil), p.inputReads...), p.inputReadsObserved
}
func (c *specializationContext) observeInputRead(fn *ast.FuncDecl, use string) {
	if c == nil || c.inputReads == nil {
		return
	}
	if original := c.worker.parallel.original[fn]; original != nil {
		fn = original
	}
	read := SpecializationInputRead{fn, use}
	trace := c.inputReads
	if trace.seen == nil {
		trace.seen = map[SpecializationInputRead]bool{}
	}
	if !trace.seen[read] {
		trace.seen[read] = true
		trace.reads = append(trace.reads, read)
	}
}
func (a *Analyzer) observeSpecializationInputRead(fn *ast.FuncDecl, use string) {
	if a.parallel != nil && a.parallel.specialization != nil {
		a.parallel.specialization.observeInputRead(fn, use)
	}
}
