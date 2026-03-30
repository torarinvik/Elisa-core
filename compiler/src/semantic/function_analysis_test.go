package semantic

import (
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
