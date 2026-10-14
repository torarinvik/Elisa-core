package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"os"
	"reflect"
)

// View provenance of a body local passed where a callee only stores VIEWS out of it (storeViews).
//
// For a local X whose type holds no bytes, a view read out of X points where some view written
// into X points -- never into X's own storage. So at a storeViews call site X's own storage
// region is irrelevant, and the regions that matter are the dependencies of everything written
// into X anywhere in the function (flow-insensitive, hence complete regardless of order):
//
//   - X's initializer and every direct store into X (`X.f <- v`, `X.f.push(v)`, `X <- v`);
//   - every call that may write X (X passed to a writable parameter): the other arguments the
//     callee may store into that parameter (calleeStoreModeInto), each judged by what it is.
//
// One more way a view can land in X: a callee places a region-polymorphic result -- possibly
// holding views of bytes it allocated there -- in X's own region (destination-driven allocation,
// or an explicit / non-growth region parameter bound to it). Then X's region DOES matter; it is
// kept unless no writer of X can allocate into it (storeScanMayAllocateInto, transitive).
//
// Anything not accounted for -- X bound by another construct, X aliased into another value, an
// unresolvable or arena-taking callee writing X -- makes the provenance unknown.

// localViewProvenance returns the regions the views in local X may point into, and whether that
// set is complete.
func (a *Analyzer) localViewProvenance(arg ast.Expr) ([]string, bool) {
	ident, ok := stripAddrAndParens(arg).(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil || a.currentFuncDecl == nil {
		return nil, false
	}
	sym, found := a.currentScope.Lookup(ident.Name)
	if !found || sym == nil || sym.Kind != SymbolLocal {
		return nil, false
	}
	if _, isRef := sym.Type.(*RefType); isRef {
		return nil, false
	}
	if !typeHoldsNoBytes(stripRefForBounds(sym.Type), map[Type]bool{}) {
		return nil, false
	}
	decl, isDecl := sym.Node.(*ast.VarDeclStmt)
	if !isDecl || decl == nil || decl.Name != ident.Name {
		return nil, false
	}
	fn := a.currentFuncDecl
	name := ident.Name
	regions := []string{}
	complete := true
	ownRegionMatters := false
	explained := map[*ast.Ident]bool{}
	explain := func(node any) {
		for _, id := range storeScanIdentsNamed(node, name) {
			explained[id] = true
		}
	}
	addDeps := func(value ast.Expr) {
		if !complete || value == nil {
			return
		}
		deps, ok := a.localViewValueDeps(value, name)
		if !ok {
			complete = false
			viewProvWhy = "c36"
			return
		}
		regions = append(regions, deps...)
	}
	declCount := 0
	scopeIdents := []*ast.Ident{}
	// Per declaration: only the nodes where name refers to decl. A name declared once needs no
	// scope tracking (the plain walk, every mention).
	walk := walkStoreScanNodes
	shadowed := len(a.storeScanIndex(fn).decls[name]) > 1
	if shadowed {
		walk = func(body []ast.Stmt, visit func(any)) { walkDeclScope(body, decl, visit) }
	}
	walk(fn.Body, func(node any) {
		if id, isIdent := node.(*ast.Ident); shadowed && isIdent && id != nil && (id.Name == name || rootNameOfDotted(id.Name) == name) {
			scopeIdents = append(scopeIdents, id)
		}
		if !complete {
			return
		}
		switch n := node.(type) {
		case *ast.VarDeclStmt:
			if n == nil {
				return
			}
			if n.Name == name {
				if n != decl {
					return // a shadowing declaration: another local (its initializer is still visited)
				}
				declCount++
				addDeps(n.Value)
			}
		case *ast.AssignStmt:
			if n == nil {
				return
			}
			if root := rootIdentExpr(n.Target); root != nil && root.Name == name {
				explain(n.Target)
				addDeps(n.Value)
			}
		case *ast.ReturnStmt:
			if n != nil && n.Value != nil {
				explain(n.Value) // handing X to the caller is the caller's provenance problem
			}
		case *ast.CallExpr:
			if n == nil {
				return
			}
			args := returnBorrowCallArgs(n)
			if field, isMethod := n.Func.(*ast.FieldExpr); isMethod && field != nil && len(args) == len(n.Args) {
				if root := rootIdentExpr(field.Object); root != nil && root.Name == name {
					builtin := a.storeScanReceiverIsBuiltinDArray(fn, field.Object)
					switch {
					case builtin && containerGrowthMethods[field.Field]:
						explain(field.Object)
						for _, arg := range n.Args {
							addDeps(arg)
						}
					case builtin && storeScanReadOnlyMethods[field.Field]:
						explain(field.Object)
					}
				}
				return
			}
			xIndex := -1
			for i, arg := range args {
				if root := rootIdentExpr(stripAddrAndParens(arg)); root != nil && root.Name == name {
					if xIndex >= 0 {
						complete = false // X passed twice
						viewProvWhy = "c95"
						return
					}
					xIndex = i
				}
			}
			if xIndex < 0 {
				return
			}
			callee, ok := a.resolveReadOnlyScanCallee(n)
			if !ok || callee == nil || !storeFlowCallFitsParams(callee, n) {
				return // unexplained mention: decided below
			}
			explain(args[xIndex])
			if !a.declParamIsWritableRef(callee, xIndex) {
				return // the callee only reads X
			}
			if funcHasArenaParam(callee) {
				complete = false
				viewProvWhy = "c113"
				return
			}
			if a.storeScanMayAllocateInto(callee, xIndex, map[storeScanKey]bool{}) {
				ownRegionMatters = true
			}
			for j, other := range args {
				if j == xIndex || !a.declParamCarriesStorage(callee, j) {
					continue
				}
				if a.calleeStoreModeInto(callee, j, xIndex) == storeNone {
					continue
				}
				addDeps(other)
			}
		case ast.Expr:
			// A read whose value is a view or a plain scalar cannot alias X's storage.
			if t, ok := a.storeScanStaticType(fn, n); ok && (a.storeScanViewOnlyCached(t) || storeScanPlainScalarType(t)) {
				explain(n)
			}
		case ast.Stmt:
			for _, bound := range stmtBoundNames(n) {
				if bound == name {
					if _, isVar := node.(*ast.VarDeclStmt); !isVar {
						complete = false
						viewProvWhy = "c137"
					}
				}
			}
		}
	})
	if !complete || declCount != 1 {
		if storeScanTrace {
			fmt.Fprintf(os.Stderr, "VIEWPROV %s local=%s incomplete why=%s decls=%d\n", fn.Name, name, viewProvWhy, declCount)
		}
		return nil, false
	}
	if !shadowed {
		scopeIdents = storeScanIdentsNamed(fn.Body, name)
	}
	for _, id := range scopeIdents {
		if !explained[id] {
			if storeScanTrace {
				fmt.Fprintf(os.Stderr, "VIEWPROV %s local=%s unexplained at %s\n", fn.Name, name, id.Pos())
			}
			return nil, false
		}
	}
	if storeScanTrace {
		fmt.Fprintf(os.Stderr, "VIEWPROV %s local=%s ok own=%v regions=%v\n", fn.Name, name, ownRegionMatters, regions)
	}
	if ownRegionMatters {
		if own := a.structLocalArgRegion(arg); own != "" {
			regions = append(regions, own)
		}
		if own := a.containerRegionOf(stripRefForBounds(sym.Type)); own != "" {
			regions = append(regions, own)
		}
	}
	return regions, true
}

// storeScanReadOnlyMethods: builtin methods that read a receiver without storing into it.
var storeScanReadOnlyMethods = map[string]bool{
	"count": true, "len": true, "contains": true, "index_of": true, "is_empty": true, "as_sview": true,
}

// storeScanStaticType: the analyzed type of expr, or one derived from declarations (param or
// literal-initialized local field chains, indexes into them, `.count`).
func (a *Analyzer) storeScanStaticType(fn *ast.FuncDecl, expr ast.Expr) (Type, bool) {
	if t, known := a.exprTypes[expr]; known && t != nil {
		return t, true
	}
	if f, isField := expr.(*ast.FieldExpr); isField && f != nil && (f.Field == "count" || f.Field == "len") {
		if t, ok := a.storeScanDeclTypeOf(fn, storeScanUnwrap(f.Object)); ok {
			switch stripRefForBounds(t).(type) {
			case *DArrayType, *ArrayType, *SViewType:
				return a.namedTypes["usize"], a.namedTypes["usize"] != nil
			}
		}
	}
	return a.storeScanDeclTypeOf(fn, expr)
}

// storeScanIdentsNamed: the identifier nodes naming name in node (field selectors are not names).
func storeScanIdentsNamed(node any, name string) []*ast.Ident {
	out := []*ast.Ident{}
	seen := map[uintptr]bool{}
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if id, ok := v.Interface().(*ast.Ident); ok && id != nil {
				if id.Name == name || rootNameOfDotted(id.Name) == name {
					out = append(out, id)
				}
				return
			}
			if fe, ok := v.Interface().(*ast.FieldExpr); ok && fe != nil {
				rec(reflect.ValueOf(fe.Object))
				return
			}
			rec(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				rec(v.Elem())
			}
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
	rec(reflect.ValueOf(node))
	return out
}

// localViewValueDeps: the regions a value written into X may carry views of. Params, globals and
// literals carry nothing the caller-side check would judge; a local L contributes its tracked
// dependencies and its own storage regions. X itself (`X.f <- X.f.push(..)`) adds nothing new.
func (a *Analyzer) localViewValueDeps(value ast.Expr, self string) ([]string, bool) {
	deps := []string{}
	ok := true
	seen := map[string]bool{}
	walkStoreScanNodes([]ast.Stmt{&ast.ExprStmt{Expr: value}}, func(node any) {
		id, isIdent := node.(*ast.Ident)
		if !ok || !isIdent || id == nil || id.Name == self || seen[id.Name] || a.currentScope == nil {
			return
		}
		seen[id.Name] = true
		sym, found := a.currentScope.Lookup(id.Name)
		if !found || sym == nil || sym.Kind != SymbolLocal {
			return
		}
		state, recorded := a.regionRefStateForExpr(id)
		if !recorded {
			if typeCarriesRegionStorage(sym.Type) || a.typeCanContainRegionRefs(stripRefForBounds(sym.Type), map[string]bool{}) {
				ok = false
				if storeScanTrace {
					fmt.Fprintf(os.Stderr, "VIEWDEP %s unrecorded=%s type=%s at %s\n", self, id.Name, typeString(sym.Type), id.Pos())
				}
			}
			return
		}
		deps = append(deps, liveLocalRegionDependencyNames(state)...)
		if own := a.structLocalArgRegion(id); own != "" {
			deps = append(deps, own)
		}
		if own := a.containerRegionOf(stripRefForBounds(sym.Type)); own != "" {
			deps = append(deps, own)
		}
		if storeScanTrace {
			fmt.Fprintf(os.Stderr, "VIEWDEP %s via=%s deps=%v\n", self, id.Name, deps)
		}
	})
	return deps, ok
}

// storeScanMayAllocateInto: may a call to decl leave, in parameter k, a view of bytes allocated in
// the CALLER's memory (k's region)? Syntactic and order-independent. A callee with no region or
// arena parameter cannot allocate in caller regions at all. With one, it may when its body
//   - stores or appends a user call's result into a place rooted at k (destination-driven
//     allocation, or an implicit region argument bound to k's region),
//   - declares a local that may hold bytes (adoption can place it in k's region; a local with
//     no annotation, or an unknown named type, counts), or
//   - passes k to a callee that may (unknown callees may).
func (a *Analyzer) storeScanMayAllocateInto(decl *ast.FuncDecl, k int, seen map[storeScanKey]bool) bool {
	if decl == nil || k < 0 || k >= len(decl.Params) || len(decl.Body) == 0 {
		return true
	}
	key := storeScanKey{decl, k, -3}
	if cached, ok := a.storeScanAllocCache[key]; ok {
		return cached
	}
	if seen[key] {
		a.storeScanAllocCycleHit = true
		return false // the cycle's other members decide
	}
	seen[key] = true
	savedHit := a.storeScanAllocCycleHit
	a.storeScanAllocCycleHit = false
	result := funcHasArenaParam(decl)
	if !result && len(decl.RegionParams) > 0 {
		param := decl.Params[k].Name
		a.storeScanEachNode(decl, func(node any) {
			if result {
				return
			}
			switch n := node.(type) {
			case *ast.VarDeclStmt:
				if n != nil && a.storeScanDeclMayHoldBytes(n) {
					result = true
				}
			case *ast.AssignStmt:
				if n == nil {
					return
				}
				if root := rootIdentExpr(n.Target); root != nil && root.Name == param && storeScanContainsUserCall(n.Value) {
					result = true
				}
			case *ast.CallExpr:
				if n == nil {
					return
				}
				args := returnBorrowCallArgs(n)
				if field, isMethod := n.Func.(*ast.FieldExpr); isMethod && field != nil && len(args) == len(n.Args) {
					if root := rootIdentExpr(field.Object); root != nil && root.Name == param {
						for _, arg := range n.Args {
							if storeScanContainsUserCall(arg) {
								result = true
							}
						}
					}
					return
				}
				for i, arg := range args {
					if root := rootIdentExpr(stripAddrAndParens(arg)); root == nil || root.Name != param {
						continue
					}
					callee, ok := a.resolveReadOnlyScanCallee(n)
					if !ok || callee == nil || !storeFlowCallFitsParams(callee, n) || a.storeScanMayAllocateInto(callee, i, seen) {
						result = true
						return
					}
				}
			}
		})
	}
	hit := a.storeScanAllocCycleHit
	if !hit || result {
		if a.storeScanAllocCache == nil {
			a.storeScanAllocCache = map[storeScanKey]bool{}
		}
		a.storeScanAllocCache[key] = result
	}
	a.storeScanAllocCycleHit = savedHit || hit
	return result
}

// storeScanDeclMayHoldBytes: a local declaration whose annotated type may hold bytes (a dstr or
// u8/i8 anywhere, an unknown named type, or no annotation).
func (a *Analyzer) storeScanDeclMayHoldBytes(v *ast.VarDeclStmt) bool {
	if v.Type == nil {
		return v.Value != nil && !storeScanEmptyOrScalarLiteral(v.Value)
	}
	return a.storeScanTypeExprMayHoldBytes(v.Type)
}

func storeScanEmptyOrScalarLiteral(value ast.Expr) bool {
	switch storeScanUnwrap(value).(type) {
	case *ast.IntLit, *ast.BoolLit, *ast.FloatLit:
		return true
	}
	return false
}

func (a *Analyzer) storeScanTypeExprMayHoldBytes(t ast.TypeExpr) bool {
	switch tt := t.(type) {
	case nil:
		return true
	case *ast.MutableType:
		return a.storeScanTypeExprMayHoldBytes(tt.Elem)
	case *ast.RefType:
		return false // a reference owns nothing
	case *ast.BuiltinTypeExpr:
		if tt == nil {
			return true
		}
		switch tt.Name {
		case "dstr", "u8", "i8":
			return true
		}
		if storeScanPlainScalarNames[tt.Name] || tt.Name == "sview" || tt.Name == "cstr" {
			return false
		}
		for _, arg := range tt.TypeArgs {
			if a.storeScanTypeExprMayHoldBytes(arg) {
				return true
			}
		}
		return tt.Name != "darray" && tt.Name != "dict" && tt.Name != "set" && tt.Name != "Optional"
	case *ast.NamedType:
		if tt == nil {
			return true
		}
		switch tt.Name {
		case "dstr", "u8", "i8":
			return true
		case "sview", "cstr":
			return false
		}
		if storeScanPlainScalarNames[tt.Name] {
			return false
		}
		if st := a.storeScanUniqueStruct(tt.Name); st != nil {
			return !typeHoldsNoBytes(st, map[Type]bool{})
		}
		return true
	}
	return true
}

// storeScanContainsUserCall: the value contains a call that is not a builtin container method.
func storeScanContainsUserCall(value ast.Expr) bool {
	found := false
	walkStoreScanNodes([]ast.Stmt{&ast.ExprStmt{Expr: value}}, func(node any) {
		call, isCall := node.(*ast.CallExpr)
		if !isCall || call == nil {
			return
		}
		if field, isMethod := call.Func.(*ast.FieldExpr); isMethod && field != nil && (containerGrowthMethods[field.Field] || storeScanReadOnlyMethods[field.Field]) {
			return
		}
		found = true
	})
	return found
}

// viewProvWhy: trace only -- the last reason localViewProvenance gave up.
var viewProvWhy string

// walkDeclScope visits the nodes of body in which name refers to decl: from decl to the end of
// its statement list (nested lists included), until another declaration of the same name in
// that list or a nested one shadows it. Declarations are lexically scoped by statement list, so
// the same name declared in two match arms is two locals. The shadowing declaration itself is
// visited while decl is still in scope (its initializer may read decl).
func walkDeclScope(body []ast.Stmt, decl *ast.VarDeclStmt, visit func(any)) {
	name := decl.Name
	seen := map[[2]uintptr]bool{}
	var rec func(v reflect.Value, active bool)
	var list func(v reflect.Value, active bool)
	list = func(v reflect.Value, active bool) {
		for i := 0; i < v.Len(); i++ {
			elem := v.Index(i)
			var stmt any
			if elem.Kind() == reflect.Interface && !elem.IsNil() {
				stmt = elem.Interface()
			}
			if vd, ok := stmt.(*ast.VarDeclStmt); ok && vd != nil && vd.Name == name {
				if vd == decl {
					rec(elem, true)
					active = true
				} else {
					rec(elem, active)
					active = false
				}
				continue
			}
			rec(elem, active)
		}
	}
	rec = func(v reflect.Value, active bool) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			key := [2]uintptr{v.Pointer(), 0}
			if active {
				key[1] = 1
			}
			if seen[key] {
				return
			}
			seen[key] = true
			if active {
				visit(v.Interface())
			}
			rec(v.Elem(), active)
		case reflect.Interface:
			if !v.IsNil() {
				rec(v.Elem(), active)
			}
		case reflect.Struct:
			if v.Type() == storeScanPosType {
				return
			}
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i), active)
			}
		case reflect.Slice:
			if v.Type() == storeScanStmtListType {
				list(v, active)
				return
			}
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i), active)
			}
		case reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i), active)
			}
		}
	}
	list(reflect.ValueOf(body), false)
}

var storeScanStmtListType = reflect.TypeOf([]ast.Stmt(nil))
