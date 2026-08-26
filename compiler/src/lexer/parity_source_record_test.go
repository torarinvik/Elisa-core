package lexer

import "testing"

func TestParitySourceIsReleasedAfterItsConsumersFinish(t *testing.T) {
	oldEnabled := parityRecordEnabled
	oldSources := paritySources
	defer func() {
		parityRecordEnabled = oldEnabled
		paritySources = oldSources
	}()

	parityRecordEnabled = true
	paritySources = nil
	t.Setenv("ELISA_SEMANTIC_INTERNAL_PARITY_OUT", "semantic.tsv")
	t.Setenv("ELISA_BACKEND_PARITY_OUT", "backend.tsv")

	recordParitySource("release.elisa", []byte("def main() -> i64:\n    return 42\n"))
	first, ok := ConsumeParitySource("release.elisa")
	if !ok || first == "" {
		t.Fatalf("first parity consumer did not receive the source")
	}
	if _, ok := ParitySourceFor("release.elisa"); !ok {
		t.Fatalf("source was released before the second parity consumer")
	}
	second, ok := ConsumeParitySource("release.elisa")
	if !ok || second != first {
		t.Fatalf("second parity consumer received %q, want %q", second, first)
	}
	if _, ok := ConsumeParitySource("release.elisa"); ok {
		t.Fatalf("parity source remained retained after its final consumer")
	}
}

func TestDropParitySourceReleasesUnparseableInput(t *testing.T) {
	oldEnabled := parityRecordEnabled
	oldSources := paritySources
	defer func() {
		parityRecordEnabled = oldEnabled
		paritySources = oldSources
	}()

	parityRecordEnabled = true
	paritySources = nil
	t.Setenv("ELISA_SEMANTIC_INTERNAL_PARITY_OUT", "semantic.tsv")
	t.Setenv("ELISA_BACKEND_PARITY_OUT", "")
	recordParitySource("bad.elisa", []byte("not retained forever"))
	DropParitySource("bad.elisa")
	if _, ok := ParitySourceFor("bad.elisa"); ok {
		t.Fatalf("unparseable parity source was not dropped")
	}
}
