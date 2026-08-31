package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
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

func TestMachineForDrivers(t *testing.T) {
	src := `def scan(lexer: mutable Lexer&, count: u8, bytes: darray[u8]):
    machine over lexer for _ in count..>0 -> lexer:
        state Advance
        start Advance
        Advance, _:
            lexer <- lexer.advance_char()
            -> Advance
    machine over bytes for byte in bytes:
        state Visit
        start Visit
        Visit, _:
            -> Visit
`
	_, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected machine-for parse errors: %v", errs)
	}
}

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
	// Body = fresh input read + coverage obligation + single match on the mode.
	if len(loop.Body) != 3 {
		t.Fatalf("loop body has %d stmts, want 3", len(loop.Body))
	}
	inputDecl := loop.Body[0].(*ast.VarDeclStmt)
	if !strings.HasPrefix(inputDecl.Name, "__machine_input_") || inputDecl.Mutable {
		t.Fatalf("input decl = %+v", inputDecl)
	}
	if _, ok := loop.Body[1].(*ast.MachineCoverageStmt); !ok {
		t.Fatalf("expected coverage obligation, got %T", loop.Body[1])
	}
	matchStmt, ok := loop.Body[2].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected match, got %T", loop.Body[2])
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

func TestMachineSingleStateSelfLoopRetainsDispatch(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Text
        start Text
        Text, _:
            lexer <- lexer.advance_char()
            -> Text
`)
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	var enum *ast.EnumDecl
	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		switch n := decl.(type) {
		case *ast.EnumDecl:
			enum = n
		case *ast.FuncDecl:
			fn = n
		}
	}
	if enum == nil || fn == nil {
		t.Fatal("single-state machine must still synthesize its mode enum and host function")
	}
	if len(enum.Variants) != 1 || enum.Variants[0].Name != "Text" {
		t.Fatalf("mode enum variants = %#v, want [Text]", enum.Variants)
	}
	if len(fn.Body) == 0 {
		t.Fatal("host function has no lowered machine body")
	}
	wrapper, ok := fn.Body[0].(*ast.IfStmt)
	if !ok || len(wrapper.Then) != 2 {
		t.Fatalf("single-state machine must retain scoped mode declaration and loop, got %T with %d statements", fn.Body[0], func() int {
			if ok {
				return len(wrapper.Then)
			}
			return 0
		}())
	}
	if _, ok := wrapper.Then[0].(*ast.VarDeclStmt); !ok {
		t.Fatalf("first scoped statement = %T, want mode declaration", wrapper.Then[0])
	}
	loop, ok := wrapper.Then[1].(*ast.WhileStmt)
	if !ok || len(loop.Body) != 3 {
		t.Fatalf("single-state machine must retain input, coverage, and dispatch body, got %T with %d statements", wrapper.Then[1], func() int {
			if ok {
				return len(loop.Body)
			}
			return 0
		}())
	}
	if _, ok := loop.Body[2].(*ast.MatchStmt); !ok {
		t.Fatalf("single-state machine body = %T, want mode dispatch match", loop.Body[2])
	}
}

// Calls in arm classifiers execute inside the generated loop. A driven resource used only
// by a guard must therefore still be licensed by the loop capture manifest.
func TestMachineGuardCallCapturesDrivenRoot(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Text
        start Text
        Text, _ if lexer.peek(1) == '{':
            -> Text
        Text, _:
            break
`)
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if candidate, ok := decl.(*ast.FuncDecl); ok {
			fn = candidate
			break
		}
	}
	if fn == nil || len(fn.Body) == 0 {
		t.Fatal("host function missing")
	}
	wrapper, ok := fn.Body[0].(*ast.IfStmt)
	if !ok || len(wrapper.Then) == 0 {
		t.Fatalf("expected scoped machine wrapper, got %#v", fn.Body[0])
	}
	loop, ok := wrapper.Then[len(wrapper.Then)-1].(*ast.WhileStmt)
	if !ok {
		t.Fatalf("expected while loop, got %T", wrapper.Then[len(wrapper.Then)-1])
	}
	if len(loop.Captures) != 1 || loop.Captures[0] != "lexer" {
		t.Fatalf("guard-only call captures = %v, want [lexer]", loop.Captures)
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

func TestMachineTransitionCopiesShadowingArmLocal(t *testing.T) {
	pos := lexer.Pos{Offset: 17}
	arm := &machineArm{
		state:     "Read",
		target:    "Ready",
		targetPos: pos,
		args:      []ast.Expr{&ast.Ident{Position: pos, Name: "value"}},
	}
	target := &machineState{name: "Ready", fields: []machineField{{name: "value"}}}
	stmts := lowerMachineTransition(arm, target, func(p lexer.Pos, name string) ast.Expr {
		return &ast.Ident{Position: p, Name: name}
	}, "mode")
	if len(stmts) != 2 {
		t.Fatalf("shadowing transition emitted %d statements, want payload copy plus mode rebind", len(stmts))
	}
	assign, ok := stmts[0].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("first transition statement = %T, want AssignStmt", stmts[0])
	}
	targetIdent, targetOK := assign.Target.(*ast.Ident)
	valueIdent, valueOK := assign.Value.(*ast.Ident)
	if !targetOK || !valueOK || targetIdent.Name != "value" || valueIdent.Name != "value" {
		t.Fatalf("shadowing payload copy = %#v, want value <- value", assign)
	}
}

