package semantic

import (
	"sort"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type ProgressObligationKind string

const (
	ProgressObligationLoop      ProgressObligationKind = "Loop"
	ProgressObligationRecursion ProgressObligationKind = "Recursion"
)

type ProgressObligation struct {
	Kind       ProgressObligationKind
	Pos        lexer.Pos
	Discharged bool
	Detail     string
}

type FunctionProgressSummary struct {
	Name                 string
	Decl                 *ast.FuncDecl
	Obligations          []ProgressObligation
	HasProgressEvidence  bool
	HasUnsafeNonProgress bool
	HasBlocking          bool
	HasUnsafeBlockMain   bool
	BlockingPath         []string
	MainThread           bool
}

func (a *Analyzer) beginFunctionProgressSummary(fn *ast.FuncDecl) *FunctionProgressSummary {
	if fn == nil {
		return nil
	}
	summary := &FunctionProgressSummary{Name: fn.Name, Decl: fn, MainThread: annotationsHave(fn.Annotations, "main_thread")}
	if a.progressSummaries != nil {
		a.progressSummaries[fn] = summary
	}
	return summary
}

func (a *Analyzer) finishFunctionProgressSummary(fn *ast.FuncDecl, refs []ast.PermissionRef) {
	if a == nil || fn == nil || a.currentProgressSummary == nil {
		return
	}
	summary := a.currentProgressSummary
	if progressPermissionRefsContainEvidence(refs) {
		summary.HasProgressEvidence = true
	}
	if unsafePermissionRefsContainNonProgress(refs) {
		summary.HasUnsafeNonProgress = true
	}
	if blockingPermissionRefsContainBlocking(refs) {
		summary.HasBlocking = true
	}
	if unsafePermissionRefsContainBlockMain(refs) {
		summary.HasUnsafeBlockMain = true
	}
	if !a.enforceProgressSafety {
		return
	}
	for _, obligation := range summary.Obligations {
		if obligation.Discharged {
			continue
		}
		switch obligation.Kind {
		case ProgressObligationLoop:
			a.warnf(obligation.Pos, "progress warning: while loop has no progress evidence; add a Progress.Tick/Progress.Yield budget check in the loop, or wrap an intentional non-progress loop in trusted Unsafe.NonProgress")
		}
	}
}

func (a *Analyzer) recordProgressLoopObligation(stmt *ast.WhileStmt) int {
	if a == nil || stmt == nil || a.currentProgressSummary == nil {
		return -1
	}
	discharged := a.currentTrustedNonProgressDepth > 0 || a.currentTrustedAssumeProgressDepth > 0
	if a.currentTrustedNonProgressDepth > 0 {
		a.currentProgressSummary.HasUnsafeNonProgress = true
	}
	if a.currentTrustedAssumeProgressDepth > 0 {
		a.currentProgressSummary.HasProgressEvidence = true
	}
	a.currentProgressSummary.Obligations = append(a.currentProgressSummary.Obligations, ProgressObligation{
		Kind:       ProgressObligationLoop,
		Pos:        stmt.Pos(),
		Discharged: discharged,
	})
	return len(a.currentProgressSummary.Obligations) - 1
}

func (a *Analyzer) finishProgressLoopObligation(index int, bodyRefs []ast.PermissionRef) {
	if a == nil || a.currentProgressSummary == nil || index < 0 || index >= len(a.currentProgressSummary.Obligations) {
		return
	}
	if progressPermissionRefsContainEvidence(bodyRefs) || unsafePermissionRefsContainNonProgress(bodyRefs) {
		a.currentProgressSummary.Obligations[index].Discharged = true
	}
}

func permissionRefsContain(refs []ast.PermissionRef, family string, member string) bool {
	for _, ref := range canonicalizePermissionRefs(refs) {
		if ref.Name != family {
			continue
		}
		if member == "" || ref.Member == "" || ref.Member == member {
			return true
		}
	}
	return false
}

func progressPermissionRefsContainEvidence(refs []ast.PermissionRef) bool {
	for _, ref := range canonicalizePermissionRefs(refs) {
		if ref.Name == "Progress" {
			return true
		}
		if ref.Name == "Unsafe" && ref.Member == "AssumeProgress" {
			return true
		}
	}
	return false
}

func unsafePermissionRefsContainNonProgress(refs []ast.PermissionRef) bool {
	return permissionRefsContain(refs, "Unsafe", "NonProgress")
}

func unsafePermissionRefsContainBlockMain(refs []ast.PermissionRef) bool {
	return permissionRefsContain(refs, "Unsafe", "BlockMain")
}

func blockingPermissionRefsContainBlocking(refs []ast.PermissionRef) bool {
	return permissionRefsContain(refs, "Blocking", "")
}

func (a *Analyzer) validateProgressBlocking(decls []scopedDecl) {
	funcs := map[string]*ast.FuncDecl{}
	namesByDecl := map[*ast.FuncDecl]string{}
	for _, scoped := range decls {
		fn, ok := scoped.Decl.(*ast.FuncDecl)
		if !ok || fn == nil {
			continue
		}
		name := joinQualifiedName(scoped.Namespace, fn.Name)
		funcs[name] = fn
		funcs[fn.Name] = fn
		namesByDecl[fn] = name
	}
	for fn, name := range namesByDecl {
		summary := a.progressSummaries[fn]
		if summary == nil || !summary.HasBlocking || summary.HasUnsafeBlockMain {
			continue
		}
		path := a.progressBlockingPath(fn, funcs, map[*ast.FuncDecl]bool{})
		if len(path) == 0 {
			path = []string{name}
		}
		summary.BlockingPath = path
		pathText := strings.Join(path, " -> ")
		if summary.MainThread {
			a.errorf(fn.Pos(), "progress error: @main_thread function may block via Blocking.* permission (path: %s); use a yielding/budgeted wait, move the work off the main thread, or wrap an intentional block in trusted Unsafe.BlockMain", pathText)
		} else {
			a.warnf(fn.Pos(), "progress warning: function may block via Blocking.* permission (path: %s); keep it off main-thread paths, use a yielding/budgeted wait, or wrap an intentional main-thread block in trusted Unsafe.BlockMain", pathText)
		}
	}
}

func (a *Analyzer) progressBlockingPath(fn *ast.FuncDecl, funcs map[string]*ast.FuncDecl, visiting map[*ast.FuncDecl]bool) []string {
	if fn == nil {
		return nil
	}
	if visiting[fn] {
		return nil
	}
	visiting[fn] = true
	defer delete(visiting, fn)
	for _, call := range collectProgressDirectCalls(fn.Body) {
		sym, ok := a.globalScope.Lookup(call)
		if !ok || sym == nil {
			continue
		}
		fnType, _ := sym.Type.(*FuncType)
		if fnType == nil || !blockingPermissionRefsContainBlocking(functionPermissionRefs(fnType)) {
			continue
		}
		if callee := funcs[call]; callee != nil {
			childPath := a.progressBlockingPath(callee, funcs, visiting)
			if len(childPath) != 0 {
				return append([]string{fn.Name}, childPath...)
			}
		}
		return []string{fn.Name, call}
	}
	return []string{fn.Name}
}

func (a *Analyzer) validateProgressRecursion(decls []scopedDecl) {
	funcs := map[string]*ast.FuncDecl{}
	namesByDecl := map[*ast.FuncDecl]string{}
	for _, scoped := range decls {
		fn, ok := scoped.Decl.(*ast.FuncDecl)
		if !ok || fn == nil {
			continue
		}
		name := joinQualifiedName(scoped.Namespace, fn.Name)
		funcs[name] = fn
		funcs[fn.Name] = fn
		namesByDecl[fn] = name
	}
	if len(funcs) == 0 {
		return
	}
	edges := map[*ast.FuncDecl]map[*ast.FuncDecl]bool{}
	for fn := range namesByDecl {
		calls := collectProgressDirectCalls(fn.Body)
		for _, call := range calls {
			callee := funcs[call]
			if callee == nil {
				continue
			}
			if edges[fn] == nil {
				edges[fn] = map[*ast.FuncDecl]bool{}
			}
			edges[fn][callee] = true
		}
	}
	components := progressTarjan(namesByDecl, edges)
	for _, component := range components {
		if len(component) == 0 {
			continue
		}
		recursive := len(component) > 1 || edges[component[0]][component[0]]
		if !recursive {
			continue
		}
		names := make([]string, 0, len(component))
		hasEvidence := false
		for _, fn := range component {
			name := namesByDecl[fn]
			if name == "" {
				name = fn.Name
			}
			names = append(names, name)
			summary := a.progressSummaries[fn]
			if summary != nil {
				summary.Obligations = append(summary.Obligations, ProgressObligation{
					Kind:   ProgressObligationRecursion,
					Pos:    fn.Pos(),
					Detail: name,
				})
				if summary.HasProgressEvidence || summary.HasUnsafeNonProgress {
					hasEvidence = true
				}
			}
			if sym, ok := a.globalScope.Lookup(name); ok {
				if fnType, ok := sym.Type.(*FuncType); ok {
					refs := functionPermissionRefs(fnType)
					if progressPermissionRefsContainEvidence(refs) || unsafePermissionRefsContainNonProgress(refs) {
						hasEvidence = true
					}
				}
			}
		}
		if hasEvidence {
			continue
		}
		sort.Strings(names)
		a.warnf(component[0].Pos(), "progress warning: recursive cycle %s has no progress evidence; add Progress.EnterRecursion/Progress.Tick budget checks, make the recursion structurally bounded, or wrap intentional non-progress recursion in trusted Unsafe.NonProgress", strings.Join(names, " -> "))
	}
}

func collectProgressDirectCalls(stmts []ast.Stmt) []string {
	collector := progressCallCollector{}
	collector.collectStmts(stmts)
	return collector.calls
}

type progressCallCollector struct {
	calls []string
}

func (c *progressCallCollector) collectStmts(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		c.collectStmt(stmt)
	}
}

