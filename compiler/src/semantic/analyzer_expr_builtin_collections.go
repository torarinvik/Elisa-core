package semantic

import (
	"elisacore/src/ast"
)

// checkDarrayGrowthRegionEscape rejects growing a darray whose storage outlives
// the current function while the ambient allocation arena is a function-local
// `Arena` value. A darray records no "home" arena: each push/extend/reserve
// grows the backing buffer from whatever `in <arena>:` scope is active. So
// growing a longer-lived darray (a struct field reached through a reference, a
// global, or a `mutable darray&` parameter) from a local `arena: Arena = zeroed`
// allocates the new buffer in that local arena, which is freed when the function
// returns — leaving the longer-lived darray pointing at freed memory. This was a
// silent, ASLR-dependent use-after-free source. The fix is to allocate from a
// persistent arena (a global `Arena`, or an `Arena&` parameter the caller owns).
func (a *Analyzer) checkDarrayGrowthRegionEscape(receiver ast.Expr, op string) {
	if a == nil || receiver == nil || a.staticContextDepth != 0 {
		return
	}
	if a.checkContainerGrowthStoreEscape(receiver, "darray "+op) {
		return
	}
	arenaName, ok := a.ambientArenaLocalValueName()
	if !ok {
		return
	}
	if a.lvalueStorageOutlivesFunction(receiver) {
		a.errorf(receiver.Pos(), "darray %s grows a non-local darray from local arena %q; its backing buffer is freed when %q goes out of scope (use-after-free). Allocate from a persistent arena instead (a global Arena, or an Arena& parameter owned by the caller)", op, arenaName, arenaName)
		return
	}
	// The receiver is a function-local collection grown in a function-local
	// arena: its backing now lives in that arena. Record it so that returning it
	// (or a pointer into it) or storing it into a longer-lived location is caught
	// downstream as a use-after-free.
	if sym := a.rootLocalValueSymbol(receiver); sym != nil {
		if a.localArenaEscapeLocals == nil {
			a.localArenaEscapeLocals = map[*Symbol]string{}
		}
		a.localArenaEscapeLocals[sym] = arenaName
	}
}

// checkContainerGrowthStoreEscape rejects a growth that writes a longer-lived container's
// replacement backing into a shorter or unrelated active allocation region. In particular,
// region-polymorphic parameters must not be made to appear safe merely because an `in scratch:`
// scope supplies some allocator: unless that allocator is the container's own region or has
// program lifetime, the container would retain a pointer into storage that can disappear first.
func (a *Analyzer) checkContainerGrowthStoreEscape(receiver ast.Expr, operation string) bool {
	if a == nil || receiver == nil || a.currentAllocExpr == nil {
		return false
	}
	storeRegion := storeRegionRootName(a.currentAllocExpr)
	if storeRegion == "" || a.isCompilerGeneratedAutoRegion(storeRegion) || !a.activeStoreIsLocalRegion(storeRegion) {
		return false
	}
	receiverType := a.exprType(receiver)
	if receiverType == nil {
		receiverType = a.analyzeExpr(receiver)
	}
	containerRegion := containerOrEntryRegion(receiverType)
	if storeRegion == containerRegion || (!a.containerReceiverStorageOutlivesFunction(receiver) && !a.regionOutlives(containerRegion, storeRegion)) {
		return false
	}
	a.errorf(receiver.Pos(), "%s allocates into function-scoped region %q while growing non-local storage in region %q; this would leave the container pointing at freed memory", operation, storeRegion, a.regionDisplayName(containerRegion))
	return true
}

func (a *Analyzer) activeStoreIsLocalRegion(name string) bool {
	if a == nil || name == "" || a.currentScope == nil || a.lookupRegionParam(name) {
		return false
	}
	sym, ok := a.currentScope.Lookup(name)
	return ok && sym != nil && sym.Kind == SymbolRegion
}

