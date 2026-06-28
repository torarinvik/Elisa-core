//go:build cgo

package backend

import (
	"strconv"
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersRegularEnumValuesWithoutArenaAlloc(t *testing.T) {
	src := `enum Small:
    Int(value: i64)
    Pair(left: i64, right: i64)
    Done

def make_node(value: i64) -> Small:
    return Small.Int(value)

def score(node: Small) -> i64:
    match node:
        Small.Int(value):
            return value
        Small.Pair(left, right):
            return left + right
        Small.Done:
            return 0

def total(seed: i64) -> i64:
    items: array[Small, 3] = [Small.Int(1), make_node(seed), Small.Done]
    maybe: Small? = Small.Pair(2, 3)
    total: i64 = score(items[0]) + score(items[1]) + score(items[2])
    if maybe is node:
        total = total + score(node)
    return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_regular_enum_values.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"%Small = type { i32, [2 x i64] }",
		"define %Small @make_node(i64",
		"define i64 @score(%Small",
		"define i64 @total(i64",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected regular enum value lowering to include %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "@arena_alloc") {
		t.Fatalf("expected regular enum value lowering to avoid arena allocation helpers, got:\n%s", output)
	}
}

func tagOnlyEnumWidthSource(enumName string, variantCount int, suffix string) string {
	var b strings.Builder
	b.WriteString("enum ")
	b.WriteString(enumName)
	b.WriteString(":\n")
	for i := 0; i < variantCount; i++ {
		b.WriteString("    V")
		b.WriteString(strconv.Itoa(i))
		b.WriteByte('\n')
	}
	last := "V" + strconv.Itoa(variantCount-1)
	b.WriteString("\ndef pick_tag_")
	b.WriteString(suffix)
	b.WriteString("() -> ")
	b.WriteString(enumName)
	b.WriteString(":\n    return ")
	b.WriteString(enumName)
	b.WriteByte('.')
	b.WriteString(last)
	b.WriteString("\n\ndef is_last_tag_")
	b.WriteString(suffix)
	b.WriteString("(value: ")
	b.WriteString(enumName)
	b.WriteString(") -> bool:\n    return value == ")
	b.WriteString(enumName)
	b.WriteByte('.')
	b.WriteString(last)
	b.WriteByte('\n')
	return b.String()
}

func TestGenerateLLVMIRNarrowsTagOnlyRegularEnumToU8Through256Variants(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_tag_only_enum_u8.elisa", tagOnlyEnumWidthSource("TagOnly256", 256, "u8"))
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"define i8 @pick_tag_u8()",
		"define i1 @is_last_tag_u8(i8",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tag-only enum to lower with i8 in %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRNarrowsTagOnlyRegularEnumToU16After256Variants(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_tag_only_enum_u16.elisa", tagOnlyEnumWidthSource("TagOnly257", 257, "u16"))
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"define i16 @pick_tag_u16()",
		"define i1 @is_last_tag_u16(i16",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tag-only enum to lower with i16 in %q, got:\n%s", check, output)
		}
	}
}
