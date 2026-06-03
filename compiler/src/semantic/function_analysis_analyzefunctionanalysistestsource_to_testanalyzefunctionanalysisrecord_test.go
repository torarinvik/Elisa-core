package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"strings"
	"testing"
)

func analyzeFunctionAnalysisTestSource(t *testing.T, filename string, src string) *Result {
	t.Helper()
	return analyzeFunctionAnalysisTestSourceWithOptions(t, filename, src, AnalyzeOptions{})
}

func analyzeFunctionAnalysisTestSourceWithOptions(t *testing.T, filename string, src string, options AnalyzeOptions) *Result {
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
	result := AnalyzeWithOptions(file, options)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	return result
}

// analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics runs analysis
// with the given options WITHOUT failing on semantic errors, so tests can assert
// on error-level diagnostics (e.g. Unsafe.* memory-safety gates, which are hard
// errors under EnforceUnsafePermissions).
func analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t *testing.T, filename string, src string, options AnalyzeOptions) *Result {
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
	return AnalyzeWithOptions(file, options)
}

// allDiagnostics joins errors and warnings into one string, so assertions about
// whether a diagnostic is present/absent are robust to its severity (Unsafe.*
// gates are now errors; effect-authority diagnostics remain warnings).
func allDiagnostics(result *Result) string {
	return strings.Join(append(append([]string{}, result.Errors()...), result.Warnings()...), "\n")
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
func hasFactTransform(transforms []FactTransform, kind FactTransformKind, class FactClass, target string, reason string) bool {
	for _, transform := range transforms {
		if transform.Kind != kind || transform.Target != target || transform.Reason != reason {
			continue
		}
		foundClass := false
		for _, candidate := range transform.Classes {
			if candidate == class {
				foundClass = true
				break
			}
		}
		if !foundClass {
			continue
		}
		return true
	}
	return false
}
func hasString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func TestAnalyzeInfersDirectSinkParamSummary(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "direct_sink_summary.elisa", `extern join(thread: Thread[i64, Joinable]) -> i64 can[Thread.Join]

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
	analyzeFunctionAnalysisTestSource(t, "variant_let_bindings.elisa", `enum Expr:
	Int(value: i64)
	Pair(left: i64, right: i64)

def score(node: Expr, maybe: i64?, enabled: bool) -> i64:
	guard enabled else return 0
	if let value = maybe and node is Expr.Pair(left, right):
		return value + left + right
	return 0
`)
}
func TestAnalyzeIfLetConditionBindsNullableReferenceAsNonNull(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "if_let_nullable_ref_binding.elisa", `struct Node:
	value: i64

def read(node: Node&?) -> i64:
	if let present = node:
		return present.value
	return 0
`)
}
func TestAnalyzeReturnQuestionEarlyReturnsOptionalPayload(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "return_question_optional_payload.elisa", `def first(left: i64?, right: i64?) -> i64?:
	return left else return null
	return right else return null
	return null
`)
}
func TestAnalyzeLetConditionRejectsNonOptionalValue(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "let_bind_non_optional.elisa", `def bad(value: i64) -> bool:
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
	result := analyzeFunctionAnalysisTestSource(t, "guard_nonnull_cfg.elisa", `struct Box:
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
	if !hasFactTransform(analysis.FactTransforms, FactTransformRefine, FactRefState, "box", "guard proves non-null") {
		t.Fatalf("expected function analysis to expose non-null refine transform, got %#v", analysis.FactTransforms)
	}
}
func TestAnalyzeFunctionAnalysisCFGRecordsGuardVariantCallFacts(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "guard_variant_cfg.elisa", `packed enum Expr:
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
	if !hasFactTransform(analysis.FactTransforms, FactTransformRefine, FactTypestate, "node", "guard proves variant Expr.Int") {
		t.Fatalf("expected function analysis to expose variant refine transform, got %#v", analysis.FactTransforms)
	}
}
func TestAnalyzeFunctionAnalysisCFGRecordsTreeGuardVariantCallFacts(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "tree_guard_variant_cfg.elisa", `tree Lua:
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
	if !hasFactTransform(analysis.FactTransforms, FactTransformRefine, FactTypestate, "node", "guard proves variant Lua.Expr.Binary") {
		t.Fatalf("expected function analysis to expose tree variant refine transform, got %#v", analysis.FactTransforms)
	}
}
func TestAnalyzeFunctionAnalysisRecordsConservativeCallWidening(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "call_widening_fact_transform.elisa", `struct Player[state Alive | Dead]:
	health: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

extern unknown_update(mutable player: Player[Alive]&) -> void

def use(mutable player: Player[Alive]&) -> void:
	unknown_update(player)
`)
	analysis, ok := result.FunctionAnalysisByName("use")
	if !ok || analysis == nil {
		t.Fatal("expected analysis for use")
	}
	if len(analysis.FactTransforms) != 1 {
		t.Fatalf("expected one fact transform, got %#v", analysis.FactTransforms)
	}
	transform := analysis.FactTransforms[0]
	if transform.Kind != FactTransformWiden || transform.Target != "player" || transform.Reason != "ref call without matching ensures" {
		t.Fatalf("unexpected fact transform: %#v", transform)
	}
	if transform.Source != "unknown_update(player)" || transform.SourceKind != FactSourceCallWiden {
		t.Fatalf("expected call-site widening source, got %#v", transform)
	}
	if transform.SourcePos.IsZero() || factTransformDetailValue(transform.Details, "before") == "" || factTransformDetailValue(transform.Details, "after") == "" {
		t.Fatalf("expected widening transform to include source position and before/after details, got %#v", transform)
	}
	if len(transform.Classes) != 1 || transform.Classes[0] != FactTypestate {
		t.Fatalf("expected typestate fact class, got %#v", transform.Classes)
	}
	if !hasString(analysis.FactSnapshot.Widened, "player") {
		t.Fatalf("expected fact snapshot to record widened player, got %#v", analysis.FactSnapshot)
	}
	if len(analysis.FactSnapshot.PathFacts) == 0 || analysis.FactSnapshot.PathFacts[0].Root != "player" {
		t.Fatalf("expected path-aware snapshot entry for player, got %#v", analysis.FactSnapshot.PathFacts)
	}
}
func TestAnalyzeFunctionAnalysisRecordsConsumeAndRecomputeTransforms(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "flow_fact_transforms.elisa", `struct Player[state Alive | Dead]:
	health: mutable int

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

extern join(thread: Thread[i64, Joinable]) -> i64 can[Thread.Join]

def update(mutable player: Player[Alive]&, thread: Thread[i64, Joinable]) -> i64 can[Thread.Join]:
	player.health <- player.health + 1
	return join(move thread)
`)
	analysis, ok := result.FunctionAnalysisByName("update")
	if !ok || analysis == nil {
		t.Fatal("expected update function analysis")
	}
	if !hasFactTransform(analysis.FactTransforms, FactTransformRecompute, FactTypestate, "player.health", "assign") {
		t.Fatalf("expected function analysis to expose assignment recompute transform, got %#v", analysis.FactTransforms)
	}
	if !hasFactTransform(analysis.FactTransforms, FactTransformConsume, FactUsage, "thread", "explicit move") {
		t.Fatalf("expected function analysis to expose explicit move consume transform, got %#v", analysis.FactTransforms)
	}
	if !hasString(analysis.FactSnapshot.Consumed, "thread") || !hasString(analysis.FactSnapshot.RequiredEffects, "Thread.Join") {
		t.Fatalf("expected fact snapshot to record consumed thread and required Thread.Join, got %#v", analysis.FactSnapshot)
	}
	if !hasString(analysis.EffectSummary.Required, "Thread.Join") {
		t.Fatalf("expected effect summary to record required Thread.Join, got %#v", analysis.EffectSummary)
	}
	if len(analysis.BlockFactTransforms) == 0 || len(analysis.CFG.Blocks[analysis.CFG.Entry].FactTransforms) == 0 {
		t.Fatalf("expected per-block fact transforms, got blocks=%#v cfg=%#v", analysis.BlockFactTransforms, analysis.CFG.Blocks)
	}
}
func TestAnalyzeFunctionAnalysisRecordsProduceAndRebaseTransforms(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "produce_rebase_fact_transforms.elisa", `struct RegionNode[region owner]:
	next: RegionNode&? @owner
	value: i32

packed enum Expr:
	common:
		span: int
	Int(value: int)

def build_region(seed: i32) -> i32:
	region scratch(1024)
	first: RegionNode[scratch]& @scratch = new[scratch] RegionNode[scratch](null, seed)
	out: i32 = first.value
	destroy scratch
	return out

def freeze_expr_store(owner: Arena) -> Expr.Store[Frozen]:
	store: Expr.Store[Local] = Expr.Store(owner)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return frozen
`)
	regionAnalysis, ok := result.FunctionAnalysisByName("build_region")
	if !ok || regionAnalysis == nil {
		t.Fatal("expected build_region function analysis")
	}
	if !hasFactTransform(regionAnalysis.FactTransforms, FactTransformProduce, FactStorage, "first", "allocation produces value") {
		t.Fatalf("expected function analysis to expose region allocation produce transform, got %#v", regionAnalysis.FactTransforms)
	}
	freezeAnalysis, ok := result.FunctionAnalysisByName("freeze_expr_store")
	if !ok || freezeAnalysis == nil {
		t.Fatal("expected freeze_expr_store function analysis")
	}
	if !hasFactTransform(freezeAnalysis.FactTransforms, FactTransformRebase, FactStoreDeps, "store", "freeze rebases store provenance") {
		t.Fatalf("expected function analysis to expose store rebase transform, got %#v", freezeAnalysis.FactTransforms)
	}
	if !hasFactTransform(freezeAnalysis.FactTransforms, FactTransformProduce, FactStorage, "frozen", "freeze produces frozen store") {
		t.Fatalf("expected function analysis to expose frozen store produce transform, got %#v", freezeAnalysis.FactTransforms)
	}
	if !hasString(freezeAnalysis.FactSnapshot.RebasedStores, "store") || !hasString(freezeAnalysis.FactSnapshot.Produced, "frozen") {
		t.Fatalf("expected fact snapshot to record freeze rebase and production, got %#v", freezeAnalysis.FactSnapshot)
	}
	if !hasString(freezeAnalysis.FactSnapshot.HandleStoreDeps, "frozen<-store") {
		t.Fatalf("expected fact snapshot to record frozen store dependency, got %#v", freezeAnalysis.FactSnapshot)
	}
}
func TestBuildFunctionFactSnapshotRecordsStoreDependencyLabels(t *testing.T) {
	enumType := &EnumType{Name: "Expr"}
	frozenState := &BuiltinType{Name: "Frozen"}
	storeType := &PackedEnumStoreType{Name: "Expr.Store", Enum: enumType, State: frozenState}
	storeSym := &Symbol{Name: "store", Type: storeType}
	fnType := &FuncType{
		Return: &BuiltinType{Name: "Expr"},
		ReturnProvenance: regionRefState{StoreDeps: map[*Symbol]packedStoreDependencyState{
			storeSym: {Type: storeType},
		}},
	}

	snapshot := buildFunctionFactSnapshot(fnType, &CFG{ParamLocations: []string{"owner"}}, nil, nil)
	if !hasString(snapshot.Parameters, "owner") || !hasString(snapshot.StoreDeps, "Expr.Store[Frozen]") {
		t.Fatalf("expected snapshot to include parameter and packed store dependency label, got %#v", snapshot)
	}
}
func TestAnalyzeFunctionAnalysisRecordsRegionInvalidateTransforms(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "region_invalidate_fact_transforms.elisa", `def checkpoint_demo(seed: i32) -> i32:
	region scratch(1024)
	mark scratch as cp
	temp: i32& @scratch = new[scratch] seed + 1
	restore scratch from cp
	reset scratch
	destroy scratch
	return seed

def destroy_demo() -> void:
	region scratch(1024)
	destroy scratch
`)
	checkpointAnalysis, ok := result.FunctionAnalysisByName("checkpoint_demo")
	if !ok || checkpointAnalysis == nil {
		t.Fatal("expected checkpoint_demo function analysis")
	}
	if !hasFactTransform(checkpointAnalysis.FactTransforms, FactTransformInvalidate, FactRegionDeps, "scratch", "restore region checkpoint") {
		t.Fatalf("expected function analysis to expose restore invalidate transform, got %#v", checkpointAnalysis.FactTransforms)
	}
	for _, transform := range checkpointAnalysis.FactTransforms {
		if transform.Kind == FactTransformInvalidate && transform.Target == "scratch" && transform.Reason == "restore region checkpoint" {
			if len(transform.Details) == 0 || transform.Details[0].Name != "operation" || transform.Details[0].Value != "restore region checkpoint" {
				t.Fatalf("expected invalidate transform details, got %#v", transform)
			}
		}
	}
	var sawGenerationDetails bool
	for _, transform := range checkpointAnalysis.FactTransforms {
		if transform.Kind == FactTransformInvalidate && transform.Target == "scratch" && factTransformDetailValue(transform.Details, "generation_before") != "" && factTransformDetailValue(transform.Details, "generation_after") != "" {
			sawGenerationDetails = true
		}
	}
	if !sawGenerationDetails {
		t.Fatalf("expected invalidate transform to include region generation details, got %#v", checkpointAnalysis.FactTransforms)
	}
	if len(checkpointAnalysis.FactSnapshot.RegionDeps) == 0 {
		t.Fatalf("expected snapshot to include region generation dependency keys, got %#v", checkpointAnalysis.FactSnapshot)
	}
	if !hasFactTransform(checkpointAnalysis.FactTransforms, FactTransformInvalidate, FactRegionDeps, "scratch", "reset region") {
		t.Fatalf("expected function analysis to expose reset invalidate transform, got %#v", checkpointAnalysis.FactTransforms)
	}
	destroyAnalysis, ok := result.FunctionAnalysisByName("destroy_demo")
	if !ok || destroyAnalysis == nil {
		t.Fatal("expected destroy_demo function analysis")
	}
	if !hasFactTransform(destroyAnalysis.FactTransforms, FactTransformInvalidate, FactRegionDeps, "scratch", "destroy region") {
		t.Fatalf("expected function analysis to expose destroy invalidate transform, got %#v", destroyAnalysis.FactTransforms)
	}
}
func TestAnalyzeFunctionAnalysisRecordsAliasClassTransforms(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "alias_fact_transforms.elisa", `struct RegionNode[region owner]:
	next: RegionNode&? @owner
	value: i32

def alias_region_ref(seed: i32) -> i32:
	region scratch(1024)
	first: RegionNode[scratch]& @scratch = new[scratch] RegionNode[scratch](null, seed)
	alias: mutable RegionNode[scratch]& @scratch = first
	out: i32 = alias.value
	destroy scratch
	return out
`)
	analysis, ok := result.FunctionAnalysisByName("alias_region_ref")
	if !ok || analysis == nil {
		t.Fatal("expected alias_region_ref function analysis")
	}
	if !hasFactTransform(analysis.FactTransforms, FactTransformRefine, FactAliasClass, "alias", "var init alias") {
		t.Fatalf("expected function analysis to expose alias-class refine transform, got %#v", analysis.FactTransforms)
	}
	if len(analysis.AliasSets) == 0 || !hasString(analysis.FactSnapshot.AliasClasses, "alias-class#0") {
		t.Fatalf("expected explicit alias set and snapshot alias class, got sets=%#v snapshot=%#v", analysis.AliasSets, analysis.FactSnapshot)
	}
}
func TestAnalyzeFunctionAnalysisRecordsAliasClassMutationTransforms(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "alias_mutation_fact_transforms.elisa", `struct RegionNode[region owner]:
	next: RegionNode&? @owner
	value: mutable i32

def alias_region_mutation(seed: i32) -> i32:
	region scratch(1024)
	first: RegionNode[scratch]& @scratch = new[scratch] RegionNode[scratch](null, seed)
	alias: mutable RegionNode[scratch]& @scratch = first
	alias.value <- alias.value + 1
	out: i32 = first.value
	destroy scratch
	return out
`)
	analysis, ok := result.FunctionAnalysisByName("alias_region_mutation")
	if !ok || analysis == nil {
		t.Fatal("expected alias_region_mutation function analysis")
	}
	if !hasFactTransform(analysis.FactTransforms, FactTransformRecompute, FactAliasClass, "alias-class#0", "mutation recomputes facts for alias class") {
		t.Fatalf("expected alias-class mutation recompute transform, got %#v", analysis.FactTransforms)
	}
	if !hasFactTransform(analysis.FactTransforms, FactTransformRecompute, FactStoreDeps, "alias", "alias mutation recomputes dependent path facts") {
		t.Fatalf("expected alias mutation to recompute dependent path facts, got %#v", analysis.FactTransforms)
	}
}
