package semantic

import (
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// storeFlowMarkCall marks what a call may write: a mutated receiver, and the arguments bound to
// writable parameters of the callee (all of them when the callee is not a resolvable declared
// function, except for builtin methods, whose arguments are only read into the receiver).
func (a *Analyzer) storeFlowMarkCall(call *ast.CallExpr, written map[string]bool, mark func(ast.Expr)) {
	if a.storeFlowEnumVariantConstructor(call) {
		return
	}
	args := returnBorrowCallArgs(call)
	switch fn := call.Func.(type) {
	case *ast.FieldExpr:
		if fn == nil {
			return
		}
		if a.storeFlowBuiltinDarrayTruncate(call) || a.storeFlowBuiltinDarrayReserve(call) {
			mark(fn.Object)
			return
		}
		if a.storeFlowBuiltinDarrayGrowth(fn) {
			// The builtin moves its arguments into the receiver: only the receiver is written;
			// the statement link carries the arguments' flow into it.
			mark(fn.Object)
			return
		}
		if a.storeFlowBuiltinReadOnlyQuery(fn) {
			// A builtin container query writes neither its receiver nor its arguments.
			return
		}
		if qualified := a.storeFlowQualifiedCall(call); qualified != nil {
			a.storeFlowMarkCall(qualified, written, mark)
			return
		}
		if a.isDeclaredFuncName(fn.Field) {
			mark(fn.Object)
			for _, arg := range args {
				mark(arg)
			}
			return
		}
		if !storeFlowReadOnlyMethods[fn.Field] {
			mark(fn.Object)
		}
		if call.SafeReceiver != nil {
			mark(call.SafeReceiver)
		}
	case *ast.Ident:
		// A plain call without arguments has no caller-owned argument to mark.
		// Its own summary cannot affect this written-name set.
		if len(args) == 0 {
			return
		}
		if a.storeFlowRuntimeDictQuery(call) {
			return
		}
		if callee := a.storeFlowCallee(call); callee != nil {
			var ft *FuncType
			if sym := a.funcDeclSymbols[callee]; sym != nil {
				ft, _ = sym.Type.(*FuncType)
			}
			if ft != nil && funcTypeExplicitParamCount(ft) == len(callee.Params) && len(callee.LmutThreadSlots) == 0 {
				if summary := a.storeFlowSummaryFor(callee); summary != nil {
					for j, arg := range args {
						if summary.writtenParam(j) {
							mark(arg)
						}
					}
					return
				}
				for j, arg := range args {
					if j >= len(callee.Params) || callee.Params[j].Mutable || a.returnBorrowWritableParam(ft.Params[j]) {
						mark(arg)
					}
				}
				return
			}
		}
		for _, arg := range args {
			mark(arg)
		}
	default:
		mark(call.Func)
		for _, arg := range args {
			mark(arg)
		}
	}
}

// storeFlowQualifiedCall rewrites a module-qualified call `Module.f(args)` to the plain call
// `f(args)` with an Ident callee, when Module is not a value in scope and the one declaration
// that call resolves to is declared in Module; the arguments then bind f's parameters one to one
// instead of shifting under a UFCS receiver.
func (a *Analyzer) storeFlowQualifiedCall(call *ast.CallExpr) *ast.CallExpr {
	fn, ok := call.Func.(*ast.FieldExpr)
	if !ok || fn == nil {
		return nil
	}
	mod, ok := fn.Object.(*ast.Ident)
	if !ok || mod == nil || a.storeFlowParams[mod.Name] || a.storeFlowLocals[mod.Name] {
		return nil
	}
	a.callEdgesOf(nil) // builds callDeclsByName
	for _, decl := range a.callDeclsByName[fn.Field] {
		ns := a.declNamespace(decl)
		if decl.Name != fn.Field || (ns != mod.Name && !strings.HasSuffix(ns, "."+mod.Name)) {
			continue
		}
		plain := *call
		plain.Func = &ast.Ident{Position: mod.Position, Name: decl.Name}
		if a.storeFlowCallee(&plain) == decl {
			return &plain
		}
	}
	return nil
}

// storeFlowDarrayGrowthMethods are builtin darray methods that store their arguments into the
// receiver without writing through them.
var storeFlowDarrayGrowthMethods = map[string]bool{"push": true, "insert": true, "extend": true}

// reserve can grow the receiver without retaining its capacity argument. Keep
// this separate from the payload-storing growth methods. Argument type errors
// are diagnosed by builtin call analysis; they cannot turn capacity into payload.
func (a *Analyzer) storeFlowBuiltinDarrayReserve(call *ast.CallExpr) bool {
	if call == nil || call.SafeReceiver != nil || call.HasArgForward || call.NamedArgCount() != 0 || len(call.Args) != 1 {
		return false
	}
	field, ok := call.Func.(*ast.FieldExpr)
	return ok && field != nil && field.Field == "reserve" && a.storeFlowBuiltinDarrayGrowthReceiver(field)
}

// storeFlowBuiltinDarrayTruncate recognizes the builtin DArray truncate operation
// only for a proven DArray receiver and an integer count. In particular, arbitrary
// UFCS methods named truncate stay on the conservative call path.
func (a *Analyzer) storeFlowBuiltinDarrayTruncate(call *ast.CallExpr) bool {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || field.Field != "truncate" || call.SafeReceiver != nil || call.NamedArgCount() != 0 || len(call.Args) != 1 {
		return false
	}
	if !a.storeFlowBuiltinDarrayGrowthReceiver(field) {
		return false
	}
	return a.storeFlowIntegerExpr(call.Args[0])
}

func (a *Analyzer) storeFlowEnumVariantConstructor(call *ast.CallExpr) bool {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || field.Object == nil {
		return false
	}
	if ident, isIdent := field.Object.(*ast.Ident); isIdent && ident != nil {
		if a.storeFlowCurrentDecl != nil && a.storeFlowBinderIndexFor(a.storeFlowCurrentDecl).binderNames()[ident.Name] {
			return false
		}
		if a.storeFlowCurrentDecl != nil {
			for _, param := range a.storeFlowCurrentDecl.TypeParams {
				if param == ident.Name {
					return false
				}
			}
			for _, param := range a.storeFlowCurrentDecl.GenericParams {
				if param.Kind == ast.GenericParamType && param.Name == ident.Name {
					return false
				}
			}
		}
		if a.globalScope != nil {
			if sym, found := a.globalScope.Lookup(ident.Name); found && sym != nil && sym.Kind == SymbolGlobal {
				return false
			}
		}
	}
	return a.isEnumVariantConstructorCall(call)
}

func (a *Analyzer) storeFlowIntegerExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.IntLit:
		return e != nil
	case *ast.ParenExpr:
		return e != nil && a.storeFlowIntegerExpr(e.Inner)
	case *ast.UnaryExpr:
		return e != nil && (e.Op == lexer.TOKEN_MINUS || e.Op == lexer.TOKEN_PLUS) && a.storeFlowIntegerExpr(e.Operand)
	case *ast.Ident:
		if e == nil || a.storeFlowCurrentDecl == nil {
			return false
		}
		return a.storeFlowBinderIndexFor(a.storeFlowCurrentDecl).integerBinderNames()[e.Name]
	default:
		return false
	}
}

