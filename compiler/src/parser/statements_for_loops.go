package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
)

func (p *Parser) parseParallelForStmt() *ast.ParallelForStmt {
	pos := p.cur().Pos
	p.expectIdentText("parallel")
	p.expectIdentText("for")
	name := p.expect(lexer.TOKEN_IDENT).Text
	indexName := ""
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "at" {
		p.advance()
		indexName = p.expect(lexer.TOKEN_IDENT).Text
	}
	p.expect(lexer.TOKEN_IN)
	source := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.ParallelForStmt{Position: pos, Name: name, IndexName: indexName, Source: source, Body: body}
}
func (p *Parser) parseForStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("for")
	reverse := false
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "rev" && p.hasToken(p.pos+1) {
		nextKind := p.tokenAt(p.pos + 1).Kind
		if nextKind == lexer.TOKEN_IDENT || nextKind == lexer.TOKEN_MUTABLE {
			p.errorf("legacy reverse iterable loop syntax `for rev ... in ...:` is no longer supported; use `for ... in rev(...):` instead")
			reverse = true
			p.advance()
		}
	}
	mode := p.parseIterBindMode()
	pattern := p.parseIterLoopPattern()
	p.expect(lexer.TOKEN_IN)
	startOrSource := p.parseForHeaderExpr()
	if iterSource, sourceReverse := unwrapReverseIterableSource(startOrSource); sourceReverse {
		if reverse {
			p.errorf("reverse iterable loop specified twice; use either `for rev ... in ...:` or `for ... in rev(...):`")
		}
		reverse = true
		startOrSource = iterSource
	}
	if p.peek() == lexer.TOKEN_RANGE || p.peek() == lexer.TOKEN_RANGE_LT || p.peek() == lexer.TOKEN_RANGE_GT || p.peek() == lexer.TOKEN_RANGE_LE {
		namePattern, ok := pattern.(*ast.MoveBindNamePattern)
		if !ok {
			p.errorf("range for loop requires a simple loop name")
			namePattern = &ast.MoveBindNamePattern{Position: pos, Name: "_"}
		}
		if mode != ast.IterBindValue {
			p.errorf("range for loop does not support ref binders")
		}
		op := p.advance()
		if op.Kind == lexer.TOKEN_RANGE {
			// Bare `lo .. hi` as a range is ambiguous about its endpoint (historically inclusive in
			// Elisa, but exclusive in Rust/Python/Swift — a footgun either way). Require an explicit
			// operator. Parsing recovers as the historical inclusive form so a single clear error is
			// reported rather than a cascade.
			p.errorAt(op.Pos, ambiguousBareRangeMsg)
			op.Kind = lexer.TOKEN_RANGE_LE
		}
		end := p.parseForHeaderExpr()
		if op.Kind == lexer.TOKEN_RANGE_LE {
			// Inclusive `lo ..= hi` desugars to the half-open `lo ..< (hi + 1)`, so everything
			// downstream (step, `by par`, the ForStmt lowering) treats it as the existing `..<` form.
			end = &ast.BinaryExpr{Position: op.Pos, Op: lexer.TOKEN_PLUS, Left: end, Right: &ast.IntLit{Position: op.Pos, Value: "1"}}
			op.Kind = lexer.TOKEN_RANGE_LT
		}
		var step ast.Expr
		if p.match(lexer.TOKEN_RANGE) {
			step = p.parseForHeaderExpr()
		} else if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "step" {
			// `for i in lo..<hi step N:` was never syntax — it used to be silently
			// truncated (the loop stepped by 1). Directed error, recover as the
			// canonical range stride `lo..<hi..N` so the loop parses with the
			// intended step and no cascade.
			p.errorf("`step N` is not range syntax; spell the stride on the range: `lo..<hi..N`")
			p.advance()
			step = p.parseForHeaderExpr()
		}
		// Optional `by par` data-parallel marker on a range for-loop: lowers to the runtime
		// `for_indices_par` combinator (disjoint index bands over a structured nursery). `by simd`
		// is rejected -- recognized loop shapes vectorize by default (the -Wperf verifier checks).
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "by" {
			byPos := p.cur().Pos
			p.advance()
			mode := p.expect(lexer.TOKEN_IDENT).Text
			switch mode {
			case "par":
				if reverse {
					p.errorAt(byPos, "`by par` is not supported on a reverse range loop")
				}
				if step != nil {
					p.errorAt(byPos, "`by par` is not supported on a stepped range loop")
				}
				if op.Kind != lexer.TOKEN_RANGE_LT {
					p.errorAt(byPos, "`by par` requires a half-open `..<` range loop")
				}
				body := p.parseForStmtBody()
				return p.buildParallelIndexFor(pos, namePattern.Name, startOrSource, end, body)
			case "simd":
				p.errorAt(byPos, "`by simd` was removed; recognized loop shapes vectorize by default (a non-vectorizable shape warns under -Wperf)")
			default:
				p.errorAt(byPos, "expected `par` after `by`, got %q", mode)
			}
		}
		body, hdr := p.parseForStmtBodyWithHeader()
		return p.wrapLoopHeader(hdr, &ast.ForStmt{Position: pos, Reverse: reverse, Name: namePattern.Name, Start: startOrSource, End: end, Step: step, Op: op.Kind, Body: body})
	}
	var patternFilter ast.MatchPattern
	var patternFilterSubject string
	var whereFilter ast.Expr
	if p.matchIdentText("where") {
		if subject, ok := p.peekForWhereSubjectPattern(pattern); ok {
			patternFilterSubject = subject
			p.expect(lexer.TOKEN_IDENT)
			p.expect(lexer.TOKEN_IS)
			patternFilter = p.parseMatchPattern()
			if p.peek() == lexer.TOKEN_COLON && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind != lexer.TOKEN_NEWLINE {
				p.advance()
				whereFilter = p.parseForHeaderExpr()
			}
		} else if p.peekForWherePatternFilter() {
			patternFilter = p.parseMatchPattern()
			if p.peek() == lexer.TOKEN_COLON && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind != lexer.TOKEN_NEWLINE {
				p.advance()
				whereFilter = p.parseForHeaderExpr()
			}
		} else {
			whereExpr := p.parseForHeaderExpr()
			if p.isForWherePredicateShorthand(whereExpr) {
				startOrSource = &ast.CallExpr{
					Position: pos,
					Func:     &ast.Ident{Position: pos, Name: "where"},
					Args:     []ast.Expr{startOrSource, whereExpr},
				}
			} else {
				whereFilter = whereExpr
			}
		}
	}
	var filter ast.Expr
	if p.match(lexer.TOKEN_IF) {
		filter = p.parseExpr()
	}
	// Optional `by par` data-parallel marker on a collection for-loop: lowers to the same
	// `for_indices_par` disjoint-band machinery as the range form, indexing the source through a
	// thread-shareable Slice (so reading each element is race-free) and binding the loop name to
	// the band's element. `by simd` is rejected; recognized loop shapes vectorize by default.
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "by" {
		byPos := p.cur().Pos
		p.advance()
		marker := p.expect(lexer.TOKEN_IDENT).Text
		switch marker {
		case "par":
			namePattern, ok := pattern.(*ast.MoveBindNamePattern)
			if !ok {
				p.errorAt(byPos, "`by par` collection loop requires a simple loop name")
				namePattern = &ast.MoveBindNamePattern{Position: pos, Name: "_"}
			}
			if reverse {
				p.errorAt(byPos, "`by par` is not supported on a reverse loop")
			}
			if mode != ast.IterBindValue {
				p.errorAt(byPos, "`by par` collection loop does not support ref binders")
			}
			if patternFilter != nil || whereFilter != nil || filter != nil {
				p.errorAt(byPos, "`by par` is not supported on a filtered loop (`where`/`if`)")
			}
			body := p.parseForStmtBody()
			return p.buildParallelCollectionFor(pos, namePattern.Name, startOrSource, body)
		case "simd":
			p.errorAt(byPos, "`by simd` was removed; recognized loop shapes vectorize by default (a non-vectorizable shape warns under -Wperf)")
		default:
			p.errorAt(byPos, "expected `par` after `by`, got %q", marker)
		}
	}
	body, hdr := p.parseForStmtBodyWithHeader()
	// `for x in move c:` is a consuming move-drain. The `move c` source parses as a MoveExpr;
	// unwrap it to the container and flag MovedSource (Mode stays IterBindValue so the binding is
	// by value and backend lowering is reused). Only the value-binding form supports a moved
	// source — `for ref x in move c` / `for mutable x in move c` is contradictory.
	source := startOrSource
	movedSource := false
	if moveExpr, ok := source.(*ast.MoveExpr); ok {
		if mode != ast.IterBindValue {
			p.errorAt(moveExpr.Pos(), "`for ref`/`for mutable` cannot bind a moved source; `move` requires a by-value drain")
		}
		source = moveExpr.Operand
		movedSource = true
	}
	return p.wrapLoopHeader(hdr, &ast.IterForStmt{Position: pos, Reverse: reverse, Pattern: pattern, Mode: mode, Source: source, MovedSource: movedSource, PatternFilter: patternFilter, PatternFilterSubject: patternFilterSubject, WhereFilter: whereFilter, Filter: filter, Body: body})
}

