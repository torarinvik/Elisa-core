package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/astscan"
	"elisacore/src/lexer"
	"reflect"
)

// Struct declarations can occur in namespaces and static declaration branches,
// but not inside expression or statement bodies. Inspect all branches as the
// conservative census did, without walking every function body in the file.
func fillMayAdoptStructFacts(file *ast.File) (map[string]bool, map[string]bool) {
	structNames, funcFields := map[string]bool{}, map[string]bool{}
	if file == nil {
		return structNames, funcFields
	}
	seen := map[ast.Decl]bool{}
	var walk func([]ast.Decl)
	walk = func(decls []ast.Decl) {
		for _, decl := range decls {
			if decl == nil || seen[decl] {
				continue
			}
			seen[decl] = true
			switch n := decl.(type) {
			case *ast.StructDecl:
				if n == nil {
					continue
				}
				structNames[fillMayAdoptLastSegment(n.Name)] = true
				for _, field := range n.Fields {
					if fillMayAdoptHasFuncType(reflect.ValueOf(field)) {
						funcFields[field.Name] = true
					}
				}
			case *ast.NamespaceDecl:
				if n != nil {
					walk(n.Decls)
				}
			case *ast.StaticIfDecl:
				if n != nil {
					walk(n.Then)
					for _, branch := range n.Elifs {
						walk(branch.Body)
					}
					walk(n.Else)
				}
			}
		}
	}
	walk(file.Decls)
	return structNames, funcFields
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
	// Both separators are ASCII, so a reverse byte scan is equivalent to the
	// Unicode-aware LastIndexAny, including for malformed UTF-8.
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' || name[i] == ':' {
			return name[i+1:]
		}
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
	state := fillMayAdoptSeenPool.Get().(*fillWalkSeen)
	seen := state.values
	walk = func(v reflect.Value) {
		if v.Kind() == reflect.Interface {
			if v.IsNil() {
				return
			}
			switch v.Interface().(type) {
			case *ast.Ident, *ast.IntLit, *ast.FloatLit, *ast.StringLit,
				*ast.CharLit, *ast.BoolLit, *ast.NullLit, *ast.ZeroedLit:
				return // these leaves cannot contain a statement list
			}
			v = v.Elem()
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			key := v.Pointer()
			if seen[key] {
				return
			}
			seen[key] = true
			walk(v.Elem())
		case reflect.Struct:
			for _, i := range fillMayAdoptExportedFields(v.Type()) {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			if v.Type() == stmtList && v.CanInterface() {
				if v.CanAddr() {
					visit(*v.Addr().Interface().(*[]ast.Stmt))
				} else {
					visit(v.Interface().([]ast.Stmt))
				}
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
	if state.reset() {
		fillMayAdoptSeenPool.Put(state)
	}
}

func fillMayAdoptWalk(v reflect.Value, visit func(any)) {
	state := fillMayAdoptSeenPool.Get().(*fillWalkSeen)
	fillMayAdoptWalkSeen(v, visit, state.values)
	if state.reset() {
		fillMayAdoptSeenPool.Put(state)
	}
}

// fillMayAdoptWalkSeen walks v through the generated typed walker (ast_pointer_walk_gen.go, from
// internal/astwalkgen) when v is an AST pointer or statement/declaration list, and reflectively
// otherwise. Both visit exactly the same pointers in the same order; reflection cost ~7 s plus
// garbage in a stage1 build.
func fillMayAdoptWalkSeen(v reflect.Value, visit func(any), seen map[uintptr]bool) {
	if v.IsValid() && v.CanInterface() && astscan.WalkPointers(v.Interface(), visit, seen) {
		return
	}
	fillMayAdoptWalkReflect(v, visit, seen)
}

// fillMayAdoptWalkReflect is the reflective walker: the specification the generated one follows.
func fillMayAdoptWalkReflect(v reflect.Value, visit func(any), seen map[uintptr]bool) {
	astscan.WalkPointersReflect(v, visit, seen)
}

func fillMayAdoptExportedFields(t reflect.Type) []int {
	return reflectMetaOf(t).exportedFields
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
