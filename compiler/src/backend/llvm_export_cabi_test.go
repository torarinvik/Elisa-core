package backend

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The export boundary must present exactly the C ABI clang implements. For every
// aggregate shape that exercises a classification rule, this compiles the same
// shape with clang for each target and compares the LLVM parameter and return
// types of the exported function against ours. clang is the oracle; no execution
// is needed, so x86-64 and wasm32 are checked on any host.
var cAbiOracleShapes = []struct {
	name   string
	elisa  []string // field: type
	c      string   // C struct body
	ret    string   // elisa scalar return type of the taker
	cret   string
}{
	{"C1", []string{"a: i8"}, "signed char a;", "i64", "long long"},
	{"C3", []string{"a: i8", "b: i8", "c: i8"}, "signed char a, b, c;", "i64", "long long"},
	{"I5", []string{"a: i32", "b: i8"}, "int a; signed char b;", "i64", "long long"},
	{"S6", []string{"a: i16", "b: i16", "c: i16"}, "short a, b, c;", "i64", "long long"},
	{"I12", []string{"a: i32", "b: i32", "c: i32"}, "int a, b, c;", "i64", "long long"},
	{"F4", []string{"f: f32"}, "float f;", "f64", "double"},
	{"F12", []string{"a: f32", "b: f32", "c: f32"}, "float a, b, c;", "f64", "double"},
	{"F16", []string{"x: f32", "y: f32", "w: f32", "h: f32"}, "float x, y, w, h;", "f64", "double"},
	{"D16", []string{"x: f64", "y: f64"}, "double x, y;", "f64", "double"},
	{"M8", []string{"x: f32", "i: i32"}, "float x; int i;", "f64", "double"},
	{"DF", []string{"d: f64", "f: f32"}, "double d; float f;", "f64", "double"},
	{"FD", []string{"f: f32", "d: f64"}, "float f; double d;", "f64", "double"},
	{"IFF", []string{"a: i32", "b: f32", "c: f32"}, "int a; float b, c;", "f64", "double"},
	{"LF", []string{"a: i64", "b: f32"}, "long long a; float b;", "f64", "double"},
	{"L24", []string{"a: i64", "b: i64", "c: i64"}, "long long a, b, c;", "i64", "long long"},
	{"F32", []string{"a: f32", "b: f32", "c: f32", "d: f32", "e: f32", "f: f32", "g: f32", "h: f32"}, "float a, b, c, d, e, f, g, h;", "f64", "double"},
}

var cAbiOracleTargets = []string{"arm64-apple-darwin", "x86_64-apple-darwin", "wasm32-unknown-unknown"}

// signature reduces a `define` line to "@name(paramtypes) -> rettype" with attributes,
// names and alignment stripped, so ours and clang's compare structurally.
func cAbiSignature(line string) (string, string) {
	m := regexp.MustCompile(`^define\s+(?:[a-z_]+\s+)*?(?:range\([^)]*\)\s+)?(.+?)\s+@([A-Za-z0-9_.]+)\((.*)\)`).FindStringSubmatch(line)
	if m == nil {
		return "", ""
	}
	ret, name, params := strings.TrimSpace(m[1]), m[2], m[3]
	ret = strings.TrimPrefix(ret, "hidden ")
	ret = strings.TrimPrefix(ret, "dso_local ")
	var kept []string
	depth := 0
	cur := ""
	for _, ch := range params {
		switch ch {
		case '(', '<', '{', '[':
			depth++
		case ')', '>', '}', ']':
			depth--
		}
		if ch == ',' && depth == 0 {
			kept = append(kept, cur)
			cur = ""
			continue
		}
		cur += string(ch)
	}
	if strings.TrimSpace(cur) != "" {
		kept = append(kept, cur)
	}
	norm := make([]string, 0, len(kept))
	for _, p := range kept {
		p = strings.TrimSpace(p)
		p = regexp.MustCompile(`%[A-Za-z0-9_.]+$`).ReplaceAllString(p, "")
		p = regexp.MustCompile(`\s+align \d+`).ReplaceAllString(p, "")
		p = regexp.MustCompile(`\b(noundef|nofree|readonly|writeonly|writable|noalias|dead_on_unwind|dead_on_return|returned|nonnull|captures\(none\))\b`).ReplaceAllString(p, "")
		p = regexp.MustCompile(`initializes\([^)]*\)`).ReplaceAllString(p, "")
		p = regexp.MustCompile(`%struct\.`).ReplaceAllString(p, "%")
		p = strings.Join(strings.Fields(p), " ")
		norm = append(norm, p)
	}
	ret = regexp.MustCompile(`%struct\.`).ReplaceAllString(ret, "%")
	return name, strings.Join(norm, ", ") + " -> " + ret
}

