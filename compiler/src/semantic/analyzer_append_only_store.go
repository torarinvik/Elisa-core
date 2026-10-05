package semantic

// `@append_only` store structs: an ENFORCED, not trusted, append-only byte store. Twin of
// stage1 src/semantic/check_append_only_store*.elisa; the rules and detail strings match.
//
//   declaration  the struct sits in a module and has exactly two private fields, a
//                `darray[T]` buffer and a `darray[darray[T]]` retired list.
//   in-module    only `make_room` and `append` may mention the buffer fields; a store literal
//                takes empty buffers only; no pattern may open a store.
//   make_room    may read `.count`/`.capacity` and do exactly one thing to the buffers:
//                `retired.push(current)`, then `current <- []`, then `current.reserve(n)`.
//   append       may read `.count`/`.capacity`, `current.push(x)` and
//                `bytes_view_range(current, a, b)`; nothing else.
//   everywhere   a store value may only be passed to a function of the store's own module
//                (whose store parameters must be references), have its address taken, or
//                bind a reference. Reassignment, copies, `move`, method calls, iteration and
//                nesting the type in another type are rejected.
//
// Every expression is walked with a SLOT: 0 nothing store-shaped, 1 a store PLACE by
// reference, 2 a FRESH store value. Nodes without a rule are walked generically (reflection)
// at slot 0, so a node kind this file never heard of is checked conservatively, not skipped.

