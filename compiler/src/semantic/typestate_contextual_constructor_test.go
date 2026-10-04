//go:build cgo

package semantic

import "testing"

func TestTypestateContextualConstructor(t *testing.T) {
	for _, tc := range []struct {
		name      string
		value     string
		wantError bool
	}{
		{"valid", "true", false},
		{"contradictory", "false", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `struct ProtocolFile[state Open | Closed]:
    is_open: mutable bool
    derive state:
        Open when self.is_open == true
        Closed when self.is_open == false

def use() -> bool:
    file: ProtocolFile[Open] = ProtocolFile{is_open: ` + tc.value + `}
    return file.is_open
`
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "contextual_constructor.elisa", src, AnalyzeOptions{})
			if got := len(result.Errors()) != 0; got != tc.wantError {
				t.Fatalf("error=%v, want %v: %v", got, tc.wantError, result.Errors())
			}
		})
	}
}
