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
