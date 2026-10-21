package semantic

import "testing"

func TestProgramViewReturnDoesNotBorrowCallerFrame(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "program_view_frame.elisa", `global region source
extern stable_copy(text: sview) -> sview @source
struct Holder:
    name: sview @source
def field_name(holder: Holder&) -> sview @source:
    return holder.name
def consume(text: sview @source) -> void:
    return
def caller() -> void:
    can Memory.Allocate, Abort.Panic:
        bytes: mutable darray[u8] = [65]
        text: sview @source = stable_copy(bytes.as_sview())
        consume(text)
        holder: Holder = Holder{name: "stable"}
        field: sview @source = field_name(&holder)
        consume(field)
`)
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("program view retains frame borrow: %v", errors)
	}
}
