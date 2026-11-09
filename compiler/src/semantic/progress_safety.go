package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"sort"
	"strings"
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
			// Prefer propagation (`can`) over firewalling (`trusted`): push the unsafe
			// assumption upward until a boundary where propagating no longer helps. Warning
			// under -Wprogress (observe); hard error under full -Wstrict (enforce).
			msg := "while loop has no progress evidence; prove termination with `decreases <measure>`, add a Progress.Tick/Progress.Yield budget check, or — if progress is real but unprovable — `can Unsafe.AssumeProgress` to propagate the assumption upward (reach for `trusted` only at the boundary where propagation no longer helps); for a deliberately non-terminating loop, `can Unsafe.NonProgress`"
			if a.enforceUnsafePermissions {
				a.errorf(obligation.Pos, "progress error: %s", msg)
			} else {
				a.warnf(obligation.Pos, "progress warning: %s", msg)
			}
		}
	}
}

func (a *Analyzer) recordProgressLoopObligation(stmt *ast.WhileStmt) int {
	if a == nil || stmt == nil || a.currentProgressSummary == nil {
		return -1
	}
	nonProgress := a.currentTrustedNonProgressDepth > 0 || a.currentGrantedNonProgressDepth > 0
	assumeProgress := a.currentTrustedAssumeProgressDepth > 0 || a.currentGrantedAssumeProgressDepth > 0
	discharged := nonProgress || assumeProgress
	if nonProgress {
		a.currentProgressSummary.HasUnsafeNonProgress = true
	}
	if assumeProgress {
		a.currentProgressSummary.HasProgressEvidence = true
	}
	// A `decreases <measure>` clause proves termination — the strongest possible progress
	// evidence — so it discharges the progress obligation too (the termination prover and this
	// checker are otherwise separate systems). `decreases *` (the audited give-up) carries no
	// measure and so does not discharge here. A recognized counting-loop shape (docs/102 slice 3)
	// also discharges without an annotation.
	if !discharged && (len(leadingDecreases(stmt.Body)) > 0 || isCountingLoop(stmt)) {
		discharged = true
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

// isCountingLoop recognizes the canonical terminating shape — `while v </<=/>/>= bound:` with a
// monotone step `v <- v + c` / `v += c` (or `- c` / `-= c`) among the loop's direct statements, c a
// positive integer literal, moving v toward the bound, with v and the bound not otherwise reassigned
// at the top level. Such loops obviously make progress, so they discharge the progress obligation
// without an explicit `decreases` (docs/102 slice 3). Conservative by design: an unrecognized shape
// just keeps the warning (add `decreases` / `can Unsafe.AssumeProgress`); a wrong discharge only drops
// a warning, never soundness — termination is not memory safety, and `-Wstrict`'s explicit `decreases`
// is the sound fallback.
func isCountingLoop(stmt *ast.WhileStmt) bool {
	if stmt == nil {
		return false
	}
	cond, ok := stmt.Cond.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	lhs, ok := cond.Left.(*ast.Ident)
	if !ok {
		return false
	}
	v := lhs.Name
	var wantIncrease bool
	switch cond.Op {
	case lexer.TOKEN_LT, lexer.TOKEN_LTEQ:
		wantIncrease = true
	case lexer.TOKEN_GT, lexer.TOKEN_GTEQ:
		wantIncrease = false
	default:
		return false
	}
	boundName := identNameOf(cond.Right) // "" when the bound is not a bare identifier (e.g. a literal)
	hasStep := false
	for _, s := range stmt.Body {
		var tgt string
		var isStep bool
		switch n := s.(type) {
		case *ast.AssignStmt:
			tgt = identNameOf(n.Target)
			isStep = assignIsMonotoneStep(n.Value, v, wantIncrease)
		case *ast.AugAssignStmt:
			tgt = identNameOf(n.Target)
			isStep = augIsMonotoneStep(n.Op, n.Value, wantIncrease)
		default:
			continue
		}
		if tgt == v {
			if !isStep {
				return false // v reassigned non-monotonically at the top level
			}
			hasStep = true
		} else if boundName != "" && tgt == boundName {
			return false // bound is not loop-invariant
		}
	}
	return hasStep
}

func identNameOf(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func assignIsMonotoneStep(value ast.Expr, v string, wantIncrease bool) bool {
	be, ok := value.(*ast.BinaryExpr)
	if !ok || identNameOf(be.Left) != v {
		return false
	}
	switch be.Op {
	case lexer.TOKEN_PLUS:
		return wantIncrease && isPositiveIntLit(be.Right)
	case lexer.TOKEN_MINUS:
		return !wantIncrease && isPositiveIntLit(be.Right)
	}
	return false
}

func augIsMonotoneStep(op lexer.TokenKind, value ast.Expr, wantIncrease bool) bool {
	switch op {
	case lexer.TOKEN_PLUSEQ:
		return wantIncrease && isPositiveIntLit(value)
	case lexer.TOKEN_MINUSEQ:
		return !wantIncrease && isPositiveIntLit(value)
	}
	return false
}

func isPositiveIntLit(e ast.Expr) bool {
	lit, ok := e.(*ast.IntLit)
	if !ok {
		return false
	}
	val := strings.TrimSpace(lit.Value)
	if val == "" {
		return false
	}
	// IntLit values are unsigned magnitudes (negatives are UnaryExpr); any non-zero literal is
	// positive. Accept if a non-zero digit appears (handles hex, underscores, leading zeros).
	for _, r := range strings.TrimPrefix(strings.ToLower(val), "0x") {
		if r >= '1' && r <= '9' || r >= 'a' && r <= 'f' {
			return true
		}
	}
	return false
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
	for _, fn := range sortedFuncDeclKeys(namesByDecl) { // map: source order for a stable report order
		name := namesByDecl[fn]
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
		cycle := strings.Join(names, " -> ")
		rmsg := "recursive cycle %s has no progress evidence; prove it with `decreases <measure>` or structural recursion, add Progress.EnterRecursion/Progress.Tick budget checks, or — if non-progress is intended — `can Unsafe.NonProgress` to propagate it upward (reach for `trusted` only at the boundary where propagation no longer helps)"
		if a.enforceUnsafePermissions {
			a.errorf(component[0].Pos(), "progress error: "+rmsg, cycle)
		} else {
			a.warnf(component[0].Pos(), "progress warning: "+rmsg, cycle)
		}
	}
}