func (p *Parser) peekForWhereSubjectPattern(pattern ast.MoveBindPattern) (string, bool) {
	if p.peek() != lexer.TOKEN_IDENT || !p.hasToken(p.pos+1) || p.tokenAt(p.pos+1).Kind != lexer.TOKEN_IS {
		return "", false
	}
	subject := p.cur().Text
	switch bind := pattern.(type) {
	case *ast.MoveBindNamePattern:
		return subject, bind.Name != "" && bind.Name == subject
	case *ast.MoveBindTuplePattern:
		for _, arg := range bind.Args {
			if arg.Name == subject && subject != "_" {
				return subject, true
			}
		}
	case *ast.MoveBindStructPattern:
		for _, arg := range bind.Args {
			if arg.Name == subject && subject != "_" {
				return subject, true
			}
		}
	}
	return "", false
}

// buildParallelIndexFor lowers `for <name> in <start> ..< <end> by par: <body>` into a call to the
// runtime `for_indices_par(count, lambda(idx) => { ...; 0 })` combinator, which partitions [0,count)
// into disjoint index bands on a structured nursery. Race-safety is the same as the Slice-band path:
// each global index is visited exactly once, so writing one's own `out[i]` slot cannot collide.
// Sharing a non-thread-shareable value (e.g. a darray) into the body is still rejected at the
// lambda/nursery transfer boundary. When the range starts at integer 0 the loop name binds the band
// index directly; otherwise the name is rebound to `start + idx` inside the body.
func (p *Parser) buildParallelIndexFor(pos lexer.Pos, name string, start ast.Expr, end ast.Expr, body []ast.Stmt) ast.Stmt {
	startIsZero := false
	if lit, ok := start.(*ast.IntLit); ok && lit.Value == "0" {
		startIsZero = true
	}
	usizeT := &ast.NamedType{Position: pos, Name: "usize"}
	idxName := name
	var lambdaBody []ast.Stmt
	if !startIsZero {
		idxName = "__by_par_off"
		// name: usize = start + __by_par_off
		lambdaBody = append(lambdaBody, &ast.VarDeclStmt{
			Position: pos, Name: name, Type: usizeT,
			Value: &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS,
				Left: start, Right: &ast.Ident{Position: pos, Name: idxName}},
		})
	}
	lambdaBody = append(lambdaBody, body...)
	lambdaBody = append(lambdaBody, &ast.ReturnStmt{Position: pos, Value: &ast.IntLit{Position: pos, Value: "0"}})
	lambda := &ast.LambdaExpr{
		Position: pos, Keyword: "lambda", ParallelBody: true,
		Params:     []ast.ParamDecl{{Position: pos, Name: idxName, Type: usizeT}},
		ReturnType: &ast.NamedType{Position: pos, Name: "i64"},
		Body:       lambdaBody,
	}
	var count ast.Expr = end
	if !startIsZero {
		count = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_MINUS, Left: end, Right: start}
	}
	call := &ast.CallExpr{Position: pos,
		Func: &ast.Ident{Position: pos, Name: "for_indices_par"},
		Args: []ast.Expr{count, lambda}}
	return &ast.ExprStmt{Position: pos, Expr: call}
}

