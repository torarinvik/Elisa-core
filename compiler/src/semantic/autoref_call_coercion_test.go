package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestAnalyzeImplicitAutorefOnDirectCall(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_autoref_direct_call.elisa", `struct ScratchArena:
    value: i64

def read(alloc: ScratchArena&) -> i64:
    return alloc.value

def build() -> i64:
    owner: mutable ScratchArena = zeroed
    return read(owner)
`)

	buildSym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	ret := buildDecl.Body[1].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 1 {
		t.Fatalf("expected one resolved arg, got %#v", call.ResolvedArgs)
	}
	addr, ok := call.ResolvedArgs[0].(*ast.AddrOfExpr)
	if !ok {
		t.Fatalf("expected direct call arg to autoref, got %T", call.ResolvedArgs[0])
	}
	if ident, ok := addr.Operand.(*ast.Ident); !ok || ident.Name != "owner" {
		t.Fatalf("expected autoref operand owner, got %T %#v", addr.Operand, addr.Operand)
	}
}

func TestAnalyzeImplicitAutorefAllowsMutableRefFromMutableLocal(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_autoref_mutable_local.elisa", `struct ScratchArena:
	value: mutable i64

def bump(alloc: mutable ScratchArena&) -> i64:
    alloc.value <- alloc.value + 1
    return alloc.value

def build() -> i64:
    owner: mutable ScratchArena = zeroed
    return bump(owner)
`)

	buildSym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	ret := buildDecl.Body[1].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 1 {
		t.Fatalf("expected one resolved arg, got %#v", call.ResolvedArgs)
	}
	addr, ok := call.ResolvedArgs[0].(*ast.AddrOfExpr)
	if !ok {
		t.Fatalf("expected mutable-local arg to autoref, got %T", call.ResolvedArgs[0])
	}
	if ident, ok := addr.Operand.(*ast.Ident); !ok || ident.Name != "owner" {
		t.Fatalf("expected autoref operand owner, got %T %#v", addr.Operand, addr.Operand)
	}
}

func TestAnalyzeImplicitAutorefRejectsMutableRefFromImmutableLocal(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "implicit_autoref_mutable_local_rejected.elisa", `struct ScratchArena:
	value: mutable i64

def bump(alloc: mutable ScratchArena&) -> i64:
    alloc.value <- alloc.value + 1
    return alloc.value

def build() -> i64:
    owner: ScratchArena = zeroed
    return bump(owner)
`)

	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `argument 1 to "bump" expects mutable ScratchArena&`) {
		t.Fatalf("expected mutable-local rejection diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeImplicitAutorefOnNestedFieldProjection(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_autoref_nested_field.elisa", `struct ScratchArena:
    value: i64

struct Holder:
    arena: ScratchArena

def read(alloc: ScratchArena&) -> i64:
    return alloc.value

def build() -> i64:
    holder: mutable Holder = zeroed
    return read(holder.arena)
`)

	buildSym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	ret := buildDecl.Body[1].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 1 {
		t.Fatalf("expected one resolved arg, got %#v", call.ResolvedArgs)
	}
	addr, ok := call.ResolvedArgs[0].(*ast.AddrOfExpr)
	if !ok {
		t.Fatalf("expected nested field arg to autoref, got %T", call.ResolvedArgs[0])
	}
	field, ok := addr.Operand.(*ast.FieldExpr)
	if !ok || field.Field != "arena" {
		t.Fatalf("expected autoref operand holder.arena, got %T %#v", addr.Operand, addr.Operand)
	}
	if ident, ok := field.Object.(*ast.Ident); !ok || ident.Name != "holder" {
		t.Fatalf("expected nested field base holder, got %T %#v", field.Object, field.Object)
	}
}

func TestAnalyzeImplicitAutorefRejectsMutableRefFromImmutableField(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "implicit_autoref_mutable_field_rejected.elisa", `struct ScratchArena:
	value: mutable i64

struct Holder:
    arena: ScratchArena

def bump(alloc: mutable ScratchArena&) -> i64:
    alloc.value <- alloc.value + 1
    return alloc.value

def build() -> i64:
    holder: mutable Holder = zeroed
    return bump(holder.arena)
`)

	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `argument 1 to "bump" expects mutable ScratchArena&`) {
		t.Fatalf("expected immutable-field rejection diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeImplicitAutorefOnExtensionMethodReceiver(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_autoref_extension_method.elisa", `struct Builder:
    value: i64

impl Builder:
    def read(self: Builder, other: Builder&) -> i64:
        return self.value + other.value

def build() -> i64:
    owner: mutable Builder = zeroed
    peer: mutable Builder = zeroed
    return owner.read(peer)
`)

	buildSym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	ret := buildDecl.Body[2].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	lowered := call.LoweredArgs()
	if len(lowered) != 2 {
		t.Fatalf("expected receiver plus one lowered arg, got %d", len(lowered))
	}
	if recv, ok := lowered[0].(*ast.Ident); !ok || recv.Name != "owner" {
		t.Fatalf("expected rewritten receiver owner, got %T %#v", lowered[0], lowered[0])
	}
	addr, ok := lowered[1].(*ast.AddrOfExpr)
	if !ok {
		t.Fatalf("expected method arg autoref, got %T", lowered[1])
	}
	if ident, ok := addr.Operand.(*ast.Ident); !ok || ident.Name != "peer" {
		t.Fatalf("expected autoref operand peer, got %T %#v", addr.Operand, addr.Operand)
	}
}

func TestAnalyzeImplicitRefUpcastOnExistingRefArg(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_ref_upcast_existing_ref.elisa", `struct ScratchArena:
    value: i64

def read(alloc: ScratchArena&) -> i64:
    return alloc.value

def build() -> i64:
    owner: mutable ScratchArena = zeroed
    alloc: stack ScratchArena& = &owner
    return read(alloc)
`)

	buildSym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	ret := buildDecl.Body[2].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 1 {
		t.Fatalf("expected one resolved arg, got %#v", call.ResolvedArgs)
	}
	if ident, ok := call.ResolvedArgs[0].(*ast.Ident); !ok || ident.Name != "alloc" {
		t.Fatalf("expected existing ref arg to stay as alloc, got %T %#v", call.ResolvedArgs[0], call.ResolvedArgs[0])
	}
}

func TestAnalyzeImplicitAutorefAllowsNullableRefTargets(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_autoref_nullable_target.elisa", `struct ScratchArena:
    value: i64

def read(alloc: ScratchArena&?) -> i64:
    return 0

def build() -> i64:
    owner: mutable ScratchArena = zeroed
    return read(owner)
`)

	buildSym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	ret := buildDecl.Body[1].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	if !call.ResolvedArgsValid || len(call.ResolvedArgs) != 1 {
		t.Fatalf("expected one resolved arg, got %#v", call.ResolvedArgs)
	}
	addr, ok := call.ResolvedArgs[0].(*ast.AddrOfExpr)
	if !ok {
		t.Fatalf("expected nullable-ref arg to autoref, got %T", call.ResolvedArgs[0])
	}
	if ident, ok := addr.Operand.(*ast.Ident); !ok || ident.Name != "owner" {
		t.Fatalf("expected autoref operand owner, got %T %#v", addr.Operand, addr.Operand)
	}
}

func TestAnalyzeImplicitAutorefOnStructLiteralArgs(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "implicit_autoref_struct_literal.elisa", `struct ScratchArena:
    value: i64

struct Holder:
    arena: ScratchArena&

def build() -> i64:
    owner: mutable ScratchArena = zeroed
    holder = Holder{arena: owner}
    return holder.arena.value
`)

	buildSym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	buildDecl := buildSym.Node.(*ast.FuncDecl)
	holderDecl := buildDecl.Body[1].(*ast.VarDeclStmt)
	lit, ok := holderDecl.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected struct literal, got %T", holderDecl.Value)
	}
	addr, ok := lit.Args[0].(*ast.AddrOfExpr)
	if !ok {
		t.Fatalf("expected struct literal arg to autoref, got %T", lit.Args[0])
	}
	if ident, ok := addr.Operand.(*ast.Ident); !ok || ident.Name != "owner" {
		t.Fatalf("expected autoref operand owner, got %T %#v", addr.Operand, addr.Operand)
	}
}
