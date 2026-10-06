package semantic

import (
	"reflect"
	"sort"
	"strings"

	"elisacore/src/ast"
)

// Per-callee field-write summaries for whole-struct `mutable T&` arguments.
//
// A view into one field's buffer (`parser.module_name_storage`, reached through a helper's
// "into" return origin) is an address into that field's current generation. Handing the
// whole struct to a callee by `mutable T&` ends the view only when the callee can write
// THAT field: grow, clear or reassign it. Without a summary every such call ends every
// field view, which ties views of one side buffer to every unrelated parser step.
//
// The summary is a syntactic MAY-write over-approximation for one (callee, parameter):
//   - an assignment, a declaration value or an address-of whose place is rooted at the
//     parameter writes its first field (the bare parameter writes everything);
//   - a builtin method on a rooted receiver writes its first field unless the method is in
//     the read-only list;
//   - a rooted argument handed to a resolved user callee follows that callee's summary for a
//     bare parameter, writes its first field for a field path, and writes nothing when the
//     callee's parameter is an immutable `T&`; an unresolved callee writes as above;
//   - any other use of the bare parameter (a copy, an alias binding, a rebind or a shadowing
//     declaration) writes everything.
// Recursion is a least fixpoint solved in rounds (storageViewWriteSolve); a call the summary
// cannot map to its callee's parameters is treated as writing what it is handed.

type storageViewCalleeWrites struct {
	All    bool
	Fields map[string]bool
}

type storageViewCalleeWriteKey struct {
	decl  *ast.FuncDecl
	param int
}

const storageViewCalleeWriteDepthLimit = 48

// storageViewReadOnlyBuiltinMethods never grow, clear or reassign their receiver.
var storageViewReadOnlyBuiltinMethods = map[string]bool{
	"count": true, "len": true, "get": true, "contains": true, "is_empty": true, "as_sview": true,
	"slice": true, "starts_with": true, "ends_with": true, "find": true, "index_of": true,
	"first": true, "last": true, "capacity": true, "has": true, "keys": true, "values": true,
	"usize": true, "i64": true, "i32": true, "u8": true, "u32": true, "u64": true, "to_string": true,
}

func (w *storageViewCalleeWrites) write(field string) {
	if field == "" {
		w.All = true
		return
	}
	if w.Fields == nil {
		w.Fields = map[string]bool{}
	}
	w.Fields[field] = true
}

func (w *storageViewCalleeWrites) union(other storageViewCalleeWrites) {
	if other.All {
		w.All = true
	}
	for field := range other.Fields {
		w.write(field)
	}
}

// storageViewRootedField reports whether EXPR is a place rooted at the identifier NAME, and
// the first field directly above that identifier ("" for the bare identifier or an index
// straight on it, both of which are treated as the whole parameter).
func storageViewRootedField(expr ast.Expr, name string) (string, *ast.Ident, bool) {
	field := ""
	current := stripOptimizationParens(expr)
	if addr, ok := current.(*ast.AddrOfExpr); ok && addr != nil {
		current = stripOptimizationParens(addr.Operand)
	}
	for depth := 0; depth < 256 && current != nil; depth++ {
		switch node := current.(type) {
		case *ast.Ident:
			if node == nil || node.Name != name {
				return "", nil, false
			}
			return field, node, true
		case *ast.FieldExpr:
			if node == nil {
				return "", nil, false
			}
			field = node.Field
			current = stripOptimizationParens(node.Object)
		case *ast.IndexExpr:
			if node == nil {
				return "", nil, false
			}
			field = ""
			current = stripOptimizationParens(node.Object)
		default:
			return "", nil, false
		}
	}
	return "", nil, false
}

// storageViewCalleeParamReadOnly: an immutable `T&` parameter cannot write the caller's place.
// Everything else (mutable, lmut, by-value, unknown) is treated as writing.
func storageViewCalleeParamReadOnly(param ast.ParamDecl) bool {
	if param.Mutable {
		return false
	}
	_, isRef := param.Type.(*ast.RefType)
	return isRef
}

