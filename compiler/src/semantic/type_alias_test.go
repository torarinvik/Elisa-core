package semantic

import (
	"strings"
	"testing"
)

func TestTypeAliasRegistersNamedType(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "type_alias_ok.elisa", `
type NameId = u32

struct Box:
	name: NameId

def read(box: Box) -> NameId:
	return box.name
`)
	alias, ok := result.NamedTypes["NameId"]
	if !ok {
		t.Fatal("expected NameId alias in named types")
	}
	if !SameType(alias, result.NamedTypes["u32"]) {
		t.Fatalf("expected NameId alias to resolve to u32, got %s", alias)
	}
	sym, ok := result.GlobalScope.Lookup("read")
	if !ok {
		t.Fatal("expected read symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", sym.Type)
	}
	if !SameType(fnType.Return, result.NamedTypes["u32"]) {
		t.Fatalf("expected read return type to resolve through alias to u32, got %s", fnType.Return)
	}
}

func TestConstModuleQualifiedConstants(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "const_module_ok.elisa", `
const module OS:
	WIN = 1
	MAC = 2
	UNIX = 3

def is_win(os: i32) -> bool:
	return os == OS::WIN
`)
	value, ok := result.ConstValues["OS.WIN"]
	if !ok {
		t.Fatalf("expected OS.WIN const value to be registered, got keys %#v", result.ConstValues)
	}
	if value.Kind != ConstInt || value.Int != 1 {
		t.Fatalf("expected OS.WIN = 1, got %#v", value)
	}
	if _, ok := result.ConstValues["OS.MAC"]; !ok {
		t.Fatal("expected OS.MAC const value to be registered")
	}
}

func TestTypeAliasDuplicateErrors(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "type_alias_duplicate.elisa", `
type NameId = u32
type NameId = usize
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, DuplicateTypeMessage("NameId")) {
		t.Fatalf("expected duplicate type alias diagnostic, got:\n%s", joined)
	}
}

func TestTypeAliasDuplicateSameTargetIsAllowed(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "type_alias_duplicate_same_target.elisa", `
type NameId = u32
type NameId = u32
`)
	if errors := strings.Join(result.Errors(), "\n"); strings.Contains(errors, DuplicateTypeMessage("NameId")) {
		t.Fatalf("did not expect duplicate type alias diagnostic for same target, got:\n%s", errors)
	}
}

func TestTypeAliasUnknownTargetTypeErrors(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "type_alias_unknown_target.elisa", `
type NameId = MissingType
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, UnknownTypeMessage("MissingType")) {
		t.Fatalf("expected unknown type alias diagnostic, got:\n%s", joined)
	}
}

func TestIDTypeAliasIsStronglyTypedHandle(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "id_type_alias_ok.elisa", `
extern Name
extern Symbol
type NameId = id[Name]
type SymbolId = id[Symbol]

def raw(name: NameId) -> u32:
	return !name

def wrap(raw: u32) -> NameId:
	return raw as NameId
`)
	nameID, ok := result.NamedTypes["NameId"].(*IDType)
	if !ok {
		t.Fatalf("expected NameId to resolve to IDType, got %T", result.NamedTypes["NameId"])
	}
	symbolID, ok := result.NamedTypes["SymbolId"].(*IDType)
	if !ok {
		t.Fatalf("expected SymbolId to resolve to IDType, got %T", result.NamedTypes["SymbolId"])
	}
	if SameType(nameID, symbolID) {
		t.Fatalf("expected id[Name] and id[Symbol] to be distinct")
	}
	if !SameType(nameID.Storage, result.NamedTypes["u32"]) {
		t.Fatalf("expected id storage to be u32, got %s", nameID.Storage)
	}
}

func TestRowIDTypeRequiresSOATag(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "row_id_type_ok.elisa", `
soa SymbolRows:
	name: u32

type SymbolRow = RowId[SymbolRows]
`)
	rowID, ok := result.NamedTypes["SymbolRow"].(*IDType)
	if !ok {
		t.Fatalf("expected SymbolRow to resolve to IDType, got %T", result.NamedTypes["SymbolRow"])
	}
	if !SameType(rowID.Storage, result.NamedTypes["u32"]) {
		t.Fatalf("expected RowId storage to be u32, got %s", rowID.Storage)
	}

	layoutResult := analyzeFunctionAnalysisTestSource(t, "row_id_layout_soa_ok.elisa", `
layout soa struct LayoutRows:
	name: u32

type LayoutRow = RowId[LayoutRows]
`)
	layoutRowID, ok := layoutResult.NamedTypes["LayoutRow"].(*IDType)
	if !ok {
		t.Fatalf("expected LayoutRow to resolve to IDType, got %T", layoutResult.NamedTypes["LayoutRow"])
	}
	if !SameType(layoutRowID.Storage, layoutResult.NamedTypes["u32"]) {
		t.Fatalf("expected layout soa RowId storage to be u32, got %s", layoutRowID.Storage)
	}

	bad := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "row_id_type_reject.elisa", `
struct Plain:
	name: u32

type PlainRow = RowId[Plain]
`)
	joined := strings.Join(bad.Errors(), "\n")
	if !strings.Contains(joined, "RowId expects an soa type argument, got Plain") {
		t.Fatalf("expected RowId non-SOA rejection, got:\n%s", joined)
	}
}

