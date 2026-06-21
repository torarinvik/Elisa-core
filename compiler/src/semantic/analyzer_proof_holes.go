package semantic

import (
	"sort"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
)

// docs/98 — proof holes + missing-fact suggestions.
//
// When an obligation cannot be discharged, instead of dumping a raw SMT counterexample we print a
// CONSTRUCTIVE diagnostic assembled from facts the analyzer already tracks: the GOAL to prove, the
// KNOWN FACTS currently in scope (interval `rangeFacts` + boolean `smtAssertFacts`), and a heuristic
// SUGGESTED missing invariant/precondition. This is the ergonomics multiplier that turns "feeding the
// solver" into "programming with an assistant".

// proofHoleReport renders the structured goal / known-facts / suggestion block for an unprovable
// `goal` evaluated against the current scope's facts. The text is deterministic (facts sorted) so it
// is stable across runs and testable.
func (a *Analyzer) proofHoleReport(header string, goal ast.Expr) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n  goal:        ")
	b.WriteString(unparse.FormatExpr(goal))

	facts := a.inScopeKnownFacts()
	b.WriteString("\n  known facts:")
	if len(facts) == 0 {
		b.WriteString(" (none in scope)")
	} else {
		for _, f := range facts {
			b.WriteString("\n    - ")
			b.WriteString(f)
		}
	}

	if sug := a.suggestMissingFact(goal, facts); sug != "" {
		b.WriteString("\n  suggested:   ")
		b.WriteString(sug)
	}
	return b.String()
}

func (a *Analyzer) proofHolePlaceholderReport(header string) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n  goal:        ?")

	facts := a.inScopeKnownFacts()
	b.WriteString("\n  known facts:")
	if len(facts) == 0 {
		b.WriteString(" (none in scope)")
	} else {
		for _, f := range facts {
			b.WriteString("\n    - ")
			b.WriteString(f)
		}
	}
	return b.String()
}

func (a *Analyzer) emitAssertHole(pos lexer.Pos) {
	if a == nil {
		return
	}
	a.warnf(pos, "%s", a.proofHolePlaceholderReport("proof hole: explicit assertion hole"))
}

// inScopeKnownFacts collects the analyzer's current hypothesis set as readable strings: each known
// interval bound from `rangeFacts` (closer scopes shadow outer) plus each boolean `smtAssertFacts`
// entry. De-duplicated and sorted for determinism.
func (a *Analyzer) inScopeKnownFacts() []string {
	if a == nil || a.currentScope == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}

	boundSeen := map[string]bool{}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		names := make([]string, 0, len(sc.rangeFacts))
		for name := range sc.rangeFacts {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if boundSeen[name] {
				continue // a closer scope's interval shadows the outer one
			}
			boundSeen[name] = true
			r := sc.rangeFacts[name]
			if r.loKnown {
				add(name + " >= " + intToStr(r.lo))
			}
			if r.hiKnown {
				add(name + " <= " + intToStr(r.hi))
			}
		}
	}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for _, fact := range sc.smtAssertFacts {
			if fact.Expr == nil {
				continue
			}
			add(unparse.FormatExpr(fact.Expr))
		}
	}
	sort.Strings(out)
	return out
}

type proofHoleSuggestion struct {
	rank int
	text string
}

