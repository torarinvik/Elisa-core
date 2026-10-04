//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

const typestateLoopJoinProtocol = `typestate ProtocolFile:
    id: i64
    states: Closed, Open
    transition open_file: Closed -> Open
    transition read_file: Open -> Open

`

func TestTypestateLoopJoin(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		wantError bool
	}{
		{"for_zero_iteration", `def use(file: mutable ProtocolFile[Closed]&, count: usize) ensures file => Open:
    for index in 0..<count |file|:
        open_file(file)
    read_file(file)
`, true},
		{"while_zero_iteration", `def use(file: mutable ProtocolFile[Closed]&, run: bool) ensures file => Open:
    while run |file|:
        open_file(file)
    read_file(file)
`, true},
		{"for_preserve", `def use(file: mutable ProtocolFile[Open]&, count: usize):
    for index in 0..<count |file|:
        read_file(file)
    read_file(file)
`, false},
		{"while_preserve", `def use(file: mutable ProtocolFile[Open]&, run: bool):
    while run |file|:
        read_file(file)
    read_file(file)
`, false},
		{"match_same_exit", `def use(file: mutable ProtocolFile[Closed]&, flag: i64) ensures file => Open:
    match flag:
        0:
            open_file(file)
        _:
            open_file(file)
    read_file(file)
`, false},
		{"match_different_exits", `def use(file: mutable ProtocolFile[Closed]&, flag: i64) ensures file => Open:
    match flag:
        0:
            open_file(file)
        _:
            pass
    read_file(file)
`, true},
		{"match_arm_uses_entry_state", `def use(file: mutable ProtocolFile[Closed]&, flag: i64) ensures file => Open:
    match flag:
        0:
            open_file(file)
        _:
            read_file(file)
`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "typestate_loop_join.elisa", typestateLoopJoinProtocol+tc.body, AnalyzeOptions{})
			if got := len(result.Errors()) != 0; got != tc.wantError {
				t.Fatalf("error=%v, want %v: %v", got, tc.wantError, result.Errors())
			}
			if tc.wantError && !strings.Contains(strings.Join(result.Errors(), "\n"), `argument 1 to "read_file"`) {
				t.Fatalf("expected rejected post-loop read, got %v", result.Errors())
			}
		})
	}
}