func TestExportCABIMatchesClang(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}
	var elisa, csrc strings.Builder
	for _, s := range cAbiOracleShapes {
		elisa.WriteString("struct " + s.name + " layout(c):\n")
		for _, f := range s.elisa {
			elisa.WriteString("    " + f + "\n")
		}
		first := strings.Split(s.elisa[0], ":")[0]
		elisa.WriteString("export type " + s.name + " as " + s.name + "\n")
		elisa.WriteString("def t_" + s.name + "(v: " + s.name + ") -> " + s.ret + ":\n    return v." + first + "." + s.ret + "()\n")
		elisa.WriteString("export fn t_" + s.name + "(v: " + s.name + ") -> " + s.ret + " = t_" + s.name + "\n")
		var lit []string
		for _, f := range s.elisa {
			parts := strings.SplitN(f, ":", 2)
			zero := "0"
			if strings.HasPrefix(strings.TrimSpace(parts[1]), "f") {
				zero = "0.0"
			}
			lit = append(lit, strings.TrimSpace(parts[0])+": "+zero)
		}
		elisa.WriteString("def r_" + s.name + "() -> " + s.name + ":\n    return " + s.name + "{" + strings.Join(lit, ", ") + "}\n")
		elisa.WriteString("export fn r_" + s.name + "() -> " + s.name + " = r_" + s.name + "\n")
		csrc.WriteString("typedef struct { " + s.c + " } " + s.name + ";\n")
		csrc.WriteString(s.cret + " t_" + s.name + "(" + s.name + " v) { return 0; }\n")
		csrc.WriteString(s.name + " r_" + s.name + "(void) { " + s.name + " r = {0}; return r; }\n")
	}
	cPath := t.TempDir() + "/shapes.c"
	if err := os.WriteFile(cPath, []byte(csrc.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	result := parseAndAnalyzeBackendTest(t, "cabi_shapes.elisa", elisa.String())
	for _, target := range cAbiOracleTargets {
		out, err := exec.Command(clang, "-O0", "-S", "-emit-llvm", "-target", target, "-o", "-", cPath).Output()
		if err != nil {
			t.Fatalf("clang %s: %v", target, err)
		}
		want := map[string]string{}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "define ") {
				name, sig := cAbiSignature(line)
				want[name] = sig
			}
		}
		ours, err := GenerateLLVMIRWithOptAndPackedLoweringProfileForTarget(result, OptimizationLevel0, DefaultPackedLoweringProfile(), target)
		if err != nil {
			t.Fatalf("our IR for %s: %v", target, err)
		}
		got := map[string]string{}
		for _, line := range strings.Split(ours, "\n") {
			if strings.HasPrefix(line, "define ") {
				name, sig := cAbiSignature(line)
				got[name] = sig
			}
		}
		for _, s := range cAbiOracleShapes {
			for _, fn := range []string{"t_" + s.name, "r_" + s.name} {
				if got[fn] != want[fn] {
					t.Errorf("%s %s:\n  clang: %s\n  ours:  %s", target, fn, want[fn], got[fn])
				}
			}
		}
	}
}
