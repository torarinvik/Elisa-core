package main

import "testing"

// Regression: a packed-enum handle wrapped in a PLAIN STRUCT literal and pushed onto a
// `mutable darray&` out-param must carry its region with it — including the darray
// SIDE-TABLE payloads of the wrapped node (`IndexN.arguments`). Before the fix,
// regionValueTester did not classify a brace struct literal (`ParamDecl{type_expression:
// node}`) as region-valued, so the grower's ambient allocation region was never bound to
// the out-param's region: the node records went to the caller-threaded store, but the
// `darray[Expr]` side-table literal was allocated in the callee's function-local auto
// region and arena_free'd on return — the caller then decoded a dangling items pointer
// (read-order-dependent aliasing, or a segfault). This is the stage1 self-hosted parser's
// parse_one_param shape (function-parameter type annotations read back aliased).
func TestPackedPayloadStructWrapOutParamSurvivesCallee(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `enum Node layout(handle: u32):
    Nothing

enum Expr is Node:
    Invalid
    Ident(name: sview, line: u32)
    IndexN(object: Expr, arguments: darray[Expr], line: u32)

struct ParamDecl:
    name: sview
    type_expression: Expr

def build_param(items: mutable darray[ParamDecl]&) -> void can[Memory.Allocate]:
    head: Expr = Expr.Ident("dict", 1)
    a0: Expr = Expr.Ident("cstr", 1)
    a1: Expr = Expr.Ident("i64", 1)
    arguments: darray[Expr] = [a0, a1]
    built: Expr? = Expr.IndexN(head, arguments, 1)
    if built is param_type:
        items.push(ParamDecl{name: "e", type_expression: param_type})

def code_of(e: Expr) -> i64 can[Abort.Panic]:
    match e:
        Expr.Ident(name, line):
            return name[0].i64() + name.len.i64()
        _:
            return -1

def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    params: mutable darray[ParamDecl] = []
    build_param(params)
    total: mutable i64 = 0
    for param in params |total|:
        match param.type_expression:
            Expr.IndexN(head, arguments, line):
                total <- total + code_of(head)
                weight: mutable i64 = 10
                for argument in arguments |total, weight|:
                    total <- total + weight * code_of(argument)
                    weight <- weight * 10
            _:
                total <- total - 1000000
    print(total)
    return 0`)
	// head "dict" -> 'd'+4 = 104; args "cstr" -> 'c'+4 = 103 (x10), "i64" -> 'i'+3 = 108 (x100).
	if status != "RAN" || out != "11934" {
		t.Fatalf("expected RAN 11934 (side-table payloads survive callee return), got %s %q", status, out)
	}
}