func (a *Analyzer) storageViewCalleeWritesFor(decls []*ast.FuncDecl, param int) storageViewCalleeWrites {
	solve := &storageViewWriteSolve{prov: map[storageViewCalleeWriteKey]storageViewCalleeWrites{}}
	var out storageViewCalleeWrites
	for round := 0; ; round++ {
		solve.visited = map[storageViewCalleeWriteKey]bool{}
		solve.changed = false
		out = storageViewCalleeWrites{}
		for _, decl := range decls {
			inner, _ := a.storageViewCalleeWritesForDecl(decl, param, solve, 0)
			out.union(inner)
		}
		if !solve.changed || round > 4096 {
			if solve.changed {
				return storageViewCalleeWrites{All: true}
			}
			break
		}
	}
	if a.storageViewCalleeWriteCache == nil {
		a.storageViewCalleeWriteCache = map[storageViewCalleeWriteKey]storageViewCalleeWrites{}
	}
	for key, writes := range solve.prov {
		a.storageViewCalleeWriteCache[key] = writes
	}
	return out
}

// storageViewWriteSolve is one round-robin least-fixpoint solve: every (callee, parameter) key
// is evaluated at most once per round, a key met again in the same round (a cycle, or a shared
// callee) answers with its value so far, and rounds repeat until no key grows. Summaries only
// grow (union), so this terminates, and the final round's values are the least fixpoint.
type storageViewWriteSolve struct {
	prov    map[storageViewCalleeWriteKey]storageViewCalleeWrites
	visited map[storageViewCalleeWriteKey]bool
	changed bool
	bodies  map[*ast.FuncDecl]storageViewSolveBody
}

// storageViewSolveBody is a callee body's reflect-walk node list and local names (read-only).
type storageViewSolveBody struct {
	nodes  []any
	locals map[string]bool
}

func (w storageViewCalleeWrites) covers(other storageViewCalleeWrites) bool {
	if w.All {
		return true
	}
	if other.All {
		return false
	}
	for field := range other.Fields {
		if !w.Fields[field] {
			return false
		}
	}
	return true
}

func (a *Analyzer) storageViewCalleeWritesForDecl(decl *ast.FuncDecl, param int, solve *storageViewWriteSolve, depth int) (storageViewCalleeWrites, bool) {
	if decl == nil || param < 0 || param >= len(decl.Params) || depth > storageViewCalleeWriteDepthLimit {
		return storageViewCalleeWrites{All: true}, true
	}
	if storageViewCalleeParamReadOnly(decl.Params[param]) {
		return storageViewCalleeWrites{}, true
	}
	key := storageViewCalleeWriteKey{decl: decl, param: param}
	if cached, ok := a.storageViewCalleeWriteCache[key]; ok {
		return cached, true
	}
	if solve.visited[key] {
		return solve.prov[key], true
	}
	solve.visited[key] = true
	name := decl.Params[param].Name
	writes := storageViewCalleeWrites{}
	complete := true
	if decl.Body == nil {
		// A bodiless (extern/abstract) callee: nothing to read, assume it writes everything.
		writes.All = true
	}
	// A body's node list and local names depend only on the decl; walk it once per solve, not
	// once per (parameter, round).
	body, cached := solve.bodies[decl]
	if !cached {
		fillMayAdoptWalk(reflect.ValueOf(decl.Body), func(node any) { body.nodes = append(body.nodes, node) })
		body.locals = storageViewDeclLocalNames(decl, body.nodes)
		if solve.bodies == nil {
			solve.bodies = map[*ast.FuncDecl]storageViewSolveBody{}
		}
		solve.bodies[decl] = body
	}
	nodes, locals := body.nodes, body.locals
	handled := map[*ast.Ident]bool{}
	markRooted := func(expr ast.Expr, asWrite bool) {
		if expr == nil {
			return
		}
		if field, ident, ok := storageViewRootedField(expr, name); ok {
			handled[ident] = true
			if asWrite {
				writes.write(field)
			}
		}
	}
	for _, node := range nodes {
		if writes.All {
			break
		}
		switch n := node.(type) {
		case *ast.CallExpr:
			if n == nil {
				continue
			}
			pairs, resolved := a.storageViewWriteCallees(n, locals)
			if resolved {
				for _, pair := range pairs {
					for index, arg := range pair.args {
						field, ident, ok := storageViewRootedField(arg, name)
						if !ok {
							continue
						}
						handled[ident] = true
						if field != "" {
							if index >= len(pair.decl.Params) || !storageViewCalleeParamReadOnly(pair.decl.Params[index]) {
								writes.write(field)
							}
							continue
						}
						inner, innerComplete := a.storageViewCalleeWritesForDecl(pair.decl, index, solve, depth+1)
						complete = complete && innerComplete
						writes.union(inner)
					}
				}
				continue
			}
			if method, isMethod := n.Func.(*ast.FieldExpr); isMethod && method != nil {
				markRooted(method.Object, !storageViewReadOnlyBuiltinMethods[method.Field])
			}
			callArgs := n.Args
			if n.ResolvedArgsValid {
				callArgs = n.ResolvedArgs
			}
			for _, arg := range callArgs {
				markRooted(arg, true)
			}
		case *ast.AddrOfExpr:
			if n != nil {
				markRooted(n.Operand, true)
			}
		case *ast.FieldExpr:
			// A read of a field path; the root identifier is accounted for here.
			if n != nil {
				markRooted(n, false)
			}
		case *ast.IndexExpr:
			if n != nil {
				markRooted(n, false)
			}
		}
		if stmt, isStmt := node.(ast.Stmt); isStmt && stmt != nil {
			threaded := a.storageViewLmutThreadedStmt(stmt, name, locals)
			a.storageViewCalleeWriteStmtFields(stmt, name, &writes, markRooted, threaded)
		}
	}
	if !writes.All {
		for _, node := range nodes {
			if ident, isIdent := node.(*ast.Ident); isIdent && ident != nil && ident.Name == name && !handled[ident] {
				writes.All = true
				break
			}
		}
	}
	old := solve.prov[key]
	if !old.covers(writes) {
		solve.changed = true
	}
	merged := storageViewCalleeWrites{}
	merged.union(old)
	merged.union(writes)
	solve.prov[key] = merged
	return merged, complete
}

