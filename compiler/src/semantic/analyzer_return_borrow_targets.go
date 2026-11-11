package semantic

import (
	"reflect"
	"sync"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// returnBorrowMayTarget reports whether a value of type holder may hold a borrow into storage of
// type referent: some reference or view reachable in holder targets referent or one of its
// by-value components. Anything opaque (a type parameter, an unresolved generic, `void&`) may.
func (a *Analyzer) returnBorrowMayTarget(holder Type, referent Type) bool {
	key := [2]Type{holder, referent}
	if answer, ok := a.returnBorrowMayTargetMemo[key]; ok {
		return answer
	}
	answer := a.computeReturnBorrowMayTarget(holder, referent)
	if a.returnBorrowMayTargetMemo == nil {
		a.returnBorrowMayTargetMemo = map[[2]Type]bool{}
	}
	a.returnBorrowMayTargetMemo[key] = answer
	return answer
}

// returnBorrowResultMayTarget is returnBorrowMayTarget for a returned value: its own outer
// reference or view is a borrow it hands out, so it counts.
func (a *Analyzer) returnBorrowResultMayTarget(result Type, referent Type) bool {
	key := [2]Type{result, referent}
	if answer, ok := a.returnBorrowResultMayTargetMemo[key]; ok {
		return answer
	}
	answer := a.computeReturnBorrowTargetsReach(result, referent, true)
	if a.returnBorrowResultMayTargetMemo == nil {
		a.returnBorrowResultMayTargetMemo = map[[2]Type]bool{}
	}
	a.returnBorrowResultMayTargetMemo[key] = answer
	return answer
}

func (a *Analyzer) computeReturnBorrowMayTarget(holder Type, referent Type) bool {
	// The writable parameter's own reference is how the callee reaches the holder, not a stored
	// borrow: only what the holder stores counts.
	switch outer := holder.(type) {
	case *RefType:
		holder = outer.Elem
	case *ViewType:
		holder = outer.Elem
	}
	// A borrow stored into the holder at the call is attributed to the referent's ADDRESS only when
	// it can point at the referent's by-value storage; a darray, dict or set inside the referent keeps
	// its elements in region storage the region checks own, so a borrow of those elements is part
	// of what the referent holds, not of where it lives.
	return a.computeReturnBorrowTargetsReach(holder, referent, true)
}

// Target and component walks are sequential. Reuse their cycle guard and
// result storage across queries, clearing references before returning scratch.
type returnBorrowTargetScratch struct {
	seen             map[Type]bool
	targets          []Type
	components       []Type
	componentIDs     []canonicalTypeIDResult
	componentIDSet   map[TypeID]struct{}
	otherComponents  []Type
	bridgeComponents []Type
}

var returnBorrowTargetScratchPool = sync.Pool{New: func() any {
	return &returnBorrowTargetScratch{seen: make(map[Type]bool, 32)}
}}

var returnBorrowByteTargets = [...]BuiltinType{{Name: "u8"}, {Name: "i8"}}

func (a *Analyzer) computeReturnBorrowTargetsReach(holder Type, referent Type, inlineOnly bool) bool {
	scratch := returnBorrowTargetScratchPool.Get().(*returnBorrowTargetScratch)
	defer func() {
		if len(scratch.seen) > 4096 || cap(scratch.targets) > 4096 || cap(scratch.components) > 4096 {
			return
		}
		clear(scratch.seen)
		clear(scratch.targets)
		clear(scratch.components)
		clear(scratch.componentIDSet)
		clear(scratch.otherComponents)
		clear(scratch.bridgeComponents)
		scratch.otherComponents = scratch.otherComponents[:0]
		scratch.bridgeComponents = scratch.bridgeComponents[:0]
		scratch.targets = scratch.targets[:0]
		scratch.components = scratch.components[:0]
		scratch.componentIDs = scratch.componentIDs[:0]
		returnBorrowTargetScratchPool.Put(scratch)
	}()
	opaque := false
	a.collectReturnBorrowTargets(holder, &scratch.targets, &opaque, scratch.seen)
	if opaque {
		return true
	}
	// A holder with no reference or view inside can hold no borrow of anything.
	if len(scratch.targets) == 0 {
		return false
	}
	if len(scratch.seen) > 4096 {
		scratch.seen = make(map[Type]bool, 32)
	} else {
		clear(scratch.seen)
	}
	collectReturnBorrowComponents(referent, inlineOnly, &scratch.components, &opaque, scratch.seen)
	if opaque {
		return true
	}
	if len(scratch.targets) > 1 && len(scratch.targets)*len(scratch.components) >= 128 {
		return scratch.anyMatchingComponent()
	}
	// Computing each side's canonical ID once avoids repeating the type walk
	// for every target/component pair.
	for _, component := range scratch.components {
		scratch.componentIDs = append(scratch.componentIDs, tryCanonicalTypeIDResult(component))
	}
	for _, target := range scratch.targets {
		targetID := tryCanonicalTypeIDResult(target)
		for index, component := range scratch.components {
			if sameTypeWithCanonicalIDs(target, targetID, component, scratch.componentIDs[index]) {
				return true
			}
		}
	}
	return false
}

func (a *Analyzer) collectReturnBorrowTargets(t Type, out *[]Type, opaque *bool, seen map[Type]bool) {
	if t == nil || *opaque || seen[t] {
		return
	}
	seen[t] = true
	switch tt := t.(type) {
	case *RefType:
		if tt.Elem == nil {
			*opaque = true
			return
		}
		if builtin, ok := tt.Elem.(*BuiltinType); ok && builtin.Name == "void" {
			*opaque = true
			return
		}
		// A `heap`/`static` reference cannot point into a frame (storing `&local` or a forwarded
		// stack reference into one is rejected), so it is no target; what its pointee holds still is.
		if !outlivesFrameStorage(tt.Storage) {
			*out = append(*out, tt.Elem)
		}
		// A reference to a holder reaches whatever that holder holds.
		a.collectReturnBorrowTargets(tt.Elem, out, opaque, seen)
	case *ViewType:
		if tt.Elem == nil {
			*opaque = true
			return
		}
		*out = append(*out, tt.Elem)
		a.collectReturnBorrowTargets(tt.Elem, out, opaque, seen)
	case *SViewType, *CStrType:
		*out = append(*out, &returnBorrowByteTargets[0], &returnBorrowByteTargets[1])
	case *BuiltinType, *BitIntType, *ConstEnumType, *BitGroupType:
	case *OpaqueType:
		// An `extern` handle type (LLVMValueRef, ...) is a foreign pointer the language cannot
		// dereference: it holds no Elisa reference or view, so it targets nothing.
	case *ArrayType:
		a.collectReturnBorrowTargets(tt.Elem, out, opaque, seen)
	case *DArrayType:
		a.collectReturnBorrowTargets(tt.Elem, out, opaque, seen)
	case *OptionalType:
		a.collectReturnBorrowTargets(tt.Value, out, opaque, seen)
	case *ErrorUnionType:
		a.collectReturnBorrowTargets(tt.Value, out, opaque, seen)
	case *TupleType:
		for _, field := range tt.Fields {
			a.collectReturnBorrowTargets(field.Type, out, opaque, seen)
		}
	case *DictType:
		a.collectReturnBorrowTargets(tt.Key, out, opaque, seen)
		a.collectReturnBorrowTargets(tt.Value, out, opaque, seen)
	case *SetType:
		a.collectReturnBorrowTargets(tt.Elem, out, opaque, seen)
	case *StructType:
		if len(tt.TypeParams) > 0 {
			*opaque = true
			return
		}
		for _, field := range tt.Fields {
			// A `tail` field is the struct's own trailing storage, typed as a reference to it:
			// nothing can be stored in it but its elements.
			if ref, isRef := field.Type.(*RefType); isRef && ref != nil && field.IsTail {
				a.collectReturnBorrowTargets(ref.Elem, out, opaque, seen)
				continue
			}
			a.collectReturnBorrowTargets(field.Type, out, opaque, seen)
		}
	case *EnumType:
		for _, field := range tt.Common {
			a.collectReturnBorrowTargets(field.Type, out, opaque, seen)
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				a.collectReturnBorrowTargets(payload, out, opaque, seen)
			}
		}
	default:
		*opaque = true
	}
}

