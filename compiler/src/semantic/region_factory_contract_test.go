package semantic

import (
	"os"
	"strings"
	"testing"
)

func TestFactoryAggregateIndependentInputContract(t *testing.T) {
	b, e := os.ReadFile("../../../docs/repros/explicit-region-factory-independent-input.elisa")
	if e != nil {
		t.Fatal(e)
	}
	base := string(b)
	for _, tc := range []struct {
		name, source string
		reject       bool
	}{
		{"independent", base, true},
		{"same", strings.ReplaceAll(strings.ReplaceAll(base, "@s", "@r"), "[@r, @r]", "[@r]"), false},
		{"alias", strings.Replace(base, "    return Packet[r]{bytes: bytes}", "    packet: Packet[r] = Packet[r]{bytes: bytes}\n    return packet", 1), true},
		{"nested", strings.Replace(strings.Replace(base, "def wrong", "struct Wrapper[@r]:\n    packet: Packet[r]\n\ndef wrong", 1), "-> Packet[r] @r:\n    return Packet[r]{bytes: bytes}", "-> Wrapper[r] @r:\n    return Wrapper[r]{packet: Packet[r]{bytes: bytes}}", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "contract.elisa", tc.source, AnalyzeOptions{})
			if !tc.reject && len(r.Errors()) != 0 {
				t.Fatal(r.Errors())
			}
			if tc.reject && !strings.Contains(strings.Join(r.Errors(), "\n"), "independent region parameter") {
				t.Fatalf("unchecked independent input: %v", r.Errors())
			}
		})
	}
}
