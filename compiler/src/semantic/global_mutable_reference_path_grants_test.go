package semantic

import (
	"strings"
	"testing"
)

func TestMutableGlobalReferencePathRequiresReadAndWrite(t *testing.T) {
	tests := []struct {
		name, source, want, wantRow string
	}{
		{"missing_read", `struct Cell:
    value: mutable i32
global mutable cell: Cell = Cell{value: 0}
global mutable alias: mutable Cell& = &cell
def update():
    can Global.Write:
        alias.value <- 1
	`, "mutable global read", " can[Global.Read, Global.Write]"},
		{"both_grants", `struct Cell:
    value: mutable i32
global mutable cell: Cell = Cell{value: 0}
global mutable alias: mutable Cell& = &cell
def update():
    can Global{Read, Write}:
        alias.value <- 1
	`, "", " can[Global.Read, Global.Write]"},
		{"direct_global_field_write", `struct Cell:
    value: mutable i32
global mutable cell: Cell = Cell{value: 0}
def update():
    can Global.Write:
        cell.value <- 1
	`, "", " can[Global.Write]"},
		{"reference_valued_field_write", `struct Cell:
    value: mutable i32
struct Holder:
    alias: mutable Cell&
global mutable cell: Cell = Cell{value: 0}
global mutable holder: Holder = Holder{alias: &cell}
def update():
    can Global.Write:
        holder.alias.value <- 1
	`, "mutable global read", " can[Global.Read, Global.Write]"},
		{"immutable_global_reference_valued_field_write", `struct Cell:
    value: mutable i32
struct Holder:
    alias: mutable Cell&
global mutable cell: Cell = Cell{value: 0}
global holder: Holder = Holder{alias: &cell}
def update():
    holder.alias.value <- 1
	`, "mutable global read", " can[Global.Read, Global.Write]"},
		{"immutable_global_reference_valued_field_write_with_grants", `struct Cell:
    value: mutable i32
struct Holder:
    alias: mutable Cell&
global mutable cell: Cell = Cell{value: 0}
global holder: Holder = Holder{alias: &cell}
def update():
    can Global{Read, Write}:
        holder.alias.value <- 1
	`, "", " can[Global.Read, Global.Write]"},
		{"reference_field_rebind", `struct Cell:
    value: mutable i32
struct Holder:
    alias: mutable Cell&
global mutable cell: Cell = Cell{value: 0}
global mutable holder: Holder = Holder{alias: &cell}
def update(value: mutable Cell&):
    can Unsafe.StaleRef, Global.Write:
        holder.alias <- value
	`, "", " can[Global.Write, Unsafe.StaleRef]"},
		{"immutable_global_reference_to_mutable_global", `struct Cell:
    value: mutable i32
global mutable cell: Cell = Cell{value: 0}
global alias: mutable Cell& = &cell
def update():
    alias.value <- 1
	`, "mutable global read", " can[Global.Read, Global.Write]"},
		{"immutable_global_mutable_field_write", `struct Cell:
    value: mutable i32
global cell: Cell = Cell{value: 0}
def update():
    cell.value <- 1
	`, "mutable global write", " can[Global.Write]"},
		{"immutable_global_mutating_collection_call", `struct State:
    values: mutable darray[i32]
global state: State = State{values: []}
def update():
    state.values.clear()
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"immutable_global_mutable_container_call_rejected", `global values: mutable darray[i32] = zeroed
def update():
    values.clear()
	`, "darray clear requires a mutable darray receiver", "*"},
		{"immutable_global_mutable_container_index_write_rejected", `global values: mutable darray[i32] = zeroed
def update():
    values[0] <- 2
	`, "cannot mutate through readonly ref", "*"},
		{"immutable_global_container_index_write_rejected", `global values: darray[i32] = zeroed
def update():
    values[0] <- 2
	`, "cannot mutate through readonly ref", "*"},
		{"immutable_global_mutable_field_index_write", `struct State:
    values: mutable darray[i32]
global state: State = State{values: zeroed}
def update():
    state.values[0] <- 2
	`, "mutable global write", " can[Global.Write]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, tt.name+".elisa", tt.source, AnalyzeOptions{})
			errors := strings.Join(result.Errors(), "\n")
			if tt.want == "" {
				if errors != "" {
					t.Fatalf("unexpected errors:\n%s", errors)
				}
			} else if !strings.Contains(errors, tt.want) {
				t.Fatalf("expected %q diagnostic, got:\n%s", tt.want, errors)
			}
			sym, ok := result.GlobalScope.Lookup("update")
			if !ok {
				t.Fatal("expected update function symbol")
			}
			fnType, ok := sym.Type.(*FuncType)
			if !ok {
				t.Fatalf("expected update function type, got %T", sym.Type)
			}
			if got := PermissionRefsString(fnType.PermissionRefs); tt.wantRow != "*" && got != tt.wantRow {
				t.Fatalf("expected inferred row %q, got %q", tt.wantRow, got)
			}
		})
	}
}
