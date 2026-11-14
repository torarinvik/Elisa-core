package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"strings"
)

// returnBorrowUntrustedNames collects the local names with a write the walker does not model.
// Name-based, so a shadowed binding is untrusted whenever any same-named binding is.
func (a *Analyzer) returnBorrowUntrustedNames(fn *ast.FuncDecl, processed map[*ast.CallExpr]bool) map[string]bool {
	untrusted := map[string]bool{}
	markRoot := func(expr ast.Expr) {
		if name := returnBorrowPlaceRootName(returnBorrowStripAddr(expr)); name != "" {
			untrusted[name] = true
		}
	}
	markAll := func(expr ast.Expr) {
		collectReturnBorrowNodeNames(reflect.ValueOf(expr), untrusted)
	}
	var boundMachineInputs map[string]bool
	machineInputBound := func(name string) bool {
		if boundMachineInputs == nil {
			boundMachineInputs = returnBorrowBoundMachineInputs(fn)
		}
		return boundMachineInputs[name]
	}
	exemptAddr := map[*ast.AddrOfExpr]bool{}
	refLinks := map[string][]string{}
	refDecls := map[string]bool{}
	// A call whose result is discarded (`out.push(x)` as a statement) hands back no alias.
	discarded := map[*ast.CallExpr]bool{}
	var walk func(v reflect.Value, inExpr bool)
	// A value-form loop lowers to nested ExprBlocks (`do: do: ... acc`): every level's statements
	// are modeled like a nested body; only the innermost value is an expression.
	var walkValueBlock func(block *ast.ExprBlock)
	walkValueBlock = func(block *ast.ExprBlock) {
		walk(reflect.ValueOf(block.Stmts), false)
		if inner, isBlock := block.Value.(*ast.ExprBlock); isBlock && inner != nil {
			walkValueBlock(inner)
			return
		}
		walk(reflect.ValueOf(block.Value), true)
	}
	walk = func(v reflect.Value, inExpr bool) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem(), inExpr)
			}
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			node := v.Interface()
			if stmt, ok := node.(ast.Stmt); ok {
				if inExpr || (!returnBorrowWalkedStmt(stmt) && a.returnBorrowDiscardedCall(stmt) == nil) {
					collectReturnBorrowNodeNames(v, untrusted)
					return
				}
				switch n := stmt.(type) {
				case *ast.VarDeclStmt:
					// A value-form loop as the declaration's value is modeled like a nested body.
					if block, isBlock := n.Value.(*ast.ExprBlock); isBlock && block != nil {
						walkValueBlock(block)
						return
					}
					// `r: mutable T& = x` aliases x: stores through r reach x.
					_, isRef := n.Type.(*ast.RefType)
					if !isRef {
						_, isRef = a.exprType(n.Value).(*RefType)
					}
					// A machine's input var is only compared against its arms, unless an arm binds it.
					if isRef && (!strings.HasPrefix(n.Name, "__machine_input_") || machineInputBound(n.Name)) {
						if addr, root := a.returnBorrowRefDeclTarget(n); root != "" && root != n.Name {
							// Stores through r are forwarded into x's union; x stays trusted unless r
							// itself reaches a write the walker does not model.
							exemptAddr[addr] = true
							refLinks[n.Name] = append(refLinks[n.Name], root)
						} else {
							markRoot(n.Value)
						}
						refDecls[n.Name] = true
					}
				case *ast.AssignStmt:
					if _, isRef := a.exprType(n.Target).(*RefType); isRef {
						markRoot(n.Value)
					}
					// `xs <- xs.truncate(0)` and `xs <- xs.push(v)` copy the returned receiver back
					// into itself: no new name aliases it (the push's store into xs is modeled).
					if call, isCall := returnBorrowStripParens(n.Value).(*ast.CallExpr); isCall && call != nil && (a.returnBorrowCountOnlyDArrayMethod(call) || a.returnBorrowDArrayValueArgMethod(call) || a.returnBorrowBorrowFreeContainerUpdate(call)) {
						if field, isField := call.Func.(*ast.FieldExpr); isField && returnBorrowSamePlace(n.Target, field.Object) {
							discarded[call] = true
						}
					}
					if returnBorrowPlaceRootName(n.Target) == "" {
						markAll(n.Target)
					}
				case *ast.AugAssignStmt:
					if returnBorrowPlaceRootName(n.Target) == "" {
						markAll(n.Target)
					}
				case *ast.IterForStmt:
					// A reference loop binding writes into the iterated container.
					if n.Mode != ast.IterBindValue {
						markRoot(n.Source)
					}
				case *ast.ParallelForStmt:
					markRoot(n.Source)
				case *ast.MatchStmt:
					if n.Store != nil {
						markAll(n.Store)
					}
				case *ast.DiscardStmt:
					discarded[a.returnBorrowDiscardedCall(n)] = true
				case *ast.ExprStmt:
					if call := a.returnBorrowAsCall(n.Expr); call != nil {
						discarded[call] = true
					}
					// A value-form loop (`for x in xs |acc| -> acc:`) desugars to an ExprBlock
					// statement; the walker models its statements like a nested body.
					if block, isBlock := n.Expr.(*ast.ExprBlock); isBlock && block != nil {
						walkValueBlock(block)
						return
					}
				}
			}
			if expr, ok := node.(ast.Expr); ok {
				if lit, isLit := expr.(*ast.StructLitExpr); isLit {
					if call := a.returnBorrowAsCall(lit); call != nil {
						expr = call
					}
				}
				switch n := expr.(type) {
				case *ast.LambdaExpr:
					collectReturnBorrowNodeNames(v, untrusted)
					return
				case *ast.AddrOfExpr:
					if !exemptAddr[n] {
						markRoot(n.Operand)
					}
				case *ast.CallExpr:
					if processed[n] {
						// An address passed straight to a processed call is modeled by its stores.
						for _, arg := range returnBorrowCallArgs(n) {
							if addr, isAddr := returnBorrowStripParens(arg).(*ast.AddrOfExpr); isAddr && addr != nil {
								exemptAddr[addr] = true
							}
						}
						if !discarded[n] {
							a.markReturnBorrowCallResultAliases(n, markRoot)
						}
					} else if a.returnBorrowCountOnlyDArrayMethod(n) && discarded[n] {
						// Resizes its receiver by integer counts: stores no borrow anywhere.
					} else {
						byValue := a.returnBorrowDArrayValueArgMethod(n)
						for _, arg := range returnBorrowCallArgs(n) {
							if byValue && a.returnBorrowCopiedArg(arg) {
								continue
							}
							markRoot(arg)
						}
						if field, isField := n.Func.(*ast.FieldExpr); isField && field != nil {
							markRoot(field.Object)
						}
					}
				}
				walk(v.Elem(), true)
				return
			}
			walk(v.Elem(), inExpr)
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Field(i).CanInterface() {
					walk(v.Field(i), inExpr)
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), inExpr)
			}
		}
	}
	for _, stmt := range fn.Body {
		walk(reflect.ValueOf(stmt), false)
	}
	// A reference whose own uses escape the model can write its referent anywhere.
	for changed := true; changed; {
		changed = false
		for name, roots := range refLinks {
			if !untrusted[name] {
				continue
			}
			for _, root := range roots {
				if !untrusted[root] {
					untrusted[root] = true
					changed = true
				}
			}
		}
	}
	for name := range refDecls {
		untrusted[name] = true
	}
	return untrusted
}