// storageViewCalleeWriteStmtFields reads a statement's place-bearing fields generically:
// Target/Targets/Value/Values/Init that are rooted places write their first field, and a
// statement that (re)binds or shadows the parameter's name writes everything.
func (a *Analyzer) storageViewCalleeWriteStmtFields(stmt ast.Stmt, name string, writes *storageViewCalleeWrites, markRooted func(ast.Expr, bool), threaded bool) {
	v := reflect.ValueOf(stmt)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < v.NumField(); i++ {
		fieldInfo := v.Type().Field(i)
		if !fieldInfo.IsExported() {
			continue
		}
		value := v.Field(i)
		switch fieldInfo.Name {
		case "Target", "Targets", "Value", "Values", "Init", "Place":
			isTarget := fieldInfo.Name == "Target" || fieldInfo.Name == "Targets"
			var visitTarget func(expr ast.Expr)
			visitTarget = func(expr ast.Expr) {
				if tuple, isTuple := stripOptimizationParens(expr).(*ast.TupleExpr); isTuple && tuple != nil && isTarget {
					for _, elem := range tuple.Elems {
						visitTarget(elem)
					}
					return
				}
				// The lmut thread target of `p <- f(p)` / `p, x <- p.f()` is the same place
				// written back in place; the call's own summary already covers its writes.
				if isTarget && threaded {
					if ident, isIdent := stripOptimizationParens(expr).(*ast.Ident); isIdent && ident != nil && ident.Name == name {
						markRooted(expr, false)
						return
					}
				}
				markRooted(expr, true)
			}
			forEachStorageViewExpr(value, visitTarget)
		case "Name", "Names", "Binding", "Bindings":
			if storageViewReflectMentionsName(value, name) {
				writes.All = true
			}
		}
		if strings.HasSuffix(fieldInfo.Name, "Name") && fieldInfo.Name != "Name" && value.Kind() == reflect.String && value.String() == name {
			writes.All = true
		}
	}
}

// storageViewLmutThreadedStmt: STMT assigns a single call value that passes the bare parameter
// NAME to an `lmut` parameter of every resolved callee. That is the lmut thread (docs/120): the
// callee mutates the caller's place in place and the assignment names the same place back, so
// the bare target is not a replacement of the whole parameter.
func (a *Analyzer) storageViewLmutThreadedStmt(stmt ast.Stmt, name string, locals map[string]bool) bool {
	var value ast.Expr
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		if s == nil {
			return false
		}
		value = s.Value
	default:
		v := reflect.ValueOf(stmt)
		for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
			if v.IsNil() {
				return false
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			return false
		}
		if _, hasTargets := v.Type().FieldByName("Targets"); !hasTargets {
			return false
		}
		field := v.FieldByName("Value")
		if !field.IsValid() || !field.CanInterface() {
			return false
		}
		expr, ok := field.Interface().(ast.Expr)
		if !ok {
			return false
		}
		value = expr
	}
	call, ok := stripOptimizationParens(value).(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	pairs, resolved := a.storageViewWriteCallees(call, locals)
	if !resolved || len(pairs) == 0 {
		return false
	}
	for _, pair := range pairs {
		found := false
		for index, arg := range pair.args {
			ident, isIdent := stripOptimizationParens(arg).(*ast.Ident)
			if !isIdent || ident == nil || ident.Name != name {
				continue
			}
			if index >= len(pair.decl.Params) {
				return false
			}
			mut, isMut := pair.decl.Params[index].Type.(*ast.MutableType)
			if !isMut || mut == nil || !mut.Linear {
				return false
			}
			found = true
		}
		if !found {
			return false
		}
	}
	return true
}