// collectReturnBorrowComponents collects a type and every type stored BY VALUE inside it: the
// storage a borrow into a value of that type can point at. inlineOnly stops at the element buffers of
// darrays, dicts and sets, keeping only storage laid out inside the value itself.
func collectReturnBorrowComponents(t Type, inlineOnly bool, out *[]Type, opaque *bool, seen map[Type]bool) {
	if t == nil || *opaque || seen[t] {
		return
	}
	seen[t] = true
	*out = append(*out, t)
	switch tt := t.(type) {
	case *RefType, *ViewType, *SViewType, *CStrType, *BuiltinType, *BitIntType, *ConstEnumType, *BitGroupType, *OpaqueType:
	case *ArrayType:
		collectReturnBorrowComponents(tt.Elem, inlineOnly, out, opaque, seen)
	case *DArrayType:
		if !inlineOnly {
			collectReturnBorrowComponents(tt.Elem, inlineOnly, out, opaque, seen)
		}
	case *OptionalType:
		collectReturnBorrowComponents(tt.Value, inlineOnly, out, opaque, seen)
	case *ErrorUnionType:
		collectReturnBorrowComponents(tt.Value, inlineOnly, out, opaque, seen)
	case *TupleType:
		for _, field := range tt.Fields {
			collectReturnBorrowComponents(field.Type, inlineOnly, out, opaque, seen)
		}
	case *DictType:
		if !inlineOnly {
			collectReturnBorrowComponents(tt.Key, inlineOnly, out, opaque, seen)
			collectReturnBorrowComponents(tt.Value, inlineOnly, out, opaque, seen)
		}
	case *SetType:
		if !inlineOnly {
			collectReturnBorrowComponents(tt.Elem, inlineOnly, out, opaque, seen)
		}
	case *StructType:
		if len(tt.TypeParams) > 0 {
			*opaque = true
			return
		}
		for _, field := range tt.Fields {
			collectReturnBorrowComponents(field.Type, inlineOnly, out, opaque, seen)
		}
	case *EnumType:
		for _, field := range tt.Common {
			collectReturnBorrowComponents(field.Type, inlineOnly, out, opaque, seen)
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				collectReturnBorrowComponents(payload, inlineOnly, out, opaque, seen)
			}
		}
	default:
		*opaque = true
	}
}

