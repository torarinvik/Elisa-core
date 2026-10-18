//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

const clonedBytePrefixStep = `# FNV-1a byte fold for the enum-annotation owner buckets. The hash is only a candidate key:
# consumers compare the original owner/name strings before accepting a row.
module Backend:
    private:
        const ANNOTATION_INDEX_HASH_OFFSET: u64 = 14695981039346656037
        const ANNOTATION_INDEX_HASH_PRIME: u64 = 1099511628211

        # Hash the prefix before INDEX, starting from HASH.
        ghost def annotation_index_hash_prefix(text: sview, index: usize, hash: u64) -> u64:
            requires index <= text.len
            decreases index
            ensure index == 0 implies result == hash
            ensure index > 0 implies result == (annotation_index_hash_prefix(text, index - 1.usize(), hash) + text[index - 1.usize()].u64()) * ANNOTATION_INDEX_HASH_PRIME
            return hash if index == 0
            return (annotation_index_hash_prefix(text, index - 1.usize(), hash) + text[index - 1.usize()].u64()) * ANNOTATION_INDEX_HASH_PRIME

        ghost def prefix_step(text: sview, index: usize) -> bool:
            requires index < text.len
            ensure annotation_index_hash_prefix(text, index + 1.usize(), ANNOTATION_INDEX_HASH_OFFSET) == ((annotation_index_hash_prefix(text, index, ANNOTATION_INDEX_HASH_OFFSET) + text[index].u64()) * ANNOTATION_INDEX_HASH_PRIME)
            return true

def main() -> i64:
    return 0
`

func TestSMTCharCalleeBytePrefixStep(t *testing.T) {
	r := analyzeContractStrict(t, "hash_prefix_step.elisa", clonedBytePrefixStep)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("exact recursive byte fold must prove: %v", errs)
	}
}

func TestSMTCharCalleeBytePrefixRejectsDifferentByte(t *testing.T) {
	source := strings.Replace(clonedBytePrefixStep, "+ text[index].u64()", "+ text[0].u64()", 1)
	if source == clonedBytePrefixStep {
		t.Fatal("negative mutation did not apply")
	}
	r := analyzeContractStrict(t, "hash_prefix_wrong_byte.elisa", source)
	if !strings.Contains(strings.Join(r.Errors(), "\n"), "ensure postcondition of \"prefix_step\"") {
		t.Fatalf("wrong byte must not prove: %v", r.Errors())
	}
}

func TestSMTCharCalleeBytePrefixRejectsDifferentView(t *testing.T) {
	source := strings.Replace(clonedBytePrefixStep, "prefix_step(text: sview, index: usize)", "prefix_step(text: sview, other: sview, index: usize)", 1)
	source = strings.Replace(source, "            requires index < text.len", "            requires index < text.len\n            requires index < other.len", 1)
	source = strings.Replace(source, "+ text[index].u64()", "+ other[index].u64()", 1)
	if !strings.Contains(source, "+ other[index].u64()") {
		t.Fatal("different-view mutation did not apply")
	}
	r := analyzeContractStrict(t, "hash_prefix_wrong_view.elisa", source)
	if !strings.Contains(strings.Join(r.Errors(), "\n"), "ensure postcondition of \"prefix_step\"") {
		t.Fatalf("different view must not prove: %v", r.Errors())
	}
}

func TestSMTCharToU64PreservesNegativeBits(t *testing.T) {
	source := `def convert(ch: char) -> u64:
    requires ch.i64() == -1
    ensure result == 18446744073709551615u64
    return ch.u64()
`
	r := analyzeContractStrict(t, "negative_char_wrap.elisa", source)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("char -1 must retain all64bits: %v", errs)
	}
	wrong := strings.Replace(source, "ensure result == 18446744073709551615u64", "ensure result == 0", 1)
	r = analyzeContractStrict(t, "negative_char_wrong.elisa", wrong)
	if !strings.Contains(strings.Join(r.Errors(), "\n"), "ensure postcondition") {
		t.Fatalf("negative char must not prove zero: %v", r.Errors())
	}
}

func TestSMTCharCalleeXorPrefixStep(t *testing.T) {
	source := strings.ReplaceAll(clonedBytePrefixStep, " + ", " ^ ")
	// Keep the index increment arithmetic rather than changing it to XOR.
	source = strings.ReplaceAll(source, "index ^ 1.usize()", "index + 1.usize()")
	r := analyzeContractStrict(t, "xor_byte_prefix.elisa", source)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("recursive XOR prefix step must prove: %v", errs)
	}
}

func TestSMTCharCalleeXorPrefixRejectsDifferentByteOrView(t *testing.T) {
	for _, other := range []bool{false, true} {
		source := strings.ReplaceAll(clonedBytePrefixStep, " + ", " ^ ")
		source = strings.ReplaceAll(source, "index ^ 1.usize()", "index + 1.usize()")
		if other {
			source = strings.Replace(source, "prefix_step(text: sview, index: usize)", "prefix_step(text: sview, other: sview, index: usize)", 1)
			source = strings.Replace(source, "            requires index < text.len", "            requires index < text.len\n            requires index < other.len", 1)
			source = strings.Replace(source, "^ text[index].u64()", "^ other[index].u64()", 1)
		} else {
			source = strings.Replace(source, "^ text[index].u64()", "^ text[0].u64()", 1)
		}
		r := analyzeContractStrict(t, "xor_prefix_false.elisa", source)
		if !strings.Contains(strings.Join(r.Errors(), "\n"), "ensure postcondition of \"prefix_step\"") {
			t.Fatalf("false XOR prefix must not prove (other=%v): %v", other, r.Errors())
		}
	}
}
