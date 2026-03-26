//go:build cgo

package backend

import (
	"strings"
	"testing"

	"llcontext/src/lexer"
	"llcontext/src/parser"
	"llcontext/src/semantic"
)

const packedLoweringBenchmarkSource = `packed enum Expr:
    common:
        span: i64
        depth: i32
    Lit(value: i64)
    Add(left: Expr, right: Expr)

def checksum(frozen: Expr.Store[Frozen]) -> i64:
    total: mutable i64 = 0
    i: mutable usize = 0u
    while i < frozen.count:
        node: Expr = frozen[i]
        total <- total + node.span + node.span
        total <- total + node.depth.i64() + node.depth.i64()
        i <- i + 1u
    return total
`

const packedLoweringRetainedReadsBenchmarkSource = `@packed_profile(retained_reads)
packed enum Expr:
    common:
        span: i64
        depth: i32
    Lit(value: i64)
    Add(left: Expr, right: Expr)

def checksum(frozen: Expr.Store[Frozen]) -> i64:
    total: mutable i64 = 0
    i: mutable usize = 0u
    while i < frozen.count:
        node: Expr = frozen[i]
        total <- total + node.span + node.span
        total <- total + node.depth.i64() + node.depth.i64()
        i <- i + 1u
    return total
`

const packedLoweringRetainedReadsSideTableBenchmarkSource = `@packed_profile(retained_reads)
packed enum Expr:
    common:
        @storage(side_table)
        span: i64
    Lit(value: i64)
    Add(left: Expr, right: Expr)

def checksum(frozen: Expr.Store[Frozen]) -> i64:
    total: mutable i64 = 0
    i: mutable usize = 0u
    while i < frozen.count:
        node: Expr = frozen[i]
        total <- total + node.span + node.span
        i <- i + 1u
    return total
`