// returnBorrowTypeNameObject reports a member-access object that names a visible type rather than
// a value (`E` in `E.Invalid` or `E.Ident(x)`). A local of the same name shadows the type.
func (a *Analyzer) returnBorrowTypeNameObject(object ast.Expr) bool {
	path, ok := qualifiedTypePathFromExpr(object)
	if !ok {
		return false
	}
	if ident, isIdent := object.(*ast.Ident); isIdent && ident != nil && a.currentScope != nil {
		if sym, found := a.currentScope.Lookup(ident.Name); found && sym != nil && (sym.Kind == SymbolLocal || sym.Kind == SymbolParam || sym.Kind == SymbolGlobal) {
			return false
		}
	}
	_, _, visible := a.lookupVisibleType(path)
	return visible
}

// returnBorrowDeclaredType resolves a local's declared type without reporting: a summary may be
// computed for a generic callee outside its type-parameter scope, where `K` is unknown. An
// unresolvable type comes back invalid, which returnBorrowOwnedValueType treats as opaque.
func (a *Analyzer) returnBorrowDeclaredType(expr ast.TypeExpr) Type {
	proofProbe := a.beginProofReportProbe()
	defer proofProbe.restore()
	saved := a.suppressDiagnostics
	a.suppressDiagnostics = true
	defer func() { a.suppressDiagnostics = saved }()
	return a.resolveType(expr)
}

