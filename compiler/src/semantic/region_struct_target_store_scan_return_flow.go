package semantic

import (
	"fmt"
	"os"
	"reflect"

	"elisacore/src/ast"
)

// calleeReturnsNothingWritableFrom: every value decl returns is explicit (a `return`, no lmut
// thread slots) and carries from parameter i only read-only parts -- views and scalars, judged by
// storeScanBundleHasWritableTargetRO over the names derived from that parameter.
func (a *Analyzer) calleeReturnsNothingWritableFrom(decl *ast.FuncDecl, i int, seen map[storeScanKey]bool) bool {
	decl = a.readDecl(decl)
	if decl == nil || i < 0 || i >= len(decl.Params) || len(decl.Body) == 0 || len(decl.LmutThreadSlots) > 0 {
		return false
	}
	key := storeScanKey{decl, i, -2}
	if cached, ok := a.storeScanWritableCache[key]; ok {
		return cached
	}
	_, _, content, _, _ := a.storeScanTaint(decl, i, "", seen)
	readOnly := a.storeScanReadOnlyLocals(decl)
	ok, sawReturn := true, false
	for _, ret := range a.storeScanIndex(decl).returns {
		if ret == nil || !ok {
			continue
		}
		sawReturn = true
		if ret.Value != nil && a.storeScanBundleHasWritableTargetRO(ret.Value, content, readOnly) {
			ok = false
		}
	}
	result := ok && (sawReturn || a.declReturnsVoid(decl))
	if a.storeScanWritableCache == nil {
		a.storeScanWritableCache = map[storeScanKey]bool{}
	}
	a.storeScanWritableCache[key] = result
	return result
}

// storeScanReadOnlyLocals: body locals every declaration of which is annotated as a view
// (`sview`/`cstr`) or a plain scalar, bound by no other construct. Memoized per function.
func (a *Analyzer) storeScanReadOnlyLocals(decl *ast.FuncDecl) map[string]bool {
	decl = a.readDecl(decl)
	if cached, ok := a.storeScanReadOnlyCache[decl]; ok {
		return cached
	}
	idx := a.storeScanIndex(decl)
	readOnly := map[string]bool{}
	for name, declarations := range idx.decls {
		if idx.otherBound[name] || len(declarations) == 0 {
			continue
		}
		eligible := true
		for _, v := range declarations {
			if v == nil || !(storeScanTypeExprIsView(v.Type) || storeScanTypeExprIsPlainScalar(v.Type)) {
				eligible = false
				break
			}
		}
		if eligible {
			readOnly[name] = true
		}
	}
	for _, p := range decl.Params {
		delete(readOnly, p.Name)
	}
	if a.storeScanReadOnlyCache == nil {
		a.storeScanReadOnlyCache = map[*ast.FuncDecl]map[string]bool{}
	}
	a.storeScanReadOnlyCache[decl] = readOnly
	return readOnly
}

// storeScanBundleHasWritableTarget reports whether a bundled argument carries a target-side
// component the callee could WRITE source storage into: a target-aliasing name whose type shares
// indirect storage (a container, reference or aggregate holding one). A target-derived string or
// view (`sview`, `cstr`) is read-only, so bundling it next to a source component moves nothing
// into the target. An untyped mention is assumed writable.
func (a *Analyzer) storeScanBundleHasWritableTarget(arg ast.Expr, targets map[string]bool) bool {
	return a.storeScanBundleHasWritableTargetRO(arg, targets, nil)
}

// storeScanBundleHasWritableTargetRO: storeScanBundleHasWritableTarget, where a name in readOnly
// (a local only ever declared as a view or plain scalar) is a read-only mention.
func (a *Analyzer) storeScanBundleHasWritableTargetRO(arg ast.Expr, targets, readOnly map[string]bool) bool {
	// A typed sub-expression that cannot share storage (`h.name: sview`, `h.count`) is read-only
	// whatever it mentions, so the walk does not descend into it.
	found := false
	seen := map[uintptr]bool{}
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if expr, isExpr := v.Interface().(ast.Expr); isExpr && expr != nil {
				if t, typed := a.lookupExprType(expr); typed && t != nil && !a.storeScanSharesCached(t) {
					return
				}
				if id, isIdent := expr.(*ast.Ident); isIdent {
					if readOnly[id.Name] {
						return
					}
					if targets[id.Name] || targets[rootNameOfDotted(id.Name)] {
						found = true
					}
					return
				}
				if fe, isField := expr.(*ast.FieldExpr); isField {
					rec(reflect.ValueOf(fe.Object))
					return
				}
			}
			rec(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			if v.Type() == storeScanPosType {
				return
			}
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		}
	}
	rec(reflect.ValueOf(arg))
	return found
}

