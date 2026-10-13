//go:build cgo

package backend

import (
	"regexp"
	"testing"
)

const libcNoreturnSrc = `extern exit(code: i32) -> void
extern abort() -> void
extern _exit(code: i64) -> void
extern my_exit(code: i32) -> void

def main(argc: i64, argv: mutable void&) -> i64 can[Unsafe.PointerCast]:
    if argc == 7:
        exit(3)
    if argc == 8:
        abort()
    if argc == 9:
        _exit(4)
    if argc == 10:
        my_exit(5)
    return 0
`

func declAttrs(t *testing.T, ir, name string) string {
	t.Helper()
	m := regexp.MustCompile(`declare [^\n]*@` + regexp.QuoteMeta(name) + `\([^\n]*\) #(\d+)`).FindStringSubmatch(ir)
	if m == nil {
		return ""
	}
	attrs := regexp.MustCompile(`(?m)^attributes #` + m[1] + ` = \{([^\n]*)\}`).FindStringSubmatch(ir)
	if attrs == nil {
		return ""
	}
	return attrs[1]
}

// exit/abort with the C signature are noreturn; a mismatched signature (`_exit(i64)`) and a
// non-libc name stay plain externs.
func TestLibcNoreturnExternsMarkedNoreturnOnlyWithCSignature(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "libc_noreturn.elisa", libcNoreturnSrc)
	ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt: %v", err)
	}
	noreturn := regexp.MustCompile(`\bnoreturn\b`)
	for _, name := range []string{"exit", "abort"} {
		if !noreturn.MatchString(declAttrs(t, ir, name)) {
			t.Fatalf("%s must be declared noreturn:\n%s", name, ir)
		}
	}
	for _, name := range []string{"_exit", "my_exit"} {
		if noreturn.MatchString(declAttrs(t, ir, name)) {
			t.Fatalf("%s must not be declared noreturn:\n%s", name, ir)
		}
	}
}
