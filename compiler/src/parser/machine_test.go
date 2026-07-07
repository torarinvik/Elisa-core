package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// machineSrc wraps a machine body in a host function with a driven `lexer` resource.
func machineSrc(body string) string {
	return "def scan(lexer: mutable Lexer&) -> i64:\n" + body + "\n    return 0\n"
}

const fstringMachine = `    machine over lexer.current_char() while not lexer.is_end_of_source():
        state Text
        state Expr(depth: usize)
        state InnerString(depth: usize)

        start Text

        Text, '"':
            return 1

        Text, '{' if lexer.peek(1) == '{':
            lexer <- lexer.advance_chars(2)
            -> Text

        Text, '{':
            lexer <- lexer.advance_char()
            -> Expr(1)

        Text, _:
            lexer <- lexer.advance_char()
            -> Text

        Expr(depth), '{':
            lexer <- lexer.advance_char()
            -> Expr(depth + 1)

        Expr(1), '}':
            lexer <- lexer.advance_char()
            -> Text

        Expr(depth > 1), '}':
            lexer <- lexer.advance_char()
            -> Expr(depth - 1)

        Expr(depth), '"':
            lexer <- lexer.advance_char()
            -> InnerString(depth)

        Expr(_), _:
            lexer <- lexer.advance_char()
            -> Expr(depth)

        InnerString(depth), '"':
            lexer <- lexer.advance_char()
            -> Expr(depth)

        InnerString(_), _:
            lexer <- lexer.advance_char()
            -> InnerString(depth)
`

func TestMachineDesugarShape(t *testing.T) {
	file, errs := parseSourceFile(t, machineSrc(fstringMachine))
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	// The synthesized mode enum is hoisted to file scope.
	var enum *ast.EnumDecl
	var fn *ast.FuncDecl
	for _, d := range file.Decls {
		switch n := d.(type) {
		case *ast.EnumDecl:
			enum = n
		case *ast.FuncDecl:
			fn = n
		}
	}
	if enum == nil {
		t.Fatal("expected hoisted __MachineMode enum decl at file scope")
	}
	if !strings.HasPrefix(enum.Name, "__MachineMode_") {
		t.Fatalf("enum name = %q, want __MachineMode_ prefix", enum.Name)
	}
	if len(enum.Variants) != 3 || enum.Variants[0].Name != "Text" || enum.Variants[1].Name != "Expr" || enum.Variants[2].Name != "InnerString" {
		t.Fatalf("enum variants = %#v", enum.Variants)
	}
	if len(enum.Variants[1].Payload) != 0 {
		t.Fatal("mode enum must be payload-less (scalarized desugar)")
	}
	if fn == nil {
		t.Fatal("host function missing")
	}
	// Statement form: wrapLoopHeader's `if true:` wrapper holding [mode decl, depth decl, while].
	wrapper, ok := fn.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected if-true scoped wrapper, got %T", fn.Body[0])
	}
	if lit, ok := wrapper.Cond.(*ast.BoolLit); !ok || !lit.Value {
		t.Fatal("wrapper cond must be literal true")
	}
	if len(wrapper.Then) != 3 {
		t.Fatalf("wrapper holds %d stmts, want 3 (mode, depth, loop)", len(wrapper.Then))
	}
	modeDecl := wrapper.Then[0].(*ast.VarDeclStmt)
	if !strings.HasPrefix(modeDecl.Name, "__machine_mode_") || !modeDecl.Mutable {
		t.Fatalf("mode decl = %+v", modeDecl)
	}
	depthDecl := wrapper.Then[1].(*ast.VarDeclStmt)
	if depthDecl.Name != "depth" || !depthDecl.Mutable {
		t.Fatalf("depth decl = %+v", depthDecl)
	}
	if _, ok := depthDecl.Value.(*ast.ZeroedLit); !ok {
		t.Fatalf("non-start payload local must init zeroed, got %#v", depthDecl.Value)
	}
	loop, ok := wrapper.Then[2].(*ast.WhileStmt)
	if !ok {
		t.Fatalf("expected while loop, got %T", wrapper.Then[2])
	}
	// `while COND` clause becomes the loop condition (not `true`).
	if _, isTrue := loop.Cond.(*ast.BoolLit); isTrue {
		t.Fatal("while-clause machine must not have a literal-true condition")
	}
	// The driven resource is captured (mutation licensing).
	if len(loop.Captures) != 1 || loop.Captures[0] != "lexer" {
		t.Fatalf("loop captures = %v, want [lexer]", loop.Captures)
	}
	// Body = fresh input read + single match on the mode.
	if len(loop.Body) != 2 {
		t.Fatalf("loop body has %d stmts, want 2", len(loop.Body))
	}
	inputDecl := loop.Body[0].(*ast.VarDeclStmt)
	if !strings.HasPrefix(inputDecl.Name, "__machine_input_") || inputDecl.Mutable {
		t.Fatalf("input decl = %+v", inputDecl)
	}
	matchStmt, ok := loop.Body[1].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected match, got %T", loop.Body[1])
	}
	if len(matchStmt.Arms) != 3 {
		t.Fatalf("match has %d arms, want 3", len(matchStmt.Arms))
	}
	pat := matchStmt.Arms[0].Pattern.(*ast.MatchVariantPattern)
	if pat.EnumName != enum.Name || pat.Variant != "Text" {
		t.Fatalf("arm 0 pattern = %+v", pat)
	}
	// Text state: 4 arms → if/elif/elif/else ladder (one IfStmt).
	textIf, ok := matchStmt.Arms[0].Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("Text arm body = %T", matchStmt.Arms[0].Body[0])
	}
	if len(textIf.Elifs) != 2 || len(textIf.Else) == 0 {
		t.Fatalf("Text ladder: %d elifs, else=%d", len(textIf.Elifs), len(textIf.Else))
	}
	// The `Text, '"'` arm ends in return; the wildcard arm's transition is a self-loop
	// (mode rebind elided) so its lowered body is just the advance call.
	if _, ok := textIf.Then[len(textIf.Then)-1].(*ast.ReturnStmt); !ok {
		t.Fatalf("first Text arm should end in return, got %T", textIf.Then[len(textIf.Then)-1])
	}
	for _, s := range textIf.Else {
		if as, ok := s.(*ast.AssignStmt); ok {
			if id, ok := as.Target.(*ast.Ident); ok && strings.HasPrefix(id.Name, "__machine_mode_") {
				t.Fatal("self-transition must elide the mode rebind")
			}
		}
	}
	// `Text, '{'` → Expr(1): elif body ends with depth <- 1 and mode <- Enum.Expr.
	exprEntry := textIf.Elifs[1].Body
	last := exprEntry[len(exprEntry)-1].(*ast.AssignStmt)
	if id := last.Target.(*ast.Ident); !strings.HasPrefix(id.Name, "__machine_mode_") {
		t.Fatalf("transition must end with mode rebind, got target %q", id.Name)
	}
	prev := exprEntry[len(exprEntry)-2].(*ast.AssignStmt)
	if id := prev.Target.(*ast.Ident); id.Name != "depth" {
		t.Fatalf("transition payload assign target = %q, want depth", id.Name)
	}
}

