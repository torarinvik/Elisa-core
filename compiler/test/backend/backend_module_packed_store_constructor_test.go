package backend_test

import (
	"elisacore/src/backend"
	"testing"
)

// `Message.Store(owner)` written inside the module that declares `packed enum Message`
// must lower as a store constructor. The backend resolved the owner with a raw lookup of
// the bare spelling, missed the module-qualified registry entry, and fell through to the
// enum-constructor path: "unknown enum constructor" for a program the analyzer accepts.
func TestGenerateLLVMIRLowersPackedStoreConstructorInsideModule(t *testing.T) {
	src := `module Right:
	packed enum Message:
		A(x: i64)
		B(y: i64)

	def run(owner: Arena) -> i64:
		store: Message.Store[Local] = Message.Store(owner)
		in store:
			n: Message = new Message.B(y: 42)
			match n:
				Message.A(x): return x
				Message.B(y): return y
		return 0

def main() -> i64:
	region r(4096):
		return Right::run(r)
`
	result := parseAndAnalyze(t, "backend_module_packed_store_ctor.elisa", src)
	if _, err := backend.GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}
