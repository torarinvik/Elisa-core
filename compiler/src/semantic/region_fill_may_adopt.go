package semantic

import (
	"reflect"
	"strings"
	"sync"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// fillMayAdopt reports whether a function may put data allocated in a caller's arena into a
// container parameter. Only a void grower allocates there (its ambient region is the grown
// container's), so a function may do so only if it can reach one through calls: this is the
// syntactic over-approximation used for callees whose body summary (ambientFillAnalyzed /
// ambientFillFresh) is not available yet — declared later, or recursive and still in progress.
func (a *Analyzer) fillMayAdopt(decl *ast.FuncDecl) bool {
	if a.fillMayAdoptSet == nil {
		a.fillMayAdoptSet = a.computeFillMayAdopt()
	}
	adopt, known := a.fillMayAdoptSet[decl]
	return adopt || !known
}

// computeFillMayAdopt builds the call graph over every declared function by name (all overloads,
// methods by their last name segment) and closes the void-grower seeds backwards over it. A call
// whose target cannot be named from the syntax — through a parameter, local, global or field
// holding a function value, or any computed callee — is a seed too.
func (a *Analyzer) computeFillMayAdopt() map[*ast.FuncDecl]bool {
	byName := map[string][]*ast.FuncDecl{}
	for decl := range a.funcDeclSymbols {
		if decl != nil {
			byName[fillMayAdoptLastSegment(decl.Name)] = append(byName[fillMayAdoptLastSegment(decl.Name)], decl)
		}
	}
	funcFields := map[string]bool{}
	structNames := map[string]bool{}
	if a.file != nil {
		fillMayAdoptWalk(reflect.ValueOf(a.file), func(v any) {
			if s, ok := v.(*ast.StructDecl); ok && s != nil {
				structNames[fillMayAdoptLastSegment(s.Name)] = true
				for _, field := range s.Fields {
					if fillMayAdoptHasFuncType(reflect.ValueOf(field)) {
						funcFields[field.Name] = true
					}
				}
			}
		})
	}
	result := map[*ast.FuncDecl]bool{}
	callers := map[*ast.FuncDecl][]*ast.FuncDecl{}
	var work []*ast.FuncDecl
	seed := func(decl *ast.FuncDecl) {
		if !result[decl] {
			result[decl] = true
			work = append(work, decl)
		}
	}
	for decl := range a.funcDeclSymbols {
		if decl == nil {
			continue
		}
		result[decl] = false
		// A void grower is a seed only if its body can put freshly allocated data (data in the
		// adopted arena) into its container: a construction, an allocating call, or a call to a
		// region-polymorphic callee. One that only moves argument-derived values around adopts
		// nothing, and its callers' fills stay tracked. Calls into other seeds close over callers.
		if decl.AmbientGrownContainerRegion != "" && a.fillMayAdoptFreshShapes(decl, byName) {
			seed(decl)
		}
		valueParams := map[string]bool{}
		// declaredHeads maps each binding name to its declared type's head, "" when any binding
		// of the name is unannotated or has another head.
		declaredHeads := map[string]string{}
		declare := func(name string, typ ast.TypeExpr) {
			head := fillMayAdoptTypeHead(typ)
			if prior, seen := declaredHeads[name]; seen && prior != head {
				head = ""
			}
			declaredHeads[name] = head
		}
		for _, param := range decl.Params {
			valueParams[param.Name] = true
			declare(param.Name, param.Type)
		}
		fillMayAdoptWalk(reflect.ValueOf(decl.Body), func(v any) {
			switch n := v.(type) {
			case *ast.LambdaExpr:
				for _, param := range n.Params {
					valueParams[param.Name] = true
					declare(param.Name, param.Type)
				}
			case *ast.VarDeclStmt:
				declare(n.Name, n.Type)
			case *ast.ForStmt:
				declare(n.Name, nil)
			case *ast.IterForStmt:
				for name := range fillMayAdoptIterBinders(n) {
					declare(name, nil)
				}
			}
		})
		// A parameter shadows a function of its name throughout the body (lambdas capture it;
		// a body holds no nested `def`, and one would end the shadowing, so it disables all of it).
		shadowed := map[string]bool{}
		for _, param := range decl.Params {
			shadowed[param.Name] = true
		}
		// A match arm's payload binders shadow it within the arm's guard and body, a loop binder
		// within the loop's filters and body, and a local within the statements after its
		// declaration in the same block.
		armShadowed := map[*ast.Ident]bool{}
		markScoped := func(binders map[string]bool, scopes ...any) {
			if len(binders) == 0 {
				return
			}
			for _, scope := range scopes {
				fillMayAdoptWalk(reflect.ValueOf(scope), func(inner any) {
					if ident, ok := inner.(*ast.Ident); ok && ident != nil && binders[ident.Name] {
						armShadowed[ident] = true
					}
				})
			}
		}
		// One pass per list: a statement's idents are shadowed by every local declared before it
		// in the list. (Walking the remaining statements once per local was quadratic in block
		// length: ~3% of compiling the stage1 driver.)
		fillMayAdoptStmtLists(reflect.ValueOf(decl.Body), func(list []ast.Stmt) {
			var declaredBefore map[string]bool
			for _, stmt := range list {
				if len(declaredBefore) != 0 {
					markScoped(declaredBefore, stmt)
				}
				if local, ok := stmt.(*ast.VarDeclStmt); ok && local != nil {
					if declaredBefore == nil {
						declaredBefore = map[string]bool{}
					}
					declaredBefore[local.Name] = true
				}
			}
		})
		markMatchArm := func(arm *ast.MatchArm) {
			binders := map[string]bool{}
			switch pattern := arm.Pattern.(type) {
			case *ast.MatchVariantPattern, *ast.MatchStructPattern:
				collectMatchPatternBindNames(pattern, binders)
			}
			markScoped(binders, arm.Guard, arm.Body)
		}
		nestedDef := false
		fillMayAdoptWalk(reflect.ValueOf(decl.Body), func(v any) {
			switch n := v.(type) {
			case *ast.FuncDecl:
				nestedDef = true
			case *ast.ForStmt:
				markScoped(map[string]bool{n.Name: true}, n.Body)
			case *ast.IterForStmt:
				markScoped(fillMayAdoptIterBinders(n), n.WhereFilter, n.Filter, n.Body)
			// Arms are held by value, so they are reached through their match, never visited alone.
			case *ast.MatchStmt:
				for i := range n.Arms {
					markMatchArm(&n.Arms[i])
				}
			case *ast.MatchExpr:
				for i := range n.Arms {
					markMatchArm(&n.Arms[i])
				}
			case *ast.CatchExpr:
				for _, arm := range n.Arms {
					binders := map[string]bool{}
					for _, name := range arm.Payload {
						binders[name] = true
					}
					markScoped(binders, arm.Body)
				}
			}
		})
		if nestedDef {
			shadowed = map[string]bool{}
			armShadowed = map[*ast.Ident]bool{}
		}
		unknown := false
		fillMayAdoptWalk(reflect.ValueOf(decl.Body), func(v any) {
			switch n := v.(type) {
			case *ast.Ident:
				// Any mention, not only a callee: a function referenced as a value may be called later.
				if shadowed[n.Name] || armShadowed[n] {
					break
				}
				for _, callee := range byName[n.Name] {
					callers[callee] = append(callers[callee], decl)
				}
			case *ast.FieldExpr:
				// `recv.m`: a builtin-container receiver never selects a method whose receiver
				// parameter is a declared struct (the darray `push` is not InlineVec's).
				receiverHead := ""
				if root, ok := fillMayAdoptStripParens(n.Object).(*ast.Ident); ok && root != nil {
					receiverHead = declaredHeads[root.Name]
				}
				for _, callee := range byName[n.Field] {
					if fillMayAdoptBuiltinHeads[receiverHead] && len(callee.Params) > 0 {
						if head := fillMayAdoptTypeHead(callee.Params[0].Type); head != receiverHead && structNames[head] {
							continue
						}
					}
					callers[callee] = append(callers[callee], decl)
				}
			}
		})
		fillMayAdoptWalk(reflect.ValueOf(decl.Body), func(v any) {
			call, ok := v.(*ast.CallExpr)
			if !ok || call == nil || unknown {
				return
			}
			target := call.Func
			if special, ok := target.(*ast.SpecializeExpr); ok && special != nil {
				target = special.Operand
			}
			defer func() {
			}()
			switch callee := target.(type) {
			case *ast.Ident:
				if fillMayAdoptIntrinsics[callee.Name] {
					break
				}
				// A parser-lowered f-string: the `__fstr` builtin reads its parts and returns a
				// fresh dstr. A user function of that name is not lowered and resolves normally.
				if call.FStringLowering && callee.Name == "__fstr" {
					break
				}
				// A parameter, local or pattern binder holds a function value, whatever global
				// shares its name.
				_, local := declaredHeads[callee.Name]
				if valueParams[callee.Name] || local || armShadowed[callee] {
					unknown = true
				} else if len(byName[callee.Name]) == 0 && !a.fillMayAdoptNamedCallee(callee.Name) {
					unknown = true
				}
			case *ast.FieldExpr:
				if funcFields[callee.Field] {
					unknown = true
				}
			default:
				unknown = true
			}
		})
		if unknown {
			seed(decl)
		}
	}
	for len(work) > 0 {
		decl := work[len(work)-1]
		work = work[:len(work)-1]
		for _, caller := range callers[decl] {
			seed(caller)
		}
	}
	return result
}

// fillMayAdoptNamedCallee reports whether a callee name that is no declared function still names
// a fixed target — a builtin, extern or type — rather than a value holding a function.
func (a *Analyzer) fillMayAdoptNamedCallee(name string) bool {
	if a.globalScope == nil {
		return false
	}
	sym, found := a.globalScope.Lookup(name)
	if !found || sym == nil {
		return false
	}
	switch sym.Kind {
	case SymbolFunc, SymbolExternFunc, SymbolStruct, SymbolExternType:
		return true
	}
	return false
}

// fillMayAdoptIntrinsics are callee names the analyzer handles itself; they only evaluate
// their arguments.
var fillMayAdoptIntrinsics = map[string]bool{"assert": true, "ASSERT": true}

// fillMayAdoptBuiltinHeads are builtin container type heads, whose methods are the compiler's.
var fillMayAdoptBuiltinHeads = map[string]bool{"darray": true, "set": true, "map": true, "dstr": true, "astr": true, "sview": true}

// fillMayAdoptTypeHead is the head name of a declared type, through references; "" if none.
func fillMayAdoptTypeHead(typ ast.TypeExpr) string {
	switch t := typ.(type) {
	case *ast.RefType:
		if t != nil {
			return fillMayAdoptTypeHead(t.Elem)
		}
	case *ast.NamedType:
		if t != nil {
			return fillMayAdoptLastSegment(t.Name)
		}
	case *ast.GenericType:
		if t != nil {
			return fillMayAdoptLastSegment(t.Name)
		}
	case *ast.BuiltinTypeExpr:
		if t != nil {
			return t.Name
		}
	}
	return ""
}

func fillMayAdoptStripParens(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok || paren == nil {
			return expr
		}
		expr = paren.Inner
	}
}

