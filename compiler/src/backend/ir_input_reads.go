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
	CanonicalFromResult    bool
	// Syntax is captured at observation time, before later AST mutation.
	// It excludes source positions and does not encode semantic facts.
	SyntaxDigest      [32]byte
	SyntaxDigestValid bool
}

type irInputQueryRead struct {
	Result     *semantic.Result
	Context    string
	Kind, Name string
	Symbol     *semantic.Symbol
	Type       semantic.Type
	Found      bool
}

type irInputRangeEntry struct {
	Name   string
	Symbol *semantic.Symbol
	Type   semantic.Type
}

type irInputRangeRead struct {
	Result    *semantic.Result
	Context   string
	Kind, Use string
	Nil       bool
	Entries   []irInputRangeEntry
}

type irInputReadReport struct {
	Semantic  []semantic.SpecializationInputRead
	Codegen   []semantic.CodegenFactRead
	Bodies    []irFunctionBodyRead
	Queries   []irInputQueryRead
	Ranges    []irInputRangeRead
	Uncovered []string
	Lowered   bool
}

type irInputReadRecorder struct {
	semanticReads []semantic.SpecializationInputRead
	codegenReads  []semantic.CodegenFactRead
	mu            sync.Mutex
	bodies        []irFunctionBodyRead
	queries       []irInputQueryRead
	ranges        []irInputRangeRead
	seen          map[irFunctionBodyRead]bool
	lowered       bool
	// shared contains build-setup reads used by every partition (for example
	// declaration routing and shared symbol indexes). It is not another unit.
	shared *irInputReadRecorder
}

func (r *irInputReadRecorder) body(decl, canonical *ast.FuncDecl, use string) {
	r.recordBody(decl, canonical, use, false)
}

// bodyMapped records a private-to-source mapping obtained from the active
// specialization Result. It does not establish body or fact equivalence.
func (r *irInputReadRecorder) bodyMapped(decl, canonical *ast.FuncDecl, use string) {
	r.recordBody(decl, canonical, use, true)
}

func (r *irInputReadRecorder) recordBody(decl, canonical *ast.FuncDecl, use string, mapped bool) {
	if r == nil || decl == nil {
		return
	}
	read := irFunctionBodyRead{Declaration: decl, Canonical: canonical, Use: use, CanonicalFromResult: mapped}
	if index, err := semantic.IndexFunctionNodes(decl); err == nil {
		read.SyntaxDigest, read.SyntaxDigestValid = index.Fingerprint(), true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen == nil {
		r.seen = map[irFunctionBodyRead]bool{}
	}
	if !r.seen[read] {
		r.seen[read] = true
		r.bodies = append(r.bodies, read)
	}
}

func (r *irInputReadRecorder) query(result *semantic.Result, context, kind, name string, symbol *semantic.Symbol, found bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.queries = append(r.queries, irInputQueryRead{Result: result, Context: context, Kind: kind, Name: name, Symbol: symbol, Found: found})
	r.mu.Unlock()
}

func (r *irInputReadRecorder) namedTypeQuery(result *semantic.Result, name string, value semantic.Type, found bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.queries = append(r.queries, irInputQueryRead{Result: result, Context: "codegen", Kind: "named-type", Name: name, Type: value, Found: found})
	r.mu.Unlock()
}

func (r *irInputReadRecorder) rangeRead(read irInputRangeRead) {
	if r == nil {
		return
	}
	read.Entries = append([]irInputRangeEntry(nil), read.Entries...)
	r.mu.Lock()
	r.ranges = append(r.ranges, read)
	r.mu.Unlock()
}

func (r *irInputReadRecorder) codegenFactRead(read semantic.CodegenFactRead) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.codegenReads = append(r.codegenReads, read)
	r.mu.Unlock()
}

func (r *irInputReadRecorder) snapshot() irInputReadReport {
	if r == nil {
		return irInputReadReport{}
	}
	r.mu.Lock()
	report := irInputReadReport{
		Semantic: append([]semantic.SpecializationInputRead(nil), r.semanticReads...),
		Codegen:  append([]semantic.CodegenFactRead(nil), r.codegenReads...),
		Bodies:   append([]irFunctionBodyRead(nil), r.bodies...),
		Queries:  append([]irInputQueryRead(nil), r.queries...),
		Ranges:   cloneIRInputRanges(r.ranges),
		Lowered:  r.lowered,
		// These are hard coverage gaps, even for a unit with no observed
		// body reads. No caller may infer reuse authority from this report.
		Uncovered: []string{
			"owned and derived AST-keyed semantic facts, including proof/noalias",
			"semantic getter observations retain opaque AST keys and values without portable canonical encoding",
			"ExprTypes write and overlay-read effects are observed but lack canonical replay and portable encoding",
			"derived Result APIs and non-schema inputs (optimization facts, index-proof/span, memo and ABI queries) remain outside this recorder",
			"global declarations, layouts, contracts/defaults/laws, resolution candidate sets",
			"NamedTypes and GlobalScope values are observed as opaque in-process payloads; canonical value encoding is not implemented",
			"private semantic foreign-body consultations and inherited summary/memo reads",
			"reflective and opaque metadata walks that may carry foreign function syntax",
			"complete positive/negative query and lexical/captured environment coverage",
			"target/codegen settings and source-position/generated-name provenance",
		},
	}
	shared := r.shared
	r.mu.Unlock()
	if shared != nil && shared != r {
		common := shared.snapshot()
		report.Semantic = append(report.Semantic, common.Semantic...)
		report.Codegen = append(report.Codegen, common.Codegen...)
		report.Bodies = append(report.Bodies, common.Bodies...)
		report.Queries = append(report.Queries, common.Queries...)
		report.Ranges = append(report.Ranges, cloneIRInputRanges(common.Ranges)...)
	}
	return report
}

func cloneIRInputRanges(reads []irInputRangeRead) []irInputRangeRead {
	if reads == nil {
		return nil
	}
	copy := make([]irInputRangeRead, len(reads))
	for i, read := range reads {
		copy[i] = read
		copy[i].Entries = append([]irInputRangeEntry(nil), read.Entries...)
	}
	return copy
}

// semanticObservations preserves nil-declaration negative queries separately from
// concrete body reads. These observations retain unresolved summary provenance.
func (r *irInputReadRecorder) semanticObservations(reads []semantic.SpecializationInputRead) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.semanticReads = append(r.semanticReads, reads...)
}