func TestMachineTransitionLeavesArmLocalScopeBeforePayloadStore(t *testing.T) {
	pos := lexer.Pos{Offset: 31}
	arm := &machineArm{
		pos:       pos,
		state:     "Read",
		target:    "Ready",
		targetPos: pos,
		exit:      machineExitTransition,
		body: []ast.Stmt{
			&ast.VarDeclStmt{Name: "value", Value: &ast.IntLit{Value: "7"}},
		},
		args: []ast.Expr{&ast.Ident{Position: pos, Name: "value"}},
	}
	state := &machineState{name: "Read", fields: []machineField{{name: "input"}}}
	target := &machineState{name: "Ready", fields: []machineField{{name: "value"}}}
	lowered := (&Parser{}).lowerMachineArm(arm, state, map[string]*machineState{
		"Read":  state,
		"Ready": target,
	}, func(p lexer.Pos, name string) ast.Expr {
		return &ast.Ident{Position: p, Name: name}
	}, "input", "mode")
	if len(lowered.body) != 4 {
		t.Fatalf("lowered shadowing arm has %d statements, want temp declaration, scoped body, payload store, mode store", len(lowered.body))
	}
	if _, ok := lowered.body[0].(*ast.VarDeclStmt); !ok {
		t.Fatalf("first shadowing statement = %T, want outer temporary declaration", lowered.body[0])
	}
	if scoped, ok := lowered.body[1].(*ast.CanStmt); !ok || len(scoped.Body) != 2 {
		t.Fatalf("lowered shadowing arm body = %#v, want an internal scope containing local and captured arg", lowered.body)
	}
	assign, ok := lowered.body[2].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("post-scope transition statement = %T, want AssignStmt", lowered.body[2])
	}
	targetIdent, ok := assign.Target.(*ast.Ident)
	if !ok || targetIdent.Name != "value" {
		t.Fatalf("post-scope payload target = %#v, want outer Ready.value binding", assign.Target)
	}
}

