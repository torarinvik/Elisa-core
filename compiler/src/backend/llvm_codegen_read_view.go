//go:build cgo

package backend

import (
	"sort"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// irCodegenReadView is an explicit, generator-local path to the two global
// semantic indexes with the broadest direct use in LLVM lowering. It carries
// no authority: recorded type/symbol payloads are still opaque pointers and
// irInputReadReport keeps the encoder gap visible.
//
// Keep direct Result.NamedTypes and Result.GlobalScope field access inside this
// file. A source-coverage test enforces that boundary for production backend
// code. The nil-recorder branch is the ordinary fast path and performs no trace
// allocation or locking.
type irCodegenReadView struct {
	result   *semantic.Result
	recorder *irInputReadRecorder
}

func newIRCodegenReadView(result *semantic.Result, recorder *irInputReadRecorder) irCodegenReadView {
	return irCodegenReadView{result: result, recorder: recorder}
}

func (g *llvmGenerator) codegenReadView() irCodegenReadView {
	if g == nil {
		return irCodegenReadView{}
	}
	return newIRCodegenReadView(g.result, g.inputReads)
}

func (g *llvmGenerator) lookupNamedType(name string) (semantic.Type, bool) {
	return g.codegenReadView().LookupNamedType(name)
}

func (g *llvmGenerator) namedType(name string) semantic.Type {
	t, _ := g.lookupNamedType(name)
	return t
}

func (g *llvmGenerator) hasNamedTypes() bool {
	return g.codegenReadView().HasNamedTypes()
}

func (g *llvmGenerator) namedTypesCount() int {
	return g.codegenReadView().NamedTypesCount("named-type-index-size")
}

func (g *llvmGenerator) namedTypesMap(use string) map[string]semantic.Type {
	return g.codegenReadView().NamedTypesMap(use)
}

func (g *llvmGenerator) lookupGlobalSymbol(name, use string) (*semantic.Symbol, bool) {
	return g.codegenReadView().LookupGlobalSymbol(name, use)
}

func (g *llvmGenerator) semanticReadView() semantic.CodegenReadView {
	if g == nil {
		return semantic.NewCodegenReadView(nil, nil)
	}
	if g.inputReads == nil {
		return semantic.NewCodegenReadView(g.result, nil)
	}
	return semantic.NewCodegenReadView(g.result, g.inputReads.codegenFactRead)
}

// semanticReadViewFor binds observations to a Result returned by an explicit
// semantic query (for example IndexProofSpanResult). Such a Result is a
// distinct read context from g.result and must keep its own identity in the
// diagnostic stream.
func (g *llvmGenerator) semanticReadViewFor(result *semantic.Result) semantic.CodegenReadView {
	if g == nil || g.inputReads == nil {
		return semantic.NewCodegenReadView(result, nil)
	}
	return semantic.NewCodegenReadView(result, g.inputReads.codegenFactRead)
}

// indexProofSpanResult records which semantic view owns the queried proof.
// The observed pointers are diagnostic only and cannot authorize cache reuse.
func (g *llvmGenerator) indexProofSpanResult(expr *ast.IndexExpr) *semantic.Result {
	view := g.result.IndexProofSpanResult(expr)
	if g.inputReads != nil {
		g.inputReads.codegenFactRead(semantic.CodegenFactRead{Result: g.result, Table: "IndexProofSpan", Operation: "owner", Key: expr, Value: view, Present: view != nil})
	}
	return view
}

func (g *llvmGenerator) indexProofSpanBlocked(view *semantic.Result, expr *ast.IndexExpr) bool {
	blocked := view.IndexProofSpanBlocked(expr)
	if g.inputReads != nil {
		g.inputReads.codegenFactRead(semantic.CodegenFactRead{Result: view, Table: "IndexProofSpan", Operation: "blocked", Key: expr, Value: blocked, Present: true})
	}
	return blocked
}

func (g *llvmGenerator) lookupGlobal(name string) (*semantic.Symbol, bool) {
	return g.lookupGlobalSymbol(name, "backend-lookup")
}

func (g *llvmGenerator) codegenDeclarationResult(decl *ast.FuncDecl) *semantic.Result {
	if g == nil || g.result == nil {
		return nil
	}
	// CodegenDeclarationResult performs this lookup after its fast membership
	// checks. Record the result here as a conservative superset of that method's
	// conditional read; it keeps the semantic boundary explicit without changing
	// which canonical/private view is selected.
	if g.inputReads != nil {
		root := g.result.CanonicalCodegenResult()
		name := ""
		if decl != nil {
			name = decl.Name
		}
		newIRCodegenReadView(root, g.inputReads).LookupGlobalSymbol(name, "codegen-declaration-result")
	}
	return g.result.CodegenDeclarationResult(decl)
}

func (g *llvmGenerator) hasGlobalScope() bool {
	return g.codegenReadView().HasGlobalScope()
}

func (g *llvmGenerator) globalSymbolsCount() int {
	return g.codegenReadView().GlobalSymbolsCount("global-symbol-index-size")
}

func (g *llvmGenerator) globalSymbolsMap(use string) map[string]*semantic.Symbol {
	return g.codegenReadView().GlobalSymbolsMap(use)
}

func (v irCodegenReadView) LookupNamedType(name string) (semantic.Type, bool) {
	if v.result == nil {
		if v.recorder != nil {
			v.recorder.namedTypeQuery(v.result, name, nil, false)
		}
		return nil, false
	}
	typ, found := v.result.NamedTypes[name]
	if v.recorder != nil {
		v.recorder.namedTypeQuery(v.result, name, typ, found)
	}
	return typ, found
}

func (v irCodegenReadView) HasNamedTypes() bool {
	present := v.result != nil && v.result.NamedTypes != nil
	if v.recorder != nil {
		v.recorder.query(v.result, "named-types", "named-types-map-presence", "", nil, present)
	}
	return present
}

func (v irCodegenReadView) NamedTypesCount(use string) int {
	entries := v.NamedTypesMap(use)
	return len(entries)
}

func (v irCodegenReadView) NamedTypesMap(use string) map[string]semantic.Type {
	if v.result == nil {
		if v.recorder != nil {
			v.recorder.rangeRead(irInputRangeRead{Result: v.result, Context: "named-types", Kind: "named-types", Use: use, Nil: true})
		}
		return nil
	}
	entries := v.result.NamedTypes
	if v.recorder != nil {
		keys := make([]string, 0, len(entries))
		for key := range entries {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		read := irInputRangeRead{Result: v.result, Context: "named-types", Kind: "named-types", Use: use, Nil: entries == nil, Entries: make([]irInputRangeEntry, 0, len(keys))}
		for _, key := range keys {
			read.Entries = append(read.Entries, irInputRangeEntry{Name: key, Type: entries[key]})
		}
		v.recorder.rangeRead(read)
	}
	return entries
}

func (v irCodegenReadView) HasGlobalScope() bool {
	present := v.result != nil && v.result.GlobalScope != nil
	if v.recorder != nil {
		v.recorder.query(v.result, "global-scope", "global-scope-presence", "", nil, present)
	}
	return present
}

func (v irCodegenReadView) LookupGlobalSymbol(name, use string) (*semantic.Symbol, bool) {
	var symbol *semantic.Symbol
	var found bool
	if v.result != nil && v.result.GlobalScope != nil {
		symbol, found = v.result.GlobalScope.Lookup(name)
	}
	if v.recorder != nil {
		v.recorder.query(v.result, "global-scope", "global-symbol-"+use, name, symbol, found)
	}
	return symbol, found
}

func (v irCodegenReadView) GlobalSymbolsCount(use string) int {
	return len(v.GlobalSymbolsMap(use))
}

func (v irCodegenReadView) GlobalSymbolsMap(use string) map[string]*semantic.Symbol {
	var entries map[string]*semantic.Symbol
	if v.result != nil && v.result.GlobalScope != nil {
		entries = v.result.GlobalScope.Symbols
	}
	if v.recorder != nil {
		keys := make([]string, 0, len(entries))
		for key := range entries {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		read := irInputRangeRead{Result: v.result, Context: "global-scope", Kind: "global-scope-symbols", Use: use, Nil: entries == nil, Entries: make([]irInputRangeEntry, 0, len(keys))}
		for _, key := range keys {
			read.Entries = append(read.Entries, irInputRangeEntry{Name: key, Symbol: entries[key]})
		}
		v.recorder.rangeRead(read)
	}
	return entries
}
