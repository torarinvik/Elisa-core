package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

func TestParseStaticInterfaceSpellingIsRejected(t *testing.T) {
	_, errs := parseSourceFile(t, `
static interface Builder:
    type Node
`)
	if len(errs) == 0 {
		t.Fatal("expected parser error for removed static interface spelling")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "`static interface` has been removed; use `protocol`") {
		t.Fatalf("expected removed static interface parser error, got: %v", errs)
	}
}

func TestParseLegacySpecializeSpellingIsRejected(t *testing.T) {
	_, errs := parseSourceFile(t, `
def identity[T](value: T) -> T:
    return value

def use_it() -> int:
    return identity.specialize[int]()(7)
`)
	if len(errs) == 0 {
		t.Fatal("expected parser error for removed .specialize spelling")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "legacy generic specialization `.specialize[T]()` has been removed") {
		t.Fatalf("expected removed .specialize parser error, got: %v", errs)
	}
}

// The `interface` keyword was a silent alias for `protocol`; it is now rejected outright.
func TestParseInterfaceSpellingIsRejected(t *testing.T) {
	_, errs := parseSourceFile(t, "interface Box:\n    def get() -> int\n")
	if !strings.Contains(strings.Join(errs, "\n"), "`interface` has been removed; use `protocol`") {
		t.Fatalf("expected removed interface parser error, got: %v", errs)
	}
}

// Protocol depth: a protocol may declare base protocols (`protocol Ord is Eq:`) and default-method
// bodies (`def le(...): <body>`). Bases parse into InterfaceDecl.Bases; a default method parses as a
// FuncDecl member (carrying a Body) rather than a bodiless ExternFuncDecl.
func TestParseProtocolBasesAndDefaultMethod(t *testing.T) {
	file, errs := parseSourceFile(t, `protocol Eq:
    def eq(self: Self, other: Self) -> bool

protocol Ord is Eq:
    def lt(self: Self, other: Self) -> bool
    def le(self: Self, other: Self) -> bool:
        return Self.lt(self, other) or Self.eq(self, other)
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	var ord *ast.InterfaceDecl
	for _, decl := range file.Decls {
		if iface, ok := decl.(*ast.InterfaceDecl); ok && iface.Name == "Ord" {
			ord = iface
		}
	}
	if ord == nil {
		t.Fatal("expected protocol Ord to parse")
	}
	if len(ord.Bases) != 1 || ord.Bases[0] != "Eq" {
		t.Fatalf("expected Ord to inherit Eq, got bases %v", ord.Bases)
	}
	var sawDefault, sawBodyless bool
	for _, member := range ord.Members {
		switch m := member.(type) {
		case *ast.FuncDecl:
			if m.Name == "le" && len(m.Body) != 0 {
				sawDefault = true
			}
		case *ast.ExternFuncDecl:
			if m.Name == "lt" {
				sawBodyless = true
			}
		}
	}
	if !sawDefault {
		t.Fatal("expected le to parse as a default method (FuncDecl with body)")
	}
	if !sawBodyless {
		t.Fatal("expected lt to parse as a bodiless ExternFuncDecl")
	}

	// Round-trips through the formatter without dropping bases or the default body.
	formatted := unparse.FormatDecl(ord)
	if !strings.Contains(formatted, "protocol Ord is Eq:") {
		t.Fatalf("expected formatted protocol to retain its base list, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "def le(") || !strings.Contains(formatted, "Self.lt(self, other)") {
		t.Fatalf("expected formatted protocol to retain the default-method body, got:\n%s", formatted)
	}
}

// The legacy `sizeof` / `alignof` / `offsetof` spellings are removed; use the `_of` forms.
func TestParseRejectsLegacyLayoutIntrospectionNames(t *testing.T) {
	_, errs := parseSourceFile(t, "struct Header layout c:\n    tag: u8\n    count: u32\n\ndef read() -> usize:\n    return sizeof(Header) + alignof(Header) + offsetof(Header, count)\n")
	joined := strings.Join(errs, "\n")
	for _, want := range []string{
		"`sizeof` has been removed; use `size_of`",
		"`alignof` has been removed; use `align_of`",
		"`offsetof` has been removed; use `offset_of`",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q, got: %v", want, errs)
		}
	}
}

func TestParseStaticInterfaceImplAndBoundedGeneric(t *testing.T) {
	file, errs := parseSourceFile(t, `
struct BuilderTag:
    tag: int

protocol Builder:
    type Node
    def make(value: int) -> Node

impl Builder for BuilderTag:
    type Node = int
    def make(value: int) -> int:
        return value

def build[B: Builder](value: int) -> B.Node:
    return B.make(value)
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 4 {
		t.Fatalf("expected 4 top-level decls, got %d", len(file.Decls))
	}
	iface, ok := file.Decls[1].(*ast.InterfaceDecl)
	if !ok {
		t.Fatalf("expected interface decl, got %T", file.Decls[1])
	}
	if iface.Name != "Builder" {
		t.Fatalf("expected interface Builder, got %q", iface.Name)
	}
	if len(iface.Members) != 2 {
		t.Fatalf("expected 2 interface members, got %d", len(iface.Members))
	}
	assoc, ok := iface.Members[0].(*ast.AssociatedTypeDecl)
	if !ok || assoc.Name != "Node" {
		t.Fatalf("expected associated type Node, got %T %#v", iface.Members[0], iface.Members[0])
	}
	method, ok := iface.Members[1].(*ast.ExternFuncDecl)
	if !ok || method.Name != "make" {
		t.Fatalf("expected interface method make, got %T %#v", iface.Members[1], iface.Members[1])
	}
	impl, ok := file.Decls[2].(*ast.ImplDecl)
	if !ok {
		t.Fatalf("expected impl decl, got %T", file.Decls[2])
	}
	if impl.InterfaceName != "Builder" {
		t.Fatalf("expected impl for Builder, got %q", impl.InterfaceName)
	}
	forType, ok := impl.ForType.(*ast.NamedType)
	if !ok || forType.Name != "BuilderTag" {
		t.Fatalf("expected impl target BuilderTag, got %T %#v", impl.ForType, impl.ForType)
	}
	if len(impl.Members) != 2 {
		t.Fatalf("expected 2 impl members, got %d", len(impl.Members))
	}
	funcDecl, ok := file.Decls[3].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected generic function decl, got %T", file.Decls[3])
	}
	if len(funcDecl.GenericParams) != 1 {
		t.Fatalf("expected one generic param, got %d", len(funcDecl.GenericParams))
	}
	if param := funcDecl.GenericParams[0]; param.Name != "B" || param.InterfaceBound != "Builder" {
		t.Fatalf("expected generic bound B: Builder, got %#v", param)
	}
	retType, ok := funcDecl.ReturnType.(*ast.NamedType)
	if !ok || retType.Name != "B.Node" {
		t.Fatalf("expected return type B.Node, got %T %#v", funcDecl.ReturnType, funcDecl.ReturnType)
	}
}