// returnBorrowLocalRebound reports whether the current function assigns the named local directly
// (`r <- v`, `(r, s) <- t`) anywhere. Name-based, so a shadowing binding counts too.
func (a *Analyzer) returnBorrowLocalRebound(name string) bool {
	fn := a.currentFuncDecl
	if fn == nil {
		fn = a.returnBorrowWalkFn
	}
	if fn == nil {
		return true
	}
	if a.returnBorrowReboundNames == nil {
		a.returnBorrowReboundNames = map[*ast.FuncDecl]map[string]bool{}
	}
	rebound, ok := a.returnBorrowReboundNames[fn]
	if !ok {
		rebound = map[string]bool{}
		var walk func(v reflect.Value)
		walk = func(v reflect.Value) {
			if !v.IsValid() {
				return
			}
			switch v.Kind() {
			case reflect.Pointer, reflect.Interface:
				if v.IsNil() {
					return
				}
				switch n := v.Interface().(type) {
				case *ast.AssignStmt:
					if ident, isIdent := returnBorrowStripParens(n.Target).(*ast.Ident); isIdent && ident != nil {
						rebound[ident.Name] = true
					}
				case *ast.TupleBindStmt:
					if !n.Declare {
						for _, target := range n.Names {
							rebound[target.Name] = true
						}
					}
				}
				walk(v.Elem())
			case reflect.Struct:
				for i := 0; i < v.NumField(); i++ {
					if v.Field(i).CanInterface() {
						walk(v.Field(i))
					}
				}
			case reflect.Slice, reflect.Array:
				for i := 0; i < v.Len(); i++ {
					walk(v.Index(i))
				}
			}
		}
		walk(reflect.ValueOf(fn.Body))
		a.returnBorrowReboundNames[fn] = rebound
	}
	return rebound[name]
}

// sliceViewsFrameArray reports a slice whose bytes are an inline fixed array held by value in a local
// or by-value parameter of the current function (`buf[0:2]`, `s.arr[1:]`, `grid[2][0:4]`). Every step
// from the root to the sliced array must be a by-value field or fixed-array index, so a reference, a
// view, a container element or a global anywhere on the path keeps the slice out of this rule.
func (a *Analyzer) sliceViewsFrameArray(n *ast.SliceExpr) bool {
	if a == nil || n == nil || a.currentScope == nil {
		return false
	}
	if _, ok := a.exprType(stripParenExpr(n.Object)).(*ArrayType); !ok {
		return false
	}
	root := stripParenExpr(n.Object)
	for {
		switch step := root.(type) {
		case *ast.FieldExpr:
			if _, ok := a.exprType(stripParenExpr(step.Object)).(*StructType); !ok {
				return false
			}
			root = stripParenExpr(step.Object)
			continue
		case *ast.IndexExpr:
			if _, ok := a.exprType(stripParenExpr(step.Object)).(*ArrayType); !ok {
				return false
			}
			root = stripParenExpr(step.Object)
			continue
		}
		break
	}
	ident, ok := root.(*ast.Ident)
	if !ok || ident == nil {
		return false
	}
	sym, found := a.currentScope.Lookup(ident.Name)
	if !found || sym == nil || (sym.Kind != SymbolLocal && sym.Kind != SymbolParam) {
		return false
	}
	switch sym.Type.(type) {
	case *ArrayType, *StructType:
		return true
	}
	return false
}

// collectOrPatternTests gathers the `is` tests of an or-chain of pattern conditions.
func collectOrPatternTests(expr ast.Expr, out *[]*ast.BinaryExpr) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		collectOrPatternTests(n.Inner, out)
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_OR:
			collectOrPatternTests(n.Left, out)
			collectOrPatternTests(n.Right, out)
		case lexer.TOKEN_IS:
			*out = append(*out, n)
		}
	}
}