// A payload alias is an arm-local binding. Mutating it is legal, while the scalarized
// destination field remains protected and is updated only by the transition.
func TestMachinePayloadAliasMutationAccepted(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Read(depth: usize)
        start Read(0)
        Read(value), _:
            value <- value + 1
            -> Read(value)
`)
	if _, errs := parseSourceFile(t, src); len(errs) != 0 {
		t.Fatalf("payload alias mutation should parse, got %v", errs)
	}
}

// A `machine from` arm body must be straight-line — a nested branch before the
// `next`/`done` terminator is the confirmed docs/125 §5 soundness hole (R1 for the
// expression form). This mirrors TestMachineRefusalBranchInArm for `machine over`.
func TestMachineFromRefusalBranchInArm(t *testing.T) {
	src := "enum Scan:\n    A\n    B\n\n" +
		"def probe(flag: bool) -> i64:\n" +
		"    outer: mutable i64 = 0\n" +
		"    result: i64 = machine from Scan.A:\n" +
		"        Scan.A:\n" +
		"            if flag:\n" +
		"                outer <- outer + 1\n" +
		"            next Scan.B\n" +
		"        Scan.B:\n" +
		"            done outer\n" +
		"    return result\n"
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "cannot branch") {
		t.Fatalf("expected machine-from branch refusal, got %v", errs)
	}
}

// A `machine from` arm resolves via `done`, so a bare `return` is a control-flow escape.
func TestMachineFromRefusalReturnInArm(t *testing.T) {
	src := "enum Scan:\n    A\n\n" +
		"def probe() -> i64:\n" +
		"    result: i64 = machine from Scan.A:\n" +
		"        Scan.A:\n" +
		"            return 7\n" +
		"    return result\n"
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "cannot `return`") {
		t.Fatalf("expected machine-from return refusal, got %v", errs)
	}
}

// Straight-line mutation plus a guarded `next` terminator is the blessed shape and must
// still parse cleanly.
func TestMachineFromStraightLineArmAccepted(t *testing.T) {
	src := "enum Scan:\n    A\n    B\n\n" +
		"def probe(flag: bool) -> i64:\n" +
		"    outer: mutable i64 = 0\n" +
		"    result: i64 = machine from Scan.A:\n" +
		"        Scan.A:\n" +
		"            outer <- outer + 1\n" +
		"            next Scan.B if flag\n" +
		"            done outer\n" +
		"        Scan.B:\n" +
		"            done outer\n" +
		"    return result\n"
	_, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("expected blessed machine-from arm to parse, got %v", errs)
	}
}

// Qualified machine-from state references must keep the start enum's identity. The AST stores
// only variant names, so the parser has to reject a foreign prefix before it is discarded.
func TestMachineFromRejectsForeignEnumQualifier(t *testing.T) {
	src := "enum Scan:\n    A\n    B\n\n" +
		"enum Other:\n    A\n    B\n\n" +
		"def probe() -> i64:\n" +
		"    result: i64 = machine from Scan.A:\n" +
		"        Other.A:\n" +
		"            next Other.B\n" +
		"        Scan.B:\n" +
		"            done 1\n" +
		"    return result\n"
	_, errs := parseSourceFile(t, src)
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "uses enum") {
		t.Fatalf("expected foreign machine-from enum qualifier refusal, got %v", errs)
	}
}

func TestMachineFromRequiresQualifiedStartState(t *testing.T) {
	src := "def probe() -> i64:\n" +
		"    result: i64 = machine from A:\n" +
		"        A:\n" +
		"            done 1\n" +
		"    return result\n"
	_, errs := parseSourceFile(t, src)
	if !strings.Contains(strings.Join(errs, "\n"), "qualified start state") {
		t.Fatalf("expected qualified machine-from start-state refusal, got %v", errs)
	}
}

// A range input alternative (`Num, '0'..<'9':`, docs/122 §5.2 shared grammar) lowers to a
// bounds test on the input var — `lo <= input and input < hi` — not an equality. Exclusive
// `..<` uses `<` on the upper bound; inclusive `..=` uses `<=`.
func TestMachineRangeArmLowering(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char() while not lexer.is_end_of_source():
        state Num
        start Num
        Num, '0'..<'9':
            lexer <- lexer.advance_char()
            -> Num
        Num, _:
            break
`)
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	var fn *ast.FuncDecl
	for _, d := range file.Decls {
		if n, ok := d.(*ast.FuncDecl); ok {
			fn = n
		}
	}
	wrapper := fn.Body[0].(*ast.IfStmt)
	loop := wrapper.Then[len(wrapper.Then)-1].(*ast.WhileStmt)
	matchStmt := loop.Body[2].(*ast.MatchStmt)
	// Num state: range arm + wildcard `break` → if/else. The range arm is the `if` cond.
	numIf := matchStmt.Arms[0].Body[0].(*ast.IfStmt)
	and, ok := numIf.Cond.(*ast.BinaryExpr)
	if !ok || and.Op != lexer.TOKEN_AND {
		t.Fatalf("range arm cond must be an AND of two bounds, got %#v", numIf.Cond)
	}
	lo := and.Left.(*ast.BinaryExpr)
	hi := and.Right.(*ast.BinaryExpr)
	if lo.Op != lexer.TOKEN_LTEQ {
		t.Fatalf("lower bound must be `lo <= input`, got op %v", lo.Op)
	}
	if hi.Op != lexer.TOKEN_LT {
		t.Fatalf("exclusive upper bound must be `input < hi`, got op %v", hi.Op)
	}
}

