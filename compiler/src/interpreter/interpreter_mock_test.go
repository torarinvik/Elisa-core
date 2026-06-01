package interpreter_test

import (
	"errors"
	"strconv"
	"testing"

	"elisacore/src/interpreter"
)

// A function mock lets pure logic be stepped against injected data: here an extern
// guest_read_byte is served from a host-side "memory" map instead of executing a native
// body, which is the foundation for debugging real port logic against captured guest memory.
func TestInterpreterFunctionMockServesInjectedData(t *testing.T) {
	src := `extern guest_read_byte(addr: i64) -> i64

def run() -> i64:
    a: i64 = guest_read_byte(16)
    b: i64 = guest_read_byte(17)
    return a + b
`
	result := parseAndAnalyzeInterpreterTest(t, "interpreter_mock.elisa", src)
	mem := map[int64]int64{16: 0xAB, 17: 0xCD}
	mocks := map[string]func([]interpreter.Value) (interpreter.Value, error){
		"guest_read_byte": func(args []interpreter.Value) (interpreter.Value, error) {
			addr, _ := strconv.ParseInt(args[len(args)-1].String(), 10, 64)
			return interpreter.IntValue(mem[addr]), nil
		},
	}
	res, err := interpreter.Execute(result, interpreter.Options{Entry: "run", Mocks: mocks})
	if err != nil {
		t.Fatalf("execute with mock: %v", err)
	}
	if got := res.Return.String(); got != "376" { // 0xAB + 0xCD
		t.Fatalf("expected mocked sum 376, got %s", got)
	}
}

// The debugger can break on a value produced from a mocked call -- so logic driven by
// injected (memory) data can be inspected and time-traveled like any other.
func TestInterpreterFunctionMockWorksWithDebugger(t *testing.T) {
	src := `extern guest_read_byte(addr: i64) -> i64

def run() -> i64:
    a: i64 = guest_read_byte(16)
    return a
`
	result := parseAndAnalyzeInterpreterTest(t, "interpreter_mock_debug.elisa", src)
	mocks := map[string]func([]interpreter.Value) (interpreter.Value, error){
		"guest_read_byte": func(args []interpreter.Value) (interpreter.Value, error) {
			return interpreter.IntValue(0xAB), nil
		},
	}
	debugger := interpreter.NewDebugger(interpreter.BreakWhenExpr("a == 0xAB"))
	_, err := interpreter.Execute(result, interpreter.Options{Entry: "run", Debugger: debugger, Mocks: mocks})
	var halt *interpreter.DebugHaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected halt on value from mocked call, got %v", err)
	}
}