// suggestMissingFact applies proof-hole heuristics: cite a lemma/law whose postcondition has the goal
// shape, show a two-hop transitive comparison chain, or fall back to the missing-bound increment-1
// hints. Advisory only — a wrong guess never affects soundness.
func (a *Analyzer) suggestMissingFact(goal ast.Expr, facts []string) string {
	bin, ok := stripOptimizationParens(goal).(*ast.BinaryExpr)
	if !ok {
		return ""
	}
	left := unparse.FormatExpr(bin.Left)
	right := unparse.FormatExpr(bin.Right)

	var candidates []proofHoleSuggestion
	candidates = append(candidates, a.lemmaShapedSuggestions(goal)...)
	if chain := transitiveFactSuggestion(bin, facts); chain != "" {
		candidates = append(candidates, proofHoleSuggestion{rank: 20, text: chain})
	}

	switch bin.Op {
	case lexer.TOKEN_LT, lexer.TOKEN_LTEQ:
		// Goal `a < b` / `a <= b`: the upper end of `a` is unbounded relative to `b`. If no known
		// fact already relates them, the missing invariant/precondition IS the goal.
		if !factsRelate(facts, left, right) {
			inv := left + " " + opString(bin.Op) + " " + right
			if a.loopDepth > 0 {
				candidates = append(candidates, proofHoleSuggestion{rank: 90, text: "no fact bounds `" + left + "` above; add a loop invariant `" + inv +
					"` or a precondition `requires " + inv + "`"})
			} else {
				candidates = append(candidates, proofHoleSuggestion{rank: 90, text: "no fact bounds `" + left + "` above; add a precondition `requires " + inv + "`"})
			}
		}
	case lexer.TOKEN_GT, lexer.TOKEN_GTEQ:
		// Goal `a > b` / `a >= b`: missing lower bound on `a`.
		if !factsRelate(facts, left, right) {
			inv := left + " " + opString(bin.Op) + " " + right
			if a.loopDepth > 0 {
				candidates = append(candidates, proofHoleSuggestion{rank: 90, text: "no fact bounds `" + left + "` below; establish `" + inv +
					"` before the loop or add a precondition `requires " + inv + "`"})
			} else {
				candidates = append(candidates, proofHoleSuggestion{rank: 90, text: "no fact bounds `" + left + "` below; add a precondition `requires " + inv + "`"})
			}
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	ce := parseSimpleCounterexample(a.lastSMTCounterexample)
	filtered := candidates[:0]
	for _, c := range candidates {
		if !suggestionContradictsCounterexample(c.text, ce) {
			filtered = append(filtered, c)
		}
	}
	if len(filtered) == 0 {
		return ""
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].rank != filtered[j].rank {
			return filtered[i].rank < filtered[j].rank
		}
		return filtered[i].text < filtered[j].text
	})
	return filtered[0].text
}

func (a *Analyzer) lemmaShapedSuggestions(goal ast.Expr) []proofHoleSuggestion {
	if a == nil || a.globalScope == nil || goal == nil {
		return nil
	}
	goalText := unparse.FormatExpr(goal)
	var out []proofHoleSuggestion
	seen := map[string]bool{}
	for _, decl := range a.lemmaAndLawDecls() {
		for _, expr := range lemmaSuggestionClauses(decl) {
			args, ok := matchLemmaShape(expr, goal, decl.Params)
			if !ok {
				continue
			}
			call := decl.Name + "(" + strings.Join(args, ", ") + ")"
			text := "goal matches `" + goalText + "` from `" + decl.Name + "`; add citation `" + call + "`"
			if !seen[text] {
				seen[text] = true
				out = append(out, proofHoleSuggestion{rank: 10, text: text})
			}
		}
	}
	return out
}