// Two unconditional arms handling the same literal in one state: the second is unreachable
// (docs/123 §5.5 duplicate-arm rejection). Previously both compiled silently.
func TestMachineRefusalDuplicateLiteralArm(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char() while not lexer.is_end_of_source():
        state Go
        start Go
        Go, 'm':
            -> Go
        Go, 'm':
            -> Go
        Go, _:
            break
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "repeats input 'm'") {
		t.Fatalf("expected duplicate-literal-arm refusal, got %v", errs)
	}
}

func TestMachineRefusalDuplicateFloatLiteralArm(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char() while not lexer.is_end_of_source():
        state Go
        start Go
        Go, 1.0:
            -> Go
        Go, (1.0):
            -> Go
        Go, _:
            break
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "repeats input 1.0") {
		t.Fatalf("expected duplicate-float-arm refusal, got %v", errs)
	}
}

func TestMachineRefusalDuplicateEnumTagArms(t *testing.T) {
	for _, tag := range []string{"TokenKind.A", ".A"} {
		src := machineSrc("    machine over lexer.current_char():\n" +
			"        state Text\n" +
			"        start Text\n" +
			"        Text, " + tag + ":\n" +
			"            -> Text\n" +
			"        Text, " + tag + ":\n" +
			"            -> Text\n" +
			"        Text, _:\n" +
			"            break\n")
		_, errs := parseSourceFile(t, src)
		if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "repeats input") {
			t.Fatalf("duplicate enum tag %s was not refused: %v", tag, errs)
		}
	}
}