func (c *progressCallCollector) collectStmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		c.collectExpr(n.Value)
		c.collectExpr(n.Owner)
	case *ast.LetDestructureStmt:
		c.collectExpr(n.Value)
	case *ast.TupleBindStmt:
		c.collectExpr(n.Value)
	case *ast.MoveBindStmt:
		c.collectExpr(n.Value)
		c.collectExpr(n.Store)
	case *ast.AssignStmt:
		c.collectExpr(n.Target)
		c.collectExpr(n.Value)
	case *ast.AugAssignStmt:
		c.collectExpr(n.Target)
		c.collectExpr(n.Value)
	case *ast.AsRefAssignStmt:
		c.collectExpr(n.Target)
		c.collectExpr(n.Value)
	case *ast.DeferStmt:
		c.collectStmts(n.Body)
	case *ast.ReturnStmt:
		c.collectExpr(n.Value)
	case *ast.IfStmt:
		c.collectExpr(n.Cond)
		c.collectStmts(n.Then)
		for _, elif := range n.Elifs {
			c.collectExpr(elif.Cond)
			c.collectStmts(elif.Body)
		}
		c.collectStmts(n.Else)
	case *ast.MatchStmt:
		c.collectExpr(n.Value)
		c.collectExpr(n.Store)
		for _, arm := range n.Arms {
			c.collectStmts(arm.Body)
		}
	case *ast.ExpectPatternStmt:
		c.collectExpr(n.Value)
	case *ast.InStoreStmt:
		c.collectExpr(n.Store)
		c.collectStmts(n.Body)
	case *ast.CanStmt:
		c.collectStmts(n.Body)
	case *ast.WithStmt:
		for _, arg := range n.Args {
			c.collectExpr(arg.Value)
		}
		for _, bundle := range n.Bundles {
			for _, arg := range bundle.Args {
				c.collectExpr(arg.Value)
			}
		}
		c.collectStmts(n.Body)
	case *ast.ArgsScopeStmt:
		for _, arg := range n.Args {
			c.collectExpr(arg.Value)
		}
		for _, pack := range n.ParamPacks {
			for _, arg := range pack.Args {
				c.collectExpr(arg.Value)
			}
		}
		c.collectStmts(n.Body)
	case *ast.PoolStmt:
		c.collectExpr(n.Workers)
		c.collectStmts(n.Body)
	case *ast.LockStmt:
		c.collectExpr(n.Mutex)
		c.collectStmts(n.Body)
	case *ast.WhileStmt:
		c.collectExpr(n.Cond)
		c.collectStmts(n.Body)
	case *ast.ForStmt:
		c.collectExpr(n.Start)
		c.collectExpr(n.End)
		c.collectExpr(n.Step)
		c.collectStmts(n.Body)
	case *ast.IterForStmt:
		c.collectExpr(n.Source)
		c.collectExpr(n.WhereFilter)
		c.collectExpr(n.Filter)
		c.collectStmts(n.Body)
	case *ast.ParallelForStmt:
		c.collectExpr(n.Source)
		c.collectStmts(n.Body)
	case *ast.PanicStmt:
		c.collectExpr(n.Message)
	case *ast.ExprStmt:
		c.collectExpr(n.Expr)
	case *ast.StaticIfStmt:
		c.collectStmts(n.Then)
		for _, elif := range n.Elifs {
			c.collectStmts(elif.Body)
		}
		c.collectStmts(n.Else)
	case *ast.StaticErrorStmt:
		c.collectExpr(n.Message)
	case *ast.StaticAssertStmt:
		c.collectExpr(n.Cond)
		c.collectExpr(n.Message)
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			c.collectExpr(item.Cond)
			c.collectExpr(item.Message)
		}
	case *ast.StaticBlockStmt:
		c.collectStmts(n.Body)
	case *ast.DiscardStmt:
		c.collectExpr(n.Value)
	case *ast.RegionStmt:
		c.collectExpr(n.Capacity)
		c.collectStmts(n.Body)
	case *ast.DestroyStmt:
	case *ast.MarkStmt:
	case *ast.CheckpointStmt:
		c.collectExpr(n.Target)
		c.collectStmts(n.Body)
	case *ast.GroupedCheckpointStmt:
		for _, target := range n.Targets {
			c.collectExpr(target)
		}
		c.collectStmts(n.Body)
	case *ast.RestoreStmt:
	case *ast.RestoreCheckpointStmt:
	case *ast.ResetStmt:
	}
}

