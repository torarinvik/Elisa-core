package semantic

import (
	"fmt"
	"maps"
	"reflect"
	"strings"

	"elisacore/src/ast"
)

// SpecializedFunction is a read-only codegen product. Decl and Result form one
// view: lowering its syntax with the canonical Result would discard facts for
// rewritten expressions. Nominal types and global declaration identity remain
// canonical so module ABI/type caches do not acquire a second identity.
type SpecializedFunction struct {
	Decl          *ast.FuncDecl
	Result        *Result
	originalExprs map[ast.Expr]ast.Expr
}

type specializationContext struct {
	base         *Result
	worker       *Analyzer
	syntax       *specializationSyntax
	declarations map[*ast.FuncDecl]*ast.FuncDecl
	err          error
}

// SpecializeFunction returns nil without reanalysis when no declaration-ordered
// semantic type arguments were supplied. Nonempty enclosing backend bindings
// alone do not request a semantic specialization of this function.
func (r *Result) SpecializeFunction(fn *ast.FuncDecl, args []Type) (*SpecializedFunction, error) {
	if r == nil || fn == nil {
		return nil, fmt.Errorf("specialization requires a semantic result and declaration")
	}
	// Enclosing impl/region bindings are not necessarily semantic arguments for
	// this declaration. Preserve the old type-overlay API's no-work contract.
	if len(args) == 0 {
		return nil, nil
	}
	root := r
	if r.specializationRoot != nil {
		root = r.specializationRoot
	}
	if root.analyzer == nil {
		return nil, fmt.Errorf("specialization requires a canonical semantic analyzer")
	}
	if root.analyzer.optimizationSymbolIdentityOverlay != nil {
		return nil, fmt.Errorf("specialization root is not a canonical analyzer")
	}
	if root.analyzer.noStorageOverlay != nil {
		return nil, fmt.Errorf("specialization root retains construction no-storage overlay")
	}
	if original := r.specializationCanonical[fn]; original != nil {
		fn = original
	}
	if sh := root.shared; sh != nil {
		key, keyed := specializationMemoKey(fn, args)
		sh.mu.Lock()
		defer sh.mu.Unlock()
		if product, ok := sh.bodies[key]; keyed && ok {
			return product, nil
		}
		product, err := root.specializePrivateFunction(fn, args)
		if err == nil && keyed {
			sh.bodies[key] = product
		}
		return product, err
	}
	return root.specializePrivateFunction(fn, args)
}

