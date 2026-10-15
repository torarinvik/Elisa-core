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
	`, "mutable global read", "Global.Read, Global.Write"},
		{"both_grants", `struct Cell:
    value: mutable i32
global mutable cell: Cell = Cell{value: 0}
global mutable alias: mutable Cell& = &cell
def update():
    can Global{Read, Write}:
        alias.value <- 1
	`, "", "Global.Read, Global.Write"},
		{"direct_global_field_write", `struct Cell:
    value: mutable i32
global mutable cell: Cell = Cell{value: 0}
def update():
    can Global.Write:
        cell.value <- 1
	`, "", "Global.Write"},
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
			if got := PermissionRefsString(fnType.PermissionRefs); !strings.Contains(got, tt.wantRow) {
				t.Fatalf("expected inferred row to contain %q, got %q", tt.wantRow, got)
			}
		})
	}
}
