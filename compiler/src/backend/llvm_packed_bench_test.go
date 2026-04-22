//go:build cgo

package backend

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"llcontext/src/lexer"
	"llcontext/src/parser"
	"llcontext/src/semantic"
)

const packedLoweringParallelWorkerCount = 10

const packedLoweringBenchmarkSource = `packed enum Expr:
    common:
        span: i64
        depth: i32
    Lit(value: i64)
    Add(left: Expr, right: Expr)

def checksum(frozen: Expr.Store[Frozen]) -> i64:
    total: mutable i64 = 0
    i: mutable usize = 0
    while i < frozen.count:
        node: Expr = frozen[i]
        total <- total + node.span + node.span
        total <- total + node.depth.i64() + node.depth.i64()
        i <- i + 1
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
    i: mutable usize = 0
    while i < frozen.count:
        node: Expr = frozen[i]
        total <- total + node.span + node.span
        total <- total + node.depth.i64() + node.depth.i64()
        i <- i + 1
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
    i: mutable usize = 0
    while i < frozen.count:
        node: Expr = frozen[i]
        total <- total + node.span + node.span
        i <- i + 1
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
    i: mutable usize = 0
    while i < decls.count:
        total <- total + score_decl(decls[i], decls, stmts, exprs, patterns, types)
        i <- i + 1
    return total

def build_and_checksum() -> i64:
    region scratch(4096)
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

const packedLoweringParserASTMegaBenchmarkSource = `@packed_profile(retained_reads)
packed enum TypeExpr:
    common:
        span: i64
        weight: i64
    Name(symbol: i64)
    Apply(callee: TypeExpr, arg: TypeExpr)
    Func(param: TypeExpr, result: TypeExpr)
    Effect(label: i64, carrier: TypeExpr)

@packed_profile(retained_reads)
packed enum Pattern:
    common:
        span: i64
        weight: i64
    Bind(symbol: i64, ann: TypeExpr)
    Tuple(left: Pattern, right: Pattern)
    Annotated(inner: Pattern, ann: TypeExpr)
    Destructure(head: Pattern, rest: Pattern, ann: TypeExpr)

@packed_profile(retained_reads)
packed enum Expr:
    common:
        span: i64
        cost: i64
    Int(value: i64)
    Name(symbol: i64, ty: TypeExpr)
    Call(callee: Expr, arg: Expr, ty: TypeExpr)
    Construct(pattern: Pattern, body: Expr, ty: TypeExpr)
    Select(base: Expr, field: i64, ty: TypeExpr)
    LetExpr(pattern: Pattern, value: Expr, body: Expr, ty: TypeExpr)

@packed_profile(retained_reads)
packed enum Stmt:
    common:
        span: i64
        effect: i64
    Let(pattern: Pattern, ann: TypeExpr, value: Expr, next: Stmt)
    If(cond: Expr, then_branch: Stmt, else_branch: Stmt)
    Block(first: Stmt, second: Stmt, result_expr: Expr)
    While(cond: Expr, body: Stmt, next: Stmt)
    Return(value: Expr)

@packed_profile(retained_reads)
packed enum Decl:
    common:
        span: i64
        phase: i64
    Fun(name: i64, param: Pattern, signature: TypeExpr, body: Stmt)
    TypeAlias(name: i64, target: TypeExpr)
    Entry(initializer: Expr, script: Stmt, summary: Expr)
    Nested(name: i64, signature: TypeExpr, body: Stmt, inner: Decl)
    Bundle(left: Decl, right: Decl)

def score_type(node: TypeExpr, frozen: TypeExpr.Store[Frozen]) -> i64:
    return match node in frozen:
        TypeExpr.Name(symbol: symbol):
            node.span + node.weight + symbol + node.span + node.weight
        TypeExpr.Apply(callee: callee, arg: arg):
            node.span + node.weight + score_type(callee, frozen) + score_type(arg, frozen) + node.span + node.weight
        TypeExpr.Func(param: param, result: result):
            node.span + node.weight + score_type(param, frozen) + score_type(result, frozen) + node.span + node.weight
        TypeExpr.Effect(label: label, carrier: carrier):
            node.span + node.weight + label + score_type(carrier, frozen) + node.span + node.weight