func (r *Result) specializePrivateFunction(fn *ast.FuncDecl, args []Type) (*SpecializedFunction, error) {
	if r.analyzer.exprTypeOverlay != nil || r.analyzer.exprFactsOverlay != nil || r.analyzer.iterBinderElementOverlay != nil || r.analyzer.noStorageOverlay != nil || r.analyzer.optimizationSymbolIdentityOverlay != nil {
		return nil, fmt.Errorf("specialization root is not a canonical analyzer")
	}
	a := cloneAnalyzerValueForSpecialization(r.analyzer).Addr().Interface().(*Analyzer)
	// Semantic workers use a separate base memo for this table. A published
	// codegen Result exposes a concrete map instead, including canonical global
	// type expressions, so materialize that base before adding private rows.
	a.resolvedTypeNames = maps.Clone(r.analyzer.resolvedTypeNames)
	isolateWorkerScopes(r.analyzer, a)
	targetExprs := map[ast.Expr]struct{}{}
	collectSpecializedFunctionExprs(fn, targetExprs)
	ctx := &specializationContext{base: r, worker: a, syntax: newSpecializationSyntax(), declarations: map[*ast.FuncDecl]*ast.FuncDecl{}}
	a.parallel = &parallelWorkerState{worker: 0, shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, readShadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{}, specialization: ctx}
	if r.analyzer.retained != nil {
		a.retained = &RetainedAnalysis{windows: map[*ast.FuncDecl][2]int{}, rbDeps: map[*ast.FuncDecl]map[*ast.FuncDecl]bool{}}
	}
	defer a.closeSMT()
	decl := ctx.copyDecl(fn)
	if ctx.err != nil {
		return nil, ctx.err
	}
	// A self-call must remain active through its canonical identity as well as
	// its private declaration; foreign summary walks must not re-enter the root.
	if a.funcAnalysisActive == nil {
		a.funcAnalysisActive = map[*ast.FuncDecl]bool{}
	}
	a.funcAnalysisActive[fn] = true
	// Proofs from the template may not authorize a concrete rewritten access.
	// Remove both original and copied target entries so position fallback cannot
	// resurrect the old declaration's detached facts in this private view.
	for expr, owner := range a.indexBoundsProofOwners {
		if owner == fn || owner == decl {
			delete(a.indexBoundsProven, expr)
			delete(a.indexBoundsProofOwners, expr)
			delete(a.indexBoundsProofHeaders, expr)
			delete(a.indexBoundsProofOrigins, expr)
		}
	}
	for original, copied := range ctx.syntax.nodes {
		if old, ok := original.(*ast.IndexExpr); ok {
			delete(a.indexBoundsProven, old)
			delete(a.indexBoundsProven, copied.(*ast.IndexExpr))
		}
	}
	namespace := ""
	// The private declaration has no canonical symbol-table entry: Symbol.Node
	// keeps its immutable program identity. Resolve the declaring namespace from
	// the canonical declaration before entering the private body context. Inside
	// that context, symbolForFuncDecl can resolve the private bare name normally.
	if symbol, ok := a.symbolForFuncDecl(fn); ok && symbol != nil {
		if function, ok := symbol.Type.(*FuncType); ok && function != nil {
			if split := strings.LastIndex(function.Name, "."); split >= 0 {
				namespace = function.Name[:split]
			}
		}
	}
	a.withResolutionContext(namespace, a.funcDeclUsings[decl], func() { a.analyzeFuncWithTypeArgs(decl, args) })
	delete(a.funcAnalysisActive, fn)
	if ctx.err != nil {
		return nil, ctx.err
	}
	// Ordinary semantic failures do not set ctx.err. The specialization clone
	// owns fresh diagnostics and fresh deduplication, so no canonical error can
	// hide a failed private pass or be discarded by canonical-only publication.
	for _, diagnostic := range a.diagnostics {
		if diagnostic.Severity == "" || diagnostic.Severity == DiagnosticSeverityError {
			return nil, fmt.Errorf("cannot specialize %q: %s", fn.Name, diagnostic.String())
		}
	}
	view, err := newSpecializationPublication(ctx, decl).publish()
	if err != nil {
		return nil, err
	}
	originalExprs := map[ast.Expr]ast.Expr{}
	for original, copied := range ctx.syntax.nodes {
		if expr, ok := original.(ast.Expr); ok {
			if _, belongs := targetExprs[expr]; belongs {
				originalExprs[copied.(ast.Expr)] = expr
			}
		}
	}
	return &SpecializedFunction{Decl: decl, Result: view, originalExprs: originalExprs}, nil
}