// declParamCarriesStorage reports whether a declared function's parameter type can carry region
// storage (a container, view, reference or aggregate holding one). Unknown means it can.
func (a *Analyzer) declParamCarriesStorage(decl *ast.FuncDecl, index int) bool {
	decl = a.readDecl(decl)
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return true
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || index < 0 || index >= len(fnType.Params) {
		return true
	}
	param := fnType.Params[index]
	if a.containsTypeParamCached(param) {
		return true
	}
	return !a.storeScanHoldsNoRegionStorage(param)
}

func (a *Analyzer) declParamMayEncodeAddress(decl *ast.FuncDecl, index int) bool {
	decl = a.readDecl(decl)
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return true
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || index < 0 || index >= len(fnType.Params) {
		return true
	}
	return a.storeScanTypeMayEncodeAddress(fnType.Params[index], map[Type]bool{})
}

// storeScanTypeIsProgramLivedForStoreProof permits an early-out only when the complete value
// cannot encode an address and all of its region-bearing parts are program-lived. A source-lived
// view sibling must not make a uintptr field disappear from the sink scan.
func (a *Analyzer) storeScanTypeIsProgramLivedForStoreProof(t Type) bool {
	if t == nil || len(a.programRegions) == 0 {
		return false
	}
	return a.memoTypePredicate(&a.storeScanProgramLivedMemo, t, func() bool {
		return !a.containsTypeParamCached(t) && !a.storeScanTypeMayEncodeAddress(t, map[Type]bool{}) &&
			a.typeCanContainRegionRefs(stripRefForBounds(t), map[string]bool{}) && a.typeIsProgramLivedCached(t)
	})
}

// storeScanTypeIsPlainValue reports a concrete type that holds no region storage and cannot
// encode an address: a value of it carries nothing into a store.
func (a *Analyzer) storeScanTypeIsPlainValue(t Type) bool {
	if t == nil {
		return false
	}
	return a.memoTypePredicate(&a.storeScanPlainValueMemo, t, func() bool {
		return a.storeScanHoldsNoRegionStorage(t) && !a.containsTypeParamCached(t) && !a.storeScanTypeMayEncodeAddress(t, map[Type]bool{})
	})
}