import (
	"maps"
	"reflect"
	"slices"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type aoStore struct {
	module, current, retired string
}

type aoFacts struct {
	stores      map[string]*aoStore
	moduleFuncs map[string]bool // namespace + "\x00" + name
	returning   map[string]string
	fieldStore  map[string]string
}

type aoEnv struct {
	module string
	store  string // the store owned by the enclosing module, or ""
	kind   int    // 0 any, 1 make_room, 2 append
	locals map[string]string
}

func aoLast(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

func (a *Analyzer) aoReport(pos lexer.Pos, store, detail string) {
	a.errorf(pos, "append-only store %q: %s", store, detail)
}

func (f *aoFacts) typeHead(t ast.TypeExpr) string {
	switch tt := t.(type) {
	case *ast.NamedType:
		if _, ok := f.stores[aoLast(tt.Name)]; ok {
			return aoLast(tt.Name)
		}
	case *ast.RefType:
		return f.typeHead(tt.Elem)
	case *ast.MutableType:
		return f.typeHead(tt.Elem)
	case *ast.RefinementTypeExpr:
		return f.typeHead(tt.Base)
	case *ast.WhereRefinementTypeExpr:
		return f.typeHead(tt.Base)
	}
	return ""
}

func aoTypeIsRef(t ast.TypeExpr) bool {
	switch tt := t.(type) {
	case *ast.RefType:
		return true
	case *ast.MutableType:
		return aoTypeIsRef(tt.Elem)
	}
	return false
}

// A store named anywhere below the head of a type (`darray[S]`, `S?`, `(S, u8)`).
func (f *aoFacts) typeNests(t ast.TypeExpr, atHead bool) bool {
	if t == nil || reflect.ValueOf(t).IsNil() {
		return false
	}
	switch tt := t.(type) {
	case *ast.NamedType:
		_, ok := f.stores[aoLast(tt.Name)]
		return ok && !atHead
	case *ast.RefType:
		return f.typeNests(tt.Elem, atHead)
	case *ast.MutableType:
		return f.typeNests(tt.Elem, atHead)
	case *ast.RefinementTypeExpr:
		return f.typeNests(tt.Base, atHead)
	case *ast.WhereRefinementTypeExpr:
		return f.typeNests(tt.Base, atHead)
	}
	found := false
	aoEachChild(reflect.ValueOf(t), func(child interface{}) {
		if ct, ok := child.(ast.TypeExpr); ok && !found {
			found = f.typeNests(ct, false)
		}
	})
	return found
}

// `darray[...]` nesting depth through `mutable`; -1 for anything else.
func aoDarrayDepth(t ast.TypeExpr) int {
	switch tt := t.(type) {
	case *ast.MutableType:
		return aoDarrayDepth(tt.Elem)
	case *ast.BuiltinTypeExpr:
		if tt.Name != "darray" || len(tt.TypeArgs) != 1 {
			return -1
		}
		if inner := aoDarrayDepth(tt.TypeArgs[0]); inner >= 0 {
			return inner + 1
		}
		return 1
	}
	return -1
}

// aoEachChild hands every AST node directly reachable from v (through fields, slices and
// plain structs such as MatchArm/ElifClause) to visit. Analyzer-filled caches (Resolved*,
// Lowered*, AsOverlayCall, CollectionAppend) are skipped: they restate the source tree.
func aoEachChild(v reflect.Value, visit func(interface{})) {
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		if field.PkgPath != "" || strings.HasPrefix(field.Name, "Resolved") || strings.HasPrefix(field.Name, "Lowered") || field.Name == "AsOverlayCall" || field.Name == "CollectionAppend" {
			continue
		}
		aoVisitValue(v.Field(i), visit)
	}
}

func aoVisitValue(fv reflect.Value, visit func(interface{})) {
	switch fv.Kind() {
	case reflect.Interface, reflect.Ptr:
		if fv.IsNil() {
			return
		}
		if _, ok := fv.Interface().(ast.Node); ok {
			visit(fv.Interface())
			return
		}
		aoEachChild(fv, visit)
	case reflect.Slice, reflect.Array:
		for j := 0; j < fv.Len(); j++ {
			aoVisitValue(fv.Index(j), visit)
		}
	case reflect.Struct:
		if fv.CanAddr() {
			if n, ok := fv.Addr().Interface().(ast.Node); ok {
				visit(n)
				return
			}
		}
		aoEachChild(fv, visit)
	}
}

func (a *Analyzer) checkAppendOnlyStores(decls []scopedDecl) {
	facts := &aoFacts{stores: map[string]*aoStore{}, moduleFuncs: map[string]bool{}, returning: map[string]string{}, fieldStore: map[string]string{}}
	for _, scoped := range decls {
		st, ok := scoped.Decl.(*ast.StructDecl)
		if !ok || !annotationsHave(st.Annotations, "append_only") {
			continue
		}
		if scoped.Namespace == "" {
			a.aoReport(st.Position, st.Name, "must be declared inside a module")
		}
		store := &aoStore{module: scoped.Namespace}
		shapeOK := len(st.Fields) == 2
		for _, member := range st.Fields {
			depth := aoDarrayDepth(member.Type)
			if !member.Private {
				shapeOK = false
			}
			if depth == 1 && store.current == "" {
				store.current = member.Name
			} else if depth == 2 && store.retired == "" {
				store.retired = member.Name
			} else {
				shapeOK = false
			}
		}
		if !shapeOK || store.current == "" || store.retired == "" {
			a.aoReport(st.Position, st.Name, "must have exactly two private fields: a darray buffer and a darray of retired buffers")
		}
		facts.stores[st.Name] = store
	}
	if len(facts.stores) == 0 {
		return
	}
	owners := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(facts.stores)) { // map: sort so a module with two stores names the same owner every run
		store := facts.stores[name]
		if store.module != "" {
			owners[store.module] = name
		}
	}
	for _, scoped := range decls {
		switch d := scoped.Decl.(type) {
		case *ast.FuncDecl:
			if _, ok := owners[scoped.Namespace]; ok {
				facts.moduleFuncs[scoped.Namespace+"\x00"+d.Name] = true
			}
			if d.ReturnType != nil && !reflect.ValueOf(d.ReturnType).IsNil() {
				if head := facts.typeHead(d.ReturnType); head != "" && !aoTypeIsRef(d.ReturnType) {
					facts.returning[d.Name] = head
				}
			}
		case *ast.StructDecl:
			for _, member := range d.Fields {
				if head := facts.typeHead(member.Type); head != "" {
					facts.fieldStore[member.Name] = head
				}
			}
		}
	}
	for _, scoped := range decls {
		switch d := scoped.Decl.(type) {
		case *ast.FuncDecl:
			a.aoCheckFunc(facts, scoped.Namespace, owners[scoped.Namespace], d)
		case *ast.StructDecl:
			for _, member := range d.Fields {
				if facts.typeNests(member.Type, true) {
					a.aoReport(d.Position, member.Name, "may not be nested inside another type")
				}
			}
		}
	}
}

func (a *Analyzer) aoCheckFunc(facts *aoFacts, namespace, owned string, d *ast.FuncDecl) {
	env := &aoEnv{module: namespace, store: owned, locals: map[string]string{}}
	if owned != "" {
		switch d.Name {
		case "make_room":
			env.kind = 1
		case "append":
			env.kind = 2
		}
	}
	for _, param := range d.Params {
		if facts.typeNests(param.Type, true) {
			a.aoReport(d.Position, param.Name, "may not be nested inside another type")
		}
		if head := facts.typeHead(param.Type); head != "" {
			if !aoTypeIsRef(param.Type) {
				a.aoReport(d.Position, head, "parameters must be references")
			}
			env.locals[param.Name] = head
		}
	}
	if d.ReturnType != nil && !reflect.ValueOf(d.ReturnType).IsNil() && facts.typeNests(d.ReturnType, true) {
		a.aoReport(d.Position, d.Name, "may not be nested inside another type")
	}
	if env.kind == 1 {
		a.aoMakeRoom(facts, env, d.Body, d.Position)
		return
	}
	for _, stmt := range d.Body {
		a.aoStmt(facts, env, stmt)
	}
}