func (a *Analyzer) containerReceiverStorageOutlivesFunction(expr ast.Expr) bool {
	if a == nil || expr == nil {
		return false
	}
	if _, global := a.globalStorageRoot(expr); global {
		return true
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.containerReceiverStorageOutlivesFunction(n.Inner)
	case *ast.AddrOfExpr:
		return a.containerReceiverStorageOutlivesFunction(n.Operand)
	case *ast.CastExpr:
		return a.containerReceiverStorageOutlivesFunction(n.Operand)
	case *ast.FieldExpr:
		if a.exprObjectIsReference(n.Object) {
			return true
		}
		return a.containerReceiverStorageOutlivesFunction(n.Object)
	case *ast.IndexExpr:
		if a.exprObjectIsReference(n.Object) {
			return true
		}
		return a.containerReceiverStorageOutlivesFunction(n.Object)
	case *ast.CallExpr:
		if member, ok := n.Func.(*ast.FieldExpr); ok && member != nil {
			// Interior views such as `dict.entry(key)` keep pointing into their receiver's
			// backing. Follow that owner through the call instead of treating the temporary
			// entry expression as function-local storage.
			return a.containerReceiverStorageOutlivesFunction(member.Object)
		}
		return false
	case *ast.Ident:
		if a.currentScope == nil {
			return false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym == nil {
			return false
		}
		if sym.Kind == SymbolGlobal || sym.Kind == SymbolExternVar {
			return true
		}
		if sym.Kind != SymbolLocal && sym.Kind != SymbolParam {
			return false
		}
		_, isReference := sym.Type.(*RefType)
		return isReference
	default:
		return false
	}
}

// rootLocalValueSymbol walks an lvalue/path expression down to its root
// identifier and returns that symbol when it is a by-value local of the current
// function (the storage we own). It returns nil for parameters, globals, local
// references (which point at storage owned elsewhere), or unresolved roots.
func (a *Analyzer) rootLocalValueSymbol(expr ast.Expr) *Symbol {
	for expr != nil {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.FieldExpr:
			expr = n.Object
		case *ast.IndexExpr:
			expr = n.Object
		case *ast.AddrOfExpr:
			expr = n.Operand
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.Ident:
			if a.currentScope == nil {
				return nil
			}
			sym, ok := a.currentScope.Lookup(n.Name)
			if !ok || sym == nil {
				return nil
			}
			sym = symbolAliasRoot(sym)
			if sym.Kind != SymbolLocal {
				return nil
			}
			if _, isRef := sym.Type.(*RefType); isRef {
				return nil
			}
			return sym
		default:
			return nil
		}
	}
	return nil
}

// checkLocalArenaEscape flags returning or storing a value whose collection
// backing was grown in a function-local arena (recorded in localArenaEscapeLocals).
// Only values that actually alias the backing are flagged: a reference into it,
// or the collection header itself. Scalar fields (e.g. `.count`) and by-value
// element reads (`xs[i]`) are copies and cannot alias the freed buffer.
func (a *Analyzer) checkLocalArenaEscape(value ast.Expr, valueType Type, verb string) {
	if a == nil || value == nil || len(a.localArenaEscapeLocals) == 0 {
		return
	}
	var sym *Symbol
	switch valueType.(type) {
	case *RefType:
		// A reference result aliases storage: if it roots at a marked local it
		// points into a buffer that the local arena will free.
		sym = a.rootLocalValueSymbol(value)
	case *DArrayType:
		// A collection value carries the backing pointer; it escapes only when it
		// IS the marked collection (reached via field/ident, never via an index
		// which copies an element into a fresh value).
		sym = a.collectionValueRoot(value)
	default:
		return
	}
	if sym == nil {
		return
	}
	arenaName, ok := a.localArenaEscapeLocals[sym]
	if !ok {
		return
	}
	a.errorf(value.Pos(), "cannot %s %q: its backing was allocated in local arena %q, which is freed when the function returns (use-after-free). Build it in a persistent arena instead (a global Arena, or an Arena& parameter owned by the caller)", verb, sym.Name, arenaName)
}