func storeFlowIntegerTypeExpr(t ast.TypeExpr) bool {
	switch tt := t.(type) {
	case *ast.NamedType:
		return tt != nil && tt.Region == "" && storeFlowIntegerTypeNames[tt.Name]
	case *ast.BuiltinTypeExpr:
		return tt != nil && len(tt.TypeArgs) == 0 && len(tt.ValueArgs) == 0 && storeFlowIntegerTypeNames[tt.Name]
	default:
		return false
	}
}

var storeFlowIntegerTypeNames = map[string]bool{
	"u8": true, "u16": true, "u32": true, "u64": true, "i8": true, "i16": true,
	"i32": true, "i64": true, "usize": true, "isize": true,
}

func (a *Analyzer) storeFlowBuiltinDarrayGrowthReceiver(fn *ast.FieldExpr) bool {
	if fn == nil || !a.storeFlowDarrayPlace(fn.Object) {
		return false
	}
	for _, sym := range a.ufcsFunctionsWithLastSegment(fn.Field) {
		if sym == nil {
			return false
		}
		ft, ok := sym.Type.(*FuncType)
		if !ok || ft == nil {
			return false
		}
		if len(ft.Params) == 0 {
			continue
		}
		if storeFlowUFCSMayAcceptDarrayReceiver(ft.Params[0]) {
			return false
		}
	}
	return true
}

