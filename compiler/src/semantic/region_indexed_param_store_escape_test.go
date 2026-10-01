package semantic

import (
	"strings"
	"testing"
)

// An indexed store into a parameter container (`diags[0] <- v`) writes an element exactly like
// `diags.push(v)`; a value borrowing local storage must be rejected with the push's rule.
const indexedParamStoreEscapePrelude = `struct D:
    name: sview

def check(diags: mutable darray[D]&, other: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        buf: darray[u8] = [65, 66]
        names: mutable darray[sview] = []
        names.push(buf.as_sview())
`

func TestIndexedParamStoreOfLocalBorrowRejected(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "indexed_store_neg.elisa",
		indexedParamStoreEscapePrelude+"        diags[0] <- D{name: names[0]}\n").Errors(), " | ")
	if !strings.Contains(errs, `is stored into longer-lived region "__rg_diags"`) {
		t.Fatalf("indexed store of a local-buffer view into a parameter container must be rejected, got: %q", errs)
	}
}

func TestIndexedParamStoreOfParamOrLiteralAccepted(t *testing.T) {
	for _, value := range []string{"other[0]", `"lit"`} {
		errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "indexed_store_pos.elisa",
			indexedParamStoreEscapePrelude+"        diags[0] <- D{name: "+value+"}\n").Errors(), " | ")
		if errs != "" {
			t.Fatalf("indexed store of %s into a parameter container must compile, got: %s", value, errs)
		}
	}
}
