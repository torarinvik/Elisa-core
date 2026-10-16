//go:build cgo

package semantic

import "testing"

// A folded char classifier (`return __classtable_f[c]`) proves its ensures through the constant
// table's values; a wrong class stays unproven.
func TestSMTFoldedClassifierTable(t *testing.T) {
	src := `
module M:
    public:
        const enum NC of u8:
            Digit
            Dot
            Other

        def classify(character: char) -> NC:
            ensure character in '0'..='9' implies result == NC.Digit
            ensure character == '.' implies result == NC.Dot
            ensure not (character in '0'..='9' or character == '.') implies result == NC.Other
            return match character:
                '0'..='9': NC.Digit
                '.': NC.Dot
                _: NC.Other

        def classify_false(character: char) -> NC:
            ensure character in '0'..='9' implies result == NC.Other
            return match character:
                '0'..='9': NC.Digit
                '.': NC.Dot
                _: NC.Other
`
	result := analyzeContractStrict(t, "classify.elisa", src)
	if n := ensureFailuresFor(result.Errors(), "classify"); n != 0 {
		t.Fatalf("expected the folded table to prove the classifier ensures, got: %v", result.Errors())
	}
	if ensureFailuresFor(result.Errors(), "classify_false") == 0 {
		t.Fatalf("the wrong-class ensure must stay unproven; got: %v", result.Errors())
	}
}