func fillMayAdoptLastSegment(name string) string {
	if idx := strings.LastIndexAny(name, ".:"); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

func fillMayAdoptHasFuncType(v reflect.Value) bool {
	found := false
	fillMayAdoptWalk(v, func(n any) {
		if _, ok := n.(*ast.FuncTypeExpr); ok {
			found = true
		}
	})
	return found
}

// fillMayAdoptWalk visits every pointer node reachable from v, each once.
// fillMayAdoptIterBinders names the variables a for-each pattern binds.
func fillMayAdoptIterBinders(n *ast.IterForStmt) map[string]bool {
	binders := map[string]bool{}
	fillMayAdoptWalk(reflect.ValueOf(n.Pattern), func(v any) {
		if bind, ok := v.(*ast.MoveBindNamePattern); ok && bind != nil && bind.Name != "" {
			binders[bind.Name] = true
		}
	})
	return binders
}

// fillMayAdoptStmtLists visits every statement list (block) under v.
func fillMayAdoptStmtLists(v reflect.Value, visit func([]ast.Stmt)) {
	stmtList := reflect.TypeOf([]ast.Stmt(nil))
	var walk func(reflect.Value)
	seen := map[uintptr]bool{}
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			walk(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
			}
		case reflect.Slice, reflect.Array:
			if v.Type() == stmtList && v.CanInterface() {
				visit(v.Interface().([]ast.Stmt))
			}
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Map:
			iter := v.MapRange()
			for iter.Next() {
				walk(iter.Value())
			}
		}
	}
	walk(v)
}