// GAP #2: an associated type may carry a DEFAULT binding on the protocol, including a refined
// default (`type Field = u64 is PageAligned`). Both the plain and refined default parse and the
// default type is recorded on the AssociatedTypeDecl.
func TestParseAssociatedTypeDefaultBinding(t *testing.T) {
	file, errs := parseSourceFile(t, `
protocol FieldDecoder:
    type Word = u32
    type Field = u32 is InRange[0, 127]
    def decode(value: int) -> Field
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	iface, ok := file.Decls[0].(*ast.InterfaceDecl)
	if !ok {
		t.Fatalf("expected interface decl, got %T", file.Decls[0])
	}
	word, ok := iface.Members[0].(*ast.AssociatedTypeDecl)
	if !ok || word.Name != "Word" || word.DefaultType == nil {
		t.Fatalf("expected associated type Word with a default, got %#v", iface.Members[0])
	}
	if named, ok := word.DefaultType.(*ast.NamedType); !ok || named.Name != "u32" {
		t.Fatalf("expected Word default u32, got %T %#v", word.DefaultType, word.DefaultType)
	}
	field, ok := iface.Members[1].(*ast.AssociatedTypeDecl)
	if !ok || field.Name != "Field" || field.DefaultType == nil {
		t.Fatalf("expected associated type Field with a default, got %#v", iface.Members[1])
	}
	if _, ok := field.DefaultType.(*ast.RefinementTypeExpr); !ok {
		t.Fatalf("expected Field default to be a refinement type, got %T", field.DefaultType)
	}
}

func TestParseStaticInterfaceZeroArgMethodCall(t *testing.T) {
	file, errs := parseSourceFile(t, `
struct BuilderTag:
    tag: int

protocol Builder:
    type State
    def state() -> State

impl Builder for BuilderTag:
    type State = int

    def state() -> int:
        return 1

def build[B: Builder]() -> B.State:
    return B.state()
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	funcDecl, ok := file.Decls[3].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[3])
	}
	ret, ok := funcDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", funcDecl.Body[0])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected zero-arg method call, got %T", ret.Value)
	}
	callee, ok := call.Func.(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected field callee, got %T", call.Func)
	}
	owner, ok := callee.Object.(*ast.Ident)
	if !ok || owner.Name != "B" || callee.Field != "state" {
		t.Fatalf("expected callee B.state, got %T %#v", callee.Object, callee)
	}
	if len(call.Args) != 0 {
		t.Fatalf("expected zero call args, got %d", len(call.Args))
	}
}

