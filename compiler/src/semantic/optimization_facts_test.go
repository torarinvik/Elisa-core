package semantic

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/parser"
)

func parseAndAnalyzeOptimizationFactsTest(t *testing.T, filename string, src string) (*ast.File, *Result) {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) > 0 {
		t.Fatalf("lexer errors:\n%s", strings.Join(errs, "\n"))
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parse errors:\n%s", strings.Join(errs, "\n"))
	}
	result := Analyze(file)
	return file, result
}

func testFuncDeclByName(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name == name {
			return fn
		}
	}
	t.Fatalf("expected function %q to be present", name)
	return nil
}

func mustVarDeclValueExpr(t *testing.T, stmt ast.Stmt, name string) ast.Expr {
	t.Helper()
	decl, ok := stmt.(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected %q declaration to be a var decl, got %T", name, stmt)
	}
	if decl.Name != name {
		t.Fatalf("expected var decl %q, got %q", name, decl.Name)
	}
	if decl.Value == nil {
		t.Fatalf("expected var decl %q to have a value", name)
	}
	return decl.Value
}

func mustExprStmtCall(t *testing.T, stmt ast.Stmt, name string) *ast.CallExpr {
	t.Helper()
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected expression statement for %q, got %T", name, stmt)
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expression for %q, got %T", name, exprStmt.Expr)
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident.Name != name {
		t.Fatalf("expected call to %q, got %#v", name, call.Func)
	}
	return call
}

func mustExtentBounds(t *testing.T, extent *OptimizationExtent, wantBegin int64, wantEnd int64) {
	t.Helper()
	if extent == nil {
		t.Fatalf("expected optimization extent, got nil")
	}
	begin, ok := optimizationConstInt(extent.Begin)
	if !ok {
		t.Fatalf("expected constant extent begin, got %q", extent.Begin)
	}
	end, ok := optimizationConstInt(extent.End)
	if !ok {
		t.Fatalf("expected constant extent end, got %q", extent.End)
	}
	if begin != wantBegin || end != wantEnd {
		t.Fatalf("expected extent %d:%d, got %s:%s", wantBegin, wantEnd, extent.Begin, extent.End)
	}
}

func mustAffineExprTerms(t *testing.T, value string, wantConst int64, wantTerms map[string]int64) {
	t.Helper()
	parsed, ok := parseOptimizationAffineExpr(value)
	if !ok {
		t.Fatalf("expected affine expression, got %q", value)
	}
	if parsed.Const != wantConst {
		t.Fatalf("expected affine constant %d for %q, got %d", wantConst, value, parsed.Const)
	}
	if len(parsed.Terms) != len(wantTerms) {
		t.Fatalf("expected affine terms %#v for %q, got %#v", wantTerms, value, parsed.Terms)
	}
	for term, coeff := range wantTerms {
		if parsed.Terms[term] != coeff {
			t.Fatalf("expected affine term %q to have coefficient %d in %q, got %#v", term, coeff, value, parsed.Terms)
		}
	}
}