// returnBorrowDArrayValueArgMethod reports a builtin darray `push`/`insert`, which takes its
// arguments by value: the call cannot write into a local passed to it.
func (a *Analyzer) returnBorrowDArrayValueArgMethod(call *ast.CallExpr) bool {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || (field.Field != "push" && field.Field != "insert") || !a.returnBorrowBuiltinMethodCall(call) {
		return false
	}
	objType := a.exprType(field.Object)
	if ref, isRef := objType.(*RefType); isRef && ref != nil {
		objType = ref.Elem
	}
	_, isDArray := objType.(*DArrayType)
	return isDArray
}

// returnBorrowCopiedArg reports an argument passed as a plain value copy: neither an address nor a
// reference, so the callee receives no way to write the argument's place.
// returnBorrowBoundMachineInputs collects the machine input vars an arm binds by name
// (`State, c if ...:` lowers to `c = __machine_input_N`).
func returnBorrowBoundMachineInputs(fn *ast.FuncDecl) map[string]bool {
	bound := map[string]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if decl, ok := v.Interface().(*ast.VarDeclStmt); ok {
				if ident, isIdent := decl.Value.(*ast.Ident); isIdent && ident != nil && strings.HasPrefix(ident.Name, "__machine_input_") {
					bound[ident.Name] = true
				}
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(fn.Body))
	return bound
}

