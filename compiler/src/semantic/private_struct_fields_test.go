package semantic

import (
	"strings"
	"testing"
)

const privateFieldFixture = `
module Vault:
    struct Handle:
        private:
            value: mutable i64
        public:
            tag: i64
    def Handle() -> Handle:
        Handle{value: 41, tag: 1}
    def read(h: Handle&) -> i64:
        h.value
`

func TestPrivateStructFields(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		reject     bool
	}{
		{"constructor", "def main() -> i64:\n    h = Vault::Handle()\n    Vault::read(&h)\n", false},
		{"public", "def main() -> i64:\n    h = Vault::Handle()\n    h.tag\n", false},
		{"read", "def main() -> i64:\n    h = Vault::Handle()\n    h.value\n", true},
		{"reference", "def get(h: Vault::Handle&) -> i64:\n    h.value\n", true},
		{"write", "def set(h: mutable Vault::Handle&) -> void:\n    h.value <- 0\n", true},
		{"literal", "def main() -> i64:\n    h = Vault::Handle{value: 0, tag: 1}\n    h.tag\n", true},
		{"pattern", "def main() -> i64:\n    h = Vault::Handle()\n    match h:\n        Vault::Handle{value: v}: v\n", true},
		{"alias", "type Alias = Vault::Handle\ndef get(h: Alias&) -> i64:\n    h.value\n", true},
		{"descendant", "module Vault::Child:\n    def get(h: Vault::Handle&) -> i64:\n        h.value\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "private_fields.elisa", privateFieldFixture+tc.body, AnalyzeOptions{})
			var messages []string
			for _, d := range result.Diagnostics {
				messages = append(messages, d.Message)
			}
			all := strings.Join(messages, "\n")
			if strings.Contains(all, "is private to module") != tc.reject {
				t.Fatalf("privacy rejection=%v; diagnostics:\n%s", tc.reject, all)
			}
			if !tc.reject && len(messages) != 0 {
				t.Fatal(all)
			}
		})
	}
}