func TestOptimizationFactsMarkConstantChunksExactItemsDisjoint(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "chunks_exact_disjoint.llcontext", `
def kernel(buf: dview[i32]) -> void:
	ro: dview[i32] = readonly(buf)
	chunks: ChunksExactView[i32] = chunks_exact(ro, 4u)
	first: dview[i32] = chunks[0u]
	second: dview[i32] = chunks[1u]
	third: dview[i32] = chunks[2u]
	pass
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("semantic errors:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	if len(fn.Body) < 5 {
		t.Fatalf("expected kernel body to contain chunk declarations, got %d statements", len(fn.Body))
	}
	firstExpr := mustVarDeclValueExpr(t, fn.Body[2], "first")
	secondExpr := mustVarDeclValueExpr(t, fn.Body[3], "second")
	thirdExpr := mustVarDeclValueExpr(t, fn.Body[4], "third")

	if !result.ExprsHaveEqualExtentSize(firstExpr, secondExpr) {
		t.Fatalf("expected first and second chunk items to have equal extent size")
	}
	if !result.ExprsHaveEqualExtentSize(firstExpr, thirdExpr) {
		t.Fatalf("expected first and third chunk items to have equal extent size")
	}
	if !result.ExprsAreDisjoint(firstExpr, secondExpr) {
		t.Fatalf("expected first and second chunk items to be provably disjoint")
	}
	if !result.ExprsAreDisjoint(firstExpr, thirdExpr) {
		t.Fatalf("expected first and third chunk items to be provably disjoint")
	}

	firstFacts, ok := result.ExprOptimizationFacts(firstExpr)
	if !ok || firstFacts.Extent == nil {
		t.Fatalf("expected first chunk item to carry optimization facts")
	}
	mustExtentBounds(t, firstFacts.Extent, 0, 4)
	thirdFacts, ok := result.ExprOptimizationFacts(thirdExpr)
	if !ok || thirdFacts.Extent == nil {
		t.Fatalf("expected third chunk item to carry optimization facts")
	}
	mustExtentBounds(t, thirdFacts.Extent, 8, 12)
}

func TestAnalyzeZipMapAcceptsDisjointChunksExactItemsFromSharedBuffer(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "zip_map_chunks_exact_disjoint.llcontext", `
def add(left: i32, right: i32) -> i32:
	return left + right

def kernel(buf: dview[i32]) -> void:
	ro: dview[i32] = readonly(buf)
	ro_chunks: ChunksExactView[i32] = chunks_exact(ro, 4u)
	rw_chunks: ChunksExactView[i32] = chunks_exact(buf, 4u)
	zip_map(rw_chunks[0u], ro_chunks[1u], ro_chunks[2u], add)
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("expected zip_map over disjoint chunk items to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	call := mustExprStmtCall(t, fn.Body[len(fn.Body)-1], "zip_map")
	if len(call.Args) != 4 {
		t.Fatalf("expected zip_map to have 4 arguments, got %d", len(call.Args))
	}
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[1]) {
		t.Fatalf("expected zip_map destination and source 1 chunk items to have equal extent size")
	}
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[2]) {
		t.Fatalf("expected zip_map destination and source 2 chunk items to have equal extent size")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[1]) {
		t.Fatalf("expected zip_map destination and source 1 chunk items to be disjoint")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[2]) {
		t.Fatalf("expected zip_map destination and source 2 chunk items to be disjoint")
	}
}

func TestOptimizationFactsComposeSplitAtAndChunksExactItemOffsets(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "split_chunks_exact_offsets.llcontext", `
def kernel(buf: dview[i32]) -> void:
	whole: dview[i32] = buf[0u:16u]
	parts: SplitView[i32] = split_at(whole, 8u)
	left_ro: dview[i32] = readonly(parts.left)
	right_ro: dview[i32] = readonly(parts.right)
	left_chunks: ChunksExactView[i32] = chunks_exact(left_ro, 4u)
	right_chunks: ChunksExactView[i32] = chunks_exact(right_ro, 4u)
	left0: dview[i32] = left_chunks[0u]
	right0: dview[i32] = right_chunks[0u]
	right1: dview[i32] = right_chunks[1u]
	pass
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("semantic errors:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	left0 := mustVarDeclValueExpr(t, fn.Body[6], "left0")
	right0 := mustVarDeclValueExpr(t, fn.Body[7], "right0")
	right1 := mustVarDeclValueExpr(t, fn.Body[8], "right1")

	leftFacts, ok := result.ExprOptimizationFacts(left0)
	if !ok || leftFacts.Extent == nil {
		t.Fatalf("expected left0 chunk item facts")
	}
	mustExtentBounds(t, leftFacts.Extent, 0, 4)
	right0Facts, ok := result.ExprOptimizationFacts(right0)
	if !ok || right0Facts.Extent == nil {
		t.Fatalf("expected right0 chunk item facts")
	}
	mustExtentBounds(t, right0Facts.Extent, 8, 12)
	right1Facts, ok := result.ExprOptimizationFacts(right1)
	if !ok || right1Facts.Extent == nil {
		t.Fatalf("expected right1 chunk item facts")
	}
	mustExtentBounds(t, right1Facts.Extent, 12, 16)
	if !result.ExprsAreDisjoint(left0, right0) {
		t.Fatalf("expected left0 and right0 chunk items to be disjoint")
	}
	if !result.ExprsAreDisjoint(left0, right1) {
		t.Fatalf("expected left0 and right1 chunk items to be disjoint")
	}
	if !result.ExprsAreDisjoint(right0, right1) {
		t.Fatalf("expected right0 and right1 chunk items to be disjoint")
	}
}

func TestAnalyzeZipMapAcceptsSplitChunksExactComposition(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "zip_map_split_chunks_exact_disjoint.llcontext", `
def add(left: i32, right: i32) -> i32:
	return left + right

def kernel(buf: dview[i32]) -> void:
	whole: dview[i32] = buf[0u:16u]
	parts: SplitView[i32] = split_at(whole, 8u)
	left_chunks: ChunksExactView[i32] = chunks_exact(parts.left, 4u)
	right_ro: dview[i32] = readonly(parts.right)
	right_chunks: ChunksExactView[i32] = chunks_exact(right_ro, 4u)
	zip_map(left_chunks[0u], right_chunks[0u], right_chunks[1u], add)
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("expected composed split/chunks zip_map to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	call := mustExprStmtCall(t, fn.Body[len(fn.Body)-1], "zip_map")
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[1]) {
		t.Fatalf("expected composed zip_map destination and source 1 chunk items to have equal extent size")
	}
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[2]) {
		t.Fatalf("expected composed zip_map destination and source 2 chunk items to have equal extent size")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[1]) {
		t.Fatalf("expected composed zip_map destination and source 1 chunk items to be disjoint")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[2]) {
		t.Fatalf("expected composed zip_map destination and source 2 chunk items to be disjoint")
	}
}

