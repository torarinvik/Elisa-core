package semantic

import (
	"strings"
	"testing"
)

const protocolParameterSource = `# Protocol annotations infer independent concrete receiver types.
protocol Painter:
    def paint(self: Self) -> i32
struct Red:
    value: i32
struct Blue:
    value: i32
impl Painter for Red:
    def paint(self: Self) -> i32:
        return self.value
impl Painter for Blue:
    def paint(self: Self) -> i32:
        return self.value * 2
def draw(painter: Painter) -> i32:
    return painter.paint()
def pair(left: Painter, right: Painter) -> i32:
    return left.paint() + right.paint()
def explicit[P: Painter](painter: P) -> i32:
    return draw(painter)
def main() -> i32:
    a: Red = Red{value: 17}
    b: Red = Red{value: 25}
    c: Blue = Blue{value: 6}
    return 1 if draw(a) != 17 else 0 if draw(b) == 25 and pair(a, c) == 29 and explicit(b) == 25 else 2
`

func TestProtocolValueParameters(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "protocol_parameters.elisa", protocolParameterSource)
}
func TestProtocolParameterRejectsMissingImplementation(t *testing.T) {
	source := `protocol Painter:
    def paint(self: Self) -> i32
struct Red:
    value: i32
def draw(painter: Painter) -> i32:
    return painter.paint()
def main() -> i32:
    return draw(Red{value: 1})
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "protocol_parameter_invalid.elisa", source)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "does not satisfy required interface fact") {
		t.Fatalf("expected bound failure: %v", result.Errors())
	}
}
func TestProtocolRequiresConcreteReturnType(t *testing.T) {
	source := "protocol Painter:\n    def paint(self: Self) -> i32\ndef bad() -> Painter:\n    return 0\n"
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "protocol_return.elisa", source)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "is a constraint, not a concrete value type") {
		t.Fatalf("expected protocol diagnostic: %v", result.Errors())
	}
}
func TestProtocolParameterGeneratedNameHygiene(t *testing.T) {
	source := strings.Replace(protocolParameterSource, "def draw(painter: Painter)", "def draw[__protocol_arg_0](painter: Painter, other: __protocol_arg_0)", 1)
	source = strings.ReplaceAll(source, "draw(a)", "draw(a, a)")
	source = strings.ReplaceAll(source, "draw(b)", "draw(b, b)")
	source = strings.ReplaceAll(source, "draw(painter)", "draw(painter, painter)")
	analyzeFunctionAnalysisTestSource(t, "protocol_hygiene.elisa", source)
}