func (c *specializationContext) copyDecl(fn *ast.FuncDecl) *ast.FuncDecl {
	if original := c.worker.parallel.original[fn]; original != nil {
		fn = original
	}
	if copied := c.declarations[fn]; copied != nil {
		return copied
	}
	syntax := c.syntax
	first := len(syntax.prepared)
	syntax.prepare(reflect.ValueOf(fn))
	// Probe every newly owned key to closure. Fact values can introduce further
	// syntax whose facts live in an earlier table, so map/table order must not
	// determine the private ownership closure.
	for cursor := first; cursor < len(syntax.prepared); cursor++ {
		node := reflect.ValueOf(syntax.prepared[cursor])
		for _, path := range specializationASTMapPaths() {
			source := specializationField(reflect.ValueOf(c.base.analyzer).Elem(), path)
			if source.IsNil() || !node.Type().AssignableTo(source.Type().Key()) {
				continue
			}
			if value := source.MapIndex(node); value.IsValid() {
				c.prepareFact(value, map[specializationPointer]bool{})
			}
		}
	}
	if err := syntax.validate(); err != nil {
		c.err = fmt.Errorf("cannot isolate generic specialization %s: %w", fn.Name, err)
		// Foreign quiet analysis has no error return. Keep even the declined
		// path private, then reject the whole product before any IR consumes it.
		copied := copyASTDecl(fn).(*ast.FuncDecl)
		c.declarations[fn] = copied
		c.worker.parallel.original[copied] = fn
		c.worker.rekeyFuncDeclEntries(fn, copied, false)
		return copied
	}
	copied := syntax.copy(reflect.ValueOf(fn)).Interface().(*ast.FuncDecl)
	// Fact-held synthetic syntax must have private identities before the first
	// node-keyed table is rekeyed, irrespective of Analyzer field order.
	for _, node := range syntax.prepared[first:] {
		syntax.copy(reflect.ValueOf(node))
	}
	c.declarations[fn] = copied
	for original, private := range syntax.nodes {
		if decl, ok := original.(*ast.FuncDecl); ok {
			c.worker.parallel.original[private.(*ast.FuncDecl)] = decl
			c.declarations[decl] = private.(*ast.FuncDecl)
		}
	}
	c.worker.rekeyFuncDeclEntries(fn, copied, false)
	for _, original := range syntax.prepared[first:] {
		private := syntax.nodes[original]
		if private == nil {
			continue
		}
		originalKey, privateKey := reflect.ValueOf(original), reflect.ValueOf(private)
		for _, path := range specializationASTMapPaths() {
			source := specializationField(reflect.ValueOf(c.base.analyzer).Elem(), path)
			if source.IsNil() || !originalKey.Type().AssignableTo(source.Type().Key()) {
				continue
			}
			value := source.MapIndex(originalKey)
			if !value.IsValid() {
				continue
			}
			target := specializationField(reflect.ValueOf(c.worker).Elem(), path)
			if target.IsNil() {
				target.Set(reflect.MakeMap(target.Type()))
			}
			target.SetMapIndex(privateKey, c.copyFact(value, map[specializationPointer]reflect.Value{}))
		}
	}
	return copied
}

func specializationField(root reflect.Value, path []int) reflect.Value {
	for _, i := range path {
		root = settable(root.Field(i))
	}
	return root
}

// Unlike the parallel merge, a private codegen view also needs declaration-keyed
// facts whose values contain declaration references (CFGs, lambda metadata, etc).
func specializationASTMapPaths() [][]int {
	return specializationASTMapPathsOnce()
}

var specializationASTMapPathsOnce = func() func() [][]int {
	var paths [][]int
	var walk func(reflect.Type, []int)
	walk = func(t reflect.Type, prefix []int) {
		for i := 0; i < t.NumField(); i++ {
			typ := t.Field(i).Type
			path := append(append([]int(nil), prefix...), i)
			if typ.Kind() == reflect.Map && typ.Key().Implements(astNodeType) {
				paths = append(paths, path)
			}
			if typ.Kind() == reflect.Struct && typeHoldsMapInline(typ) {
				walk(typ, path)
			}
		}
	}
	walk(reflect.TypeOf(Analyzer{}), nil)
	return func() [][]int { return paths }
}()

func specializationCanonicalLeaf(v reflect.Value) bool {
	if !v.IsValid() || !v.CanInterface() {
		return false
	}
	if _, ok := v.Interface().(Type); ok {
		return true
	}
	switch v.Interface().(type) {
	case *Symbol, *Scope:
		return true
	}
	return false
}

func (c *specializationContext) prepareFact(v reflect.Value, seen map[specializationPointer]bool) {
	if !v.IsValid() || specializationCanonicalLeaf(v) {
		return
	}
	if node, ok := specializationASTNode(v); ok {
		if _, isDecl := node.(ast.Decl); isDecl && !c.syntax.originals[node] {
			return
		}
		c.syntax.prepare(v)
		return
	}
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			c.prepareFact(v.Elem(), seen)
		}
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		key := specializationPointer{v.Type(), v.Pointer()}
		if seen[key] {
			return
		}
		seen[key] = true
		c.prepareFact(settable(v.Elem()), seen)
	case reflect.Struct:
		addressable := reflect.New(v.Type()).Elem()
		addressable.Set(v)
		for i := 0; i < v.NumField(); i++ {
			c.prepareFact(settable(addressable.Field(i)), seen)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			c.prepareFact(v.Index(i), seen)
		}
	case reflect.Map:
		if !v.IsNil() {
			key := specializationPointer{v.Type(), v.Pointer()}
			if seen[key] {
				return
			}
			seen[key] = true
			it := v.MapRange()
			for it.Next() {
				c.prepareFact(it.Key(), seen)
				c.prepareFact(it.Value(), seen)
			}
		}
	}
}

