package parserdesugar

import (
	"elisacore/src/ast"
	"elisacore/src/astscan"
	"elisacore/src/lexer"
)

// pinnedFoldWidth is the fixed reduction lane count. The fold's tree bracketing is pinned to this
// width in the desugared source (W independent strict accumulators + a fixed strict pairwise combine),
// so the rounded result is bit-identical across every target and optimization level — the loop
// vectorizer may pack the W independent lanes into a SIMD register, but it can never reorder them
// (no reassociation flag rides on these ops), so the bits do not depend on its choices.
const pinnedFoldWidth = 8

// FoldTransformUsesAccumulator conservatively refuses transforms that read the
// running accumulator: independent lanes cannot preserve that dependency.
func FoldTransformUsesAccumulator(expr ast.Expr, name string) bool {
	dependent := false
	astscan.WalkPointers(expr, func(node any) {
		if ident, ok := node.(*ast.Ident); ok && ident != nil && ident.Name == name {
			dependent = true
		}
	}, map[uintptr]bool{})
	return dependent
}

// buildPinnedFoldReduce lowers an eligible fold to a fixed-width pinned reduction (docs/79 Part IV,
// Phase 2). Eligibility mirrors `by par` (no head bindings / filter / range; a plain darray source; an
// associative top-level op with the accumulator as one whole operand) plus a numeric-literal seed (so
// the per-lane identity is synthesizable) and a clonable per-element transform. The second return is
// false when any condition fails; the caller then falls back to the reassociating scalar loop. Unlike
// `by par` an ineligible shape is NOT an error — pinning is a best-effort reproducibility upgrade over
// the always-correct scalar default.
func BuildPinnedFoldReduce(pos lexer.Pos, bindings []ast.Stmt, body ast.Expr, name string, accName string, accType ast.TypeExpr, source ast.Expr, rangeEnd ast.Expr, filter ast.Expr, accInit ast.Expr) (ast.Expr, bool) {
	if len(bindings) != 0 || filter != nil || rangeEnd != nil {
		return nil, false
	}
	srcIdent, ok := source.(*ast.Ident)
	if !ok {
		return nil, false
	}
	bin, ok := body.(*ast.BinaryExpr)
	if !ok || (bin.Op != lexer.TOKEN_PLUS && bin.Op != lexer.TOKEN_STAR) {
		return nil, false
	}
	var transformExpr ast.Expr
	switch {
	case pinnedIdentName(bin.Left) == accName:
		transformExpr = bin.Right
	case pinnedIdentName(bin.Right) == accName:
		transformExpr = bin.Left
	default:
		return nil, false
	}
	if FoldTransformUsesAccumulator(transformExpr, accName) {
		return nil, false
	}
	// The per-lane identity must be a typed literal we can synthesize; only a bare numeric-literal seed
	// tells us the kind (int vs float) without type information at parse time.
	isMul := bin.Op == lexer.TOKEN_STAR
	isFloat := false
	switch accInit.(type) {
	case *ast.FloatLit:
		isFloat = true
	case *ast.IntLit:
		isFloat = false
	default:
		return nil, false
	}
	// The transform is spliced once per lane; it must be deep-clonable so the lanes never share a node.
	if ast.CloneExpr(transformExpr) == nil {
		return nil, false
	}

	id := func(s string) ast.Expr { return &ast.Ident{Position: pos, Name: s} }
	ilit := func(v string) ast.Expr { return &ast.IntLit{Position: pos, Value: v} }
	op := func(l, r ast.Expr) ast.Expr { return &ast.BinaryExpr{Position: pos, Op: bin.Op, Left: l, Right: r} }
	srcAt := func(ix ast.Expr) ast.Expr { return &ast.IndexExpr{Position: pos, Object: id(srcIdent.Name), Index: ix} }
	transform := func() ast.Expr { return ast.CloneExpr(transformExpr) }
	assign := func(target, val ast.Expr) ast.Stmt {
		return &ast.AssignStmt{Position: pos, Target: target, Value: val}
	}
	identityLit := func() ast.Expr {
		v := "0"
		if isMul {
			v = "1"
		}
		if isFloat {
			return &ast.FloatLit{Position: pos, Value: v + ".0"}
		}
		return ilit(v)
	}

	const (
		nName = "__fold_n"
		iName = "__fold_i"
	)
	laneNames := []string{"__fold_a0", "__fold_a1", "__fold_a2", "__fold_a3", "__fold_a4", "__fold_a5", "__fold_a6", "__fold_a7"}
	wLit := func() ast.Expr { return ilit("8") }

	// --- n >= W branch: W independent strict lanes, unrolled by W, then a strict pairwise combine. ---
	then := []ast.Stmt{
		// Reusable element binding (mutable). Valid to seed from src[0] because this branch needs n >= W.
		&ast.VarDeclStmt{Position: pos, Name: name, Mutable: true, Value: srcAt(ilit("0"))},
	}
	for j := 0; j < pinnedFoldWidth; j++ {
		seed := identityLit()
		if j == 0 {
			seed = ast.CloneExpr(accInit) // lane 0 carries the seed; the combine folds it in exactly once
		}
		then = append(then, &ast.VarDeclStmt{Position: pos, Name: laneNames[j], Mutable: true, Value: seed})
	}
	then = append(then, &ast.VarDeclStmt{Position: pos, Name: iName, Mutable: true, Type: &ast.NamedType{Position: pos, Name: "usize"}, Value: ilit("0")})

	mainBody := []ast.Stmt{}
	for j := 0; j < pinnedFoldWidth; j++ {
		// element <- src[i + j]; laneJ <- laneJ <op> transform(element)
		offset := ast.Expr(id(iName))
		if j != 0 {
			offset = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: id(iName), Right: ilit(itoa(j))}
		}
		mainBody = append(mainBody,
			assign(id(name), srcAt(offset)),
			assign(id(laneNames[j]), op(id(laneNames[j]), transform())),
		)
	}
	mainBody = append(mainBody, assign(id(iName), &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: id(iName), Right: wLit()}))
	// while i + W <= n
	then = append(then, &ast.WhileStmt{
		Position: pos,
		Cond:     &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_LTEQ, Left: &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: id(iName), Right: wLit()}, Right: id(nName)},
		Body:     mainBody,
	})
	// remainder: while i < n  { element <- src[i]; lane0 <- lane0 <op> transform; i <- i + 1 }
	then = append(then, &ast.WhileStmt{
		Position: pos,
		Cond:     &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_LT, Left: id(iName), Right: id(nName)},
		Body: []ast.Stmt{
			assign(id(name), srcAt(id(iName))),
			assign(id(laneNames[0]), op(id(laneNames[0]), transform())),
			assign(id(iName), &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: id(iName), Right: ilit("1")}),
		},
	})
	// Strict pairwise combine: ((a0 a1)(a2 a3)) op ((a4 a5)(a6 a7)) — fixed bracketing, no reassociation.
	tree := op(
		op(op(id(laneNames[0]), id(laneNames[1])), op(id(laneNames[2]), id(laneNames[3]))),
		op(op(id(laneNames[4]), id(laneNames[5])), op(id(laneNames[6]), id(laneNames[7]))),
	)
	then = append(then, assign(id(accName), tree))

	// --- n < W branch: strict scalar fold into the (already seeded) result; reproducible at small n. ---
	els := []ast.Stmt{
		&ast.IterForStmt{
			Position: pos,
			Pattern:  &ast.MoveBindNamePattern{Position: pos, Name: name},
			Mode:     ast.IterBindValue,
			Source:   id(srcIdent.Name),
			Body:     []ast.Stmt{assign(id(accName), op(id(accName), transform()))},
		},
	}

	block := &ast.ExprBlock{
		Position: pos,
		Stmts: []ast.Stmt{
			&ast.VarDeclStmt{Position: pos, Name: nName, Value: &ast.FieldExpr{Position: pos, Object: id(srcIdent.Name), Field: "count"}},
			&ast.VarDeclStmt{Position: pos, Name: accName, Mutable: true, Type: accType, Value: ast.CloneExpr(accInit)},
			&ast.IfStmt{
				Position: pos,
				Cond:     &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_GTEQ, Left: id(nName), Right: wLit()},
				Then:     then,
				Else:     els,
			},
		},
		Value: id(accName),
	}
	return block, true
}

// itoa renders a small non-negative int as a decimal string (lane offsets 0..7), avoiding an fmt
// import in this parser file.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func pinnedIdentName(expr ast.Expr) string {
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}
