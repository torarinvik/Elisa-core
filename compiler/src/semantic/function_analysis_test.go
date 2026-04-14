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

func TestAnalyzeVariantAndLetConditionBindings(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "variant_let_bindings.llcontext", `enum Expr:
	Int(value: i64)
	Pair(left: i64, right: i64)

def score(node: Expr, maybe: i64?, enabled: bool) -> i64:
	guard enabled else return 0
	if let value = maybe and node is Expr.Pair(left, right):
		return value + left + right
	return 0
`)
}

func TestAnalyzeLetConditionRejectsNonOptionalValue(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "let_bind_non_optional.llcontext", `def bad(value: i64) -> bool:
	return let item = value
`)
	if len(result.Errors()) == 0 {
		t.Fatal("expected semantic error for let-binding a non-optional value")
	}
	if !strings.Contains(result.Errors()[0], "let condition requires an optional or nullable reference") {
		t.Fatalf("expected let-condition diagnostic, got %v", result.Errors())
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

	treeNodeExpr := &ast.Ident{Name: "treeNode"}
	treeBinaryVariant := &EnumVariant{Name: "Binary", Payload: []Type{&BuiltinType{Name: "i64"}}, PayloadNames: []string{"left"}}
	treeCategory := &TreeCategoryType{Name: "Lua.Expr", Common: map[string]Field{"span": {Name: "span", Type: &BuiltinType{Name: "i64"}}}, Variants: []*EnumVariant{treeBinaryVariant}, VariantMap: map[string]*EnumVariant{"Binary": treeBinaryVariant}}
	if !facts.CheckFieldAccess(treeNodeExpr, treeCategory, "span") {
		t.Fatalf("expected tree category common field access to be allowed")
	}
	facts.AddVariantProof(treeNodeExpr, "Lua.Expr", "Binary")
	if !facts.CheckFieldAccess(treeNodeExpr, treeCategory, "left") {
		t.Fatalf("expected tree category variant proof to allow payload field access")
	}

	treeBlockExpr := &ast.Ident{Name: "treeBlock"}
	treeBlock := &TreeBlockType{Name: "Lua.Block", Fields: map[string]Field{"stmts": {Name: "stmts", Type: &DArrayType{Elem: &BuiltinType{Name: "i64"}, Shape: &WildcardShape{}, SurfaceName: "darray"}}}}
	if !facts.CheckFieldAccess(treeBlockExpr, treeBlock, "stmts") {
		t.Fatalf("expected tree block field access to be allowed")
	}

	treeStructExpr := &ast.Ident{Name: "treeStruct"}
	treeStruct := &TreeStructType{Name: "Lua.ElseIf", Fields: map[string]Field{"condition": {Name: "condition", Type: treeCategory}}}
	if !facts.CheckFieldAccess(treeStructExpr, treeStruct, "condition") {
		t.Fatalf("expected tree struct field access to be allowed")
	}
}

func TestGuardFactsForConditionRecordsTreeVariantProof(t *testing.T) {
	nodeExpr := &ast.Ident{Name: "node"}
	cond := &ast.BinaryExpr{
		Op:   lexer.TOKEN_IS,
		Left: nodeExpr,
		Right: &ast.FieldExpr{
			Object: &ast.FieldExpr{Object: &ast.Ident{Name: "Lua"}, Field: "Expr"},
			Field:  "Binary",
		},
	}
	facts := GuardFactsForCondition(cond, true)
	guard, ok := facts.PackedVariant(nodeExpr)
	if !ok {
		t.Fatalf("expected tree is-condition to record variant proof, got %#v", facts)
	}
	if guard.EnumName != "Lua.Expr" || guard.VariantName != "Binary" {
		t.Fatalf("expected Lua.Expr.Binary variant proof, got %#v", guard)
	}
}

