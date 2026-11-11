//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestUnknownStaticDeclarationBranchesDoNotPublishAliases(t *testing.T) {
	source := `
module Secret:
    private:
        def value() -> i64:
            return 1

module Public:
    public:
        def value() -> i64:
            return 2

using Public as Chosen
static if UNKNOWN_IF:
    using Secret as Chosen

static if false:
    using Secret as Chosen
static elif UNKNOWN_ELIF:
    using Secret as Chosen
static else:
    using Secret as Chosen

def main() -> i64:
    return Chosen::value()
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "static_decl_import_visibility.elisa", source, AnalyzeOptions{})
	diagnostics := strings.Join(result.Errors(), "\n")
	for _, condition := range []string{"static if", "static elif"} {
		if !strings.Contains(diagnostics, condition+" condition must be a compile-time bool") {
			t.Errorf("missing %s diagnostic: %s", condition, diagnostics)
		}
	}
	for _, unexpected := range []string{"conflicting alias", "is private to module"} {
		if strings.Contains(diagnostics, unexpected) {
			t.Errorf("unresolved branch leaked an alias: %s", diagnostics)
		}
	}
}