const packedLoweringParserASTBenchmarkSource = `@packed_profile(retained_reads)
packed enum TypeExpr:
    common:
        span: i64
        weight: i64
    Name(symbol: i64)
    Apply(callee: TypeExpr, arg: TypeExpr)
    Func(param: TypeExpr, result: TypeExpr)

@packed_profile(retained_reads)
packed enum Pattern:
    common:
        span: i64
        weight: i64
    Bind(symbol: i64, ann: TypeExpr)
    Tuple(left: Pattern, right: Pattern)
    Annotated(inner: Pattern, ann: TypeExpr)

@packed_profile(retained_reads)
packed enum Expr:
    common:
        span: i64
        cost: i64
    Int(value: i64)
    Name(symbol: i64, ty: TypeExpr)
    Call(callee: Expr, arg: Expr, ty: TypeExpr)
    Construct(pattern: Pattern, body: Expr, ty: TypeExpr)

@packed_profile(retained_reads)
packed enum Stmt:
    common:
        span: i64
        effect: i64
    Let(pattern: Pattern, ann: TypeExpr, value: Expr, next: Stmt)
    If(cond: Expr, then_branch: Stmt, else_branch: Stmt)
    Return(value: Expr)

@packed_profile(retained_reads)
packed enum Decl:
    common:
        span: i64
        phase: i64
    Fun(name: i64, param: Pattern, signature: TypeExpr, body: Stmt)
    TypeAlias(name: i64, target: TypeExpr)
    Entry(initializer: Expr, script: Stmt, summary: Expr)

def score_type(node: TypeExpr, frozen: TypeExpr.Store[Frozen]) -> i64:
    return match node in frozen:
        TypeExpr.Name(symbol: symbol):
            node.span + node.weight + symbol + node.span
        TypeExpr.Apply(callee: callee, arg: arg):
            node.span + node.weight + score_type(callee, frozen) + score_type(arg, frozen) + node.span
        TypeExpr.Func(param: param, result: result):
            node.span + node.weight + score_type(param, frozen) + score_type(result, frozen) + node.span

def score_pattern(node: Pattern, frozen: Pattern.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
    return match node in frozen:
        Pattern.Bind(symbol: symbol, ann: ann):
            node.span + node.weight + symbol + score_type(ann, types) + node.span
        Pattern.Tuple(left: left, right: right):
            node.span + node.weight + score_pattern(left, frozen, types) + score_pattern(right, frozen, types) + node.span
        Pattern.Annotated(inner: inner, ann: ann):
            node.span + node.weight + score_pattern(inner, frozen, types) + score_type(ann, types) + node.span

def score_expr(node: Expr, frozen: Expr.Store[Frozen], patterns: Pattern.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
    return match node in frozen:
        Expr.Int(value: value):
            node.span + node.cost + value + node.span
        Expr.Name(symbol: symbol, ty: ty):
            node.span + node.cost + symbol + score_type(ty, types) + node.span
        Expr.Call(callee: callee, arg: arg, ty: ty):
            node.span + node.cost + score_expr(callee, frozen, patterns, types) + score_expr(arg, frozen, patterns, types) + score_type(ty, types) + node.span
        Expr.Construct(pattern: pattern, body: body, ty: ty):
            node.span + node.cost + score_pattern(pattern, patterns, types) + score_expr(body, frozen, patterns, types) + score_type(ty, types) + node.span

def score_stmt(node: Stmt, frozen: Stmt.Store[Frozen], exprs: Expr.Store[Frozen], patterns: Pattern.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
    return match node in frozen:
        Stmt.Let(pattern: pattern, ann: ann, value: value, next: next):
            node.span + node.effect + score_pattern(pattern, patterns, types) + score_type(ann, types) + score_expr(value, exprs, patterns, types) + score_stmt(next, frozen, exprs, patterns, types) + node.span
        Stmt.If(cond: cond, then_branch: then_branch, else_branch: else_branch):
            node.span + node.effect + score_expr(cond, exprs, patterns, types) + score_stmt(then_branch, frozen, exprs, patterns, types) + score_stmt(else_branch, frozen, exprs, patterns, types) + node.span
        Stmt.Return(value: value):
            node.span + node.effect + score_expr(value, exprs, patterns, types) + node.span

def score_decl(node: Decl, frozen: Decl.Store[Frozen], stmts: Stmt.Store[Frozen], exprs: Expr.Store[Frozen], patterns: Pattern.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
    return match node in frozen:
        Decl.Fun(name: name, param: param, signature: signature, body: body):
            node.span + node.phase + name + score_pattern(param, patterns, types) + score_type(signature, types) + score_stmt(body, stmts, exprs, patterns, types) + node.span
        Decl.TypeAlias(name: name, target: target):
            node.span + node.phase + name + score_type(target, types) + node.span
        Decl.Entry(initializer: initializer, script: script, summary: summary):
            node.span + node.phase + score_expr(initializer, exprs, patterns, types) + score_stmt(script, stmts, exprs, patterns, types) + score_expr(summary, exprs, patterns, types) + node.span

def checksum_language_ast(decls: Decl.Store[Frozen], stmts: Stmt.Store[Frozen], exprs: Expr.Store[Frozen], patterns: Pattern.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
    total: mutable i64 = 0
    i: mutable usize = 0u
    while i < decls.count:
        total <- total + score_decl(decls[i], decls, stmts, exprs, patterns, types)
        i <- i + 1u
    return total

def build_and_checksum() -> i64:
    region scratch(4096u)
    type_store: TypeExpr.Store[Local] = TypeExpr.Store(scratch)
    pattern_store: Pattern.Store[Local] = Pattern.Store(scratch)
    expr_store: Expr.Store[Local] = Expr.Store(scratch)
    stmt_store: Stmt.Store[Local] = Stmt.Store(scratch)
    decl_store: Decl.Store[Local] = Decl.Store(scratch)

    name_ty: TypeExpr = new[type_store] TypeExpr.Name(span: 1, weight: 1, symbol: 11)
    list_ty: TypeExpr = new[type_store] TypeExpr.Apply(span: 2, weight: 2, callee: name_ty, arg: name_ty)
    fn_ty: TypeExpr = new[type_store] TypeExpr.Func(span: 3, weight: 3, param: list_ty, result: name_ty)

    bind_pat: Pattern = new[pattern_store] Pattern.Bind(span: 4, weight: 1, symbol: 31, ann: name_ty)
    tuple_pat: Pattern = new[pattern_store] Pattern.Tuple(span: 5, weight: 2, left: bind_pat, right: bind_pat)
    ann_pat: Pattern = new[pattern_store] Pattern.Annotated(span: 6, weight: 3, inner: tuple_pat, ann: fn_ty)

    lit_expr: Expr = new[expr_store] Expr.Int(span: 7, cost: 1, value: 99)
    name_expr: Expr = new[expr_store] Expr.Name(span: 8, cost: 2, symbol: 41, ty: name_ty)
    call_expr: Expr = new[expr_store] Expr.Call(span: 9, cost: 3, callee: name_expr, arg: lit_expr, ty: fn_ty)
    ctor_expr: Expr = new[expr_store] Expr.Construct(span: 10, cost: 4, pattern: ann_pat, body: call_expr, ty: list_ty)

    ret_stmt: Stmt = new[stmt_store] Stmt.Return(span: 11, effect: 1, value: call_expr)
    let_stmt: Stmt = new[stmt_store] Stmt.Let(span: 12, effect: 2, pattern: ann_pat, ann: fn_ty, value: ctor_expr, next: ret_stmt)
    if_stmt: Stmt = new[stmt_store] Stmt.If(span: 13, effect: 3, cond: name_expr, then_branch: let_stmt, else_branch: ret_stmt)

    _ = new[decl_store] Decl.Fun(span: 14, phase: 1, name: 71, param: ann_pat, signature: fn_ty, body: if_stmt)
    _ = new[decl_store] Decl.TypeAlias(span: 15, phase: 2, name: 72, target: list_ty)
    _ = new[decl_store] Decl.Entry(span: 16, phase: 3, initializer: ctor_expr, script: if_stmt, summary: name_expr)

    frozen_types: TypeExpr.Store[Frozen] = freeze(move type_store)
    frozen_patterns: Pattern.Store[Frozen] = freeze(move pattern_store)
    frozen_exprs: Expr.Store[Frozen] = freeze(move expr_store)
    frozen_stmts: Stmt.Store[Frozen] = freeze(move stmt_store)
    frozen_decls: Decl.Store[Frozen] = freeze(move decl_store)

    return checksum_language_ast(frozen_decls, frozen_stmts, frozen_exprs, frozen_patterns, frozen_types)
`