func TestGuardFactsForConditionRecordsIsVariantProof(t *testing.T) {
	nodeExpr := &ast.Ident{Name: "node"}
	cond := &ast.BinaryExpr{
		Op:    lexer.TOKEN_IS,
		Left:  nodeExpr,
		Right: &ast.FieldExpr{Object: &ast.Ident{Name: "Expr"}, Field: "Int"},
	}
	facts := GuardFactsForCondition(cond, true)
	guard, ok := facts.PackedVariant(nodeExpr)
	if !ok {
		t.Fatalf("expected is-condition to record packed variant proof, got %#v", facts)
	}
	if guard.EnumName != "Expr" || guard.VariantName != "Int" {
		t.Fatalf("expected Expr.Int packed variant proof, got %#v", guard)
	}
}

func TestAnalyzeFunctionAnalysisCFGRecordsGuardNonnullCallFacts(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "guard_nonnull_cfg.llcontext", `repr(c) struct Box:
	value: i32

@guard_nonnull(box)
def has_box(box: heap Box&?) -> bool:
	return box != null

def read_box(box: heap Box&?) -> i32:
	if has_box(box):
		return box.value
	return 0
`)
	analysis, ok := result.FunctionAnalysisByName("read_box")
	if !ok || analysis == nil || analysis.CFG == nil {
		t.Fatal("expected read_box function analysis CFG")
	}
	var sawNonNullGuard bool
	for _, edge := range analysis.CFG.Blocks[analysis.CFG.Entry].Edges {
		if edge.Guard.ProvesNonNull(&ast.Ident{Name: "box"}) {
			sawNonNullGuard = true
		}
	}
	if !sawNonNullGuard {
		t.Fatalf("expected entry CFG edges to carry @guard_nonnull facts, got %#v", analysis.CFG.Blocks[analysis.CFG.Entry].Edges)
	}
}

func TestAnalyzeFunctionAnalysisCFGRecordsGuardVariantCallFacts(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "guard_variant_cfg.llcontext", `packed enum Expr:
	common:
		span: i32
	Int(value: i32)
	Add(left: Expr, right: Expr)

@guard_variant(node, Expr.Int)
def is_int(node: Expr) -> bool:
	return node is Expr.Int

def fold(node: Expr) -> i32:
	if is_int(node):
		return node.value + node.span
	return 0
`)
	analysis, ok := result.FunctionAnalysisByName("fold")
	if !ok || analysis == nil || analysis.CFG == nil {
		t.Fatal("expected fold function analysis CFG")
	}
	var sawVariantGuard bool
	for _, edge := range analysis.CFG.Blocks[analysis.CFG.Entry].Edges {
		guard, ok := edge.Guard.PackedVariant(&ast.Ident{Name: "node"})
		if ok && guard.EnumName == "Expr" && guard.VariantName == "Int" {
			sawVariantGuard = true
		}
	}
	if !sawVariantGuard {
		t.Fatalf("expected entry CFG edges to carry @guard_variant facts, got %#v", analysis.CFG.Blocks[analysis.CFG.Entry].Edges)
	}
}

func TestAnalyzeFunctionAnalysisCFGRecordsTreeGuardVariantCallFacts(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "tree_guard_variant_cfg.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

@guard_variant(node, Lua.Expr.Binary)
def is_binary(node: Lua.Expr) -> bool:
	return node is Lua.Expr.Binary

def fold(node: Lua.Expr) -> i64:
	if is_binary(node):
		return node.left.span + node.right.span + node.span
	return node.span
`)
	analysis, ok := result.FunctionAnalysisByName("fold")
	if !ok || analysis == nil || analysis.CFG == nil {
		t.Fatal("expected fold function analysis CFG")
	}
	var sawVariantGuard bool
	for _, edge := range analysis.CFG.Blocks[analysis.CFG.Entry].Edges {
		guard, ok := edge.Guard.PackedVariant(&ast.Ident{Name: "node"})
		if ok && guard.EnumName == "Lua.Expr" && guard.VariantName == "Binary" {
			sawVariantGuard = true
		}
	}
	if !sawVariantGuard {
		t.Fatalf("expected entry CFG edges to carry tree @guard_variant facts, got %#v", analysis.CFG.Blocks[analysis.CFG.Entry].Edges)
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