func TestMachineRefusalBranchInArm(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Text
        start Text
        Text, _:
            if lexer.peek(1) == '{':
                lexer <- lexer.advance_char()
            -> Text
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "cannot branch") {
		t.Fatalf("expected branch refusal, got %v", errs)
	}
}

func TestMachineRefusalNoDecision(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Text
        start Text
        Text, _:
            lexer <- lexer.advance_char()
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "makes no decision") {
		t.Fatalf("expected no-decision refusal, got %v", errs)
	}
}

func TestMachineRefusalStmtAfterDecision(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Text
        start Text
        Text, _:
            -> Text
            lexer <- lexer.advance_char()
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "final statement") {
		t.Fatalf("expected decision-position refusal, got %v", errs)
	}
}

func TestMachineRefusalNonExhaustiveState(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Text
        start Text
        Text, '"':
            return 1
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "does not cover all inputs") {
		t.Fatalf("expected exhaustiveness refusal, got %v", errs)
	}
}

func TestMachineRefusalUnreachableArm(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Text
        start Text
        Text, _:
            return 1
        Text, '"':
            return 2
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "unreachable") {
		t.Fatalf("expected unreachable-arm refusal, got %v", errs)
	}
}

func TestMachineRefusalUndeclaredTarget(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Text
        start Text
        Text, _:
            -> Missing
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "undeclared state") {
		t.Fatalf("expected undeclared-target refusal, got %v", errs)
	}
}

func TestMachineRefusalForeignMutation(t *testing.T) {
	src := "def scan(lexer: mutable Lexer&, total: mutable i64&) -> i64:\n" + `    machine over lexer.current_char():
        state Text
        start Text
        Text, _:
            total <- total + 1
            return 1
` + "\n    return 0\n"
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "foreign state") {
		t.Fatalf("expected foreign-mutation refusal, got %v", errs)
	}
}