// buildParallelCollectionFor lowers `for <name> in <src> by par: <body>` over a darray (or Slice)
// `src` onto the same disjoint-band machinery as the range form. It first borrows the source's index
// space as a thread-shareable Slice (`__by_par_src = as_slice(src)`), then runs
// `for_indices_par(__by_par_src.len(), lambda(idx) => { <name> = __by_par_src.get(idx); ...; 0 })`.
// Each global index is visited exactly once across the bands, so reading `src[idx]` is race-free; the
// Slice is all-scalar so capturing it for the read is thread-safe. Capturing a non-partitioned
// darray for OUTPUT is still rejected by the parallel-body data-race guard. A `[]Stmt{decl, expr}`
// scope is returned so `__by_par_src` is hoisted once before the parallel region.
func (p *Parser) buildParallelCollectionFor(pos lexer.Pos, name string, src ast.Expr, body []ast.Stmt) ast.Stmt {
	usizeT := &ast.NamedType{Position: pos, Name: "usize"}
	p.byParSeq++
	srcName := fmt.Sprintf("__by_par_src_%d", p.byParSeq)
	srcDecl := &ast.VarDeclStmt{
		Position: pos, Name: srcName,
		Value: &ast.CallExpr{Position: pos,
			Func: &ast.Ident{Position: pos, Name: "as_slice"},
			Args: []ast.Expr{src}},
	}
	idxName := "__by_par_idx"
	var lambdaBody []ast.Stmt
	// <name> = __by_par_src.get(__by_par_idx)
	lambdaBody = append(lambdaBody, &ast.VarDeclStmt{
		Position: pos, Name: name,
		Value: &ast.CallExpr{Position: pos,
			Func: &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: srcName}, Field: "get"},
			Args: []ast.Expr{&ast.Ident{Position: pos, Name: idxName}}},
	})
	lambdaBody = append(lambdaBody, body...)
	lambdaBody = append(lambdaBody, &ast.ReturnStmt{Position: pos, Value: &ast.IntLit{Position: pos, Value: "0"}})
	lambda := &ast.LambdaExpr{
		Position: pos, Keyword: "lambda", ParallelBody: true,
		Params:     []ast.ParamDecl{{Position: pos, Name: idxName, Type: usizeT}},
		ReturnType: &ast.NamedType{Position: pos, Name: "i64"},
		Body:       lambdaBody,
	}
	count := &ast.CallExpr{Position: pos,
		Func: &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: srcName}, Field: "len"},
		Args: nil}
	call := &ast.CallExpr{Position: pos,
		Func: &ast.Ident{Position: pos, Name: "for_indices_par"},
		Args: []ast.Expr{count, lambda}}
	// The `__by_par_src` Slice decl must precede the parallel loop; emit it first and buffer the
	// loop call as a pending statement so both land flat in the enclosing block (the parser drains
	// pendingStmts right after this statement, preserving decl-then-loop order).
	p.pendingStmts = append(p.pendingStmts, &ast.ExprStmt{Position: pos, Expr: call})
	return srcDecl
}