// storeScanTaint computes, to a fixpoint over the body, which names may hold the WHOLE source
// parameter (header) or something read out of it (content), and which names may alias the
// target place (targetName; "" for none). Locals become tainted by binding (decl/assign of the
// local itself) and by receiving a tainted store (`local.f <- v`, `local.xs.push(v)`).
func (a *Analyzer) storeScanTaint(decl *ast.FuncDecl, other int, targetName string, seen map[storeScanKey]bool) (params, header, content, targets, views map[string]bool) {
	decl = a.readDecl(decl)
	// Memoized per (decl, source, target): every summary query of the same pair recomputed the
	// fixpoint. Results are read-only to callers; types learned later only make taint smaller,
	// so a result computed earlier is conservative (as the scalar / view caches already are).
	key := storeScanTaintKey{decl, other, targetName}
	if r, ok := a.storeScanTaintCache[key]; ok {
		a.storeScanLastHolders = r.holders
		return r.params, r.header, r.content, r.targets, r.views
	}
	defer func() {
		if a.storeScanTaintCache == nil {
			a.storeScanTaintCache = map[storeScanTaintKey]storeScanTaintResult{}
		}
		a.storeScanTaintCache[key] = storeScanTaintResult{params, header, content, targets, views, a.storeScanLastHolders}
	}()
	idx := a.storeScanIndex(decl)
	params = idx.parameters(decl)
	if len(idx.bindingEffectNodes()) == 0 {
		// With no binding effects, both taint sets remain the same immutable
		// singleton and no fixpoint or local-scalar scan is needed.
		header = map[string]bool{decl.Params[other].Name: true}
		content = header
		targets = map[string]bool{}
		if targetName != "" {
			targets[targetName] = true
		}
		views = a.storeScanViewLocalsCached(decl, other)
		a.storeScanLastHolders = map[string]bool{}
		return params, header, content, targets, views
	}
	header = map[string]bool{decl.Params[other].Name: true}
	content = map[string]bool{decl.Params[other].Name: true}
	targets = map[string]bool{}
	if targetName != "" {
		targets[targetName] = true
	}
	scalars := a.storeScanScalarLocals(decl)
	add := func(set map[string]bool, name string) bool {
		if name == "" || params[name] || set[name] || scalars[name] {
			return false
		}
		set[name] = true
		return true
	}
	views = a.storeScanViewLocalsCached(decl, other)
	var taint func(string, ast.Expr) bool
	taint = func(name string, value ast.Expr) bool {
		// add never changes parameters or scalars. A local that already has
		// all possible taint for its type is also at its fixpoint, so avoid
		// rescanning the value and recursively proving its callee's return.
		if name == "" || params[name] || scalars[name] || (content[name] && (header[name] || views[name])) {
			return false
		}
		inner := storeScanUnwrap(value)
		if choice, ok := inner.(*ast.TernaryExpr); ok && choice != nil {
			changed := taint(name, choice.Value)
			return taint(name, choice.Alt) || changed
		}
		// An assigned call result carries only what the callee returns. Its
		// argument/condition side effects are scanned separately below.
		if call, ok := inner.(*ast.CallExpr); ok && call != nil {
			if callee, known := a.resolveReadOnlyScanCallee(call); known && callee != nil && storeFlowCallFitsParams(callee, call) && !funcHasArenaParam(callee) && !a.declReturnsVoid(callee) {
				mode := storeNone
				for i, arg := range returnBorrowCallArgs(call) {
					argMode := storeNone
					if storeScanMentionsHeader(arg, header) {
						argMode = storeHeader
					} else if idx.mentions(arg, content) {
						argMode = storeElements
					}
					if argMode == storeNone || (!a.declParamCarriesStorage(callee, i) && !a.declParamMayEncodeAddress(callee, i)) {
						continue
					}
					returned := a.storeScanSettled(func() storeMode { return a.calleeReturnModeOf(callee, i, seen) })
					// A view read from an owned byte buffer can retain its backing
					// even when the returned value is only an element. Preserve the
					// source's storage unless it is known to hold no byte buffers.
					if argMode == storeHeader && returned != storeNone && !a.storeScanSourceHasNoBytes(decl, other) {
						returned = storeHeader
					}
					mode = maxStoreMode(mode, narrowStoreMode(argMode, returned))
					if mode == storeHeader {
						break
					}
				}
				changed := false
				if mode != storeNone {
					changed = add(content, name)
				}
				if mode == storeHeader && !views[name] {
					changed = add(header, name) || changed
				}
				return changed
			}
		}
		changed := false
		if views[name] && idx.mentions(value, content) {
			// A view-typed local derived from a non-byte darray source holds (part of) an element
			// value, never the source's header (storeScanViewLocals).
			changed = add(content, name) || changed
		} else if storeScanMentionsHeader(value, header) {
			changed = add(header, name) || changed
			changed = add(content, name) || changed
		} else if idx.mentions(value, content) {
			changed = add(content, name) || changed
		}
		return changed
	}
	// holders: locals that CONTAIN parts of the target (`holder.push(t.lists)`): their own buffer
	// is fresh, but a part read out of them (`holder[0]`) is target storage.
	holders := map[string]bool{}
	bind := func(name string, value ast.Expr) bool {
		changed := taint(name, value)
		// None of the target/holder sets accepts parameters, scalar locals,
		// or empty names, so their alias proofs cannot change the fixpoint.
		if name == "" || params[name] || scalars[name] {
			return changed
		}
		if storeScanMayAliasPlace(idx, value, targets) {
			changed = add(targets, name) || changed
		} else if storeScanMayAliasPlace(idx, value, holders) {
			if storeScanPlaceDepth(value) >= 1 {
				changed = add(targets, name) || changed // `x = holder[0]`: a target part
			} else {
				changed = add(holders, name) || changed // `x = holder`: another holder
			}
		}
		return changed
	}
	locals := idx.locals
	for changed := true; changed; {
		changed = false
		for _, node := range idx.bindingEffectNodes() {
			switch n := node.(type) {
			case *ast.VarDeclStmt:
				if n != nil && bind(n.Name, n.Value) {
					changed = true
				}
			case *ast.AssignStmt:
				if n == nil {
					continue
				}
				if id, ok := n.Target.(*ast.Ident); ok && id != nil {
					if bind(id.Name, n.Value) {
						changed = true
					}
					continue
				}
				// A store into part of a local taints the local.
				if root := rootIdentExpr(n.Target); root != nil && !params[root.Name] && taint(root.Name, n.Value) {
					changed = true
				}
			case *ast.CallExpr:
				// A method call on a local receiver may store its arguments into it. A receiver
				// that names no value (an enum constructor `Expr.Field(..)`) is not a receiver.
				if field, ok := n.Func.(*ast.FieldExpr); ok && field != nil {
					if root := rootIdentExpr(field.Object); root != nil && !params[root.Name] && a.storeScanReceiverIsValue(root.Name, locals) {
						// A builtin darray `extend(xs)` copies xs's elements: the receiver then
						// holds values read out of xs (content), never xs's header.
						copiesElements := field.Field == "extend" && a.storeScanReceiverIsBuiltinDArray(decl, field.Object)
						for _, arg := range n.Args {
							// A receiver given (part of) the target place holds the target's storage
							// (`holder.push(table.deep.lists)`; an `extend` copies element headers that
							// still share it): stores through the receiver then reach the target.
							if !storeScanReadOnlyMethods[field.Field] && (storeScanMayAliasPlace(idx, arg, targets) || storeScanMayAliasPlace(idx, arg, holders)) && a.storeScanContainerElemsMayShare(decl, field.Object) && add(holders, root.Name) {
								changed = true
								if storeScanTrace {
									fmt.Fprintf(os.Stderr, "TGTRECV %s src=%s local=%s at %s\n", decl.Name, storeScanTraceOther, root.Name, n.Pos())
								}
							}
							if copiesElements {
								// Scalar elements copied out of xs carry nothing of it.
								if a.storeScanExtendArgMode(decl, arg, storeElements) == storeNone {
									continue
								}
								if idx.mentions(arg, content) && add(content, root.Name) {
									changed = true
								}
								continue
							}
							if taint(root.Name, arg) {
								changed = true
							}
						}
					}
				}
				// A body local passed to a call may be WRITTEN by the callee with any other
				// argument (`push_list(merged, right.lists[row])`): taint it with each of them,
				// unless the callee is resolved and the local's parameter is read-only. A builtin
				// container method writes only its receiver (handled above).
				if a.storeScanTaintCallWrites(decl, n, params, locals, taint, idx, targets, holders, func(name string) bool { return add(holders, name) }, header, content, views, scalars, func(name string) bool { return add(content, name) }) {
					changed = true
				}
			case *ast.IterForStmt:
				// Loop binders over (a holder of) the target are target parts.
				if n != nil && (storeScanMayAliasPlace(idx, n.Source, targets) || storeScanMayAliasPlace(idx, n.Source, holders)) {
					for _, name := range idx.boundNames(n) {
						if add(targets, name) {
							changed = true
						}
					}
				}
				// Loop binders over a tainted source are element values.
				if n != nil && idx.mentions(n.Source, content) {
					for _, name := range idx.boundNames(n) {
						if add(content, name) {
							changed = true
						}
					}
				}
			case *ast.ForStmt:
				// A counted range loop binds an integer (analyzeForStmt rejects non-integral
				// bounds), which carries nothing, however its header mentions the source
				// (`for i in 0..<src.count |src|`).
			case ast.Stmt:
				// Any other binding construct: its binders may hold the whole source.
				if !idx.headerMentions(n, content) {
					continue
				}
				a.storeScanTaintBinders(decl, idx, n, header, content, add, &changed)
			}
		}
	}
	a.storeScanLastHolders = holders
	return params, header, content, targets, views
}