// A postfix value cast/ctor (`buf.data[i].char()`) parses as a CastExpr, not a method
// call. The driven resource is rooted in the cast's operand (`buf`), so an arm mutating
// `buf` must be allowed — before the CastExpr case in collectExprRootIdents, the base was
// dropped and the machine's own driven resource was misreported as foreign state.
func TestMachineDrivenRootThroughCast(t *testing.T) {
	src := "def scan(buf: mutable Buf&) -> i64:\n" + `    idx: mutable usize = 0
    end: usize = buf.data.count
    machine over buf.data[idx].char() while idx < end:
        state Text
        start Text
        Text, _:
            buf.errs <- buf.errs + 1
            idx <- idx + 1
            -> Text
` + "\n    return 0\n"
	_, errs := parseSourceFile(t, src)
	if strings.Contains(strings.Join(errs, "\n"), "foreign state") {
		t.Fatalf("driven resource reached through a cast was misclassified as foreign: %v", errs)
	}
}

func TestMachineRefusalPayloadDirectAssign(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Expr(depth: usize)
        start Expr(1)
        Expr(depth), _:
            depth <- depth + 1
            return 1
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "payload field") {
		t.Fatalf("expected payload-assign refusal, got %v", errs)
	}
}

// Payload `where` refinements parse as refinement TYPES carried by the scalarized local,
// so the existing typed-local refinement machinery edge-checks transitions (docs/123 §5.7).
func TestMachinePayloadWhereRefinementParses(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Expr(depth: usize where depth > 0)
        start Expr(1)
        Expr(depth), _:
            return 1
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("refined payload field failed to parse: %v", errs)
	}
}

func TestMachineRefusalStartArity(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Expr(depth: usize)
        start Expr
        Expr(depth), _:
            return 1
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "payload argument") {
		t.Fatalf("expected start-arity refusal, got %v", errs)
	}
}

func TestMachineYieldForm(t *testing.T) {
	src := "def count_spaces(lexer: mutable Lexer&) -> usize:\n" + `    machine over lexer.current_char() while not lexer.is_end_of_source() -> total:
        state Skipping(total: usize)
        start Skipping(0)
        Skipping(total), ' ':
            lexer <- lexer.advance_char()
            -> Skipping(total + 1)
        Skipping(total), _:
            break
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	var fn *ast.FuncDecl
	for _, d := range file.Decls {
		if f, ok := d.(*ast.FuncDecl); ok {
			fn = f
		}
	}
	if fn == nil {
		t.Fatal("host function missing")
	}
	es, ok := fn.Body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("yield form should produce ExprStmt, got %T", fn.Body[0])
	}
	block, ok := es.Expr.(*ast.ExprBlock)
	if !ok {
		t.Fatalf("yield form should wrap in ExprBlock, got %T", es.Expr)
	}
	if v, ok := block.Value.(*ast.Ident); !ok || v.Name != "total" {
		t.Fatalf("ExprBlock value = %#v, want Ident(total)", block.Value)
	}
	if len(block.Captures) != 1 || block.Captures[0] != "lexer" {
		t.Fatalf("ExprBlock captures = %v", block.Captures)
	}
}

// A variable actually named `machine` must still parse as a plain expression statement.
func TestMachineContextualKeywordFallthrough(t *testing.T) {
	src := "def f() -> i64:\n    machine = 3\n    return machine\n"
	_, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("plain `machine` variable failed to parse: %v", errs)
	}
}

// Input bind pattern: `Scanning, character if character.is_ident():` — binds the input
// for call-predicate dispatch; the bind hoists above the guard ladder.
func TestMachineInputBindPattern(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Scanning
        start Scanning
        Scanning, character if character.is_letter():
            lexer <- lexer.advance_char()
            -> Scanning
        Scanning, _:
            break
`)
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("input bind failed to parse: %v", errs)
	}
	var fn *ast.FuncDecl
	for _, d := range file.Decls {
		if f, ok := d.(*ast.FuncDecl); ok {
			fn = f
		}
	}
	wrapper := fn.Body[0].(*ast.IfStmt)
	loop := wrapper.Then[len(wrapper.Then)-1].(*ast.WhileStmt)
	matchStmt := loop.Body[1].(*ast.MatchStmt)
	bind, ok := matchStmt.Arms[0].Body[0].(*ast.VarDeclStmt)
	if !ok || bind.Name != "character" {
		t.Fatalf("expected hoisted input-bind decl, got %#v", matchStmt.Arms[0].Body[0])
	}
	if _, ok := matchStmt.Arms[0].Body[1].(*ast.IfStmt); !ok {
		t.Fatalf("expected guard ladder after bind, got %T", matchStmt.Arms[0].Body[1])
	}
}
