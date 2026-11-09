package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
	"testing"
)

func TestMachineBranchReturnAndBreakFlow(t *testing.T) {
	valid := machineSrc(`    machine over lexer.current_char():
        state Read
        state Write
        start Read
        Read, _:
            if lexer.peek(1) == '{':
                return 1
            -> Write
        Write, _:
            break
`)
	if _, errs := parseSourceFile(t, valid); len(errs) != 0 {
		t.Fatalf("return path plus shared transition should be complete: %v", errs)
	}
	incomplete := machineSrc(`    machine over lexer.current_char():
        state Read
        state Write
        start Read
        Read, _:
            if lexer.peek(1) == '{':
                return 1
        Write, _:
            break
`)
	_, errs := parseSourceFile(t, incomplete)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "makes no decision") {
		t.Fatalf("expected a live fallthrough path refusal, got %v", errs)
	}
	complete := machineSrc(`    machine over lexer.current_char():
        state Read
        state Write
        start Read
        Read, _:
            if lexer.peek(1) == '{':
                return 1
            else:
                return 2
        Write, _:
            break
`)
	if _, errs := parseSourceFile(t, complete); len(errs) != 0 {
		t.Fatalf("both returning paths should be complete: %v", errs)
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

// Arms may branch, so the foreign-mutation refusal must look inside every branch: an
// assignment nested in an `if` or `else` branch used to go unchecked.
func TestMachineRefusalForeignMutationInBranch(t *testing.T) {
	for _, body := range []string{
		"            if lexer.ok:\n                total <- total + 1\n            return 1\n",
		"            if lexer.ok:\n                seen: i64 = 1\n            else:\n                total <- 3\n            return 1\n",
	} {
		src := "def scan(lexer: mutable Lexer&, total: mutable i64&) -> i64:\n" + `    machine over lexer.current_char():
        state Text
        start Text
        Text, _:
` + body + "\n    return 0\n"
		_, errs := parseSourceFile(t, src)
		if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "foreign state") {
			t.Fatalf("expected foreign-mutation refusal inside a branch, got %v for\n%s", errs, body)
		}
	}
}

// A postfix value cast/ctor (`buf.data[i].char()`) parses as a CastExpr, not a method
// call. The driven resource is rooted in the cast's operand (`buf`), so an arm mutating
// `buf` must be allowed — before the CastExpr case in collectExprRootIdents, the base was
// dropped and the machine's own driven resource was misreported as foreign state.
