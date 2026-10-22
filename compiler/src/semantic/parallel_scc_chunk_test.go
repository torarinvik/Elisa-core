package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"testing"
)

func TestRecursiveBodyChunkOverridePreservesEveryMember(t *testing.T) {
	const members = 17
	var bodies []scopedDecl
	for i := 0; i < members; i++ {
		call := &ast.CallExpr{Func: &ast.Ident{Name: fmt.Sprintf("f%d", (i+1)%members)}}
		fn := &ast.FuncDecl{Name: fmt.Sprintf("f%d", i), Body: []ast.Stmt{&ast.ExprStmt{Expr: call}}}
		bodies = append(bodies, scopedDecl{Decl: fn})
	}

	for _, chunk := range []int{1, 4, 8, 64} {
		t.Run(fmt.Sprint(chunk), func(t *testing.T) {
			t.Setenv("ELISA_PARALLEL_SCC_CHUNK", fmt.Sprint(chunk))
			schedule := planBodySchedule(bodies, 4)
			if len(schedule.units) != (members+chunk-1)/chunk {
				t.Fatalf("got %d units for chunk %d", len(schedule.units), chunk)
			}
			seen := map[int]bool{}
			for _, unit := range schedule.units {
				if len(unit) > chunk {
					t.Fatal("unit exceeds chunk bound")
				}
				for _, body := range unit {
					if seen[body] {
						t.Fatal("duplicate body")
					}
					seen[body] = true
				}
			}
			if len(seen) != members || len(schedule.cyclic) != members {
				t.Fatal("recursive member or summary seed lost")
			}
		})
	}
}

func TestRecursiveBodyChunkOverrideRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "65", "invalid"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ELISA_PARALLEL_SCC_CHUNK", value)
			if got := parallelSCCChunkSize(); got != 8 {
				t.Fatalf("invalid value %q changed default to %d", value, got)
			}
		})
	}
}
