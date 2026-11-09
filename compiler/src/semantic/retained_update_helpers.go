package semantic

import (
	"elisacore/src/ast"
	"sort"
	"strings"
)

// retainedPlainFunction is a function an update may add or remove in place: not a
// law, generic, hook or overload, whose only global registration is its name.
func retainedPlainFunction(fn *ast.FuncDecl) bool {
	return !fn.IsLaw && !fn.IsContract && !fn.IsLemma && !fn.IsGhost && len(fn.TypeParams) == 0 &&
		len(fn.GenericParams) == 0 && !strings.HasPrefix(fn.Name, "__")
}

// retainedMentionsAny reports whether fn's body, contracts, or parameter defaults
// name any of names. Defaults are scanned separately because they participate in
// retained invalidation without contributing function-body recursion edges.
func retainedMentionsAny(fn *ast.FuncDecl, names map[string]bool) bool {
	hit := false
	check := func(stmts []ast.Stmt) {
		walkStoreScanNodes(stmts, func(node any) {
			if hit {
				return
			}
			switch n := node.(type) {
			case *ast.Ident:
				hit = names[n.Name] || names[fillMayAdoptLastSegment(n.Name)]
			case *ast.FieldExpr:
				hit = names[n.Field]
			case *ast.StructLitExpr:
				hit = names[n.Name] || names[fillMayAdoptLastSegment(n.Name)]
			}
		})
	}
	check(funcScanStmts(fn))
	check(funcDefaultArgStmts(fn))
	return hit
}

// removeLiveFunctions drops removed functions from the live analysis: their
// global registration, per-function tables, diagnostics and late checks.
func (r *RetainedAnalysis) removeLiveFunctions(fns []*ast.FuncDecl) {
	a := r.a
	for _, fn := range fns {
		a.clearFunctionIndexBoundsProofs(fn, true)
	}
	gone := map[*ast.FuncDecl]bool{}
	for _, fn := range fns {
		gone[fn] = true
	}
	survivingNames := map[string]bool{}
	for fn := range a.funcDeclSymbols {
		if !gone[fn] {
			survivingNames[fn.Name] = true
		}
	}
	for _, fn := range fns {
		a.removeDisjointFunction(fn)
		// Removal already queued live callees/dependents through the old
		// caller edges. A dead declaration cannot be a future solver target;
		// retain every live dirty row while retracting this exact owner key.
		delete(a.disjointDirtyCallees, fn)
		sym := a.funcDeclSymbols[fn]
		name := fn.Name
		if sym != nil {
			// A block member is registered under its qualified name.
			name = sym.Name
		}
		delete(a.globalScope.Symbols, name)
		if sym != nil {
			if ft, ok := sym.Type.(*FuncType); ok {
				for _, key := range []string{fn.Name, name} {
					if a.functionTypes[key] == ft {
						delete(a.functionTypes, key)
					}
				}
				delete(a.regionPolyReturnsCallerArena, ft)
			}
		}
		delete(a.funcDeclSymbols, fn)
		delete(a.funcDeclUsings, fn)
		delete(a.declVisibility, fn)
		if a.file != nil {
			delete(a.file.DeclVisibility, fn)
		}
		delete(a.paramStoreTargets, fn)
		if !survivingNames[fn.Name] {
			delete(a.regionParamCalleeParamIndex, fn.Name)
		}
		// The symbol leaves its overload list, which is keyed by the visible name and not
		// by a receiver-mangled symbol name.
		for visible, list := range a.ufcsFunctionsByName {
			kept := list[:0:0]
			for _, s := range list {
				if s != sym {
					kept = append(kept, s)
				}
			}
			if len(kept) == 0 {
				delete(a.ufcsFunctionsByName, visible)
			} else if len(kept) != len(list) {
				a.ufcsFunctionsByName[visible] = kept
			}
		}
		a.ufcsFunctionsVersion++
		a.ufcsByLastSegment = nil
	}
	keep := func(d ast.Decl) bool {
		fn, ok := d.(*ast.FuncDecl)
		return !ok || !gone[fn]
	}
	decls := r.decls[:0]
	for _, sd := range r.decls {
		if keep(sd.Decl) {
			decls = append(decls, sd)
		}
	}
	r.decls = decls
	var prune func(decls []ast.Decl) []ast.Decl
	prune = func(decls []ast.Decl) []ast.Decl {
		out := decls[:0]
		for _, d := range decls {
			if n, ok := d.(*ast.NamespaceDecl); ok {
				n.Decls = prune(n.Decls)
			}
			if keep(d) {
				out = append(out, d)
			}
		}
		return out
	}
	a.file.Decls = prune(a.file.Decls)
	frames := r.lateFrames[:0]
	for _, f := range r.lateFrames {
		if !gone[f.frame.fn] {
			frames = append(frames, f)
		}
	}
	r.lateFrames = frames
}