func (p *Parser) parseForStmtBody() []ast.Stmt {
	body, hdr := p.parseForStmtBodyWithHeader()
	if hdr != nil {
		p.errorAt(hdr.pos, "loop-header decls are not supported on this loop form")
	}
	return body
}

// parseForStmtBodyWithHeader parses an optional docs/119 §3 loop header
// (`|name = init, ...| [-> yield]`) before the loop's `:` and body.
func (p *Parser) parseForStmtBodyWithHeader() ([]ast.Stmt, *loopHeader) {
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "for" {
		return []ast.Stmt{p.parseForStmt()}, nil
	}
	var hdr *loopHeader
	if p.loopHeaderDeclsAhead() {
		hdr = p.parseLoopHeader()
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.loopBreakTargets = append(p.loopBreakTargets, loopBreakTarget(hdr))
	body := p.parseBlock()
	p.loopBreakTargets = p.loopBreakTargets[:len(p.loopBreakTargets)-1]
	return body, hdr
}

func (p *Parser) peekForWherePatternFilter() bool {
	switch p.peek() {
	case lexer.TOKEN_DOT, lexer.TOKEN_CARET, lexer.TOKEN_STRING_LIT, lexer.TOKEN_INT_LIT, lexer.TOKEN_FLOAT_LIT,
		lexer.TOKEN_HEX_LIT, lexer.TOKEN_CHAR_LIT, lexer.TOKEN_TRUE, lexer.TOKEN_FALSE, lexer.TOKEN_NULL,
		lexer.TOKEN_MINUS, lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
		return true
	case lexer.TOKEN_IDENT:
		if p.cur().Text == "_" {
			return true
		}
		if !p.hasToken(p.pos + 1) {
			return false
		}
		next := p.tokenAt(p.pos + 1).Kind
		if next == lexer.TOKEN_LPAREN || next == lexer.TOKEN_LBRACE {
			return forWhereIdentLooksLikePatternType(p.cur().Text)
		}
		if next == lexer.TOKEN_DOT || next == lexer.TOKEN_SCOPE {
			index := p.pos + 1
			for p.hasToken(index+1) && (p.tokenAt(index).Kind == lexer.TOKEN_DOT || p.tokenAt(index).Kind == lexer.TOKEN_SCOPE) && p.tokenAt(index+1).Kind == lexer.TOKEN_IDENT {
				index += 2
			}
			if !p.hasToken(index) {
				return false
			}
			switch p.tokenAt(index).Kind {
			case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACE:
				// `M::Enum.Variant(...)` / `M::Point(...)` are patterns; a pure `::` chain
				// ending in a lowercase name (`Lexer::text_or_empty(...)`) is a CALL.
				return p.whereChainLooksLikePattern(p.pos, index)
			case lexer.TOKEN_COLON:
				return forWhereIdentLooksLikePatternType(p.cur().Text)
			case lexer.TOKEN_IDENT:
				return p.tokenAt(index).Text == "for" && forWhereIdentLooksLikePatternType(p.cur().Text)
			default:
				return false
			}
		}
		return false
	default:
		return false
	}
}

func (p *Parser) isForWherePredicateShorthand(expr ast.Expr) bool {
	_, ok := expr.(*ast.Ident)
	return ok
}

// whereChainLooksLikePattern: the qualified chain tokens[start..end) (identifiers joined
// by `.` / `::`) names a pattern when some step is a `.` (a variant selector) or its last
// segment is capitalised (a struct/variant name); otherwise it is a module-qualified call.
func (p *Parser) whereChainLooksLikePattern(start int, end int) bool {
	hasDot := false
	last := ""
	for i := start; i < end; i++ {
		switch p.tokenAt(i).Kind {
		case lexer.TOKEN_DOT:
			hasDot = true
		case lexer.TOKEN_IDENT:
			last = p.tokenAt(i).Text
		}
	}
	return hasDot || forWhereIdentLooksLikePatternType(last)
}

func forWhereIdentLooksLikePatternType(name string) bool {
	if name == "" {
		return false
	}
	ch := name[0]
	return ch >= 'A' && ch <= 'Z'
}

// looksLikeUsesStmt reports whether a leading `uses` is a named-contract application `uses Name(`
// (docs/97) rather than a variable named `uses`.
func (p *Parser) looksLikeUsesStmt() bool {
	return p.hasToken(p.pos+2) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT &&
		(p.tokenAt(p.pos+2).Kind == lexer.TOKEN_LPAREN || p.tokenAt(p.pos+2).Kind == lexer.TOKEN_LBRACKET)
}

// looksLikeContractStmt reports whether a leading `requires` is a value-contract precondition
// rather than an identifier named "requires" being assigned/typed. It is a contract unless the next
// token ends the line or begins an assignment/binding (=, :, <-).
func (p *Parser) looksLikeContractStmt() bool {
	if !p.hasToken(p.pos + 1) {
		return false
	}
	switch p.tokenAt(p.pos + 1).Kind {
	case lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF, lexer.TOKEN_ASSIGN, lexer.TOKEN_COLON, lexer.TOKEN_LARROW:
		return false
	}
	return true
}
