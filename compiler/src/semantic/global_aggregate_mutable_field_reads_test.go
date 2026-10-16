package semantic

import (
	"strings"
	"testing"
)

func TestWholeGlobalAggregateReadsRequireMutableFieldGrant(t *testing.T) {
	tests := []struct {
		name, functionName, source, wantError, wantRow, wantMandatoryRow string
	}{
		{
			name:         "whole_value_requires_read",
			functionName: "read_state",
			source: `struct Cell:
    value: mutable i32
global state: Cell = Cell{value: 0}
def read_state() -> Cell:
    return state
`,
			wantError:        "mutable global read",
			wantRow:          " can[Global.Read]",
			wantMandatoryRow: " can[Global.Read]",
		},
		{
			name:         "whole_value_with_read_grant",
			functionName: "read_state",
			source: `struct Cell:
    value: mutable i32
global state: Cell = Cell{value: 0}
def read_state() -> Cell:
    can Global.Read:
        return state
`,
			wantRow:          " can[Global.Read]",
			wantMandatoryRow: " can[Global.Read]",
		},
		{
			name:         "nested_whole_value_requires_read",
			functionName: "read_cell",
			source: `struct Cell:
    value: mutable i32
    label: i32
struct State:
    cell: Cell
global state: State = State{cell: Cell{value: 0, label: 1}}
def read_cell() -> Cell:
    return state.cell
`,
			wantError:        "mutable global read",
			wantRow:          " can[Global.Read]",
			wantMandatoryRow: " can[Global.Read]",
		},
		{
			name:         "mutable_global_view_projection_requires_read",
			functionName: "read_alias",
			source: `global mutable backing: i32[2] = [0, 1]
global alias: mutable view[i32] = backing[0:2]
def read_alias() -> i32:
    return alias[0]
`,
			wantError:        "mutable global read",
			wantRow:          " can[Global.Read]",
			wantMandatoryRow: " can[Global.Read]",
		},
		{
			name:         "immutable_sibling_projection_stays_advisory",
			functionName: "read_label",
			source: `struct Cell:
    value: mutable i32
    label: i32
global state: Cell = Cell{value: 0, label: 1}
def read_label() -> i32:
    return state.label
`,
			wantRow: " can[Global.Read]",
		},
		{
			name:         "nested_immutable_sibling_projection_stays_advisory",
			functionName: "read_label",
			source: `struct Cell:
    value: mutable i32
    label: i32
struct State:
    cell: Cell
global state: State = State{cell: Cell{value: 0, label: 1}}
def read_label() -> i32:
    return state.cell.label
`,
			wantRow: " can[Global.Read]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, tt.name+".elisa", tt.source, AnalyzeOptions{})
			errors := strings.Join(result.Errors(), "\n")
			if tt.wantError == "" {
				if errors != "" {
					t.Fatalf("unexpected errors:\n%s", errors)
				}
			} else if !strings.Contains(errors, tt.wantError) {
				t.Fatalf("expected %q diagnostic, got:\n%s", tt.wantError, errors)
			}
			sym, ok := result.GlobalScope.Lookup(tt.functionName)
			if !ok {
				t.Fatal("expected reader function symbol")
			}
			fnType, ok := sym.Type.(*FuncType)
			if !ok {
				t.Fatalf("expected reader function type, got %T", sym.Type)
			}
			if got := PermissionRefsString(fnType.PermissionRefs); got != tt.wantRow {
				t.Fatalf("expected inferred row %q, got %q", tt.wantRow, got)
			}
			if got := PermissionRefsString(fnType.MutableGlobalPermissionRefs); got != tt.wantMandatoryRow {
				t.Fatalf("expected mandatory row %q, got %q", tt.wantMandatoryRow, got)
			}
		})
	}
}

func TestMutableStorageTraversalRecognizesWritableView(t *testing.T) {
	a := &Analyzer{}
	scalar := &BuiltinType{Name: "i32"}
	if !a.typeContainsMutableStorage(&ViewType{Elem: scalar, Mutable: true}, map[Type]bool{}, map[*StructType][][]Type{}, 0) {
		t.Fatal("mutable view of a scalar must expose writable storage")
	}
	if a.typeContainsMutableStorage(&ViewType{Elem: scalar}, map[Type]bool{}, map[*StructType][][]Type{}, 0) {
		t.Fatal("read-only view of a scalar must not expose writable storage")
	}
}