// returnBorrowCountOnlyDArrayMethod reports a darray `reserve`/`clear`/`truncate` whose arguments
// are all integers: it changes the receiver's length or capacity and stores no borrow.
func (a *Analyzer) returnBorrowCountOnlyDArrayMethod(call *ast.CallExpr) bool {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || !returnBorrowCountOnlyMethods[field.Field] || !a.returnBorrowBuiltinMethodCall(call) {
		return false
	}
	objType := a.exprType(field.Object)
	if ref, isRef := objType.(*RefType); isRef && ref != nil {
		objType = ref.Elem
	}
	if _, isDArray := objType.(*DArrayType); !isDArray {
		return false
	}
	for _, arg := range returnBorrowCallArgs(call) {
		switch t := a.exprType(returnBorrowStripParens(arg)).(type) {
		case *BuiltinType:
			if !isIntegerBuiltinName(t.Name) {
				return false
			}
		case *BitIntType:
		default:
			return false
		}
	}
	return true
}

var returnBorrowCountOnlyMethods = map[string]bool{"reserve": true, "clear": true, "truncate": true}

// returnBorrowBorrowFreeContainerUpdate reports a builtin dict/set update (`d.put(k, v)`,
// `s.add(x)`, `d.remove(k)`) whose arguments can carry no frame borrow: it hands back its receiver
// holding nothing new.
func (a *Analyzer) returnBorrowBorrowFreeContainerUpdate(call *ast.CallExpr) bool {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || !returnBorrowContainerUpdateMethods[field.Field] || !a.returnBorrowBuiltinMethodCall(call) {
		return false
	}
	objType := a.exprType(field.Object)
	if ref, isRef := objType.(*RefType); isRef && ref != nil {
		objType = ref.Elem
	}
	switch objType.(type) {
	case *DictType, *SetType:
	default:
		return false
	}
	for _, arg := range returnBorrowCallArgs(call) {
		argType := a.exprType(returnBorrowStripParens(arg))
		if argType == nil || a.typeMayHoldFrameBorrow(argType, map[Type]bool{}) {
			return false
		}
	}
	return true
}

var returnBorrowContainerUpdateMethods = map[string]bool{"put": true, "add": true, "remove": true}

// returnBorrowSamePlace reports two spellings of one field-chain place (`p.items`, `p.items`).
func returnBorrowSamePlace(left, right ast.Expr) bool {
	leftRoot, leftFields, leftOK := exprPlacePath(returnBorrowStripParens(left))
	rightRoot, rightFields, rightOK := exprPlacePath(returnBorrowStripParens(right))
	if !leftOK || !rightOK || leftRoot != rightRoot || len(leftFields) != len(rightFields) {
		return false
	}
	for i := range leftFields {
		if leftFields[i] != rightFields[i] {
			return false
		}
	}
	return true
}

func (a *Analyzer) returnBorrowCopiedArg(arg ast.Expr) bool {
	inner := returnBorrowStripParens(arg)
	if _, isAddr := inner.(*ast.AddrOfExpr); isAddr {
		return false
	}
	argType := a.exprType(inner)
	if argType == nil {
		return false
	}
	_, isRef := argType.(*RefType)
	return !isRef
}

func returnBorrowCallArgs(call *ast.CallExpr) []ast.Expr {
	if call.ResolvedArgsValid {
		return call.ResolvedArgs
	}
	return call.Args
}

func returnBorrowStripParens(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok || paren == nil {
			return expr
		}
		expr = paren.Inner
	}
}

func returnBorrowStripAddr(expr ast.Expr) ast.Expr {
	expr = returnBorrowStripParens(expr)
	if addr, ok := expr.(*ast.AddrOfExpr); ok && addr != nil {
		return addr.Operand
	}
	return expr
}

// markReturnBorrowCallResultAliases marks the arguments a call may hand back as a WRITABLE
// reference: writes through the result then reach the argument without the walker seeing them.
func (a *Analyzer) markReturnBorrowCallResultAliases(call *ast.CallExpr, markRoot func(ast.Expr)) {
	resultType := a.exprType(call)
	if resultType == nil || !a.typeHasWritableBorrow(resultType, map[Type]bool{}) {
		return
	}
	// A darray `push`/`insert` hands back its receiver; its by-value arguments are not aliased.
	byValue := a.returnBorrowDArrayValueArgMethod(call)
	for _, arg := range returnBorrowCallArgs(call) {
		if byValue && a.returnBorrowCopiedArg(arg) {
			continue
		}
		markRoot(arg)
	}
	if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil {
		markRoot(field.Object)
	}
}