func (a *Analyzer) lemmaAndLawDecls() []*ast.FuncDecl {
	decls := map[*ast.FuncDecl]bool{}
	for _, sc := range []*Scope{a.currentScope, a.globalScope} {
		for ; sc != nil; sc = sc.Parent {
			for _, sym := range sc.Symbols {
				for s := sym; s != nil; s = s.AliasOf {
					if decl, ok := s.Node.(*ast.FuncDecl); ok && decl != nil && (decl.IsLemma || decl.IsLaw) {
						decls[decl] = true
					}
				}
			}
		}
	}
	out := make([]*ast.FuncDecl, 0, len(decls))
	for decl := range decls {
		out = append(out, decl)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func lemmaSuggestionClauses(decl *ast.FuncDecl) []ast.Expr {
	if decl == nil {
		return nil
	}
	out := append([]ast.Expr(nil), decl.EnsureValues...)
	if decl.IsLaw {
		for _, stmt := range decl.Body {
			if ret, ok := stmt.(*ast.ReturnStmt); ok && ret.Value != nil {
				out = append(out, ret.Value)
			}
		}
	}
	return out
}

func matchLemmaShape(pattern, goal ast.Expr, params []ast.ParamDecl) ([]string, bool) {
	bindings := map[string]string{}
	paramNames := map[string]bool{}
	for _, p := range params {
		paramNames[p.Name] = true
	}
	if !matchLemmaExpr(pattern, goal, paramNames, bindings) {
		return nil, false
	}
	args := make([]string, len(params))
	for i, p := range params {
		arg, ok := bindings[p.Name]
		if !ok {
			return nil, false
		}
		args[i] = arg
	}
	return args, true
}

func matchLemmaExpr(pattern, goal ast.Expr, params map[string]bool, bindings map[string]string) bool {
	if id, ok := stripOptimizationParens(pattern).(*ast.Ident); ok && params[id.Name] {
		value := unparse.FormatExpr(goal)
		if existing, seen := bindings[id.Name]; seen {
			return existing == value
		}
		bindings[id.Name] = value
		return true
	}
	switch p := stripOptimizationParens(pattern).(type) {
	case *ast.BinaryExpr:
		g, ok := stripOptimizationParens(goal).(*ast.BinaryExpr)
		return ok && p.Op == g.Op &&
			matchLemmaExpr(p.Left, g.Left, params, bindings) &&
			matchLemmaExpr(p.Right, g.Right, params, bindings)
	case *ast.UnaryExpr:
		g, ok := stripOptimizationParens(goal).(*ast.UnaryExpr)
		return ok && p.Op == g.Op && matchLemmaExpr(p.Operand, g.Operand, params, bindings)
	case *ast.CallExpr:
		g, ok := stripOptimizationParens(goal).(*ast.CallExpr)
		if !ok || len(p.Args) != len(g.Args) || !matchLemmaExpr(p.Func, g.Func, params, bindings) {
			return false
		}
		for i := range p.Args {
			if !matchLemmaExpr(p.Args[i], g.Args[i], params, bindings) {
				return false
			}
		}
		return true
	default:
		return unparse.FormatExpr(pattern) == unparse.FormatExpr(goal)
	}
}

type comparisonFact struct {
	left, op, right string
}

func transitiveFactSuggestion(goal *ast.BinaryExpr, facts []string) string {
	if goal == nil || !isForwardComparison(goal.Op) {
		return ""
	}
	left := unparse.FormatExpr(goal.Left)
	right := unparse.FormatExpr(goal.Right)
	var comps []comparisonFact
	for _, fact := range facts {
		if c, ok := parseComparisonFact(fact); ok && isForwardOp(c.op) {
			comps = append(comps, c)
		}
	}
	for _, first := range comps {
		if first.left != left {
			continue
		}
		for _, second := range comps {
			if second.left == first.right && second.right == right {
				return "chain `" + first.left + " " + first.op + " " + first.right + "`, `" +
					second.left + " " + second.op + " " + second.right + "` => `" +
					left + " " + opString(goal.Op) + " " + right + "`"
			}
		}
	}
	return ""
}

// factsRelate reports whether any known fact mentions both sides of the goal comparison — a cheap
// proxy for "the prover already has something connecting these two terms" so we do not suggest a fact
// that is in some form already present.
func factsRelate(facts []string, left, right string) bool {
	for _, f := range facts {
		if strings.Contains(f, left) && strings.Contains(f, right) {
			return true
		}
	}
	return false
}

func parseComparisonFact(s string) (comparisonFact, bool) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "("), ")"))
	}
	fields := strings.Fields(s)
	if len(fields) != 3 || !isForwardOp(fields[1]) {
		return comparisonFact{}, false
	}
	return comparisonFact{left: fields[0], op: fields[1], right: fields[2]}, true
}

func isForwardComparison(op lexer.TokenKind) bool {
	return op == lexer.TOKEN_LT || op == lexer.TOKEN_LTEQ
}

func isForwardOp(op string) bool {
	return op == "<" || op == "<="
}

func parseSimpleCounterexample(ce string) map[string]int64 {
	out := map[string]int64{}
	for _, part := range strings.Split(ce, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			continue
		}
		out[strings.TrimSpace(name)] = v
	}
	return out
}

func suggestionContradictsCounterexample(text string, ce map[string]int64) bool {
	if len(ce) == 0 {
		return false
	}
	start := strings.Index(text, "`")
	for start >= 0 {
		rest := text[start+1:]
		endRel := strings.Index(rest, "`")
		if endRel < 0 {
			return false
		}
		chunk := rest[:endRel]
		if cmp, ok := parseComparisonFact(chunk); ok {
			if contradictsSimpleCounterexample(cmp, ce) {
				return true
			}
		}
		next := rest[endRel+1:]
		nextStart := strings.Index(next, "`")
		if nextStart < 0 {
			return false
		}
		start += endRel + 2 + nextStart
	}
	return false
}

func contradictsSimpleCounterexample(c comparisonFact, ce map[string]int64) bool {
	left, ok := simpleTermValue(c.left, ce)
	if !ok {
		return false
	}
	right, ok := simpleTermValue(c.right, ce)
	if !ok {
		return false
	}
	switch c.op {
	case "<":
		return !(left < right)
	case "<=":
		return !(left <= right)
	case ">":
		return !(left > right)
	case ">=":
		return !(left >= right)
	}
	return false
}