func TestParseExtensionImplAndValueMethodCall(t *testing.T) {
	file, errs := parseSourceFile(t, `
const enum Tok of i8:
	PLUS = 0

impl Tok:
	def score(self: Tok) -> i64:
		return 7

def read(tok: Tok) -> i64:
	return tok.score()
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 3 {
		t.Fatalf("expected 3 top-level decls, got %d", len(file.Decls))
	}
	impl, ok := file.Decls[1].(*ast.ImplDecl)
	if !ok {
		t.Fatalf("expected impl decl, got %T", file.Decls[1])
	}
	if !impl.IsExtension() {
		t.Fatalf("expected receiver-scoped extension impl, got interface impl %#v", impl)
	}
	receiver, ok := impl.ForType.(*ast.NamedType)
	if !ok || receiver.Name != "Tok" {
		t.Fatalf("expected extension receiver Tok, got %T %#v", impl.ForType, impl.ForType)
	}
	if len(impl.Members) != 1 {
		t.Fatalf("expected 1 extension method, got %d", len(impl.Members))
	}
	method, ok := impl.Members[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function member, got %T", impl.Members[0])
	}
	if method.Name != "score" || len(method.Params) != 1 || method.Params[0].Name != "self" {
		t.Fatalf("expected extension method score(self: Tok), got %#v", method)
	}
	funcDecl, ok := file.Decls[2].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[2])
	}
	ret, ok := funcDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", funcDecl.Body[0])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected method call, got %T", ret.Value)
	}
	callee, ok := call.Func.(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected field callee, got %T", call.Func)
	}
	owner, ok := callee.Object.(*ast.Ident)
	if !ok || owner.Name != "tok" || callee.Field != "score" {
		t.Fatalf("expected callee tok.score, got %T %#v", callee.Object, callee)
	}
	if len(call.Args) != 0 {
		t.Fatalf("expected zero explicit call args, got %d", len(call.Args))
	}
}

func TestParseTryElseRecovery(t *testing.T) {
	file, errs := parseSourceFile(t, `
error FileError:
    NotFound

extern read_value(flag: bool) -> int error[FileError]

def fallback_value(flag: bool) -> int:
    return try read_value(flag) else 11
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	funcDecl, ok := file.Decls[2].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[2])
	}
	ret, ok := funcDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", funcDecl.Body[0])
	}
	tryExpr, ok := ret.Value.(*ast.TryExpr)
	if !ok {
		t.Fatalf("expected try expression, got %T", ret.Value)
	}
	if tryExpr.Fallback == nil {
		t.Fatal("expected try expression to record fallback")
	}
	if got := unparse.FormatExpr(tryExpr); got != "try read_value(flag) else 11" {
		t.Fatalf("expected try expression to unparse with unified else formatting, got %q", got)
	}
}