func parseAndAnalyzeBackendBenchmarkSource(b *testing.B, filename string, src string) *semantic.Result {
	b.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) > 0 {
		b.Fatalf("lexer errors:\n%s", strings.Join(errs, "\n"))
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) > 0 {
		b.Fatalf("parse errors:\n%s", strings.Join(errs, "\n"))
	}
	result := semantic.Analyze(file)
	if errs := result.Errors(); len(errs) > 0 {
		b.Fatalf("semantic errors:\n%s", strings.Join(errs, "\n"))
	}
	return result
}

func benchmarkPackedLowering(b *testing.B, filename string, src string, profile PackedLoweringProfile) {
	b.Helper()
	result := parseAndAnalyzeBackendBenchmarkSource(b, filename, src)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := GenerateLLVMIRWithOptAndPackedLoweringProfile(result, OptimizationLevel0, profile); err != nil {
			b.Fatalf("GenerateLLVMIRWithOptAndPackedLoweringProfile returned error: %v", err)
		}
	}
}

func BenchmarkGenerateLLVMIRPackedLoweringCanonical(b *testing.B) {
	benchmarkPackedLowering(b, "packed_lowering_bench.llcontext", packedLoweringBenchmarkSource, DefaultPackedLoweringProfile())
}

func BenchmarkGenerateLLVMIRPackedLoweringRetainedReads(b *testing.B) {
	benchmarkPackedLowering(b, "packed_lowering_retained_reads_bench.llcontext", packedLoweringRetainedReadsBenchmarkSource, DefaultPackedLoweringProfile())
}

func BenchmarkGenerateLLVMIRPackedLoweringRetainedReadsSideTable(b *testing.B) {
	benchmarkPackedLowering(b, "packed_lowering_retained_reads_side_table_bench.llcontext", packedLoweringRetainedReadsSideTableBenchmarkSource, DefaultPackedLoweringProfile())
}

func BenchmarkGenerateLLVMIRPackedLoweringParserASTRetainedReads(b *testing.B) {
	benchmarkPackedLowering(b, "packed_lowering_parser_ast_bench.llcontext", packedLoweringParserASTBenchmarkSource, DefaultPackedLoweringProfile())
}

func BenchmarkGenerateLLVMIRPackedLoweringParserASTWordHandle(b *testing.B) {
	profile, err := LegacyPackedLoweringProfile(PackedEnumABIWordHandle)
	if err != nil {
		b.Fatalf("LegacyPackedLoweringProfile returned error: %v", err)
	}
	benchmarkPackedLowering(b, "packed_lowering_parser_ast_bench.llcontext", packedLoweringParserASTBenchmarkSource, profile)
}

func BenchmarkGenerateLLVMIRPackedLoweringWordHandle(b *testing.B) {
	profile, err := LegacyPackedLoweringProfile(PackedEnumABIWordHandle)
	if err != nil {
		b.Fatalf("LegacyPackedLoweringProfile returned error: %v", err)
	}
	benchmarkPackedLowering(b, "packed_lowering_bench.llcontext", packedLoweringBenchmarkSource, profile)
}

func BenchmarkDescribePackedLowering(b *testing.B) {
	result := parseAndAnalyzeBackendBenchmarkSource(b, "packed_describe_bench.llcontext", packedLoweringRetainedReadsBenchmarkSource)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DescribePackedLowering(result, DefaultPackedLoweringProfile()); err != nil {
			b.Fatalf("DescribePackedLowering returned error: %v", err)
		}
	}
}