func TestOptimizationFactsSupportAffineSplitChunkComposition(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "affine_split_chunks_exact_offsets.llcontext", `
def kernel(buf: dview[i32], start: usize, chunk: usize) -> void:
	limit: usize = start + (4u * chunk)
	whole: dview[i32] = buf[start:limit]
	parts: SplitView[i32] = split_at(whole, (2u * chunk))
	left_chunks: ChunksExactView[i32] = chunks_exact(parts.left, chunk)
	right_ro: dview[i32] = readonly(parts.right)
	right_chunks: ChunksExactView[i32] = chunks_exact(right_ro, chunk)
	left0: dview[i32] = left_chunks[0u]
	right0: dview[i32] = right_chunks[0u]
	right1: dview[i32] = right_chunks[1u]
	pass
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("semantic errors:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	left0 := mustVarDeclValueExpr(t, fn.Body[6], "left0")
	right0 := mustVarDeclValueExpr(t, fn.Body[7], "right0")
	right1 := mustVarDeclValueExpr(t, fn.Body[8], "right1")

	leftFacts, ok := result.ExprOptimizationFacts(left0)
	if !ok || leftFacts.Extent == nil {
		t.Fatalf("expected left0 chunk item facts")
	}
	mustAffineExprTerms(t, leftFacts.Extent.Begin, 0, map[string]int64{"start": 1})
	mustAffineExprTerms(t, leftFacts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 1})

	right0Facts, ok := result.ExprOptimizationFacts(right0)
	if !ok || right0Facts.Extent == nil {
		t.Fatalf("expected right0 chunk item facts")
	}
	mustAffineExprTerms(t, right0Facts.Extent.Begin, 0, map[string]int64{"start": 1, "chunk": 2})
	mustAffineExprTerms(t, right0Facts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 3})

	right1Facts, ok := result.ExprOptimizationFacts(right1)
	if !ok || right1Facts.Extent == nil {
		t.Fatalf("expected right1 chunk item facts")
	}
	mustAffineExprTerms(t, right1Facts.Extent.Begin, 0, map[string]int64{"start": 1, "chunk": 3})
	mustAffineExprTerms(t, right1Facts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 4})

	if !result.ExprsHaveEqualExtentSize(left0, right0) {
		t.Fatalf("expected affine chunk items to have equal extent size")
	}
	if !result.ExprsHaveEqualExtentSize(left0, right1) {
		t.Fatalf("expected affine chunk items to have equal extent size")
	}
	if !result.ExprsAreDisjoint(left0, right0) {
		t.Fatalf("expected affine left0 and right0 chunk items to be disjoint")
	}
	if !result.ExprsAreDisjoint(left0, right1) {
		t.Fatalf("expected affine left0 and right1 chunk items to be disjoint")
	}
	if !result.ExprsAreDisjoint(right0, right1) {
		t.Fatalf("expected affine right0 and right1 chunk items to be disjoint")
	}
}

func TestAnalyzeZipMapAcceptsAffineSplitChunkComposition(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "zip_map_affine_split_chunks_exact.llcontext", `
def add(left: i32, right: i32) -> i32:
	return left + right

def kernel(buf: dview[i32], start: usize, chunk: usize) -> void:
	limit: usize = start + (4u * chunk)
	whole: dview[i32] = buf[start:limit]
	parts: SplitView[i32] = split_at(whole, (2u * chunk))
	left_chunks: ChunksExactView[i32] = chunks_exact(parts.left, chunk)
	right_ro: dview[i32] = readonly(parts.right)
	right_chunks: ChunksExactView[i32] = chunks_exact(right_ro, chunk)
	zip_map(left_chunks[0u], right_chunks[0u], right_chunks[1u], add)
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("expected affine split/chunk zip_map to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	call := mustExprStmtCall(t, fn.Body[len(fn.Body)-1], "zip_map")
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[1]) {
		t.Fatalf("expected affine zip_map destination and source 1 chunk items to have equal extent size")
	}
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[2]) {
		t.Fatalf("expected affine zip_map destination and source 2 chunk items to have equal extent size")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[1]) {
		t.Fatalf("expected affine zip_map destination and source 1 chunk items to be disjoint")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[2]) {
		t.Fatalf("expected affine zip_map destination and source 2 chunk items to be disjoint")
	}
}