// retainedTrimTrailing drops the comment and blank lines that end a span.
func retainedTrimTrailing(span string) string {
	lines := strings.Split(span, "\n")
	end := len(lines)
	for end > 1 {
		t := strings.TrimSpace(lines[end-1])
		if t != "" && !strings.HasPrefix(t, "#") {
			break
		}
		end--
	}
	return strings.Join(lines[:end], "\n")
}

type retainedDeclPosition struct {
	file      string
	line, col int
}

func retainedSortDeclsBySource(decls []ast.Decl, currentOffsets map[retainedDeclPosition]int) {
	// Retained declarations keep old physical offsets even when #line attribution
	// leaves their file/line unchanged after an include was inserted before them.
	// Compare in the current expanded source's coordinate system, never a mixture
	// of old retained and newly added physical offsets.
	offset := func(decl ast.Decl) int {
		pos := decl.Pos()
		if current, ok := currentOffsets[retainedDeclPosition{pos.File, pos.Line, pos.Col}]; ok {
			return current
		}
		return pos.Offset // synthesized declarations have no pristine counterpart
	}
	sort.SliceStable(decls, func(i, j int) bool { return offset(decls[i]) < offset(decls[j]) })
}

// Overload registration belongs to a namespace. Bare names remain deliberately
// conservative for caller invalidation, but a separate module's declaration
// must keep its existing type and specialization identities.
func retainedQualifiedFunctionNames(file *ast.File) map[*ast.FuncDecl]string {
	names := map[*ast.FuncDecl]string{}
	var walk func([]ast.Decl, string)
	walk = func(decls []ast.Decl, namespace string) {
		for _, decl := range decls {
			switch n := decl.(type) {
			case *ast.FuncDecl:
				names[n] = joinQualifiedName(namespace, n.Name)
			case *ast.NamespaceDecl:
				walk(n.Decls, joinQualifiedName(namespace, n.Name))
			}
		}
	}
	walk(file.Decls, "")
	return names
}

func retainedNoLineDelta(lineDelta map[string]int) bool {
	for _, d := range lineDelta {
		if d != 0 {
			return false
		}
	}
	return true
}

// Relocation moves syntax, not declaration-time symbol-table identities or source
// coordinates rendered into diagnostic text. Decline these authorities rather
// than pretending that a shifted AST has rerun their producers.
func (r *RetainedAnalysis) fastLineShiftAuthority(a *Analyzer, marks map[string][][2]int) string {
	for _, diagnostic := range r.pre.diags {
		if diagnostic.Severity == "" || diagnostic.Severity == DiagnosticSeverityError {
			return "line movement has declaration-prefix errors"
		}
		// The sentinel warning can render a callee declaration's source position.
		// Padding and builtin-shadow warnings have no such secondary location.
		if strings.Contains(diagnostic.Message, "(declared at ") {
			return "line movement has a source-location-bearing declaration diagnostic"
		}
	}
	for fn, symbol := range a.funcDeclSymbols {
		if fn == nil || symbol == nil || retainedShiftPos(fn.Pos(), marks).Line == fn.Pos().Line {
			continue
		}
		ft, ok := symbol.Type.(*FuncType)
		if !ok || ft == nil {
			continue
		}
		// Match the actual producer's symbol authority. Direct __init__ keeps
		// its ordinary name; only cast hooks and named-type constructor sugar
		// get these location-derived names. FuncType.Name itself is unchanged.
		if fn.Name == "__cast__" && symbol.Name == castHookSymbolName(ft.Name, ft, fn.Pos()) ||
			fn.Name != "__init__" && symbol.Name != ft.Name && symbol.Name == initHookSymbolName(ft.Name, ft, fn.Pos()) {
			return "line movement changes location-derived hook symbol " + symbol.Name
		}
	}
	return ""
}

// ProofCoordinatesDeferred reports that the last update skipped proof-report
// source relocation, so this state cannot be persisted.
func (r *RetainedAnalysis) ProofCoordinatesDeferred() bool { return r.proofCoordinatesDeferred }