// storeScanScalarLocals: body locals that only ever hold a plain numeric or bool value -- every
// declaration of the name is initialized with such a value (analyzed type) and no other construct
// binds it. Such a local can never carry a reference into a source, whatever it is computed from
// (`index: int = bound_last_index(bound, name)`). uintptr is excluded: it may encode an address.
func (a *Analyzer) storeScanScalarLocals(decl *ast.FuncDecl) map[string]bool {
	decl = a.readDecl(decl)
	if cached, ok := a.storeScanScalarCache[decl]; ok {
		return cached
	}
	scalars := map[string]bool{}
	defer func() {
		if a.storeScanScalarCache == nil {
			a.storeScanScalarCache = map[*ast.FuncDecl]map[string]bool{}
		}
		a.storeScanScalarCache[decl] = scalars
	}()
	idx := a.storeScanIndex(decl)
	for name, declarations := range idx.decls {
		if idx.otherBound[name] || len(declarations) == 0 {
			continue
		}
		eligible := true
		for _, v := range declarations {
			if v == nil || !(storeScanTypeExprIsPlainScalar(v.Type) || a.storeScanTypeExprIsOpaque(v.Type) || (v.Type == nil && v.Value != nil && storeScanPlainScalarType(a.exprType(v.Value)))) {
				eligible = false
				break
			}
		}
		if eligible {
			scalars[name] = true
		}
	}
	return scalars
}

