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
		{"mutable_global_scalar_reference_write_through_missing_read", `global mutable value: i32 = 0
global mutable slot: mutable i32& = &value
def update():
    can Global.Write:
        slot <- 1
	`, "mutable global read", " can[Global.Read, Global.Write]"},
		{"mutable_global_scalar_reference_write_through_with_grants", `global mutable value: i32 = 0
global mutable slot: mutable i32& = &value
def update():
    can Global{Read, Write}:
        slot <- 1
	`, "", " can[Global.Read, Global.Write]"},
		{"immutable_global_scalar_reference_write_through_missing_read", `global mutable value: i32 = 0
global slot: mutable i32& = &value
def update():
    can Global.Write:
        slot <- 1
	`, "mutable global read", " can[Global.Read, Global.Write]"},
		{"immutable_global_scalar_reference_write_through_with_grants", `global mutable value: i32 = 0
global slot: mutable i32& = &value
def update():
    can Global{Read, Write}:
        slot <- 1
	`, "", " can[Global.Read, Global.Write]"},
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
		{"immutable_global_mutable_view_index_write", `global mutable backing: i32[2] = [0, 1]
global alias: mutable view[i32] = backing[0:2]
def update():
    can Global.Write:
        alias[0] <- 2
	`, "mutable global read", " can[Global.Read, Global.Write]"},
		{"immutable_global_mutable_view_index_write_with_grants", `global mutable backing: i32[2] = [0, 1]
global alias: mutable view[i32] = backing[0:2]
def update():
    can Global{Read, Write}:
        alias[0] <- 2
	`, "", " can[Global.Read, Global.Write]"},
		{"immutable_global_mutable_view_call_requires_write", `global mutable backing: i32[2] = [0, 1]
global alias: mutable view[i32] = backing[0:2]
def mutate(values: mutable view[i32]):
    values[0] <- 3
def update():
    can Global.Read:
        mutate(alias)
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"immutable_global_mutable_view_call_with_grants", `global mutable backing: i32[2] = [0, 1]
global alias: mutable view[i32] = backing[0:2]
def mutate(values: mutable view[i32]):
    values[0] <- 3
def update():
    can Global{Read, Write}:
        mutate(alias)
	`, "", " can[Global.Read, Global.Write]"},
		{"immutable_global_mutable_view_local_copy_requires_write", `global mutable backing: i32[2] = [0, 1]
global alias: mutable view[i32] = backing[0:2]
def update():
    can Global.Read:
        local: mutable view[i32] = alias
        local[0] <- 4
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"immutable_global_mutable_view_return_requires_write", `global mutable backing: i32[2] = [0, 1]
global alias: mutable view[i32] = backing[0:2]
def update() -> mutable view[i32]:
    can Global.Read:
        return alias
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"immutable_global_aggregate_mutable_view_call_requires_write", `global mutable backing: i32[2] = [0, 1]
struct State:
    view: (mutable view[i32])
global state: State = State{view: backing[0:2]}
def mutate(values: mutable view[i32]):
    values[0] <- 3
def update():
    can Global.Read:
        mutate(state.view)
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"immutable_global_aggregate_mutable_view_call_requires_read", `global mutable backing: i32[2] = [0, 1]
struct State:
    view: (mutable view[i32])
global state: State = State{view: backing[0:2]}
def mutate(values: mutable view[i32]):
    values[0] <- 3
def update():
    can Global.Write:
        mutate(state.view)
	`, "mutable global read", " can[Global.Read, Global.Write]"},
		{"immutable_global_aggregate_mutable_view_local_copy_requires_write", `global mutable backing: i32[2] = [0, 1]
struct State:
    view: (mutable view[i32])
global state: State = State{view: backing[0:2]}
def update():
    can Global.Read:
        local: mutable view[i32] = state.view
        local[0] <- 4
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"immutable_global_aggregate_mutable_view_whole_copy_requires_write", `global mutable backing: i32[2] = [0, 1]
struct State:
    view: (mutable view[i32])
global state: State = State{view: backing[0:2]}
def update():
    can Global.Read:
        local: State = state
        local.view[0] <- 4
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"immutable_global_aggregate_mutable_view_whole_call_requires_write", `global mutable backing: i32[2] = [0, 1]
struct State:
    view: (mutable view[i32])
def mutate(value: State):
    value.view[0] <- 3
global state: State = State{view: backing[0:2]}
def update():
    can Global.Read:
        mutate(state)
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"immutable_global_aggregate_mutable_view_whole_return_requires_write", `global mutable backing: i32[2] = [0, 1]
struct State:
    view: (mutable view[i32])
global state: State = State{view: backing[0:2]}
def update() -> State:
    can Global.Read:
        return state
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"immutable_global_generic_aggregate_mutable_view_return_requires_write", `global mutable backing: i32[2] = [0, 1]
struct State[T]:
    view: T
global state: State[mutable view[i32]] = State{view: backing[0:2]}
def update() -> State[mutable view[i32]]:
    can Global.Read:
        return state
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"immutable_global_mutable_view_field_index_write_with_grants", `global mutable backing: i32[2] = [0, 1]
struct State:
    view: (mutable view[i32])
global state: State = State{view: backing[0:2]}
def update():
    can Global{Read, Write}:
        state.view[0] <- 2
	`, "", " can[Global.Read, Global.Write]"},
		{"immutable_global_mutable_view_field_index_write_requires_write", `global mutable backing: i32[2] = [0, 1]
struct State:
    view: (mutable view[i32])
global state: State = State{view: backing[0:2]}
def update():
    can Global.Read:
        state.view[0] <- 2
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"mutable_global_soa_row_local_copy_requires_write", `struct Rows layout(soa):
    value: i32
global rows: Rows = zeroed
def update(index: RowId[Rows]):
    can Global.Read:
        row = rows[index]
        row.value <- 1
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"mutable_global_soa_row_iteration_requires_write", `struct Rows layout(soa):
    value: i32
global rows: Rows = zeroed
def update():
    can Global.Read:
        for row in rows.rows():
            row.value <- 1
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"mutable_global_soa_row_iteration_requires_read", `struct Rows layout(soa):
    value: i32
global rows: Rows = zeroed
def update():
    can Global.Write:
        for row in rows.rows():
            row.value <- 1
	`, "mutable global read", " can[Global.Read, Global.Write]"},
		{"mutable_global_view_rebind_requires_write", `global mutable backing: i32[2] = [0, 1]
global alias: mutable view[i32] = backing[0:2]
def update(source: mutable view[i32]):
    mutable local_view: mutable view[i32] = source
    can Global.Read:
        local_view <- alias
        local_view[0] <- 1
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"mutable_global_view_rebind_with_grants", `global mutable backing: i32[2] = [0, 1]
global alias: mutable view[i32] = backing[0:2]
def update(source: mutable view[i32]):
    mutable local_view: mutable view[i32] = source
    can Global{Read, Write}:
        local_view <- alias
        local_view[0] <- 1
	`, "", " can[Global.Read, Global.Write]"},
		{"mutable_global_view_value_if_rebind_requires_write", `global mutable backing: i32[2] = [0, 1]
global alias: mutable view[i32] = backing[0:2]
def update(source: mutable view[i32], choose_global: bool):
    mutable local_view: mutable view[i32] = source
    can Global.Read:
        local_view <-
            if choose_global:
                pass
                alias
            else:
                source
        local_view[0] <- 1
		`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"mutable_global_view_value_match_rebind_requires_write", `global mutable backing: i32[2] = [0, 1]
global alias: mutable view[i32] = backing[0:2]
def update(source: mutable view[i32], choice: i64):
    mutable local_view: mutable view[i32] = source
    can Global.Read:
        local_view <- match choice:
            0: alias
            _: source
        local_view[0] <- 1
		`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"mutable_global_view_catch_return_requires_write", `error ViewError:
    Failed
global mutable backing: i32[2] = [0, 1]
global alias: mutable view[i32] = backing[0:2]
extern read_view() -> mutable view[i32] error[ViewError]
def update() -> mutable view[i32]:
    can Global.Read:
        return catch read_view():
            loaded: alias
            error err:
                alias
		`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"mutable_global_view_tuple_rebind_requires_write", `global mutable backing: i32[2] = [0, 1]
global alias: mutable view[i32] = backing[0:2]
def update(source: mutable view[i32]):
    mutable boxed: (ok: bool, v: mutable view[i32]) = (false, source)
    can Global.Read:
        boxed <- (true, alias)
        boxed.v[0] <- 1
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"mutable_global_view_tuple_rebind_with_grants", `global mutable backing: i32[2] = [0, 1]
global alias: mutable view[i32] = backing[0:2]
def update(source: mutable view[i32]):
    mutable boxed: (ok: bool, v: mutable view[i32]) = (false, source)
    can Global{Read, Write}:
        boxed <- (true, alias)
        boxed.v[0] <- 1
	`, "", " can[Global.Read, Global.Write]"},
		{"mutable_global_view_struct_call_requires_write", `global mutable backing: i32[2] = [0, 1]
global alias: mutable view[i32] = backing[0:2]
struct Box:
    view: (mutable view[i32])
def mutate(box: Box):
    box.view[0] <- 1
def update():
    can Global.Read:
        mutate(Box{view: alias})
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"mutable_global_view_struct_call_with_grants", `global mutable backing: i32[2] = [0, 1]
global alias: mutable view[i32] = backing[0:2]
struct Box:
    view: (mutable view[i32])
def mutate(box: Box):
    box.view[0] <- 1
def update():
    can Global{Read, Write}:
        mutate(Box{view: alias})
	`, "", " can[Global.Read, Global.Write]"},
		{"mutable_global_soa_enumerated_row_iteration_requires_write", `struct Rows layout(soa):
    value: i32
global mutable rows: Rows = zeroed
def update():
    can Global.Read:
        for index, row in rows.rows().enumerate():
            row.value <- index.i32()
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"mutable_global_soa_direct_row_field_write_is_write_only", `struct Rows layout(soa):
    value: i32
global rows: Rows = zeroed
def update(index: RowId[Rows]):
    can Global.Write:
        rows[index].value <- 1
	`, "", " can[Global.Write]"},
		{"immutable_global_aggregate_mutable_view_return_requires_write", `global mutable backing: i32[2] = [0, 1]
struct State:
    view: (mutable view[i32])
global state: State = State{view: backing[0:2]}
def update() -> mutable view[i32]:
    can Global.Read:
        return state.view
	`, "mutable global write", " can[Global.Read, Global.Write]"},
		{"immutable_global_readonly_view_call_does_not_require_write", `global mutable backing: i32[2] = [0, 1]
global alias: view[i32] = backing[0:2]
def inspect(values: view[i32]) -> i32:
    return values[0]
def update() -> i32:
    can Global.Read:
        return inspect(alias)
	`, "", " can[Global.Read]"},
		{"immutable_global_mutable_field_write", `struct Cell:
    value: mutable i32
global cell: Cell = Cell{value: 0}
def update():
    cell.value <- 1
	`, "mutable global write", " can[Global.Write]"},
		{"immutable_global_mutable_field_write_with_write_only_grant", `struct Cell:
    value: mutable i32
global cell: Cell = Cell{value: 0}
def update():
    can Global.Write:
        cell.value <- 1
	`, "", " can[Global.Write]"},
		{"immutable_global_mutable_field_aggregate_return_does_not_require_write", `struct State:
    value: mutable i32
global state: State = State{value: 0}
def update() -> State:
    can Global.Read:
        return state
	`, "", " can[Global.Read]"},
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