func forEachStorageViewExpr(value reflect.Value, visit func(ast.Expr)) {
	if !value.IsValid() {
		return
	}
	switch value.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			forEachStorageViewExpr(value.Index(i), visit)
		}
		return
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return
		}
	}
	if value.CanInterface() {
		if expr, ok := value.Interface().(ast.Expr); ok && expr != nil {
			visit(expr)
		}
	}
}

func storageViewReflectMentionsName(value reflect.Value, name string) bool {
	switch value.Kind() {
	case reflect.String:
		return value.String() == name
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if storageViewReflectMentionsName(value.Index(i), name) {
				return true
			}
		}
	}
	return false
}

// storageViewCalleeWriteSpare returns the spare predicate for one mutable-ref argument of
// CALL: a view whose source lies strictly inside ARG (`arg.F…`) survives when no possible
// callee writes field F through that parameter. Nil (spare nothing) when the callee or the
// argument position cannot be resolved.
func (a *Analyzer) storageViewCalleeWriteSpare(call *ast.CallExpr, arg ast.Expr) func(source, candidate string) bool {
	if call == nil || arg == nil {
		return nil
	}
	target := stripOptimizationParens(arg)
	if addr, isAddr := target.(*ast.AddrOfExpr); isAddr && addr != nil {
		target = stripOptimizationParens(addr.Operand)
	}
	argKey := optimizationExprString(target)
	if argKey == "" {
		return nil
	}
	pairs, ok := a.storageViewWriteCallees(call, a.storageViewScopeLocals(call))
	if !ok {
		return nil
	}
	// The argument is located by node identity; a lowering that rebuilt the argument list
	// (resolved/defaulted args, an lmut desugar) is matched by its spelling instead, and every
	// position spelled the same is unioned.
	writes := storageViewCalleeWrites{}
	for _, pair := range pairs {
		var indices []int
		for i, candidate := range pair.args {
			c := stripOptimizationParens(candidate)
			if addr, isAddr := c.(*ast.AddrOfExpr); isAddr && addr != nil {
				c = stripOptimizationParens(addr.Operand)
			}
			if c == target {
				indices = []int{i}
				break
			}
			if optimizationExprString(c) == argKey {
				indices = append(indices, i)
			}
		}
		if len(indices) == 0 {
			return nil
		}
		for _, index := range indices {
			writes.union(a.storageViewCalleeWritesFor([]*ast.FuncDecl{pair.decl}, index))
		}
		if writes.All {
			break
		}
	}
	if writes.All {
		return nil
	}
	return func(source, candidate string) bool {
		if !strings.HasPrefix(source, argKey+".") {
			return false
		}
		rest := source[len(argKey)+1:]
		if cut := strings.IndexAny(rest, ".["); cut >= 0 {
			rest = rest[:cut]
		}
		return rest != "" && !writes.Fields[rest]
	}
}

type storageViewWriteCallee struct {
	decl *ast.FuncDecl
	args []ast.Expr
}