func (c *specializationContext) copyFact(v reflect.Value, memo map[specializationPointer]reflect.Value) reflect.Value {
	if !v.IsValid() || specializationCanonicalLeaf(v) {
		return v
	}
	if node, ok := specializationASTNode(v); ok {
		if private := c.syntax.nodes[node]; private != nil {
			return reflect.ValueOf(private)
		}
		if _, isDecl := node.(ast.Decl); isDecl {
			return v
		}
		return c.syntax.copy(v)
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(c.copyFact(v.Elem(), memo))
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		key := specializationPointer{v.Type(), v.Pointer()}
		if old, ok := memo[key]; ok {
			return old
		}
		out := reflect.New(v.Type().Elem())
		memo[key] = out
		out.Elem().Set(c.copyFact(settable(v.Elem()), memo))
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			settable(out.Field(i)).Set(c.copyFact(settable(out.Field(i)), memo))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(c.copyFact(v.Index(i), memo))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(c.copyFact(v.Index(i), memo))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		key := specializationPointer{v.Type(), v.Pointer()}
		if old, ok := memo[key]; ok {
			return old
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		memo[key] = out
		it := v.MapRange()
		for it.Next() {
			out.SetMapIndex(c.copyFact(it.Key(), memo), c.copyFact(it.Value(), memo))
		}
		return out
	}
	return v
}

func specializationASTNode(v reflect.Value) (ast.Node, bool) {
	if !v.IsValid() || !v.CanInterface() {
		return nil, false
	}
	n, ok := v.Interface().(ast.Node)
	return n, ok
}

func validateSpecializationResultMapProjection() error {
	covered := map[string]bool{"ModuleAliases": true, "NamedTypes": true, "StaticInterfaces": true, "StaticImpls": true, "ConstValues": true, "ExprTypes": true, "PermGrowthOps": true, "RewriteDefaults": true, "OptionalBindSourceTypes": true, "InterfaceMethodRefs": true, "SafeCalls": true, "ExprFacts": true, "CastHooks": true, "InitCalls": true, "PostfixShorthandCalls": true, "RegionStacks": true, "DeathTimeCohorts": true, "DeathTimeEscapeStats": true, "ResolvedTypeNames": true, "ContextualTypeBindings": true, "ResolvedValueNames": true, "DenseNodeKeys": true, "NodeTables": true, "ParallelFor": true, "TaskGroupSubmits": true, "CallArgDisjoint": true, "ReassignReclaims": true, "FuncDisjointParams": true, "LawIsCalls": true, "LemmaCalls": true, "GhostDecls": true, "GhostContracts": true, "RefinementChecks": true, "CallArgRefinementChecks": true, "ReturnRefinementChecks": true, "IndexBoundsProven": true, "Defer": true, "Fold": true, "Lambdas": true, "FunctionAnalyses": true, "ProgressSummaries": true}
	t := reflect.TypeOf(Result{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath == "" && f.Type.Kind() == reflect.Map && !covered[f.Name] {
			return fmt.Errorf("private specialization has no projection for Result.%s", f.Name)
		}
	}
	return nil
}

// Function metadata snapshots normally preserve AST identity. A private
// specialization must detach default/recovery/contract expressions too, because
// use-site cloning can intentionally retain nested recovery/type syntax.
func (c *specializationContext) snapshotFuncType(source, destination *FuncType) reflect.Value {
	root := reflect.ValueOf(source).Elem()
	snapshotFuncTypeMetadataRoot(root, source, destination, func(v reflect.Value) reflect.Value { c.syntax.prepare(v); return v })
	if err := c.syntax.validate(); err != nil {
		c.err = fmt.Errorf("cannot isolate specialization signature syntax: %w", err)
	}
	return snapshotFuncTypeMetadataRoot(root, source, destination, func(v reflect.Value) reflect.Value {
		if c.err != nil {
			return copyASTGraph(v, map[uintptr]reflect.Value{})
		}
		return c.syntax.copy(v)
	})
}