func fillMayAdoptWalk(v reflect.Value, visit func(any)) {
	seen := fillMayAdoptSeenPool.Get().(map[uintptr]bool)
	fillMayAdoptWalkSeen(v, visit, seen)
	clear(seen)
	fillMayAdoptSeenPool.Put(seen)
}

var fillMayAdoptSeenPool = sync.Pool{
	New: func() any { return make(map[uintptr]bool) },
}

func fillMayAdoptWalkSeen(v reflect.Value, visit func(any), seen map[uintptr]bool) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || !v.CanInterface() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		visit(v.Interface())
		fillMayAdoptWalkSeen(v.Elem(), visit, seen)
	case reflect.Interface:
		if !v.IsNil() {
			fillMayAdoptWalkSeen(v.Elem(), visit, seen)
		}
	case reflect.Struct:
		for _, i := range fillMayAdoptExportedFields(v.Type()) {
			fillMayAdoptWalkSeen(v.Field(i), visit, seen)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			fillMayAdoptWalkSeen(v.Index(i), visit, seen)
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			fillMayAdoptWalkSeen(iter.Value(), visit, seen)
		}
	}
}

var fillMayAdoptFieldCache sync.Map // map[reflect.Type][]int

func fillMayAdoptExportedFields(t reflect.Type) []int {
	if cached, ok := fillMayAdoptFieldCache.Load(t); ok {
		return cached.([]int)
	}
	indices := make([]int, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).IsExported() {
			indices = append(indices, i)
		}
	}
	cached, _ := fillMayAdoptFieldCache.LoadOrStore(t, indices)
	return cached.([]int)
}