// storeScanTypeExprIsOpaque: a declared foreign handle (`extern LLVMValueRef`) or a named type
// that can hold no region reference, through
// `mutable`, never through a reference or optional. Its value is produced by foreign code and
// can hold no Elisa region reference (storeFlowOpaqueExternCall's rule).
func (a *Analyzer) storeScanTypeExprIsOpaque(t ast.TypeExpr) bool {
	for {
		switch tt := t.(type) {
		case *ast.MutableType:
			if tt == nil {
				return false
			}
			t = tt.Elem
			continue
		case *ast.NamedType:
			if tt == nil || tt.Region != "" {
				return false
			}
			resolved := a.storeFlowNamedType(tt.Name)
			if _, opaque := resolved.(*OpaqueType); opaque {
				return true
			}
			// A named value type that can hold no region reference (`ValueType`, a struct of
			// scalars) carries nothing either.
			return resolved != nil && !a.containsTypeParamCached(resolved) && !a.storeScanTypeMayEncodeAddress(resolved, map[Type]bool{}) && a.storeScanHoldsNoRegionStorage(resolved)
		}
		return false
	}
}

// storeScanTypeExprIsPlainScalar: a declared `int`/`u32`/`bool`/... annotation (through `mutable`,
// never through a reference). Builtin scalar names are reserved, so the spelling is the type.
func storeScanTypeExprIsPlainScalar(t ast.TypeExpr) bool {
	for {
		switch tt := t.(type) {
		case *ast.MutableType:
			if tt == nil {
				return false
			}
			t = tt.Elem
			continue
		case *ast.BuiltinTypeExpr:
			return tt != nil && len(tt.TypeArgs) == 0 && len(tt.ValueArgs) == 0 && storeScanPlainScalarNames[tt.Name]
		case *ast.NamedType:
			return tt != nil && tt.Region == "" && storeScanPlainScalarNames[tt.Name]
		}
		return false
	}
}

var storeScanPlainScalarNames = map[string]bool{
	"bool": true, "char": true, "int": true, "i8": true, "i16": true, "i32": true, "i64": true, "isize": true,
	"u8": true, "u16": true, "u32": true, "u64": true, "usize": true, "f32": true, "f64": true,
}

func storeScanPlainScalarType(t Type) bool {
	if t == nil || containsTypeParam(t) {
		return false
	}
	if b, ok := t.(*BuiltinType); ok && b != nil {
		switch b.Name {
		case "bool", "char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "f32", "f64":
			return true
		}
		return false
	}
	_, _, ok := BitIntInfo(t)
	return ok
}