func TestAnalyzeZipMapAcceptsReadonlyDirectSliceComposition(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "zip_map_affine_direct_slices.llcontext", `
def add(left: i32, right: i32) -> i32:
	return left + right

def kernel(buf: dview[i32], start: usize, chunk: usize) -> void:
	limit: usize = start + (3u * chunk)
	whole: dview[i32] = buf[start:limit]
	ro: dview[i32] = readonly(whole)
	dst: dview[i32] = whole[0u:chunk]
	src1: dview[i32] = ro[chunk:(2u * chunk)]
	src2: dview[i32] = ro[(2u * chunk):(3u * chunk)]
	zip_map(dst, src1, src2, add)
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("expected readonly direct-slice zip_map to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	src1 := mustVarDeclValueExpr(t, fn.Body[4], "src1")
	src2 := mustVarDeclValueExpr(t, fn.Body[5], "src2")
	src1Facts, ok := result.ExprOptimizationFacts(src1)
	if !ok || src1Facts.Extent == nil {
		t.Fatalf("expected src1 slice facts")
	}
	if !src1Facts.ReadOnly {
		t.Fatalf("expected src1 slice of readonly view to remain readonly")
	}
	mustAffineExprTerms(t, src1Facts.Extent.Begin, 0, map[string]int64{"start": 1, "chunk": 1})
	mustAffineExprTerms(t, src1Facts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 2})
	src2Facts, ok := result.ExprOptimizationFacts(src2)
	if !ok || src2Facts.Extent == nil {
		t.Fatalf("expected src2 slice facts")
	}
	if !src2Facts.ReadOnly {
		t.Fatalf("expected src2 slice of readonly view to remain readonly")
	}
	mustAffineExprTerms(t, src2Facts.Extent.Begin, 0, map[string]int64{"start": 1, "chunk": 2})
	mustAffineExprTerms(t, src2Facts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 3})
	call := mustExprStmtCall(t, fn.Body[len(fn.Body)-1], "zip_map")
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[1]) {
		t.Fatalf("expected readonly direct-slice zip_map destination and source 1 to have equal extent size")
	}
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[2]) {
		t.Fatalf("expected readonly direct-slice zip_map destination and source 2 to have equal extent size")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[1]) {
		t.Fatalf("expected readonly direct-slice zip_map destination and source 1 to be disjoint")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[2]) {
		t.Fatalf("expected readonly direct-slice zip_map destination and source 2 to be disjoint")
	}
}

func TestOptimizationFactsComposeHelperViewSlicesWithAffineBase(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "helper_view_slice_affine.llcontext", `
def arena_da_view_slice[T](view: dview[T], start: usize, end: usize) -> dview[T]:
	return view[start:end]

def arena_da_view_suffix[T](view: dview[T], start: usize) -> dview[T]:
	return arena_da_view_slice(view, start, view.len)

def kernel(buf: dview[i32], start: usize, chunk: usize) -> void:
	limit: usize = start + (3u * chunk)
	whole: dview[i32] = buf[start:limit]
	rest_view: dview[i32] = arena_da_view_suffix(whole, chunk)
	first: dview[i32] = arena_da_view_slice(rest_view, 0u, chunk)
	second: dview[i32] = arena_da_view_slice(rest_view, chunk, (2u * chunk))
	pass
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("expected helper slice composition to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	first := mustVarDeclValueExpr(t, fn.Body[3], "first")
	second := mustVarDeclValueExpr(t, fn.Body[4], "second")
	firstFacts, ok := result.ExprOptimizationFacts(first)
	if !ok || firstFacts.Extent == nil {
		t.Fatalf("expected first helper slice facts")
	}
	mustAffineExprTerms(t, firstFacts.Extent.Begin, 0, map[string]int64{"start": 1, "chunk": 1})
	mustAffineExprTerms(t, firstFacts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 2})
	secondFacts, ok := result.ExprOptimizationFacts(second)
	if !ok || secondFacts.Extent == nil {
		t.Fatalf("expected second helper slice facts")
	}
	mustAffineExprTerms(t, secondFacts.Extent.Begin, 0, map[string]int64{"start": 1, "chunk": 2})
	mustAffineExprTerms(t, secondFacts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 3})
	if !result.ExprsHaveEqualExtentSize(first, second) {
		t.Fatalf("expected helper view slices to have equal extent size")
	}
	if !result.ExprsAreDisjoint(first, second) {
		t.Fatalf("expected helper view slices to be disjoint")
	}
}

