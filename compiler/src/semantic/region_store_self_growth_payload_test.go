package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestStorePayloadSelfGrowthUsesAppendedValues(t *testing.T) {
	const source = `global region source
struct State:
    stable: mutable darray[sview @source]
    borrowed: mutable darray[sview]
struct Item:
    name: sview @source
struct Mixed:
    name: sview @source
    borrowed: sview
struct Diagnostic:
    name: sview @source
    detail: sview = ""
    error_set: mutable sview = ""
    count: u32 = 0
struct AddressDefault:
    name: sview @source
    address: uintptr = 0
struct HeaderDefault:
    name: sview @source
    names: darray[sview @source] = []
extern opaque_default() -> sview
struct UnknownDefault:
    name: sview @source
    detail: sview = opaque_default()
global mutable fallback: sview = ""
struct ShadowDefault:
    name: sview @source
    detail: sview = fallback
struct UserBox:
    values: mutable darray[sview] = []
struct Table:
    box: mutable UserBox = UserBox{}
    shadows: mutable darray[ShadowDefault] = []
    diagnostics: mutable darray[Diagnostic] = []
    addresses: mutable darray[AddressDefault] = []
    headers: mutable darray[HeaderDefault] = []
    unknowns: mutable darray[UnknownDefault] = []
    mixed: mutable darray[Mixed] = []
    items: mutable darray[Item] = []
    names: mutable darray[sview] = []
def push(box: mutable UserBox&, names: darray[sview]&) -> UserBox:
    can Memory.Allocate, Abort.Panic:
        box.values.extend(names)
        return box
def copy_user_push(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.box <- table.box.push(state.borrowed)
extern source_label(table: Table&) -> sview @source
extern borrowed_label(table: Table&) -> sview
def inject(names: darray[sview]&, table: mutable Table&) -> sview @source:
    can Memory.Allocate, Abort.Panic:
        table.names.push(names[0])
        return ""
def copy_safe_table_read(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.diagnostics <- table.diagnostics.push(Diagnostic{name: state.stable[0], detail: source_label(table)})
def copy_borrowed_table_read(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.diagnostics <- table.diagnostics.push(Diagnostic{name: state.stable[0], detail: borrowed_label(table)})
def copy_hidden_write(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.diagnostics <- table.diagnostics.push(Diagnostic{name: state.stable[0], detail: inject(state.borrowed, table)})
def copy_safe(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.names <- table.names.push(state.stable[0])
def copy_safe_record(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.items <- table.items.push(Item{name: state.stable[0]})
def copy_safe_defaults(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.diagnostics <- table.diagnostics.push(Diagnostic{name: state.stable[0]})
def copy_borrowed_override(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.diagnostics <- table.diagnostics.push(Diagnostic{name: state.stable[0], detail: state.borrowed[0]})
def copy_address_default(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.addresses <- table.addresses.push(AddressDefault{name: state.stable[0]})
def copy_header_default(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.headers <- table.headers.push(HeaderDefault{name: state.stable[0]})
def copy_unknown_default(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.unknowns <- table.unknowns.push(UnknownDefault{name: state.stable[0]})
def copy_shadow_default(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic, Global.Read:
        fallback: sview @source = "local"
        table.shadows <- table.shadows.push(ShadowDefault{name: state.stable[0]})
def copy_mixed(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.mixed <- table.mixed.push(Mixed{name: state.stable[0], borrowed: state.borrowed[0]})
def copy_borrowed(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.names <- table.names.push(state.borrowed[0])
def copy_header(table: mutable Table&, state: mutable State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.names <- state.borrowed.push("literal")
`
	for _, cold := range []bool{false, true} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "self_growth_payload.elisa", source, AnalyzeOptions{})
		if cold {
			storeScanUntyped(result)
		}
		a := result.analyzer
		a.currentScope = a.globalScope
		for _, name := range []string{"copy_safe", "copy_safe_record", "copy_safe_table_read", "copy_borrowed_table_read", "copy_hidden_write", "copy_user_push", "copy_safe_defaults", "copy_borrowed_override", "copy_address_default", "copy_header_default", "copy_unknown_default", "copy_shadow_default", "copy_mixed", "copy_borrowed", "copy_header"} {
			fn := storeScanFindFunc(t, result, name)
			proof := a.calleeStoresProgramLivedElements(fn, 1, 0, map[storeScanKey]bool{})
			if proof != (name == "copy_safe" || name == "copy_safe_record" || name == "copy_safe_defaults" || name == "copy_safe_table_read") {
				t.Errorf("cold=%v %s program payload proof=%v", cold, name, proof)
			}
		}
	}
}

func TestStorePayloadLiteralTypeUsesDeclaringNamespace(t *testing.T) {
	decl := &ast.FuncDecl{Name: "store"}
	local := &StructType{Name: "Local.Item"}
	global := &StructType{Name: "Item"}
	foreign := &StructType{Name: "Foreign.Item"}
	a := &Analyzer{namedTypes: map[string]Type{"Item": global, "Local.Item": local, "Foreign.Item": foreign}, funcDeclSymbols: map[*ast.FuncDecl]*Symbol{decl: {Name: "Local.store"}}}
	for _, tc := range []struct {
		name string
		want Type
	}{{"Item", local}, {"Foreign.Item", foreign}, {"Missing.Item", nil}, {"Missing", nil}} {
		if got := a.storeScanDeclaredStructLiteralType(decl, &ast.StructLitExpr{Name: tc.name}); got != tc.want {
			t.Errorf("%s resolved to %v want %v", tc.name, got, tc.want)
		}
	}
	a.namedTypes["Local.Item"] = &SViewType{}
	if got := a.storeScanDeclaredStructLiteralType(decl, &ast.StructLitExpr{Name: "Item"}); got != nil {
		t.Error("non-struct namespace shadow supplied a global literal type")
	}
	decl.TypeParams = []string{"Item"}
	if got := a.storeScanDeclaredStructLiteralType(decl, &ast.StructLitExpr{Name: "Item"}); got != nil {
		t.Error("generic type shadow supplied concrete literal type")
	}
}
