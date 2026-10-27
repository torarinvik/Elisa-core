package semantic

import (
	"reflect"
	"strings"
	"testing"
)

func TestGenericSubmitRejectsStoreBuildingWorker(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "generic_submit_grow.elisa", submitStoreCapturePrelude+`
def grow(e: Expr) -> i64:
    can Memory.Allocate, Abort.Panic:
        return eval(build(3)) + eval(e)

def worker(e: Expr) -> i64:
    can Memory.Allocate, Abort.Panic:
        return grow(e)

def submit_generic[T](marker: T, e: Expr) -> i64:
    can Memory.Allocate, Parallel, Abort.Panic:
        nursery workers(2):
            submit worker(e)
        return 0

def main() -> i64:
    can Memory.Allocate, Parallel, Abort.Panic:
        e: Expr = build(10)
        return submit_generic[i64](0, e)
`)
	got := strings.Join(result.Errors(), "\n")
	if !strings.Contains(got, `"worker" is submitted to another thread and shares the submitting scope`) {
		t.Fatalf("generic submission failed to reject store-building worker:\n%s", got)
	}
}

func TestGenericSubmitParallelDiagnosticsMatchSerial(t *testing.T) {
	source := []byte(submitStoreCapturePrelude + `
def grow(e: Expr) -> i64:
    can Memory.Allocate, Abort.Panic:
        return eval(build(3)) + eval(e)

def worker(e: Expr) -> i64:
    can Memory.Allocate, Abort.Panic:
        return grow(e)

def submit_generic[T](marker: T, e: Expr) -> i64:
    can Memory.Allocate, Parallel, Abort.Panic:
        nursery workers(2):
            submit worker(e)
        return 0

def caller_a(e: Expr) -> i64:
    return submit_generic[i64](0, e)

def caller_b(e: Expr) -> i64:
    return submit_generic[i64](1, e)

def main() -> i64:
    can Memory.Allocate, Parallel, Abort.Panic:
        e: Expr = build(10)
        return caller_a(e) + caller_b(e)
`)
	t.Setenv("ELISA_PARALLEL_BODIES", "1")
	serial := fuzzAnalyzeText(source)
	if !strings.Contains(serial, "shares the submitting scope") {
		t.Fatalf("serial analysis failed to reject unsafe worker: %s", serial)
	}
	t.Setenv("ELISA_PARALLEL_BODIES", "4")
	for i := 0; i < 5; i++ {
		if got := fuzzAnalyzeText(source); got != serial {
			t.Fatalf("parallel diagnostics differ on run %d:\n%s\nvs serial:\n%s", i, got, serial)
		}
	}
}

func TestGenericSubmitReadOnlyParallelResultMatchesSerial(t *testing.T) {
	source := submitStoreCapturePrelude + `
def worker(e: Expr) -> i64:
    can Abort.Panic:
        return eval(e)

def submit_generic[T](marker: T, e: Expr) -> i64:
    can Memory.Allocate, Abort.Panic:
        nursery workers(2):
            submit worker(e)
        return 0

def caller_a(e: Expr) -> i64:
    return submit_generic[i64](0, e)

def caller_b(e: Expr) -> i64:
    return submit_generic[i64](1, e)

def main() -> i64:
    can Memory.Allocate, Abort.Panic:
        e: Expr = build(10)
        return caller_a(e) + caller_b(e)
`
	t.Setenv("ELISA_PARALLEL_BODIES", "1")
	serial := Analyze(parseIncrementalSessionFile(t, source))
	if len(serial.Errors()) != 0 {
		t.Fatalf("serial errors: %v", serial.Errors())
	}
	t.Setenv("ELISA_PARALLEL_BODIES", "4")
	parallel := Analyze(parseIncrementalSessionFile(t, source))
	if len(parallel.Errors()) != 0 {
		t.Fatalf("parallel errors: %v", parallel.Errors())
	}
	if !reflect.DeepEqual(serial.Diagnostics, parallel.Diagnostics) || !reflect.DeepEqual(serial.ProofReport, parallel.ProofReport) || !reflect.DeepEqual(serial.ElisionSummary, parallel.ElisionSummary) {
		t.Fatalf("parallel diagnostics or proof report differ from serial: serial=%v parallel=%v", serial.Diagnostics, parallel.Diagnostics)
	}
}