// fillMayAdoptNonAllocatingMethods are builtin container methods that never allocate a value of
// their own: they read, or move their arguments into the receiver.
var fillMayAdoptNonAllocatingMethods = map[string]bool{
	"push": true, "pop": true, "count": true, "len": true, "clear": true, "is_empty": true, "get": true,
	"contains": true, "insert": true, "remove": true, "reserve": true, "as_sview": true, "slice": true,
	"starts_with": true, "ends_with": true, "find": true, "index_of": true, "first": true, "last": true,
	"truncate": true, "swap": true, "extend": true, "append": true,
}

// fillMayAdoptFreshShapes reports whether the body of a void grower can construct data in the
// arena it adopts: an aggregate or list construction, a lambda, a `+` (string concatenation), an
// f-string, a call to a builtin or constructor that allocates, or a call whose callee may be
// region-polymorphic (the threaded region is the adopted one). It mirrors noteAmbientFillFresh's
// two direct sources; the third (a parameter passed to a callee not known to fill only from its
// arguments) is a call into another seed, which the caller closure covers.
func (a *Analyzer) fillMayAdoptFreshShapes(decl *ast.FuncDecl, byName map[string][]*ast.FuncDecl) bool {
	fresh := false
	polyCallee := func(name string) bool {
		for _, callee := range byName[name] {
			sym := a.funcDeclSymbols[callee]
			if sym == nil {
				return true
			}
			fnType, ok := sym.Type.(*FuncType)
			if !ok || fnType == nil || fnType.RegionPolymorphic {
				return true
			}
		}
		return false
	}
	fillMayAdoptWalk(reflect.ValueOf(decl.Body), func(v any) {
		if fresh {
			return
		}
		switch n := v.(type) {
		case *ast.StructLitExpr, *ast.ListLitExpr, *ast.ListComprehensionExpr, *ast.LambdaExpr,
			*ast.TupleExpr, *ast.RecordUpdateExpr, *ast.AllocExpr, *ast.QueryExpr, *ast.FoldExpr,
			*ast.MachineFromExpr, *ast.EmitExpr, *ast.FuncDecl:
			fresh = true
		case *ast.BinaryExpr:
			if n.Op == lexer.TOKEN_PLUS || n.LoweredCall != nil {
				fresh = true
			}
		case *ast.CallExpr:
			target := n.Func
			if special, ok := target.(*ast.SpecializeExpr); ok && special != nil {
				target = special.Operand
			}
			switch callee := target.(type) {
			case *ast.Ident:
				if fillMayAdoptIntrinsics[callee.Name] {
					return
				}
				if n.FStringLowering || len(byName[callee.Name]) == 0 || polyCallee(callee.Name) {
					fresh = true
				}
			case *ast.FieldExpr:
				if len(byName[callee.Field]) == 0 {
					if !fillMayAdoptNonAllocatingMethods[callee.Field] {
						fresh = true
					}
				} else if polyCallee(callee.Field) {
					fresh = true
				}
			default:
				fresh = true
			}
		}
	})
	return fresh
}
