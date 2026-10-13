package main

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// floatFormatJSON lays out Go's shortest digits with ryu's pretty-printing rules
// (what serde_json writes), the reference for f64_push_shortest_json.
func floatFormatJSON(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "null"
	}
	if v == 0 {
		if math.Signbit(v) {
			return "-0.0"
		}
		return "0.0"
	}
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	e := strconv.FormatFloat(v, 'e', -1, 64) // d[.ddd]e±XX
	mant, expText, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	sciExp, _ := strconv.Atoi(expText)
	kk := sciExp + 1
	k := kk - len(digits)
	switch {
	case k >= 0 && kk <= 16:
		return sign + digits + strings.Repeat("0", k) + ".0"
	case kk > 0 && kk <= 16:
		return sign + digits[:kk] + "." + digits[kk:]
	case kk > -5 && kk <= 0:
		return sign + "0." + strings.Repeat("0", -kk) + digits
	default:
		out := sign + digits[:1]
		if len(digits) > 1 {
			out += "." + digits[1:]
		}
		return out + "e" + strconv.Itoa(kk-1)
	}
}

func floatFormatCases() []float64 {
	rng := rand.New(rand.NewSource(20261014))
	values := []float64{0, math.Copysign(0, -1), math.Inf(1), math.Inf(-1), math.NaN(),
		math.MaxFloat64, -math.MaxFloat64, math.SmallestNonzeroFloat64, 2 * math.SmallestNonzeroFloat64,
		3 * math.SmallestNonzeroFloat64, 0x1p-1022, math.Nextafter(0x1p-1022, 0), math.Nextafter(0x1p-1022, 1),
		0.1, 0.2, 0.3, 1.0 / 3, 2.0 / 3, 1 << 53, 1<<53 + 2, 1<<53 - 1, 123456, 1234567, 999999, 1e6 - 0.5,
		0.0001, 0.00001, 0.000123, 1e15, 1e16, 1e17, 9007199254740993, 5e-324, 1.5e-323, 2.2250738585072014e-308}
	for e := -323; e <= 308; e++ {
		p, _ := strconv.ParseFloat("1e"+strconv.Itoa(e), 64)
		values = append(values, p, -p, math.Nextafter(p, 0), math.Nextafter(p, math.Inf(1)))
	}
	for e := -1074; e <= 1023; e += 3 {
		values = append(values, math.Ldexp(1, e), math.Ldexp(1.5, e))
	}
	for i := 0; i < 20000; i++ {
		values = append(values, math.Float64frombits(rng.Uint64())) // every exponent, incl. subnormals
	}
	for i := 0; i < 5000; i++ { // short decimals: the cases where shortest != %.17g
		digits := rng.Int63n(1000000)
		p, _ := strconv.ParseFloat(fmt.Sprintf("%de%d", digits, rng.Intn(60)-30), 64)
		values = append(values, p)
	}
	for i := 0; i < 2000; i++ { // subnormals
		values = append(values, math.Float64frombits(uint64(rng.Int63n(1<<52))))
	}
	return values
}

// TestStdFloatShortestMatchesGo differentially checks elisacore_fmt_float.elisa against
// strconv.FormatFloat(v, 'g', -1, 64) and against ryu's layout of Go's shortest digits.
func TestStdFloatShortestMatchesGo(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	runtimeDir, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std"))
	if err != nil {
		t.Fatal(err)
	}
	values := floatFormatCases()
	var program, expected strings.Builder
	fmt.Fprintf(&program, "include %q\ninclude %q\n", filepath.Join(runtimeDir, "elisacore_runtime.elisa"), filepath.Join(runtimeDir, "elisacore_fmt_float.elisa"))
	program.WriteString("extern printf(format: cstr, ...) -> i32 can[Console, Unsafe.RawExtern]\n\n")
	fmt.Fprintf(&program, "global CASES: u64[%d] = [\n", len(values))
	for i, v := range values {
		sep := ","
		if i == len(values)-1 {
			sep = ""
		}
		fmt.Fprintf(&program, "    0x%016X%s\n", math.Float64bits(v), sep)
		fmt.Fprintf(&expected, "%s %s\n", strconv.FormatFloat(v, 'g', -1, 64), floatFormatJSON(v))
	}
	fmt.Fprintf(&program, `]

def main() -> i64 can[Console, Memory.Allocate, Abort.Panic, Unsafe.RawExtern, Unsafe.PointerCast, Global.Read]:
    out: mutable darray[u8] = []
    for index in 0..<%d |out|:
        value: f64 = f64_from_bits(CASES[index])
        f64_push_shortest_g(&out, value)
        out.push(' ')
        f64_push_shortest_json(&out, value)
        out.push('\n')
        if out.count > 4096:
            printf("%%.*s", out.count.i32(), (&out[0]).cast[void&])
            out.resize(0)
    if out.count > 0:
        printf("%%.*s", out.count.i32(), (&out[0]).cast[void&])
    return 0
`, len(values))

	dir := t.TempDir()
	if keep := os.Getenv("ELISA_FLOAT_TEST_DIR"); keep != "" {
		dir = keep
	}
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
		t.Fatalf("float program failed: %v\n%s", err, out)
	}
	got := strings.Split(string(out), "\n")
	want := strings.Split(expected.String(), "\n")
	if len(got) != len(want) {
		t.Fatalf("line count: got %d want %d", len(got), len(want))
	}
	bad := 0
	for i := range want {
		if got[i] != want[i] {
			bad++
			if bad <= 20 {
				t.Errorf("value %#016x: got %q want %q", math.Float64bits(values[i]), got[i], want[i])
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d values differ", bad, len(values))
	}
}
