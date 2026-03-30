package semantic

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/parser"
)

func analyzeFunctionAnalysisTestSource(t *testing.T, filename string, src string) *Result {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	result := Analyze(file)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	return result
}

func analyzeFunctionAnalysisTestSourceWithSemanticErrors(t *testing.T, filename string, src string) *Result {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	return Analyze(file)
}

func TestAnalyzeInfersDirectSinkParamSummary(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "direct_sink_summary.llcontext", `extern join(thread: Thread[i64, Joinable]) -> i64 can[Thread.Join]

def take(thread: Thread[i64, Joinable]) -> i64:
	return join(move thread)
`)
	sym, ok := result.GlobalScope.Lookup("take")
	if !ok {
		t.Fatal("expected take symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected take function type, got %T", sym.Type)
	}
	if !fnType.SinkParamsKnown || len(fnType.SinkParams) != 1 || !fnType.SinkParams[0] {
		t.Fatalf("expected direct take summary to infer a sink parameter, got %#v", fnType.SinkParams)
	}
}

func TestGuardFactSetProveLEAndCheckFieldAccess(t *testing.T) {
	start := &ast.Ident{Name: "start"}
	mid := &ast.Ident{Name: "mid"}
	end := &ast.Ident{Name: "end"}
	facts := NewGuardFactSet()
	facts.AddLE(start, mid)
	facts.AddLE(mid, end)
	if !facts.ProveLE(start, end) {
		t.Fatalf("expected transitive guard proof start <= end, got %#v", facts)
	}

	boxExpr := &ast.Ident{Name: "box"}
	boxType := &StructType{Name: "Box", Fields: map[string]Field{"value": {Name: "value", Type: &BuiltinType{Name: "i32"}}}}
	boxRef := &RefType{Elem: boxType, State: RefStateNullable}
	if facts.CheckFieldAccess(boxExpr, boxRef, "value") {
		t.Fatalf("expected nullable box ref to require a non-null guard")
	}
	facts.AddNonNull(boxExpr)
	if !facts.CheckFieldAccess(boxExpr, boxRef, "value") {
		t.Fatalf("expected non-null guard to allow box.value access")
	}

	nodeExpr := &ast.Ident{Name: "node"}
	variant := &EnumVariant{Name: "Int", Payload: []Type{&BuiltinType{Name: "i32"}}, PayloadNames: []string{"value"}}
	enumType := &EnumType{
		Name:       "Expr",
		Packed:     true,
		Common:     map[string]Field{"span": {Name: "span", Type: &BuiltinType{Name: "i32"}}},
		Variants:   []*EnumVariant{variant},
		VariantMap: map[string]*EnumVariant{"Int": variant},
	}
	if facts.CheckFieldAccess(nodeExpr, enumType, "value") {
		t.Fatalf("expected packed enum payload field access to require a variant proof")
	}
	facts.AddPackedVariant(nodeExpr, variant.PackedViewType(enumType))
	if !facts.CheckFieldAccess(nodeExpr, enumType, "value") {
		t.Fatalf("expected packed variant proof to allow payload field access")
	}
}

func TestCreateTypeBoundOpsSynthesizesRecursiveCleanupOps(t *testing.T) {
	threadPool := &StructType{Name: "ThreadPool", Builtin: true}
	mutexGuardBase := &StructType{Name: "MutexGuard", Builtin: true}
	held := &BuiltinType{Name: "Held"}
	guard := &GenericInstanceType{Name: "MutexGuard", Base: mutexGuardBase, Args: []Type{held}}
	wrapper := &StructType{Name: "Wrapper", Fields: map[string]Field{
		"pool":  {Name: "pool", Type: threadPool},
		"guard": {Name: "guard", Type: guard},
	}}

	ops := CreateTypeBoundOps(wrapper)
	if len(ops) != 2 {
		t.Fatalf("expected wrapper cleanup synthesis to produce two direct field ops, got %#v", ops)
	}
	seenPool := false
	seenGuard := false
	for _, op := range ops {
		if len(op.Path) != 1 {
			t.Fatalf("expected direct field cleanup path, got %#v", op)
		}
		switch op.Path[0] {
		case "pool":
			if op.Kind != TypeBoundCleanupThreadPoolShutdown {
				t.Fatalf("expected pool field to synthesize thread-pool shutdown, got %#v", op)
			}
			seenPool = true
		case "guard":
			if op.Kind != TypeBoundCleanupMutexUnlock {
				t.Fatalf("expected guard field to synthesize mutex unlock, got %#v", op)
			}
			seenGuard = true
		}
	}
	if !seenPool || !seenGuard {
		t.Fatalf("expected both pool and guard cleanup ops, got %#v", ops)
	}

	seqOps := CreateTypeBoundOps(&ArrayType{Elem: threadPool, HasConstSize: true, ConstSize: 2})
	if len(seqOps) != 1 || !seqOps[0].IsFillSeq() || len(seqOps[0].Sequence) != 1 || seqOps[0].Sequence[0].Kind != TypeBoundCleanupThreadPoolShutdown {
		t.Fatalf("expected array cleanup synthesis to wrap element ops in a fill-seq op, got %#v", seqOps)
	}
}

func TestAnalyzeDeferFunctionRecordsCaptureMetadata(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "defer_capture.llcontext", `def keep(seed: int) -> int:
	value: int = seed
	defer function:
		_ = value
	return value
`)
	decl, ok := result.File.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", result.File.Decls[0])
	}
	stmt, ok := decl.Body[1].(*ast.DeferStmt)
	if !ok {
		t.Fatalf("expected defer stmt, got %T", decl.Body[1])
	}
	info, ok := result.Defer[stmt]
	if !ok || info == nil {
		t.Fatal("expected semantic defer metadata to be recorded")
	}
	if info.Mode != ast.DeferModeFunction {
		t.Fatalf("expected function defer mode, got %v", info.Mode)
	}
	if len(info.Captures) != 1 || info.Captures[0] != "value" {
		t.Fatalf("expected function defer to capture value, got %#v", info.Captures)
	}
}

func TestAnalyzeDeferBodyRejectsReturn(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "defer_return_invalid.llcontext", `def keep() -> int:
	defer block:
		return 1
	return 0
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "defer body cannot return from the enclosing function") {
		t.Fatalf("expected defer return diagnostic, got:\n%s", errText)
	}
}

func TestAnalyzeDeferFunctionRejectsNestedScope(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "defer_function_nested_invalid.llcontext", `def keep(flag: bool) -> int:
	if flag:
		defer function:
			pass
	return 0
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, "defer function is currently only supported in the outermost function scope") {
		t.Fatalf("expected nested function defer diagnostic, got:\n%s", errText)
	}
}