func TestParseStaticInterfaceTupleReturnAndDestructure(t *testing.T) {
	file, errs := parseSourceFile(t, `
struct BuilderTag:
    tag: int

protocol Builder:
    type Node
    def make(value: int) -> Node

impl Builder for BuilderTag:
    type Node = int

    def make(value: int) -> int:
        return value

def build_pair[B: Builder](value: int) -> (node: B.Node, checksum: int):
    return B.make(value), value

def use_pair[B: Builder](value: int) -> B.Node:
    built = build_pair[B](value)
    node, checksum = built
    _ = checksum
    return node
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 5 {
		t.Fatalf("expected 5 top-level decls, got %d", len(file.Decls))
	}
	buildDecl, ok := file.Decls[3].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected build_pair decl, got %T", file.Decls[3])
	}
	retType, ok := buildDecl.ReturnType.(*ast.TupleTypeExpr)
	if !ok {
		t.Fatalf("expected tuple return type, got %T", buildDecl.ReturnType)
	}
	if len(retType.Fields) != 2 || retType.Fields[0].Name != "node" || retType.Fields[1].Name != "checksum" {
		t.Fatalf("unexpected tuple return fields: %#v", retType.Fields)
	}
	retStmt, ok := buildDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", buildDecl.Body[0])
	}
	retTuple, ok := retStmt.Value.(*ast.TupleExpr)
	if !ok {
		t.Fatalf("expected tuple return expr, got %T", retStmt.Value)
	}
	if len(retTuple.Elems) != 2 {
		t.Fatalf("expected 2 tuple return elems, got %d", len(retTuple.Elems))
	}
	useDecl, ok := file.Decls[4].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected use_pair decl, got %T", file.Decls[4])
	}
	tupleBind, ok := useDecl.Body[1].(*ast.TupleBindStmt)
	if !ok {
		t.Fatalf("expected tuple destructuring stmt, got %T", useDecl.Body[1])
	}
	if !tupleBind.Declare {
		t.Fatal("expected tuple destructuring stmt to declare locals")
	}
	if len(tupleBind.Names) != 2 || tupleBind.Names[0].Name != "node" || tupleBind.Names[1].Name != "checksum" {
		t.Fatalf("unexpected tuple bind names: %#v", tupleBind.Names)
	}
	if ident, ok := tupleBind.Value.(*ast.Ident); !ok || ident.Name != "built" {
		t.Fatalf("expected tuple bind source to be built, got %T %#v", tupleBind.Value, tupleBind.Value)
	}
}

// A2: contract clauses may all live uniformly in the indented block of a bodiless protocol method —
// requires/ensure AND the frame conditions changes/preserves — populating the decl's frame fields.
func TestParseProtocolMethodContractsIndentedBlock(t *testing.T) {
	file, errs := parseSourceFile(t, `
protocol Sink:
    def push(self: mutable Self&, x: int) -> void
        requires x > 0
        changes self.elements
        preserves self.capacity
        ensure self.count() == old(self.count()) + 1
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	iface, ok := file.Decls[0].(*ast.InterfaceDecl)
	if !ok {
		t.Fatalf("expected protocol decl, got %T", file.Decls[0])
	}
	method, ok := iface.Members[0].(*ast.ExternFuncDecl)
	if !ok || method.Name != "push" {
		t.Fatalf("expected bodiless push method, got %T %#v", iface.Members[0], iface.Members[0])
	}
	if len(method.Requires) != 1 {
		t.Fatalf("expected 1 requires from the block, got %d", len(method.Requires))
	}
	if len(method.EnsureValues) != 1 {
		t.Fatalf("expected 1 ensure from the block, got %d", len(method.EnsureValues))
	}
	if len(method.Changes) != 1 {
		t.Fatalf("expected 1 changes path from the block, got %d", len(method.Changes))
	}
	if len(method.Preserves) != 1 {
		t.Fatalf("expected 1 preserves path from the block, got %d", len(method.Preserves))
	}
}

// A2 back-compat: the signature-line form of changes/preserves still parses and reaches the decl.
func TestParseProtocolMethodFrameSignatureLineBackCompat(t *testing.T) {
	file, errs := parseSourceFile(t, `
protocol Sink:
    def push(self: mutable Self&, x: int) -> void changes self.elements preserves self.capacity
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	iface := file.Decls[0].(*ast.InterfaceDecl)
	method, ok := iface.Members[0].(*ast.ExternFuncDecl)
	if !ok {
		t.Fatalf("expected bodiless push method, got %T", iface.Members[0])
	}
	if len(method.Changes) != 1 || len(method.Preserves) != 1 {
		t.Fatalf("expected signature-line changes/preserves to populate decl, got %d/%d", len(method.Changes), len(method.Preserves))
	}
}

// A2: a non-contract statement in the indented block is still a parse error (no executable body).
func TestParseProtocolMethodContractsRejectNonContract(t *testing.T) {
	_, errs := parseSourceFile(t, `
protocol Sink:
    def push(self: mutable Self&, x: int) -> void
        x = x + 1
`)
	if len(errs) == 0 {
		t.Fatal("expected a parse error for a non-contract statement in a bodiless method block")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "may only carry requires/ensure/changes/preserves") {
		t.Fatalf("expected the contract-only error, got: %v", errs)
	}
}

// Optional chaining `x?.…` (field `x?.f`, method `x?.m()`, and transform-apply
// `x?.(f)`) has been removed; bind the optional explicitly with `if x is v: …`.
// The parser rejects it with a message pointing at that form.
func TestParseOptionalChainingRejected(t *testing.T) {
	for _, src := range []string{
		"def f(b: Box?) -> int:\n    _ = b?.value\n    return 0\n",
		"def f(b: Box?) -> int:\n    _ = b?.scale(2)\n    return 0\n",
		"def f(b: int?) -> int?:\n    return b?.(bump)\n",
	} {
		_, errs := parseSourceFile(t, src)
		found := false
		for _, e := range errs {
			if strings.Contains(e, "optional chaining") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected optional-chaining rejection for %q, got: %v", src, errs)
		}
	}
}