func TestIDTypeRejectsAccidentalIntegerAndOtherIDAssignment(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "id_type_alias_reject.elisa", `
extern Name
extern Symbol
type NameId = id[Name]
type SymbolId = ID[Symbol]

def bad_integer(raw: u32) -> NameId:
	return raw

def bad_id(symbol: SymbolId) -> NameId:
	return symbol
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "return type expects id[Name], got u32") {
		t.Fatalf("expected raw integer assignment rejection, got:\n%s", joined)
	}
	if !strings.Contains(joined, "return type expects id[Name], got id[Symbol]") {
		t.Fatalf("expected distinct id assignment rejection, got:\n%s", joined)
	}
}

func TestIDTypeUnwrapRequiresIDOperand(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "id_type_unwrap_reject.elisa", `
def bad(raw: u32) -> u32:
	return !raw
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "id unwrap operator requires id[T] operand, got u32") {
		t.Fatalf("expected id unwrap diagnostic, got:\n%s", joined)
	}
}

func TestIDTypeInfersGenericTagArgument(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "id_type_generic_infer.elisa", `
extern Name
type NameId = id[Name]

def id_valid[T](value: id[T]) -> bool:
	return !value != 0

def check(name: NameId) -> bool:
	return id_valid(name)
`)
	sym, ok := result.GlobalScope.Lookup("check")
	if !ok {
		t.Fatal("expected check symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", sym.Type)
	}
	if fnType.Return.String() != "bool" {
		t.Fatalf("expected check return type bool, got %s", fnType.Return)
	}
}

func TestModuleScopedTypeAliasesAllowShortHandleNames(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "module_scoped_id_aliases.elisa", `
module Pascal:
    extern Symbol
    extern Scope
    type SymbolId = id[Symbol]
    type ScopeId = id[Scope]

    struct Pair:
        symbol: SymbolId
        scope: ScopeId

module SML:
    extern Symbol
    type SymbolId = id[Symbol]

using Pascal

def unwrap_symbol(pair: Pair) -> u32:
    return !pair.symbol
`)
	pascalSymbolID, ok := result.NamedTypes["Pascal.SymbolId"].(*IDType)
	if !ok {
		t.Fatalf("expected Pascal.SymbolId to resolve to IDType, got %T", result.NamedTypes["Pascal.SymbolId"])
	}
	smlSymbolID, ok := result.NamedTypes["SML.SymbolId"].(*IDType)
	if !ok {
		t.Fatalf("expected SML.SymbolId to resolve to IDType, got %T", result.NamedTypes["SML.SymbolId"])
	}
	if SameType(pascalSymbolID, smlSymbolID) {
		t.Fatalf("expected module-local SymbolId aliases to remain distinct")
	}
	if _, exists := result.NamedTypes["SymbolId"]; exists {
		t.Fatalf("did not expect module-local SymbolId to leak into the global type namespace")
	}
	sym, ok := result.GlobalScope.Lookup("unwrap_symbol")
	if !ok {
		t.Fatal("expected unwrap_symbol symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", sym.Type)
	}
	if fnType.Params[0].String() != "Pascal.Pair" {
		t.Fatalf("expected using Pascal to resolve short Pair to Pascal.Pair, got %s", fnType.Params[0])
	}
}

func TestModuleScopeQualifiedFunctionCall(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "module_scope_qualified_call.elisa", `
module LaunchPipeline:
    def normalize_exit(code: i32) -> i32:
        return code

def run() -> i32:
    return LaunchPipeline::normalize_exit(7)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	if _, ok := result.GlobalScope.Lookup("LaunchPipeline.normalize_exit"); !ok {
		t.Fatal("expected qualified function symbol LaunchPipeline.normalize_exit")
	}
}

func TestPrivateModuleMemberVisibleInsideModuleOnly(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "private_module_member.elisa", `
private module Secret:
    def hidden() -> i32:
        return 7

    def inside() -> i32:
        return hidden()

def outside() -> i32:
    return Secret::hidden()
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "undefined name \"Secret.hidden\"") && !strings.Contains(all, "unknown function \"Secret.hidden\"") {
		t.Fatalf("expected private module member to be hidden from outside, got:\n%s", all)
	}
	if _, ok := result.GlobalScope.Lookup("Secret.hidden"); !ok {
		t.Fatal("expected private symbol to still be collected under its qualified name")
	}
}

func TestVisibilitySectionsInsideModule(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "visibility_sections.elisa", `
module Api:
    private:
    def hidden() -> i32:
        return 1

    public:
    def visible() -> i32:
        return hidden()

def outside() -> i32:
    return Api::visible() + Api::hidden()
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "undefined name \"Api.hidden\"") && !strings.Contains(all, "unknown function \"Api.hidden\"") {
		t.Fatalf("expected private section member to be hidden from outside, got:\n%s", all)
	}
	if sym, ok := result.GlobalScope.Lookup("Api.visible"); !ok || sym.Private {
		t.Fatalf("expected Api.visible to be public, got %#v", sym)
	}
	if sym, ok := result.GlobalScope.Lookup("Api.hidden"); !ok || !sym.Private {
		t.Fatalf("expected Api.hidden to be private, got %#v", sym)
	}
}

func TestExplicitPrivateDefInsidePublicModule(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "private_def.elisa", `
module Api:
    private def hidden() -> i32:
        return 1

    def visible() -> i32:
        return hidden()

def outside() -> i32:
    return Api::hidden()
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "undefined name \"Api.hidden\"") && !strings.Contains(all, "unknown function \"Api.hidden\"") {
		t.Fatalf("expected private def to be hidden from outside, got:\n%s", all)
	}
}
