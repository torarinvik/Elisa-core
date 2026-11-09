package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"strings"
	"testing"
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

// A multi-line `catch` block ends on a DEDENT, and the `-> State` on the next line is the
// arm's transition — not a cast of the catch expression. parsePostfix used to claim that
// arrow, which reported the removed `expr -> T` cast and then "arm makes no decision" for
// an arm that is legal under docs/123 §5 (error handling is an EXPRESSION, not branching).
func TestMachineTransitionAfterCatchBlock(t *testing.T) {
	src := `error LoadError:
    Failed

def load(fail: bool) -> i64 error[LoadError]:
    raise LoadError.Failed if fail
    7

def run(total: mutable i64) -> i64:
    machine over total while total < 7:
        state Run
        start Run
        Run, _:
            catch load(false):
                loaded:
                    total <- total + loaded
                    true
                error failure:
                    total <- total + 90
                    true
            -> Run
    return total
`
	_, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors for a transition after a catch block: %v", errs)
	}
}

func TestMachineBranchFallsThroughToSharedTransition(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Text
        start Text
        Text, _:
            if lexer.peek(1) == '{':
                lexer <- lexer.advance_char()
            -> Text
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("branch should continue to its shared transition: %v", errs)
	}
}

func TestMachineBranchLocalTransitions(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Read
        state Write
        start Read
        Read, _:
            if lexer.peek(1) == '{':
                if lexer.peek(2) == '}':
                    lexer <- lexer.advance_char()
                    -> Write
                lexer <- lexer.advance_char()
                -> Read
            else:
                -> Write
        Write, _:
            -> Read
`)
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected branch-transition parse errors: %v", errs)
	}
	if len(file.Decls) == 0 {
		t.Fatal("machine desugaring missing")
	}
	if strings.Contains(fmt.Sprintf("%#v", file), "BreakStmt") {
		t.Fatal("branch transition parser marker escaped lowering")
	}
}

func TestMachineBranchTransitionRejectsStatementsAfterDecision(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Read
        state Write
        start Read
        Read, _:
            if lexer.peek(1) == '{':
                -> Write
                lexer <- lexer.advance_char()
            else:
                -> Read
        Write, _:
            -> Read
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "continues after its `->` decision") {
		t.Fatalf("expected post-transition branch refusal, got %v", errs)
	}
}

func TestMachineBranchTransitionAllowsTerminalBranchLocal(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Read
        state Write
        start Read
        Read, _:
            if lexer.peek(1) == '{':
                value: i64 = 7
                -> Write
            lexer <- lexer.advance_char()
            -> Read
        Write, _:
            break
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("terminal branch-local declaration should not capture the fallthrough continuation: %v", errs)
	}
}

func TestMachineBranchCatchOkBindingAndNestedLocal(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Read
        state Write
        start Read
        Read, _:
            catch read_value():
                ok payload:
                    lexer <- lexer.with_value(payload)
                error failure:
                    _ = try report_error(failure)
                    raise failure
            if lexer.current_char() < 0:
                errno: i64 = lexer.error_code()
                if errno == 4:
                    -> Write
                lexer <- lexer.mark_failed(errno)
                -> Write
            -> Read
        Write, _:
            break
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected catch/bind/scoped-branch errors: %v", errs)
	}
}

func TestMachineBranchTransitionRejectsFallthroughBranchLocal(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Read
        state Write
        start Read
        Read, _:
            if lexer.peek(1) == '{':
                value: i64 = 7
                lexer <- lexer.advance_char()
            else:
                -> Write
            -> Read
        Write, _:
            break
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "declaration in a fallthrough branch") {
		t.Fatalf("expected fallthrough branch-scope refusal, got %v", errs)
	}
}

func TestMachineBranchTransitionRejectsMarkerInsideCanWrapper(t *testing.T) {
	src := machineSrc(`    machine over lexer.current_char():
        state Read
        state Write
        start Read
        Read, _:
            if lexer.peek(1) == '{':
                can Unsafe:
                    -> Write
            else:
                -> Read
        Write, _:
            break
`)
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 {
		t.Fatal("branch transition inside a can wrapper must be rejected until wrapper scope is preserved")
	}
}

func TestMachineBranchTransitionGuardsSharedSuffix(t *testing.T) {
	src := `def scan(cursor: mutable i64) -> i64:
    machine over cursor while cursor < 50:
        state A
        state B
        state C
        start A
        A, _:
            if cursor == 0:
                if cursor == 0:
                    cursor <- 1
                    -> B
            cursor <- cursor + 2
            -> C
        B, _:
            cursor <- cursor + 10
            break
        C, _:
            break
    return cursor
def main() -> i64:
    return 0 if scan(0) == 11 and scan(1) == 3 else 1
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected shared-suffix errors: %v", errs)
	}
	if len(file.Decls) == 0 {
		t.Fatal("machine desugaring missing")
	}
	pos := lexer.Pos{File: "test.elisa", Line: 8, Col: 17, Offset: 200}
	branch := &ast.IfStmt{Cond: &ast.BoolLit{Value: true}, Then: []ast.Stmt{&ast.BreakStmt{Position: pos}}}
	suffix := &ast.AssignStmt{Target: &ast.Ident{Name: "cursor"}, Value: &ast.IntLit{Value: "1"}}
	states := map[string]*machineState{"Write": {name: "Write"}}
	lowered := lowerMachineBranchTransitions([]ast.Stmt{branch, suffix}, map[lexer.Pos]string{pos: "Write"}, states, func(p lexer.Pos, name string) ast.Expr { return &ast.Ident{Position: p, Name: name} }, "mode", nil)
	if len(lowered) != 1 {
		t.Fatalf("shared-suffix lowering emitted %d outer statements, want one continuation branch", len(lowered))
	}
	ifStmt, ok := lowered[0].(*ast.IfStmt)
	if !ok || len(ifStmt.Then) != 1 {
		t.Fatalf("terminal branch = %#v, want one mode store", lowered[0])
	}
	if _, ok := ifStmt.Then[0].(*ast.AssignStmt); !ok {
		t.Fatalf("arrow marker did not lower to mode assignment: %T", ifStmt.Then[0])
	}
	if len(ifStmt.Else) != 1 {
		t.Fatalf("fallthrough branch has %d suffix statements, want one", len(ifStmt.Else))
	}
	if _, ok := ifStmt.Else[0].(*ast.AssignStmt); !ok {
		t.Fatalf("shared suffix was not placed on the fallthrough branch: %T", ifStmt.Else[0])
	}
}
