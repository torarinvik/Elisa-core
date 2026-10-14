package main

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func floatParseCases() []string {
	rng := rand.New(rand.NewSource(20261014))
	cases := []string{
		"0", "-0", "+0", "0.0", "00000.0000e5", ".5", "5.", "-.5e-3", "1", "-1", "123456789", "0.1", "0.3",
		"1e308", "1.7976931348623157e308", "1.7976931348623158e308", "1.7976931348623159e308", "1e309", "-1e400",
		"4.9e-324", "5e-324", "2.4703282292062327e-324", "2.4703282292062328e-324", "1e-324", "3e-324", "1e-400",
		"2.2250738585072011e-308", "2.2250738585072012e-308", "2.2250738585072014e-308", "4.9406564584124654e-324",
		"9007199254740993", "9007199254740993.000000000000000000001", "9007199254740995", "18014398509481987",
		"123456789012345678901234567890", "1.00000000000000011102230246251565404236316680908203125",
		"1.00000000000000011102230246251565404236316680908203124", "1.00000000000000011102230246251565404236316680908203126",
		"0.000000000000000000000000000000000000000000001e45", "1e22", "1e23", "8.98846567431158e307",
		"inf", "-inf", "+Inf", "Infinity", "-INFINITY", "nan", "NaN",
		// rejected
		"", "+", "-", ".", "e5", "1e", "1e+", "1e-", "--1", "1..2", "1.2.3", "abc", "1x", " 1", "1 ", "+nan", "infx",
		// Not compared: Go accepts digit separators ("1_000") and hex floats; this parser, like
		// Rust str::parse and JSON, does not.
	}
	// Exact midpoints between consecutive doubles (round-half-even), and their neighbours.
	for i := 0; i < 400; i++ {
		var f float64
		switch i % 4 {
		case 0:
			f = math.Float64frombits(rng.Uint64() & 0x7FEFFFFFFFFFFFFF)
		case 1:
			f = math.Float64frombits(uint64(rng.Int63n(1 << 52))) // subnormal
		default:
			f = math.Float64frombits(uint64(rng.Int63n(0x7FE0000000000000)))
		}
		if math.IsInf(f, 0) || math.IsNaN(f) || f == 0 {
			continue
		}
		g := math.Nextafter(f, math.Inf(1))
		if math.IsInf(g, 0) {
			continue
		}
		mid := new(big.Rat).Add(new(big.Rat).SetFloat64(f), new(big.Rat).SetFloat64(g))
		mid.Quo(mid, big.NewRat(2, 1))
		text := strings.TrimRight(mid.FloatString(1100), "0")
		if len(text) > 900 {
			// keep the leading part plus a perturbation: just above or below the midpoint
			text = text[:900]
		}
		cases = append(cases, text, text+"1")
		if s := strings.TrimSuffix(text, "5"); s != text && strings.HasSuffix(text, "5") {
			cases = append(cases, s+"4999")
		}
	}
	for i := 0; i < 6000; i++ {
		var b strings.Builder
		if rng.Intn(4) == 0 {
			b.WriteByte("+-"[rng.Intn(2)])
		}
		n := 1 + rng.Intn(20)
		if rng.Intn(20) == 0 {
			n = 20 + rng.Intn(60)
		}
		dot := -1
		if rng.Intn(2) == 0 {
			dot = rng.Intn(n + 1)
		}
		for j := 0; j < n; j++ {
			if j == dot {
				b.WriteByte('.')
			}
			b.WriteByte(byte('0' + rng.Intn(10)))
		}
		if dot == n {
			b.WriteByte('.')
		}
		if rng.Intn(3) != 0 {
			b.WriteByte("eE"[rng.Intn(2)])
			if rng.Intn(2) == 0 {
				b.WriteByte("+-"[rng.Intn(2)])
			}
			b.WriteString(strconv.Itoa(rng.Intn(700) - 350 + 350*rng.Intn(1)))
		}
		s := b.String()
		s = strings.Replace(s, "e-", "e-", 1)
		if strings.Count(s, "-") > 1 && strings.Contains(s, "e--") {
			continue
		}
		cases = append(cases, s)
	}
	for i := 0; i < 3000; i++ { // shortest forms of random doubles (the JSON/CSV case)
		f := math.Float64frombits(rng.Uint64())
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		cases = append(cases, strconv.FormatFloat(f, 'g', -1, 64), strconv.FormatFloat(f, 'f', -1, 64))
	}
	return cases
}

func floatParseExpected(s string) string {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); !ok || ne.Err != strconv.ErrRange {
			return "ERR"
		}
	}
	if math.IsNaN(v) {
		return "NaN"
	}
	return fmt.Sprintf("%016x", math.Float64bits(v))
}

// TestStdFloatParseMatchesGo differentially checks elisacore_parse_float.elisa against
// strconv.ParseFloat(s, 64) (value bits; ErrRange values count as accepted).
func TestStdFloatParseMatchesGo(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	runtimeDir, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std"))
	if err != nil {
		t.Fatal(err)
	}
	cases := floatParseCases()
	var program, expected strings.Builder
	fmt.Fprintf(&program, "include %q\ninclude %q\n", filepath.Join(runtimeDir, "elisacore_runtime.elisa"), filepath.Join(runtimeDir, "elisacore_parse_float.elisa"))
	program.WriteString(`extern printf(format: cstr, ...) -> i32 can[Console, Unsafe.RawExtern]

@intrinsic("elisa.f64_to_bits")
extern bits_of(value: f64) -> u64

def check(out: mutable darray[u8]&, text: sview) -> void can[Abort.Panic, Memory.Allocate]:
    result: F64Parse = f64_parse(text)
    if not result.ok:
        out.extend("ERR\n")
        return
    if result.value != result.value:
        out.extend("NaN\n")
        return
    bits: u64 = bits_of(result.value)
    hex: sview = "0123456789abcdef"
    shift: mutable u64 = 64
    while shift > 0 |shift, out, bits, hex|:
        shift <- shift - 4
        out.push(hex[((bits >> shift) & 15).usize()].u8())
    out.push('\n'.u8())

def flush(out: mutable darray[u8]&) -> void can[Console, Unsafe.RawExtern, Unsafe.PointerCast, Abort.Panic, Memory.Allocate]:
    if out.count > 0:
        printf("%.*s", out.count.i32(), (&out[0]).cast[void&])
    out.resize(0)

def main() -> i64 can[Console, Memory.Allocate, Abort.Panic, Unsafe.RawExtern, Unsafe.PointerCast]:
    out: mutable darray[u8] = []
`)
	for i, c := range cases {
		fmt.Fprintf(&program, "    check(&out, %q)\n", c)
		if i%200 == 199 {
			program.WriteString("    flush(&out)\n")
		}
		expected.WriteString(floatParseExpected(c) + "\n")
	}
	program.WriteString("    flush(&out)\n    return 0\n")

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
		t.Fatalf("build failed:\n%s%s", lastLines(stdout.String()+stderr.String(), 30), "")
	}
	out, err := exec.Command(filepath.Join(dir, "prog")).CombinedOutput()
	if err != nil {
		t.Fatalf("float parse program failed: %v\n%s", err, lastLines(string(out), 10))
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
				shown := cases[i]
				if len(shown) > 80 {
					shown = shown[:80] + "..."
				}
				t.Errorf("%q: got %s want %s", shown, got[i], want[i])
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d strings differ", bad, len(cases))
	}
}

func lastLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	var kept []string
	for _, line := range lines {
		if !strings.Contains(line, "warning:") {
			kept = append(kept, line)
		}
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return strings.Join(kept, "\n")
}
