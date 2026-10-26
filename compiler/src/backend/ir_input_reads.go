package backend

import (
	"sync"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// This diagnostic trace is deliberately not a cache key or a dependency
// certificate. In particular, observing a body request does not close private
// analysis's foreign summary reads or the semantic facts consumed by lowering.
type irFunctionBodyRead struct {
	Declaration, Canonical *ast.FuncDecl
	Use                    string
}

type irInputQueryRead struct {
	Kind, Name string
	Symbol     *semantic.Symbol
	Found      bool
}

type irInputReadReport struct {
	Semantic  []semantic.SpecializationInputRead
	Bodies    []irFunctionBodyRead
	Queries   []irInputQueryRead
	Uncovered []string
	Lowered   bool
}

type irInputReadRecorder struct {
	semanticReads []semantic.SpecializationInputRead
	mu            sync.Mutex
	bodies        []irFunctionBodyRead
	queries       []irInputQueryRead
	seen          map[irFunctionBodyRead]bool
	lowered       bool
}

func (r *irInputReadRecorder) body(decl, canonical *ast.FuncDecl, use string) {
	if r == nil || decl == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	read := irFunctionBodyRead{decl, canonical, use}
	if r.seen == nil {
		r.seen = map[irFunctionBodyRead]bool{}
	}
	if !r.seen[read] {
		r.seen[read] = true
		r.bodies = append(r.bodies, read)
	}
}

func (r *irInputReadRecorder) query(kind, name string, symbol *semantic.Symbol, found bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.queries = append(r.queries, irInputQueryRead{kind, name, symbol, found})
	r.mu.Unlock()
}

func (r *irInputReadRecorder) snapshot() irInputReadReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	return irInputReadReport{
		Semantic: append([]semantic.SpecializationInputRead(nil), r.semanticReads...),
		Bodies:   append([]irFunctionBodyRead(nil), r.bodies...),
		Queries:  append([]irInputQueryRead(nil), r.queries...),
		Lowered:  r.lowered,
		// These are hard coverage gaps, even for a unit with no observed
		// body reads. No caller may infer reuse authority from this report.
		Uncovered: []string{
			"owned and derived AST-keyed semantic facts, including proof/noalias",
			"global declarations, layouts, contracts/defaults/laws, resolution candidate sets",
			"private semantic foreign-body consultations and inherited summary/memo reads",
			"reflective and opaque metadata walks that may carry foreign function syntax",
			"complete positive/negative query and lexical/captured environment coverage",
			"target/codegen settings and source-position/generated-name provenance",
		},
	}
}

// semanticObservations preserves nil-declaration negative queries separately from
// concrete body reads. These observations retain unresolved summary provenance.
func (r *irInputReadRecorder) semanticObservations(reads []semantic.SpecializationInputRead) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.semanticReads = append(r.semanticReads, reads...)
}