func (c *progressCallCollector) collectExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ExprBlock:
		c.collectStmts(n.Stmts)
		c.collectExpr(n.Value)
	case *ast.BinaryExpr:
		c.collectExpr(n.Left)
		c.collectExpr(n.Right)
		if n.LoweredCall != nil {
			c.collectExpr(n.LoweredCall)
		}
	case *ast.UnaryExpr:
		c.collectExpr(n.Operand)
	case *ast.MoveExpr:
		c.collectExpr(n.Operand)
	case *ast.CallExpr:
		if ident, ok := n.Func.(*ast.Ident); ok {
			c.calls = append(c.calls, ident.Name)
		}
		c.collectExpr(n.Func)
		c.collectExpr(n.SafeReceiver)
		for _, arg := range n.Args {
			c.collectExpr(arg)
		}
		for _, arg := range n.WithArgs {
			c.collectExpr(arg.Value)
		}
		for _, bundle := range n.WithBundles {
			for _, arg := range bundle.Args {
				c.collectExpr(arg.Value)
			}
		}
	case *ast.FieldExpr:
		c.collectExpr(n.Object)
	case *ast.IndexExpr:
		c.collectExpr(n.Object)
		c.collectExpr(n.Index)
		c.collectExpr(n.Fallback)
	case *ast.SliceExpr:
		c.collectExpr(n.Object)
		c.collectExpr(n.Start)
		c.collectExpr(n.End)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			c.collectExpr(elem)
		}
		c.collectExpr(n.Owner)
	case *ast.MembershipRangeExpr:
		c.collectExpr(n.Start)
		c.collectExpr(n.End)
	case *ast.ListComprehensionExpr:
		for _, b := range n.Bindings {
			if vd, ok := b.(*ast.VarDeclStmt); ok {
				c.collectExpr(vd.Value)
			}
		}
		c.collectExpr(n.Key)
		c.collectExpr(n.Value)
		c.collectExpr(n.Source)
		c.collectExpr(n.RangeEnd)
		c.collectExpr(n.RangeStep)
		c.collectExpr(n.Filter)
		c.collectExpr(n.Owner)
	case *ast.QueryExpr:
		c.collectExpr(n.Source)
		c.collectExpr(n.Filter)
		c.collectExpr(n.Projection)
		c.collectExpr(n.Owner)
	case *ast.CastExpr:
		c.collectExpr(n.Operand)
	case *ast.TernaryExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Cond)
		c.collectExpr(n.Alt)
	case *ast.AddrOfExpr:
		c.collectExpr(n.Operand)
	case *ast.SpecializeExpr:
		c.collectExpr(n.Operand)
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			c.collectExpr(arg)
		}
		for _, spread := range n.Spreads {
			c.collectExpr(spread)
		}
	case *ast.RecordUpdateExpr:
		c.collectExpr(n.Base)
		for _, arg := range n.Args {
			c.collectExpr(arg)
		}
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			c.collectExpr(elem)
		}
	case *ast.MatchExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Store)
		for _, arm := range n.Arms {
			c.collectStmts(arm.Body)
		}
	case *ast.LambdaExpr:
		c.collectStmts(n.Body)
		c.collectExpr(n.BodyExpr)
	case *ast.TryExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Fallback)
		if n.Recovery != nil {
			c.collectExpr(n.Recovery.Value)
			c.collectStmts(n.Recovery.Body)
		}
	case *ast.CatchExpr:
		c.collectExpr(n.Value)
		c.collectStmts(n.Success.Body)
		for _, arm := range n.Arms {
			c.collectStmts(arm.Body)
		}
	case *ast.UnwrapElseExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Fallback)
		if n.Recovery != nil {
			c.collectExpr(n.Recovery.Value)
			c.collectStmts(n.Recovery.Body)
		}
	case *ast.GetExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Fallback)
		if n.Recovery != nil {
			c.collectExpr(n.Recovery.Value)
			c.collectStmts(n.Recovery.Body)
		}
	case *ast.OptionalBindExpr:
		c.collectExpr(n.Value)
	case *ast.RaiseExpr:
		c.collectExpr(n.Error)
	case *ast.ParenExpr:
		c.collectExpr(n.Inner)
	case *ast.IsAliasExpr:
		c.collectExpr(n.Target)
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			c.collectExpr(target)
		}
	case *ast.AllocExpr:
		c.collectExpr(n.Owner)
		c.collectExpr(n.Value)
		c.collectExpr(n.NodeSpan)
	case *ast.FoldExpr:
		c.collectExpr(n.Value)
		for _, arm := range n.Arms {
			c.collectStmts(arm.Body)
		}
	case *ast.VisitExpr:
		c.collectExpr(n.Value)
		for _, arm := range n.Arms {
			c.collectStmts(arm.Body)
		}
	case *ast.EmitExpr:
		c.collectExpr(n.Value)
	}
}

func progressTarjan(names map[*ast.FuncDecl]string, edges map[*ast.FuncDecl]map[*ast.FuncDecl]bool) [][]*ast.FuncDecl {
	index := 0
	stack := []*ast.FuncDecl{}
	onStack := map[*ast.FuncDecl]bool{}
	indices := map[*ast.FuncDecl]int{}
	lowlink := map[*ast.FuncDecl]int{}
	var components [][]*ast.FuncDecl

	var strongConnect func(*ast.FuncDecl)
	strongConnect = func(v *ast.FuncDecl) {
		index++
		indices[v] = index
		lowlink[v] = index
		stack = append(stack, v)
		onStack[v] = true

		for w := range edges[v] {
			if _, ok := names[w]; !ok {
				continue
			}
			if indices[w] == 0 {
				strongConnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] && indices[w] < lowlink[v] {
				lowlink[v] = indices[w]
			}
		}

		if lowlink[v] != indices[v] {
			return
		}
		var component []*ast.FuncDecl
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			component = append(component, w)
			if w == v {
				break
			}
		}
		components = append(components, component)
	}

	for fn := range names {
		if indices[fn] == 0 {
			strongConnect(fn)
		}
	}
	return components
}