// collectionValueRoot walks a collection-typed expression to the local it names,
// following only field/paren steps (which preserve the collection-header alias)
// and stopping at indexing (which yields an element copy) or other forms.
func (a *Analyzer) collectionValueRoot(expr ast.Expr) *Symbol {
	for expr != nil {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.FieldExpr:
			expr = n.Object
		case *ast.Ident:
			if a.currentScope == nil {
				return nil
			}
			sym, ok := a.currentScope.Lookup(n.Name)
			if !ok || sym == nil {
				return nil
			}
			sym = symbolAliasRoot(sym)
			if sym.Kind != SymbolLocal {
				return nil
			}
			if _, isRef := sym.Type.(*RefType); isRef {
				return nil
			}
			return sym
		default:
			return nil
		}
	}
	return nil
}

// ambientArenaLocalValueName returns the name of the active allocation arena
// when it is transient: a function-local `Arena` value or a scoped `region`
// owner. It returns false for a global Arena, an Arena& reference local/parameter
// (which points at a longer-lived arena), or any non-trivial owner expression —
// i.e. cases that are persistent or that we cannot prove transient, to avoid
// false positives.
func (a *Analyzer) ambientArenaLocalValueName() (string, bool) {
	if a.currentAllocExpr == nil {
		return "", false
	}
	ident, ok := stripParenExpr(a.currentAllocExpr).(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil {
		return "", false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return "", false
	}
	// A `region NAME(...):` owner is always freed at the end of its lexical block.
	// Treat it like a local Arena value so mutations of caller-owned containers
	// inside `in NAME:` are rejected before their backing can dangle on block exit.
	if sym.Kind == SymbolRegion {
		region, isRegion := sym.Node.(*ast.RegionStmt)
		if isRegion && region != nil && !isCompilerGeneratedAutoRegionStmt(region) {
			return ident.Name, true
		}
		// A compiler-generated auto-region is eligible for region-return adoption; treating it as an
		// ordinary local Arena here would reject valid local builders that return their result.
		return "", false
	}
	sym = symbolAliasRoot(sym)
	if sym.Kind != SymbolLocal {
		return "", false
	}
	if _, isRef := sym.Type.(*RefType); isRef {
		return "", false
	}
	return ident.Name, true
}

// lvalueStorageOutlivesFunction reports whether the storage referenced by an
// lvalue expression lives beyond the current function: a global, a parameter,
// a local reference (which points elsewhere), or any field/element reached
// through a reference. A field/element of a by-value local is considered local.
func (a *Analyzer) lvalueStorageOutlivesFunction(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.lvalueStorageOutlivesFunction(n.Inner)
	case *ast.FieldExpr:
		if a.exprObjectIsReference(n.Object) {
			return true
		}
		return a.lvalueStorageOutlivesFunction(n.Object)
	case *ast.IndexExpr:
		if a.exprObjectIsReference(n.Object) {
			return true
		}
		return a.lvalueStorageOutlivesFunction(n.Object)
	case *ast.Ident:
		if a.currentScope == nil {
			return false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym == nil {
			return false
		}
		sym = symbolAliasRoot(sym)
		switch sym.Kind {
		case SymbolGlobal:
			return true
		case SymbolParam:
			// By-value parameters are callee-local copies (notably a darray header). Only a
			// reference parameter points at storage whose lifetime can exceed this function.
			_, isRef := sym.Type.(*RefType)
			return isRef
		case SymbolLocal:
			_, isRef := sym.Type.(*RefType)
			return isRef
		}
	}
	return false
}

func (a *Analyzer) exprObjectIsReference(obj ast.Expr) bool {
	if obj == nil {
		return false
	}
	t := a.exprType(obj)
	if t == nil {
		t = a.analyzeExpr(obj)
	}
	_, ok := t.(*RefType)
	return ok
}
