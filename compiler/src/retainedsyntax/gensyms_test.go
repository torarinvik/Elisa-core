package retainedsyntax

import "testing"

func TestGeneratedNamesPreserveLocalAndSharedIdentity(t *testing.T) {
	text := "__auto_12 __quantifier_9 __MachineMode_ab_18 __auto_30"
	if got, want := ShiftLocalGensyms(text, 4), "__auto_16 __quantifier_13 __MachineMode_ab_18 __auto_34"; got != want {
		t.Fatalf("shift: got %q, want %q", got, want)
	}
	if got, want := SharedGensyms(text), "__MachineMode_ab_18"; got != want {
		t.Fatalf("shared: got %q, want %q", got, want)
	}
	if NormalizeGensyms("__auto_12") != NormalizeGensyms("__auto_99") {
		t.Fatal("offset changes should normalize equally")
	}
	if !LocalGensymName("__auto_12") || LocalGensymName("__MachineMode_ab_18") {
		t.Fatal("incorrect local-name classification")
	}
}