def score_pattern(node: Pattern, frozen: Pattern.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
    return match node in frozen:
        Pattern.Bind(symbol: symbol, ann: ann):
            node.span + node.weight + symbol + score_type(ann, types) + node.span + node.weight
        Pattern.Tuple(left: left, right: right):
            node.span + node.weight + score_pattern(left, frozen, types) + score_pattern(right, frozen, types) + node.span + node.weight
        Pattern.Annotated(inner: inner, ann: ann):
            node.span + node.weight + score_pattern(inner, frozen, types) + score_type(ann, types) + node.span + node.weight
        Pattern.Destructure(head: head, rest: rest, ann: ann):
            node.span + node.weight + score_pattern(head, frozen, types) + score_pattern(rest, frozen, types) + score_type(ann, types) + node.span + node.weight

def score_expr(node: Expr, frozen: Expr.Store[Frozen], patterns: Pattern.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
    return match node in frozen:
        Expr.Int(value: value):
            node.span + node.cost + value + node.span + node.cost
        Expr.Name(symbol: symbol, ty: ty):
            node.span + node.cost + symbol + score_type(ty, types) + node.span + node.cost
        Expr.Call(callee: callee, arg: arg, ty: ty):
            node.span + node.cost + score_expr(callee, frozen, patterns, types) + score_expr(arg, frozen, patterns, types) + score_type(ty, types) + node.span + node.cost
        Expr.Construct(pattern: pattern, body: body, ty: ty):
            node.span + node.cost + score_pattern(pattern, patterns, types) + score_expr(body, frozen, patterns, types) + score_type(ty, types) + node.span + node.cost
        Expr.Select(base: base, field: field, ty: ty):
            node.span + node.cost + score_expr(base, frozen, patterns, types) + field + score_type(ty, types) + node.span + node.cost
        Expr.LetExpr(pattern: pattern, value: value, body: body, ty: ty):
            node.span + node.cost + score_pattern(pattern, patterns, types) + score_expr(value, frozen, patterns, types) + score_expr(body, frozen, patterns, types) + score_type(ty, types) + node.span + node.cost

def score_stmt(node: Stmt, frozen: Stmt.Store[Frozen], exprs: Expr.Store[Frozen], patterns: Pattern.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
    return match node in frozen:
        Stmt.Let(pattern: pattern, ann: ann, value: value, next: next):
            node.span + node.effect + score_pattern(pattern, patterns, types) + score_type(ann, types) + score_expr(value, exprs, patterns, types) + score_stmt(next, frozen, exprs, patterns, types) + node.span + node.effect
        Stmt.If(cond: cond, then_branch: then_branch, else_branch: else_branch):
            node.span + node.effect + score_expr(cond, exprs, patterns, types) + score_stmt(then_branch, frozen, exprs, patterns, types) + score_stmt(else_branch, frozen, exprs, patterns, types) + node.span + node.effect
        Stmt.Block(first: first, second: second, result_expr: result_expr):
            node.span + node.effect + score_stmt(first, frozen, exprs, patterns, types) + score_stmt(second, frozen, exprs, patterns, types) + score_expr(result_expr, exprs, patterns, types) + node.span + node.effect
        Stmt.While(cond: cond, body: body, next: next):
            node.span + node.effect + score_expr(cond, exprs, patterns, types) + score_stmt(body, frozen, exprs, patterns, types) + score_stmt(next, frozen, exprs, patterns, types) + node.span + node.effect
        Stmt.Return(value: value):
            node.span + node.effect + score_expr(value, exprs, patterns, types) + node.span + node.effect

def score_decl(node: Decl, frozen: Decl.Store[Frozen], stmts: Stmt.Store[Frozen], exprs: Expr.Store[Frozen], patterns: Pattern.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
    return match node in frozen:
        Decl.Fun(name: name, param: param, signature: signature, body: body):
            node.span + node.phase + name + score_pattern(param, patterns, types) + score_type(signature, types) + score_stmt(body, stmts, exprs, patterns, types) + node.span + node.phase
        Decl.TypeAlias(name: name, target: target):
            node.span + node.phase + name + score_type(target, types) + node.span + node.phase
        Decl.Entry(initializer: initializer, script: script, summary: summary):
            node.span + node.phase + score_expr(initializer, exprs, patterns, types) + score_stmt(script, stmts, exprs, patterns, types) + score_expr(summary, exprs, patterns, types) + node.span + node.phase
        Decl.Nested(name: name, signature: signature, body: body, inner: inner):
            node.span + node.phase + name + score_type(signature, types) + score_stmt(body, stmts, exprs, patterns, types) + score_decl(inner, frozen, stmts, exprs, patterns, types) + node.span + node.phase
        Decl.Bundle(left: left, right: right):
            node.span + node.phase + score_decl(left, frozen, stmts, exprs, patterns, types) + score_decl(right, frozen, stmts, exprs, patterns, types) + node.span + node.phase

def checksum_language_ast(decls: Decl.Store[Frozen], stmts: Stmt.Store[Frozen], exprs: Expr.Store[Frozen], patterns: Pattern.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
    total: mutable i64 = 0
    i: mutable usize = 0
    while i < decls.count:
        total <- total + score_decl(decls[i], decls, stmts, exprs, patterns, types)
        i <- i + 1
    return total

def build_and_checksum_mega() -> i64:
    region scratch(20480)
    type_store: TypeExpr.Store[Local] = TypeExpr.Store(scratch)
    pattern_store: Pattern.Store[Local] = Pattern.Store(scratch)
    expr_store: Expr.Store[Local] = Expr.Store(scratch)
    stmt_store: Stmt.Store[Local] = Stmt.Store(scratch)
    decl_store: Decl.Store[Local] = Decl.Store(scratch)

    token_ty: TypeExpr = new[type_store] TypeExpr.Name(span: 1, weight: 1, symbol: 11)
    trivia_ty: TypeExpr = new[type_store] TypeExpr.Name(span: 2, weight: 1, symbol: 12)
    effect_ty: TypeExpr = new[type_store] TypeExpr.Effect(span: 3, weight: 2, label: 91, carrier: token_ty)
    list_ty: TypeExpr = new[type_store] TypeExpr.Apply(span: 4, weight: 3, callee: trivia_ty, arg: token_ty)
    nested_ty: TypeExpr = new[type_store] TypeExpr.Apply(span: 5, weight: 4, callee: list_ty, arg: effect_ty)
    fn_ty: TypeExpr = new[type_store] TypeExpr.Func(span: 6, weight: 5, param: nested_ty, result: token_ty)
    parser_ty: TypeExpr = new[type_store] TypeExpr.Func(span: 7, weight: 6, param: fn_ty, result: nested_ty)
    result_ty: TypeExpr = new[type_store] TypeExpr.Func(span: 8, weight: 7, param: parser_ty, result: effect_ty)

    bind_pat: Pattern = new[pattern_store] Pattern.Bind(span: 9, weight: 1, symbol: 31, ann: token_ty)
    tuple_pat: Pattern = new[pattern_store] Pattern.Tuple(span: 10, weight: 2, left: bind_pat, right: bind_pat)
    ann_pat: Pattern = new[pattern_store] Pattern.Annotated(span: 11, weight: 3, inner: tuple_pat, ann: fn_ty)
    destruct_pat: Pattern = new[pattern_store] Pattern.Destructure(span: 12, weight: 4, head: ann_pat, rest: tuple_pat, ann: parser_ty)

    lit_expr: Expr = new[expr_store] Expr.Int(span: 13, cost: 1, value: 99)
    zero_expr: Expr = new[expr_store] Expr.Int(span: 14, cost: 1, value: 0)
    name_expr: Expr = new[expr_store] Expr.Name(span: 15, cost: 2, symbol: 41, ty: token_ty)
    state_expr: Expr = new[expr_store] Expr.Name(span: 16, cost: 3, symbol: 42, ty: parser_ty)
    select_expr: Expr = new[expr_store] Expr.Select(span: 17, cost: 4, base: name_expr, field: 7, ty: list_ty)
    state_select_expr: Expr = new[expr_store] Expr.Select(span: 18, cost: 5, base: state_expr, field: 9, ty: nested_ty)
    call_expr: Expr = new[expr_store] Expr.Call(span: 19, cost: 6, callee: select_expr, arg: lit_expr, ty: fn_ty)
    ctor_expr: Expr = new[expr_store] Expr.Construct(span: 20, cost: 7, pattern: destruct_pat, body: call_expr, ty: nested_ty)
    let_expr: Expr = new[expr_store] Expr.LetExpr(span: 21, cost: 8, pattern: ann_pat, value: ctor_expr, body: select_expr, ty: parser_ty)
    second_let_expr: Expr = new[expr_store] Expr.LetExpr(span: 22, cost: 9, pattern: destruct_pat, value: let_expr, body: state_select_expr, ty: result_ty)
    chain_expr: Expr = new[expr_store] Expr.Call(span: 23, cost: 10, callee: let_expr, arg: second_let_expr, ty: parser_ty)
    replay_expr: Expr = new[expr_store] Expr.Call(span: 24, cost: 11, callee: second_let_expr, arg: zero_expr, ty: result_ty)

    ret_stmt: Stmt = new[stmt_store] Stmt.Return(span: 25, effect: 1, value: chain_expr)
    alt_ret_stmt: Stmt = new[stmt_store] Stmt.Return(span: 26, effect: 2, value: replay_expr)
    let_stmt: Stmt = new[stmt_store] Stmt.Let(span: 27, effect: 3, pattern: destruct_pat, ann: parser_ty, value: let_expr, next: ret_stmt)
    parser_let_stmt: Stmt = new[stmt_store] Stmt.Let(span: 28, effect: 4, pattern: destruct_pat, ann: result_ty, value: second_let_expr, next: alt_ret_stmt)
    block_stmt: Stmt = new[stmt_store] Stmt.Block(span: 29, effect: 5, first: let_stmt, second: parser_let_stmt, result_expr: chain_expr)
    while_stmt: Stmt = new[stmt_store] Stmt.While(span: 30, effect: 6, cond: select_expr, body: block_stmt, next: parser_let_stmt)
    if_stmt: Stmt = new[stmt_store] Stmt.If(span: 31, effect: 7, cond: name_expr, then_branch: while_stmt, else_branch: block_stmt)
    mega_block: Stmt = new[stmt_store] Stmt.Block(span: 32, effect: 8, first: if_stmt, second: while_stmt, result_expr: replay_expr)

    leaf_decl: Decl = new[decl_store] Decl.TypeAlias(span: 33, phase: 1, name: 71, target: parser_ty)
    result_decl: Decl = new[decl_store] Decl.TypeAlias(span: 34, phase: 2, name: 72, target: result_ty)
    fun_decl: Decl = new[decl_store] Decl.Fun(span: 35, phase: 3, name: 73, param: destruct_pat, signature: parser_ty, body: mega_block)
    nested_decl: Decl = new[decl_store] Decl.Nested(span: 36, phase: 4, name: 74, signature: fn_ty, body: if_stmt, inner: fun_decl)
    entry_decl: Decl = new[decl_store] Decl.Entry(span: 37, phase: 5, initializer: chain_expr, script: mega_block, summary: let_expr)
    bundle_left: Decl = new[decl_store] Decl.Bundle(span: 38, phase: 6, left: nested_decl, right: entry_decl)
    _ = new[decl_store] Decl.Bundle(span: 39, phase: 7, left: bundle_left, right: new[decl_store] Decl.Bundle(span: 40, phase: 8, left: leaf_decl, right: result_decl))

    frozen_types: TypeExpr.Store[Frozen] = freeze(move type_store)
    frozen_patterns: Pattern.Store[Frozen] = freeze(move pattern_store)
    frozen_exprs: Expr.Store[Frozen] = freeze(move expr_store)
    frozen_stmts: Stmt.Store[Frozen] = freeze(move stmt_store)
    frozen_decls: Decl.Store[Frozen] = freeze(move decl_store)

    total: mutable i64 = checksum_language_ast(frozen_decls, frozen_stmts, frozen_exprs, frozen_patterns, frozen_types)
    total <- total + checksum_language_ast(frozen_decls, frozen_stmts, frozen_exprs, frozen_patterns, frozen_types)
    return total
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

func repoRootFromPackedBench(b *testing.B) string {
	b.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		b.Fatal("failed to determine backend benchmark file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}

func parsePackedBenchIncludeDirective(line string) (string, bool) {
	const prefix = "# include "
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	rest := strings.TrimSpace(line[len(prefix):])
	if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
		return "", false
	}
	return rest[1 : len(rest)-1], true
}

func readPackedBenchSourceWithIncludes(path string, seen map[string]bool) ([]byte, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if seen[abs] {
		return nil, nil
	}
	seen[abs] = true

	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}

	var out strings.Builder
	lines := strings.SplitAfter(string(raw), "\n")
	for _, line := range lines {
		includePath, ok := parsePackedBenchIncludeDirective(strings.TrimSpace(line))
		if !ok {
			out.WriteString(line)
			continue
		}
		expanded, err := readPackedBenchSourceWithIncludes(filepath.Join(filepath.Dir(abs), includePath), seen)
		if err != nil {
			return nil, err
		}
		out.Write(expanded)
	}
	return []byte(out.String()), nil
}

func loadPackedBenchSourceFromFile(b *testing.B, relativePath string) string {
	b.Helper()
	sourcePath := filepath.Join(repoRootFromPackedBench(b), relativePath)
	expanded, err := readPackedBenchSourceWithIncludes(sourcePath, map[string]bool{})
	if err != nil {
		b.Fatalf("failed to read benchmark source %s: %v", sourcePath, err)
	}
	if len(expanded) == 0 {
		b.Fatalf("expected benchmark source %s to contain input", sourcePath)
	}
	return string(expanded)
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

func benchmarkPackedLoweringParallel(b *testing.B, filename string, src string, profile PackedLoweringProfile, workers int) {
	b.Helper()
	result := parseAndAnalyzeBackendBenchmarkSource(b, filename, src)
	prevMaxProcs := runtime.GOMAXPROCS(workers)
	defer runtime.GOMAXPROCS(prevMaxProcs)
	b.ReportAllocs()
	b.ResetTimer()
	jobs := make(chan struct{}, workers)
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				if _, err := GenerateLLVMIRWithOptAndPackedLoweringProfile(result, OptimizationLevel0, profile); err != nil {
					select {
					case errs <- err:
					default:
					}
				}
			}
		}()
	}
	for i := 0; i < b.N; i++ {
		jobs <- struct{}{}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errs:
		b.Fatalf("GenerateLLVMIRWithOptAndPackedLoweringProfile returned error: %v", err)
	default:
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

func BenchmarkGenerateLLVMIRPackedLoweringParserASTMegaRetainedReads(b *testing.B) {
	benchmarkPackedLowering(b, "packed_lowering_parser_ast_mega_bench.llcontext", packedLoweringParserASTMegaBenchmarkSource, DefaultPackedLoweringProfile())
}

func BenchmarkGenerateLLVMIRPackedLoweringParserASTMegaParallelRetainedReads(b *testing.B) {
	benchmarkPackedLoweringParallel(b, "packed_lowering_parser_ast_mega_parallel_bench.llcontext", packedLoweringParserASTMegaBenchmarkSource, DefaultPackedLoweringProfile(), packedLoweringParallelWorkerCount)
}

func BenchmarkGenerateLLVMIRPackedLoweringMLASTRetainedReads(b *testing.B) {
	src := loadPackedBenchSourceFromFile(b, filepath.Join("Code", "benchmarks", "packed_lowering_ml_ast_medium_bench.llcontext"))
	benchmarkPackedLowering(b, "packed_lowering_ml_ast_medium_bench.llcontext", src, DefaultPackedLoweringProfile())
}

func BenchmarkGenerateLLVMIRPackedLoweringMLASTMegaRetainedReads(b *testing.B) {
	src := loadPackedBenchSourceFromFile(b, filepath.Join("Code", "benchmarks", "packed_lowering_ml_ast_mega_bench.llcontext"))
	benchmarkPackedLowering(b, "packed_lowering_ml_ast_mega_bench.llcontext", src, DefaultPackedLoweringProfile())
}

func BenchmarkGenerateLLVMIRPackedLoweringMLASTUltraRetainedReads(b *testing.B) {
	src := loadPackedBenchSourceFromFile(b, filepath.Join("Code", "benchmarks", "packed_lowering_ml_ast_ultra_bench.llcontext"))
	benchmarkPackedLowering(b, "packed_lowering_ml_ast_ultra_bench.llcontext", src, DefaultPackedLoweringProfile())
}

func BenchmarkGenerateLLVMIRPackedLoweringMLASTMediumRetainedReads(b *testing.B) {
	src := loadPackedBenchSourceFromFile(b, filepath.Join("Code", "benchmarks", "packed_lowering_ml_ast_medium_bench.llcontext"))
	benchmarkPackedLowering(b, "packed_lowering_ml_ast_medium_bench.llcontext", src, DefaultPackedLoweringProfile())
}

func BenchmarkGenerateLLVMIRPackedLoweringMLASTParallelRetainedReads(b *testing.B) {
	src := loadPackedBenchSourceFromFile(b, filepath.Join("Code", "benchmarks", "packed_lowering_ml_ast_medium_parallel_bench.llcontext"))
	benchmarkPackedLoweringParallel(b, "packed_lowering_ml_ast_medium_parallel_bench.llcontext", src, DefaultPackedLoweringProfile(), packedLoweringParallelWorkerCount)
}

func BenchmarkGenerateLLVMIRPackedLoweringMLASTMediumParallelRetainedReads(b *testing.B) {
	src := loadPackedBenchSourceFromFile(b, filepath.Join("Code", "benchmarks", "packed_lowering_ml_ast_medium_parallel_bench.llcontext"))
	benchmarkPackedLoweringParallel(b, "packed_lowering_ml_ast_medium_parallel_bench.llcontext", src, DefaultPackedLoweringProfile(), packedLoweringParallelWorkerCount)
}

func BenchmarkGenerateLLVMIRPackedLoweringMLASTMegaParallelRetainedReads(b *testing.B) {
	src := loadPackedBenchSourceFromFile(b, filepath.Join("Code", "benchmarks", "packed_lowering_ml_ast_mega_parallel_bench.llcontext"))
	benchmarkPackedLoweringParallel(b, "packed_lowering_ml_ast_mega_parallel_bench.llcontext", src, DefaultPackedLoweringProfile(), packedLoweringParallelWorkerCount)
}

func BenchmarkGenerateLLVMIRPackedLoweringMLASTUltraParallelRetainedReads(b *testing.B) {
	src := loadPackedBenchSourceFromFile(b, filepath.Join("Code", "benchmarks", "packed_lowering_ml_ast_ultra_parallel_bench.llcontext"))
	benchmarkPackedLoweringParallel(b, "packed_lowering_ml_ast_ultra_parallel_bench.llcontext", src, DefaultPackedLoweringProfile(), packedLoweringParallelWorkerCount)
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