// storeFlowRuntimeDictReadHelpers are the runtime helpers a read-only builtin dict method is
// lowered to (analyzeBuiltinDictMethodCall): `get`, `get` with a view key, and `contains`. Their
// `_mut` forms yield a writable alias of the dict and are not listed.
var storeFlowRuntimeDictReadHelpers = map[string]bool{
	"arena_dict_get": true, "arena_dict_get_cstr_view": true, "arena_dict_contains": true,
}

// storeFlowRuntimeDictQuery reports a lowered read-only dict query `helper(dict, key)` that
// resolves to no user function.
func (a *Analyzer) storeFlowRuntimeDictQuery(call *ast.CallExpr) bool {
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil || !storeFlowRuntimeDictReadHelpers[ident.Name] || len(call.Args) != 2 || call.NamedArgCount() != 0 || call.SafeReceiver != nil {
		return false
	}
	return a.storeFlowCallee(call) == nil
}

// storeFlowBuiltinReadOnlyQuery reports a read-only builtin method (`get`, `count`,
// `contains`, ...) on a receiver that is certainly a builtin darray or dict, where no visible
// UFCS function of that name could accept such a receiver instead. A same-named function
// elsewhere in the program (std, synthesized helpers) otherwise makes the call look like a
// UFCS call that may write every argument.
func (a *Analyzer) storeFlowBuiltinReadOnlyQuery(fn *ast.FieldExpr) bool {
	if fn == nil || !storeFlowReadOnlyMethods[fn.Field] {
		return false
	}
	isDict := false
	if !a.storeFlowDarrayPlace(fn.Object) {
		t := a.storeFlowPlaceType(fn.Object)
		if ref, ok := t.(*RefType); ok && ref != nil {
			t = ref.Elem
		}
		if _, ok := t.(*DictType); !ok {
			return false
		}
		isDict = true
	}
	for _, sym := range a.ufcsFunctionsWithLastSegment(fn.Field) {
		if sym == nil {
			return false
		}
		ft, ok := sym.Type.(*FuncType)
		if !ok || ft == nil {
			return false
		}
		if len(ft.Params) == 0 {
			continue
		}
		if isDict {
			if storeFlowUFCSMayAcceptDictReceiver(ft.Params[0]) {
				return false
			}
		} else if storeFlowUFCSMayAcceptDarrayReceiver(ft.Params[0]) {
			return false
		}
	}
	return true
}

// storeFlowUFCSMayAcceptDictReceiver: the dict analogue of storeFlowUFCSMayAcceptDarrayReceiver.
func storeFlowUFCSMayAcceptDictReceiver(t Type) bool {
	switch receiver := t.(type) {
	case *RefType:
		return receiver != nil && storeFlowUFCSMayAcceptDictReceiver(receiver.Elem)
	case *AggregateStateType:
		return receiver != nil && storeFlowUFCSMayAcceptDictReceiver(receiver.Base)
	case *DictType, *TypeParamType:
		return true
	default:
		return false
	}
}

// storeFlowUFCSMayAcceptDarrayReceiver reports whether a visible UFCS receiver could match a
// builtin DArray. A concrete nominal receiver (including a generic struct instantiation) cannot
// accept a builtin DArray; only DArray formals and unresolved type parameters stay conservative.
func storeFlowUFCSMayAcceptDarrayReceiver(t Type) bool {
	switch receiver := t.(type) {
	case *RefType:
		return receiver != nil && storeFlowUFCSMayAcceptDarrayReceiver(receiver.Elem)
	case *AggregateStateType:
		return receiver != nil && storeFlowUFCSMayAcceptDarrayReceiver(receiver.Base)
	case *DArrayType, *TypeParamType:
		return true
	case *GenericInstanceType:
		// DArray and DynArray are an explicit runtime bridge, so an overload on
		// DynArray[T] can receive a builtin DArray. Other nominal generic structs
		// (for example InlineVec[T, N]) cannot.
		_, ok := dynArrayRuntimeInstance(receiver)
		return ok
	default:
		return false
	}
}

// storeFlowBuiltinDarrayGrowth reports a call of a builtin darray growth method on a local or
// parameter whose every declaration is a darray, where no visible UFCS function of that name
// could accept a darray receiver instead.
func (a *Analyzer) storeFlowBuiltinDarrayGrowth(fn *ast.FieldExpr) bool {
	if !storeFlowDarrayGrowthMethods[fn.Field] {
		return false
	}
	return a.storeFlowBuiltinDarrayGrowthReceiver(fn)
}

