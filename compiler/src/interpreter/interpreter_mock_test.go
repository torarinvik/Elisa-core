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

// Capstone: the eh_frame_hdr decode logic (the very bug that started this) stepped against a
// captured eh_frame_hdr served from mocked guest memory. The decode reads bytes via
// guest_read_u8, and the debugger can break on the computed eh_frame_addr -- proving the
// mock-memory lever works for the real debugging case.
func TestInterpreterDecodeEhFrameAgainstMockedMemory(t *testing.T) {
	src := `extern guest_read_u8(addr: i64) -> i64

def decode_eh_frame_addr(hdr_addr: i64, base: i64) -> i64:
    version: i64 = guest_read_u8(hdr_addr)
    if version != 1:
        return 0
    b0: i64 = guest_read_u8(hdr_addr + 4)
    b1: i64 = guest_read_u8(hdr_addr + 5)
    b2: i64 = guest_read_u8(hdr_addr + 6)
    b3: i64 = guest_read_u8(hdr_addr + 7)
    raw: i64 = b0 | (b1 << 8) | (b2 << 16) | (b3 << 24)
    value: mutable i64 = raw
    if (raw & 0x80000000) != 0:
        value <- raw - 0x100000000
    eh_frame_ptr: i64 = value + hdr_addr + 4
    eh_frame_addr: i64 = eh_frame_ptr - base
    return eh_frame_addr

def run() -> i64:
    return decode_eh_frame_addr(0x11000, 0x10000)
`
	// Captured eh_frame_hdr: version=1, enc=pcrel|sdata4, sdata4 = -0x104 (FC FE FF FF).
	mem := map[int64]int64{
		0x11000: 1, 0x11001: 0x1B, 0x11002: 3, 0x11003: 0x3B,
		0x11004: 0xFC, 0x11005: 0xFE, 0x11006: 0xFF, 0x11007: 0xFF,
	}
	mocks := map[string]func([]interpreter.Value) (interpreter.Value, error){
		"guest_read_u8": func(args []interpreter.Value) (interpreter.Value, error) {
			addr, _ := strconv.ParseInt(args[len(args)-1].String(), 10, 64)
			return interpreter.IntValue(mem[addr]), nil
		},
	}
	analyzed := parseAndAnalyzeInterpreterTest(t, "interpreter_ehframe_mock.elisa", src)
	// Plain run: the decode computes eh_frame_addr = 0xF00 from the mocked bytes.
	res, err := interpreter.Execute(analyzed, interpreter.Options{Entry: "run", Mocks: mocks})
	if err != nil {
		t.Fatalf("decode against mocked memory: %v", err)
	}
	if got := res.Return.String(); got != "3840" { // 0xF00
		t.Fatalf("expected eh_frame_addr 0xF00 (3840), got %s", got)
	}
	// Under the debugger: break on the computed value, ready to step backward to the bytes.
	debugger := interpreter.NewDebugger(interpreter.BreakWhenExpr("eh_frame_addr == 0xF00"))
	_, err = interpreter.Execute(analyzed, interpreter.Options{Entry: "run", Debugger: debugger, Mocks: mocks})
	var halt *interpreter.DebugHaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected halt on the computed eh_frame_addr, got %v", err)
	}
}