func simpleTermValue(term string, ce map[string]int64) (int64, bool) {
	if v, ok := ce[term]; ok {
		return v, true
	}
	v, err := strconv.ParseInt(term, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func opString(op lexer.TokenKind) string {
	switch op {
	case lexer.TOKEN_LT:
		return "<"
	case lexer.TOKEN_LTEQ:
		return "<="
	case lexer.TOKEN_GT:
		return ">"
	case lexer.TOKEN_GTEQ:
		return ">="
	}
	return "?"
}

func intToStr(v int64) string {
	return strconv.FormatInt(v, 10)
}

// checkStrictAssertProofHole offers a constructive proof-hole HINT for a plain `assert(cond)` under
// strict proofs (docs/98) whose integer-comparison goal the static tiers cannot discharge. Outside
// `-strict` an assert is a debug runtime check, so this is a no-op there. The discharge ladder mirrors
// `assert … by:`: linear tier, then the SMT tier when enabled. When neither closes the goal the assert
// REMAINS a runtime check (ProofRuntime) and the constructive diagnostic is emitted as a non-fatal
// warning — never a hard error, so it cannot reject previously-valid code.
//
// Increment 1 deliberately scopes the trigger to integer-comparison goals (`a < b`, `a >= b`, …): this
// is the canonical unbounded-index obligation the heuristics target, and it keeps the new error off
// asserts the static tiers were never meant to decide (pointer/bool/struct shapes), so no in-tree
// strict fixture regresses on an assert the prover legitimately cannot model.
func (a *Analyzer) checkStrictAssertProofHole(pos lexer.Pos, cond ast.Expr) {
	// Gated on BOTH strict proofs and the SMT tier (docs/90/98). The SMT discharge path is the only
	// in-tree prover that consumes the analyzer's FULL in-scope hypothesis set (flow facts + asserts +
	// intervals); the linear `proveRequiresClause` tier is built for callee-requires substitution, not
	// in-body asserts, so it would reject facts a strict author legitimately established. Requiring
	// `-smt` keeps the prove-it-or-fail bar SOUND (no false "unprovable" on a genuinely entailed assert)
	// and leaves plain `-strict` builds without z3 exactly as they were: asserts are runtime checks.
	// Opt-in only: the proof-hole assistant is something the user INVOKES (docs/98), not an always-on
	// diagnostic. Without this gate the hint would fire on every strict+smt compile of a runtime-checked
	// stdlib assert (e.g. stores_packed_aos.elisa), flooding normal builds. Reserved for the future
	// `assert ?` / `--explain-hole` surface; tests opt in via AnalyzeOptions{EmitProofHoleHints: true}.
	if a == nil || !a.emitProofHoleHints || !a.enforceStrictProofs || !a.smtEnabled || cond == nil {
		return
	}
	if !isIntComparisonGoal(cond) {
		return
	}
	if a.proveRequiresClause(cond, nil) == requiresProven {
		a.recordProof(pos, "assert", "assert", ProofProvenLinear)
		return
	}
	if proven, counterexample := a.trySMTProveRequires(cond, nil); proven {
		a.recordProof(pos, "assert", "assert", ProofProvenSMT)
		return
	} else {
		a.lastSMTCounterexample = counterexample
	}
	// A plain `assert` is a leaf/debug runtime check, NOT a load-bearing prove-or-fail obligation
	// (docs/98; the three-way fallback discussion). So the assert stays a runtime check (ProofRuntime,
	// recorded above) and the constructive proof-hole report is a NON-FATAL hint, never a hard error —
	// it must not reject previously-valid code (e.g. stdlib asserts that are genuine runtime invariants).
	// The hard prove-or-fail bar is reserved for an explicit `assert ?` hole (a later increment).
	a.warnf(pos, "%s", a.proofHoleReport("proof hole: assertion could not be proven", cond))
}

// isIntComparisonGoal reports whether `cond` is a top-level integer relational comparison — the goal
// shape the increment-1 proof-hole engine reasons about.
func isIntComparisonGoal(cond ast.Expr) bool {
	bin, ok := stripOptimizationParens(cond).(*ast.BinaryExpr)
	if !ok {
		return false
	}
	switch bin.Op {
	case lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_GT, lexer.TOKEN_GTEQ:
		return true
	}
	return false
}
