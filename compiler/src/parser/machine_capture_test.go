package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	ret, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("yield form should produce ReturnStmt, got %T", fn.Body[0])
	}
	block, ok := ret.Value.(*ast.ExprBlock)
	if !ok {
		t.Fatalf("yield form should wrap in ExprBlock, got %T", ret.Value)
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

// A state with no arms (and no `start`) used to panic in buildMachineArmChain with an index out
// of range after the "has no arms" diagnostic was recorded (fuzz finding F19).
func TestMachineStateWithoutArmsReportsInsteadOfPanicking(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "machine", "state_without_arms.neg.elisa"))
	if err != nil {
		t.Fatal(err)
	}
	_, errs := parseSourceFile(t, string(src))
	got := fmt.Sprint(errs)
	for _, want := range []string{"machine has no `start` declaration", `machine state "Build" has no arms`} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q, got: %s", want, got)
		}
	}
}