// A literal shared between a guarded arm and an unguarded arm is NOT a duplicate — the
// guard distinguishes them (0-FP discipline).
func TestMachineGuardedArmSharesLiteralAccepted(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char() while not lexer.is_end_of_source():
        state Go
        start Go
        Go, 'm' if lexer.peek(1) == 'x':
            -> Go
        Go, 'm':
            -> Go
        Go, _:
            break
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("guarded+unguarded same-literal arms must be accepted, got %v", errs)
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

func TestMachineRefusalOpenDomainLiterals(t *testing.T) {
	for _, literal := range []string{"true", `"done"`, "1.0", "-1"} {
		src := machineSrc("    machine over lexer.current_char():\n" +
			"        state Text\n" +
			"        start Text\n" +
			"        Text, " + literal + ":\n" +
			"            return 1\n")
		_, errs := parseSourceFile(t, src)
		if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "does not cover all inputs") {
			t.Fatalf("open-domain literal %s without wildcard was accepted: %v", literal, errs)
		}
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

// Calls embedded in an indexed assignment target can mutate a driven resource too:
// `table[scanner.next()] <- value`. The target walk must visit the index expression;
// visiting only the assignment RHS loses `scanner` from the value-block capture set.
func TestMachineCallInAssignmentTargetDrivesCapture(t *testing.T) {
	position := lexer.Pos{}
	scanner := &ast.Ident{Position: position, Name: "scanner"}
	call := &ast.CallExpr{
		Position: position,
		Func:     &ast.FieldExpr{Position: position, Object: scanner, Field: "next"},
	}
	stmt := &ast.AssignStmt{
		Position: position,
		Target: &ast.IndexExpr{
			Position: position,
			Object:   &ast.Ident{Position: position, Name: "table"},
			Index:    call,
		},
		Value: &ast.IntLit{Position: position, Value: "1"},
	}
	overRoots := map[string]bool{"scanner": true}
	mutatedRoots := map[string]bool{}
	collectMachineCallArgRoots(stmt, overRoots, mutatedRoots)
	if !mutatedRoots["scanner"] {
		t.Fatal("call in assignment target was not recorded as a driven mutation")
	}
}

// Compound and as-ref assignments are also allowed straight-line machine-arm forms. Their
// call-bearing values must enter the same capture walk as ordinary assignments.
func TestMachineCallInCompoundAssignmentsDrivesCapture(t *testing.T) {
	position := lexer.Pos{}
	overRoots := map[string]bool{"scanner": true}
	for _, stmt := range []ast.Stmt{
		&ast.AugAssignStmt{
			Position: position,
			Target:   &ast.Ident{Position: position, Name: "value"},
			Value: &ast.CallExpr{
				Position: position,
				Func:     &ast.FieldExpr{Position: position, Object: &ast.Ident{Position: position, Name: "scanner"}, Field: "next"},
			},
		},
		&ast.AsRefAssignStmt{
			Position: position,
			Target:   &ast.Ident{Position: position, Name: "value"},
			AsKind:   "ref",
			Value: &ast.CallExpr{
				Position: position,
				Func:     &ast.FieldExpr{Position: position, Object: &ast.Ident{Position: position, Name: "scanner"}, Field: "next"},
			},
		},
	} {
		mutatedRoots := map[string]bool{}
		collectMachineCallArgRoots(stmt, overRoots, mutatedRoots)
		if !mutatedRoots["scanner"] {
			t.Fatalf("call in %T was not recorded as a driven mutation", stmt)
		}
	}
}

// Recovery/handler statement bodies are executable children of a value expression. A call in
// `get value else:` must therefore contribute its driven root just like a call in an ordinary
// expression statement; otherwise machine lowering can omit the capture and reject valid code.
func TestMachineCallInGetRecoveryDrivesCapture(t *testing.T) {
	position := lexer.Pos{}
	scanner := &ast.Ident{Position: position, Name: "scanner"}
	recoveryCall := &ast.CallExpr{
		Position: position,
		Func:     &ast.FieldExpr{Position: position, Object: scanner, Field: "next"},
	}
	expr := &ast.GetExpr{
		Position: position,
		Value:    &ast.IntLit{Position: position, Value: "0"},
		Recovery: &ast.RecoveryClause{Body: []ast.Stmt{&ast.ExprStmt{Position: position, Expr: recoveryCall}}},
	}
	stmt := &ast.ExprStmt{Position: position, Expr: expr}
	overRoots := map[string]bool{"scanner": true}
	mutatedRoots := map[string]bool{}
	collectMachineCallArgRoots(stmt, overRoots, mutatedRoots)
	if !mutatedRoots["scanner"] {
		t.Fatal("call in get recovery body was not recorded as a driven mutation")
	}
}

func TestMachineDrivenRootInGetRecoveryIsCollected(t *testing.T) {
	position := lexer.Pos{}
	scanner := &ast.Ident{Position: position, Name: "scanner"}
	expr := &ast.GetExpr{
		Position: position,
		Value:    &ast.IntLit{Position: position, Value: "0"},
		Recovery: &ast.RecoveryClause{Body: []ast.Stmt{&ast.ExprStmt{
			Position: position,
			Expr: &ast.CallExpr{
				Position: position,
				Func:     &ast.FieldExpr{Position: position, Object: scanner, Field: "next"},
			},
		}}},
	}
	roots := map[string]bool{}
	collectExprRootIdents(expr, roots)
	if !roots["scanner"] {
		t.Fatal("get recovery root was not collected for machine over capture analysis")
	}
}

// Match-expression arms are also executable children of a value expression. A driven
// resource call in an arm body must be visible to machine capture analysis, including when
// the arm is guarded; stopping at the match node would silently omit the capture.
func TestMachineCallInMatchArmDrivesCapture(t *testing.T) {
	position := lexer.Pos{}
	scanner := &ast.Ident{Position: position, Name: "scanner"}
	call := &ast.CallExpr{
		Position: position,
		Func:     &ast.FieldExpr{Position: position, Object: scanner, Field: "next"},
	}
	expr := &ast.MatchExpr{
		Position: position,
		Value:    &ast.IntLit{Position: position, Value: "0"},
		Arms: []ast.MatchArm{{
			Position: position,
			Guard:    &ast.BoolLit{Position: position, Value: true},
			Body:     []ast.Stmt{&ast.ExprStmt{Position: position, Expr: call}},
		}},
	}
	stmt := &ast.ExprStmt{Position: position, Expr: expr}
	overRoots := map[string]bool{"scanner": true}
	mutatedRoots := map[string]bool{}
	collectMachineCallArgRoots(stmt, overRoots, mutatedRoots)
	if !mutatedRoots["scanner"] {
		t.Fatal("call in match arm was not recorded as a driven mutation")
	}
}

func TestMachineCallInValueBlockStatementDrivesCapture(t *testing.T) {
	position := lexer.Pos{}
	scanner := &ast.Ident{Position: position, Name: "scanner"}
	call := &ast.CallExpr{
		Position: position,
		Func:     &ast.FieldExpr{Position: position, Object: scanner, Field: "next"},
	}
	expr := &ast.ExprBlock{
		Position: position,
		Stmts:    []ast.Stmt{&ast.ExprStmt{Position: position, Expr: call}},
		Value:    &ast.IntLit{Position: position, Value: "0"},
	}
	overRoots := map[string]bool{"scanner": true}
	mutatedRoots := map[string]bool{}
	collectMachineCallArgRoots(&ast.ExprStmt{Position: position, Expr: expr}, overRoots, mutatedRoots)
	if !mutatedRoots["scanner"] {
		t.Fatal("call in value-block statements was not recorded as a driven mutation")
	}
}

func TestMachineCallInLambdaBodyDrivesCapture(t *testing.T) {
	position := lexer.Pos{}
	scanner := &ast.Ident{Position: position, Name: "scanner"}
	call := &ast.CallExpr{
		Position: position,
		Func:     &ast.FieldExpr{Position: position, Object: scanner, Field: "next"},
	}
	expr := &ast.LambdaExpr{
		Position: position,
		Body:     []ast.Stmt{&ast.ExprStmt{Position: position, Expr: call}},
	}
	overRoots := map[string]bool{"scanner": true}
	mutatedRoots := map[string]bool{}
	collectMachineCallArgRoots(&ast.ExprStmt{Position: position, Expr: expr}, overRoots, mutatedRoots)
	if !mutatedRoots["scanner"] {
		t.Fatal("call in lambda body was not recorded as a driven mutation")
	}
}

func TestMachinePayloadPredicateWalksAggregate(t *testing.T) {
	position := lexer.Pos{}
	expr := &ast.TupleExpr{
		Position: position,
		Elems: []ast.Expr{
			&ast.IntLit{Position: position, Value: "0"},
			&ast.BinaryExpr{
				Position: position,
				Op:       lexer.TOKEN_GT,
				Left:     &ast.Ident{Position: position, Name: "depth"},
				Right:    &ast.IntLit{Position: position, Value: "1"},
			},
		},
	}
	if !exprMentionsIdent(expr, "depth") {
		t.Fatal("payload predicate identifier hidden inside tuple was not found")
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

func TestMachineCompoundAssignmentUsesMutationRules(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Text
        start Text
        Text, _:
            lexer += 1
            -> Text
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("compound assignment in a machine arm was rejected: %v", errs)
	}
}

func TestMachineRefusalForeignCompoundAssignment(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Text
        start Text
        Text, _:
            other += 1
            -> Text
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "foreign state") {
		t.Fatalf("foreign compound assignment was not refused: %v", errs)
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
// for call-predicate dispatch. The guard and body each get an arm-local binding.
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
	matchStmt := loop.Body[2].(*ast.MatchStmt)
	dispatch, ok := matchStmt.Arms[0].Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected guard ladder after arm-local binding, got %T", matchStmt.Arms[0].Body[0])
	}
	guard, ok := dispatch.Cond.(*ast.ExprBlock)
	if !ok || len(guard.Stmts) != 1 {
		t.Fatalf("expected guard expression block with one binding, got %#v", dispatch.Cond)
	}
	guardBind, ok := guard.Stmts[0].(*ast.VarDeclStmt)
	if !ok || guardBind.Name != "character" {
		t.Fatalf("expected guard-local input binding, got %#v", guard.Stmts[0])
	}
	bodyBind, ok := dispatch.Then[0].(*ast.VarDeclStmt)
	if !ok || bodyBind.Name != "character" {
		t.Fatalf("expected body-local input binding, got %#v", dispatch.Then[0])
	}
}