func TestAnalyzeZipMapAcceptsReadonlyHelperViewSlices(t *testing.T) {
	file, result := parseAndAnalyzeOptimizationFactsTest(t, "zip_map_readonly_helper_slices.llcontext", `
def arena_da_view_slice[T](view: dview[T], start: usize, end: usize) -> dview[T]:
	return view[start:end]

def add(left: i32, right: i32) -> i32:
	return left + right

def kernel(buf: dview[i32], start: usize, chunk: usize) -> void:
	limit: usize = start + (3u * chunk)
	whole: dview[i32] = buf[start:limit]
	ro: dview[i32] = readonly(whole)
	dst: dview[i32] = arena_da_view_slice(whole, 0u, chunk)
	src1: dview[i32] = arena_da_view_slice(ro, chunk, (2u * chunk))
	src2: dview[i32] = arena_da_view_slice(ro, (2u * chunk), (3u * chunk))
	zip_map(dst, src1, src2, add)
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("expected readonly helper-slice zip_map to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
	fn := testFuncDeclByName(t, file, "kernel")
	src1 := mustVarDeclValueExpr(t, fn.Body[4], "src1")
	src2 := mustVarDeclValueExpr(t, fn.Body[5], "src2")
	src1Facts, ok := result.ExprOptimizationFacts(src1)
	if !ok || src1Facts.Extent == nil {
		t.Fatalf("expected src1 helper slice facts")
	}
	if !src1Facts.ReadOnly {
		t.Fatalf("expected src1 helper slice of readonly view to remain readonly")
	}
	mustAffineExprTerms(t, src1Facts.Extent.Begin, 0, map[string]int64{"start": 1, "chunk": 1})
	mustAffineExprTerms(t, src1Facts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 2})
	src2Facts, ok := result.ExprOptimizationFacts(src2)
	if !ok || src2Facts.Extent == nil {
		t.Fatalf("expected src2 helper slice facts")
	}
	if !src2Facts.ReadOnly {
		t.Fatalf("expected src2 helper slice of readonly view to remain readonly")
	}
	mustAffineExprTerms(t, src2Facts.Extent.Begin, 0, map[string]int64{"start": 1, "chunk": 2})
	mustAffineExprTerms(t, src2Facts.Extent.End, 0, map[string]int64{"start": 1, "chunk": 3})
	call := mustExprStmtCall(t, fn.Body[len(fn.Body)-1], "zip_map")
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[1]) {
		t.Fatalf("expected readonly helper-slice zip_map destination and source 1 to have equal extent size")
	}
	if !result.ExprsHaveEqualExtentSize(call.Args[0], call.Args[2]) {
		t.Fatalf("expected readonly helper-slice zip_map destination and source 2 to have equal extent size")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[1]) {
		t.Fatalf("expected readonly helper-slice zip_map destination and source 1 to be disjoint")
	}
	if !result.ExprsAreDisjoint(call.Args[0], call.Args[2]) {
		t.Fatalf("expected readonly helper-slice zip_map destination and source 2 to be disjoint")
	}
}

func TestAnalyzeReduceSumAcceptsReadonlyHelperSuffix(t *testing.T) {
	_, result := parseAndAnalyzeOptimizationFactsTest(t, "reduce_sum_readonly_helper_suffix.llcontext", `
def arena_da_view_slice[T](view: dview[T], start: usize, end: usize) -> dview[T]:
	return view[start:end]

def arena_da_view_suffix[T](view: dview[T], start: usize) -> dview[T]:
	return arena_da_view_slice(view, start, view.len)

def sum_one(value: i32) -> i32:
	return value

def kernel(buf: dview[i32], start: usize, chunk: usize) -> i32:
	limit: usize = start + (2u * chunk)
	whole: dview[i32] = buf[start:limit]
	ro: dview[i32] = readonly(whole)
	rest_view: dview[i32] = arena_da_view_suffix(ro, chunk)
	return reduce_sum(rest_view, sum_one)
`)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("expected readonly helper-suffix reduce_sum to analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
}
