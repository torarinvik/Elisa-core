package main

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func regexTestPattern(rng *rand.Rand, depth int) string {
	atoms := []string{"a", "b", "c", "A", ".", "[ab]", "[^a]", "[a-c]", "[^B]", `\n`, `[^\n]`, `\|`, "a{2}", "b{0,1}"}
	if depth <= 0 || rng.Intn(3) == 0 {
		return atoms[rng.Intn(len(atoms))]
	}
	switch rng.Intn(9) {
	case 6:
		return "(" + regexTestPattern(rng, depth-1) + "){1,2}"
	case 7:
		return "(" + regexTestPattern(rng, depth-1) + "){2,}"
	case 8:
		return "((?i)" + regexTestPattern(rng, depth-1) + ")"
	case 0:
		return regexTestPattern(rng, depth-1) + "|" + regexTestPattern(rng, depth-1)
	case 1:
		return "(" + regexTestPattern(rng, depth-1) + ")*"
	case 2:
		return "(" + regexTestPattern(rng, depth-1) + ")+"
	case 3:
		return "(" + regexTestPattern(rng, depth-1) + ")?"
	default:
		return regexTestPattern(rng, depth-1) + regexTestPattern(rng, depth-1)
	}
}

// TestStdRegexMatchesGoLongest differentially checks elisacore_regex.elisa against
// Go's regexp in leftmost-longest mode (count of non-overlapping matches and
// replace-all output), plus the regex-redux patterns and rejected syntax.
func TestStdRegexMatchesGoLongest(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	runtimeDir, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std"))
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(20261014))
	var program, expected strings.Builder
	fmt.Fprintf(&program, "include %q\ninclude %q\n", filepath.Join(runtimeDir, "elisacore_runtime.elisa"), filepath.Join(runtimeDir, "elisacore_regex.elisa"))
	program.WriteString(`extern printf(format: cstr, ...) -> i32 can[Console, Unsafe.RawExtern]

def check(out: mutable darray[u8]&, pattern: sview, input: sview) -> void can[Abort.Panic, Memory.Allocate]:
    text: mutable darray[u8] = []
    for i in 0..<input.len.usize() |text, input|:
        text.push(input[i].u8())
    re: mutable Regex = regex_new()
    if not regex_compile(&re, pattern):
        out.push('!'.u8())
        out.push('\n'.u8())
        return
    count: mutable usize = regex_count(&re, text)
    digits: mutable darray[u8] = []
    digits.push('0'.u8()) if count == 0
    while count > 0 |count, digits|:
        digits.push('0'.u8() + (count % 10).u8())
        count <- count / 10
    index: mutable usize = digits.count
    while index > 0 |index, digits, out|:
        index <- index - 1
        out.push(digits[index])
    out.push(' '.u8())
    replaced: mutable darray[u8] = []
    regex_replace_all(&re, text, "<>", &replaced)
    for byte in replaced |out|:
        out.push('~'.u8() if byte == 10 else byte)
    out.push('\n'.u8())

def main() -> i32 can[Memory.Allocate, Abort.Panic, Console, Unsafe.RawExtern, Unsafe.PointerCast]:
    out: mutable darray[u8] = []
`)
	type regexCase struct{ pattern, text string }
	var cases []regexCase
	redux := []string{"agggtaaa|tttaccct", "[cgt]gggtaaa|tttaccc[acg]", "a[act]ggtaaa|tttacc[agt]t", "tHa[Nt]", "aND|caN|Ha[DS]|WaS", "a[NSt]|BY", "<[^>]*>", `\|[^|][^|]*\|`, ">.*\n|\n"}
	reduxText := ">ONE x\nagggtaaatttaccctHaNtaNDcaNHaSWaSaNBY<x>|a|b||\ncgggtaaatttacccg<>\n"
	for _, p := range redux {
		cases = append(cases, regexCase{p, reduxText})
	}
	for _, p := range []string{"(a", "a)", "[ab", "*a", "a|*", `a\`, "a{2", "a{3,1}", "(?s)a"} {
		cases = append(cases, regexCase{p, "aa"})
	}
	// Long texts where forward search from prefiltered starts re-steps the same bytes
	// (quadratic without the work budget), forcing the switch to the start bitmap.
	cases = append(cases, regexCase{"a[^b]*b", strings.Repeat("a", 3000) + "b" + strings.Repeat("ac", 2000)},
		regexCase{"(a|c)c*b", strings.Repeat("ac", 3000) + "b"})
	alphabet := "abcAB\n|"
	for i := 0; i < 300; i++ {
		var text strings.Builder
		for n := rng.Intn(24); n > 0; n-- {
			text.WriteByte(alphabet[rng.Intn(len(alphabet))])
		}
		cases = append(cases, regexCase{regexTestPattern(rng, 3), text.String()})
	}
	for _, c := range cases {
		fmt.Fprintf(&program, "    check(&out, %q, %q)\n", c.pattern, c.text)
		re, err := regexp.Compile(c.pattern)
		// Go accepts an unclosed `{` as a literal; this engine (like Rust's regex) rejects it.
		if err != nil || strings.Contains(c.pattern, "(?s)") || c.pattern == "a{2" {
			expected.WriteString("!\n")
			continue
		}
		re.Longest()
		count := len(re.FindAllIndex([]byte(c.text), -1))
		replaced := strings.ReplaceAll(string(re.ReplaceAllLiteral([]byte(c.text), []byte("<>"))), "\n", "~")
		fmt.Fprintf(&expected, "%d %s\n", count, replaced)
	}
	program.WriteString("    printf(\"%.*s\", out.count.i32(), (&out[0]).cast[void&] can Unsafe.PointerCast)\n    return 0\n")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.elisa"), []byte(program.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{"targets": {"default": {"entry": "main.elisa", "emit": "obj", "output": "prog", "opt": "O2"}}}`
	if err := os.WriteFile(filepath.Join(dir, "project.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"build", "--project", dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("build failed:\n%s%s", stdout.String(), stderr.String())
	}
	out, err := exec.Command(filepath.Join(dir, "prog")).CombinedOutput()
	if err != nil {
		t.Fatalf("regex program failed: %v\n%s", err, out)
	}
	got := strings.Split(string(out), "\n")
	want := strings.Split(expected.String(), "\n")
	if len(got) != len(want) {
		t.Fatalf("line count: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("case %d pattern %q text %q:\n got  %s\n want %s", i, cases[i].pattern, cases[i].text, got[i], want[i])
		}
	}
}
