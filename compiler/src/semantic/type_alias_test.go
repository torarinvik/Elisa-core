package semantic

import (
	"strings"
	"testing"
)

func TestTypeAliasRegistersNamedType(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "type_alias_ok.llcontext", `
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
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "type_alias_duplicate.llcontext", `
type NameId = u32
type NameId = usize
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, DuplicateTypeMessage("NameId")) {
		t.Fatalf("expected duplicate type alias diagnostic, got:\n%s", joined)
	}
}

func TestTypeAliasUnknownTargetTypeErrors(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "type_alias_unknown_target.llcontext", `
type NameId = MissingType
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, UnknownTypeMessage("MissingType")) {
		t.Fatalf("expected unknown type alias diagnostic, got:\n%s", joined)
	}
}