// ufcsFunctionsWithLastSegment lists every UFCS function whose registered name ends in segment
// (`push` matches `push` and `Mod.push`). The index is rebuilt only after a new registration;
// scanning every UFCS name at each darray growth call was ~2% of analysis.
func (a *Analyzer) ufcsFunctionsWithLastSegment(segment string) []*Symbol {
	if a.ufcsByLastSegment == nil || a.ufcsByLastSegmentVersion != a.ufcsFunctionsVersion {
		index := make(map[string][]*Symbol, len(a.ufcsFunctionsByName))
		for name, byName := range a.ufcsFunctionsByName {
			last := fillMayAdoptLastSegment(name)
			index[last] = append(index[last], byName...)
		}
		a.ufcsByLastSegment = index
		a.ufcsByLastSegmentVersion = a.ufcsFunctionsVersion
	}
	return a.ufcsByLastSegment[segment]
}

// storeFlowDarrayPlace reports a receiver that is certainly a builtin darray: a local or
// parameter every declaration of which is one, or a field chain from a singly declared local or
// parameter of named struct type whose every same-named struct declares the field as a darray.
func (a *Analyzer) storeFlowDarrayPlace(expr ast.Expr) bool {
	if id, ok := expr.(*ast.Ident); ok && id != nil && a.storeFlowDarrays[id.Name] {
		return true
	}
	t := a.storeFlowPlaceType(expr)
	if ref, ok := t.(*RefType); ok && ref != nil {
		t = ref.Elem
	}
	_, isDarray := t.(*DArrayType)
	return isDarray
}

// storeFlowDarrayElementPointerFree proves that a builtin DArray receiver stores
// values whose representation cannot contain an Elisa borrow. A view-bearing or
// unresolved element type stays conservative, since push must propagate its
// source storage into the destination in those cases.
func (a *Analyzer) storeFlowDarrayElementPointerFree(expr ast.Expr) bool {
	if !a.storeFlowDarrayPlace(expr) {
		return false
	}
	if typ := a.storeFlowPlaceType(expr); typ != nil {
		if ref, ok := typ.(*RefType); ok && ref != nil {
			typ = ref.Elem
		}
		if array, ok := typ.(*DArrayType); ok && array != nil {
			return storeFlowPointerFreeReturn(array.Elem, map[Type]bool{})
		}
	}
	id, ok := expr.(*ast.Ident)
	if !ok || id == nil || a.storeFlowCurrentDecl == nil {
		return false
	}
	found := false
	for _, binding := range a.storeFlowBinderIndexFor(a.storeFlowCurrentDecl).typedBindings(id.Name) {
		found = true
		elem, isDarray := storeFlowDarrayElementTypeExpr(binding.t)
		if !isDarray || !a.storeFlowPointerFreeTypeExpr(elem, map[Type]bool{}) {
			return false
		}
	}
	return found
}

// storeFlowPlaceType returns the type of a field-chain place, or nil when it is not certain.
func (a *Analyzer) storeFlowPlaceType(expr ast.Expr) Type {
	switch e := expr.(type) {
	case *ast.Ident:
		if e == nil {
			return nil
		}
		nt, ok := a.storeFlowNamedTypes[e.Name]
		if !ok {
			return nil
		}
		return a.storeFlowStructNamed(nt)
	case *ast.FieldExpr:
		if e == nil {
			return nil
		}
		base := a.storeFlowPlaceType(e.Object)
		if ref, ok := base.(*RefType); ok && ref != nil {
			base = ref.Elem
		}
		st, ok := base.(*StructType)
		if !ok || st == nil {
			return nil
		}
		field, ok := st.Fields[e.Field]
		if !ok {
			return nil
		}
		return field.Type
	case *ast.IndexExpr:
		if e == nil {
			return nil
		}
		// Assignment targets are analyzed through analyzeIndexExpr directly, so the
		// target itself may not have an exprTypes entry even though its base does.
		// Recover the element type from that resolved base to distinguish scalar or
		// pointer-free indexed writes from stores that can retain a borrow.
		base := a.exprType(e.Object)
		if base == nil {
			base = a.storeFlowPlaceType(e.Object)
		}
		if ref, ok := base.(*RefType); ok && ref != nil {
			base = ref.Elem
		}
		switch indexed := base.(type) {
		case *DArrayType:
			if indexed != nil {
				return indexed.Elem
			}
		case *ArrayType:
			if indexed != nil {
				return indexed.Elem
			}
		case *ViewType:
			if indexed != nil {
				return indexed.Elem
			}
		}
	}
	return nil
}