// storageViewWriteCallees resolves CALL for the write summary. A summary is computed for a callee
// whose body may not have been analyzed yet, so the analysis-time resolution
// (storageViewOriginCallee) often fails there; the fallback is every declaration sharing the
// callee's last name segment (the callEdgesOf over-approximation, including bodiless
// declarations, which summarize as writing everything). The union over all of them covers the
// real target. A name bound locally (a parameter or a declared local, possibly a closure) is not
// resolved by name, and neither is a call whose arity does not line up with every candidate.
func (a *Analyzer) storageViewWriteCallees(call *ast.CallExpr, locals map[string]bool) ([]storageViewWriteCallee, bool) {
	if decls, args, ok := a.storageViewOriginCallee(call); ok {
		out := make([]storageViewWriteCallee, 0, len(decls))
		for _, decl := range decls {
			out = append(out, storageViewWriteCallee{decl: decl, args: args})
		}
		return out, true
	}
	args := call.Args
	if call.ResolvedArgsValid {
		args = call.ResolvedArgs
	}
	target := call.Func
	if special, ok := target.(*ast.SpecializeExpr); ok && special != nil {
		target = special.Operand
	}
	var receiver ast.Expr
	name := ""
	switch callee := target.(type) {
	case *ast.Ident:
		if callee == nil || locals[callee.Name] {
			return nil, false
		}
		name = callee.Name
	case *ast.FieldExpr:
		if callee == nil {
			return nil, false
		}
		name = callee.Field
		receiver = callee.Object
	default:
		return nil, false
	}
	candidates := a.storageViewDeclsByLastName()[fillMayAdoptLastSegment(name)]
	if len(candidates) == 0 {
		return nil, false
	}
	// A candidate whose arity cannot accept this call (counting defaulted trailing parameters)
	// cannot be its target and is dropped; a call no candidate accepts is unresolved.
	var out []storageViewWriteCallee
	for _, decl := range candidates {
		required := len(decl.Params)
		for required > 0 && decl.Params[required-1].DefaultValue != nil {
			required--
		}
		fits := func(count int) bool { return count >= required && count <= len(decl.Params) }
		if receiver != nil && fits(len(args)+1) {
			out = append(out, storageViewWriteCallee{decl: decl, args: append([]ast.Expr{receiver}, args...)})
		}
		if fits(len(args)) {
			if receiver != nil {
				// `Type.f(x)` static form; a receiver VALUE here would be an unknown builtin.
				rootIdent, rooted := receiver.(*ast.Ident)
				if !rooted || rootIdent == nil {
					return nil, false
				}
				// A receiver that names a local or parameter is a value: the method form above
				// is the call, never the static one.
				if locals[rootIdent.Name] {
					continue
				}
			}
			out = append(out, storageViewWriteCallee{decl: decl, args: args})
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// storageViewScopeLocals answers the by-name fallback's "is this callee name a local?" at a call
// site under analysis: a name the current scope binds to a parameter or local (a closure value)
// is never resolved to a declaration by name.
func (a *Analyzer) storageViewScopeLocals(call *ast.CallExpr) map[string]bool {
	out := map[string]bool{}
	ident, isIdent := call.Func.(*ast.Ident)
	if !isIdent || ident == nil || a.currentScope == nil {
		return out
	}
	if sym, ok := a.currentScope.Lookup(ident.Name); ok && sym != nil && (sym.Kind == SymbolParam || sym.Kind == SymbolLocal) {
		out[ident.Name] = true
	}
	return out
}

func (a *Analyzer) storageViewDeclsByLastName() map[string][]*ast.FuncDecl {
	if a.storageViewWriteDeclsByName != nil {
		return a.storageViewWriteDeclsByName
	}
	a.storageViewWriteDeclsByName = map[string][]*ast.FuncDecl{}
	for decl := range a.funcDeclSymbols {
		if decl != nil {
			last := fillMayAdoptLastSegment(decl.Name)
			a.storageViewWriteDeclsByName[last] = append(a.storageViewWriteDeclsByName[last], decl)
		}
	}
	for _, decls := range a.storageViewWriteDeclsByName {
		sort.Slice(decls, func(i, j int) bool {
			if decls[i].Position.File != decls[j].Position.File {
				return decls[i].Position.File < decls[j].Position.File
			}
			return decls[i].Position.Offset < decls[j].Position.Offset
		})
	}
	return a.storageViewWriteDeclsByName
}

// storageViewDeclLocalNames: parameter names plus every name a node in the body binds.
func storageViewDeclLocalNames(decl *ast.FuncDecl, nodes []any) map[string]bool {
	out := map[string]bool{}
	for _, param := range decl.Params {
		out[param.Name] = true
	}
	for _, node := range nodes {
		if _, isIdent := node.(*ast.Ident); isIdent {
			continue
		}
		if _, isField := node.(*ast.FieldExpr); isField {
			continue
		}
		v := reflect.ValueOf(node)
		for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
			if v.IsNil() {
				break
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			continue
		}
		for i := 0; i < v.NumField(); i++ {
			info := v.Type().Field(i)
			if !info.IsExported() {
				continue
			}
			if info.Name == "Names" || info.Name == "Binding" || info.Name == "Bindings" || strings.HasSuffix(info.Name, "Name") {
				collectStorageViewStrings(v.Field(i), out)
			}
		}
	}
	return out
}

func collectStorageViewStrings(value reflect.Value, out map[string]bool) {
	switch value.Kind() {
	case reflect.String:
		if value.String() != "" {
			out[value.String()] = true
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			collectStorageViewStrings(value.Index(i), out)
		}
	}
}
