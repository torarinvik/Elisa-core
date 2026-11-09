//go:build cgo && (darwin || linux)

package backend

import (
	"fmt"
	"os"
	"strings"
)

// The independent case groups are assembled into the exact original module so
// the unroller still exercises the combined module-wide admission policy.
func tableUnrollAdversarialCases() ([]byte, error) {
	control, err := os.ReadFile("testdata/table_unroll_control_flow_cases.ll")
	if err != nil {
		return nil, err
	}
	large, err := os.ReadFile("testdata/table_unroll_large_and_debug_cases.ll")
	if err != nil {
		return nil, err
	}
	return append(control, large...), nil
}

func tableUnrollDeclineCases(name string) ([]byte, error) {
	if name != "table_unroll_graph_budget.ll" {
		return os.ReadFile("testdata/" + name)
	}
	control, err := os.ReadFile("testdata/table_unroll_control_flow_cases.ll")
	if err != nil {
		return nil, err
	}
	// One function with 70 sequential loops exhausts the shared graph budget.
	var out strings.Builder
	lines := strings.SplitN(string(control), "\n", 4)
	if len(lines) != 4 {
		return nil, fmt.Errorf("missing graph fixture header")
	}
	out.WriteString(strings.Join(lines[:3], "\n") + "\n")
	out.WriteString("define i64 @many_loops() {\nentry:\n  br label %loop0\n")
	for i := 0; i < 70; i++ {
		previous, next := fmt.Sprintf("loop%d", i-1), fmt.Sprintf("loop%d", i+1)
		if i == 0 {
			previous = "entry"
		}
		if i == 69 {
			next = "exit"
		}
		fmt.Fprintf(&out, "loop%d:\n  %%i%d = phi i64 [ 0, %%%s ], [ %%next%d, %%loop%d ]\n", i, i, previous, i, i)
		for j := 0; j < 4; j++ {
			fmt.Fprintf(&out, "  %%ix%d_%d = add i64 %%i%d, %d\n", i, j, i, j*12)
			fmt.Fprintf(&out, "  %%p%d_%d = getelementptr [48 x i64], ptr @table, i64 0, i64 %%ix%d_%d\n", i, j, i, j)
			fmt.Fprintf(&out, "  %%v%d_%d = load i64, ptr %%p%d_%d, align 8\n", i, j, i, j)
		}
		fmt.Fprintf(&out, "  %%next%d = add nuw nsw i64 %%i%d, 1\n", i, i)
		fmt.Fprintf(&out, "  %%done%d = icmp eq i64 %%next%d, 12\n", i, i)
		fmt.Fprintf(&out, "  br i1 %%done%d, label %%%s, label %%loop%d\n", i, next, i)
	}
	out.WriteString("exit:\n  ret i64 0\n}\n")
	return []byte(out.String()), nil
}
