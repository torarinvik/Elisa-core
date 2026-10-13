package main

import (
	"bytes"
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bigintTestValue builds a value whose base-2^32 limbs are biased toward the
// boundary patterns that exercise carries, borrows and Knuth D's add-back step.
func bigintTestValue(rng *rand.Rand, limbs int) *big.Int {
	patterns := []uint32{0, 1, 0x7fffffff, 0x80000000, 0xffffffff, 0xfffffffe}
	v := new(big.Int)
	for i := 0; i < limbs; i++ {
		var limb uint32
		if rng.Intn(3) == 0 {
			limb = patterns[rng.Intn(len(patterns))]
		} else {
			limb = rng.Uint32()
		}
		v.Lsh(v, 32)
		v.Or(v, new(big.Int).SetUint64(uint64(limb)))
	}
	if rng.Intn(2) == 0 {
		v.Neg(v)
	}
	return v
}

// TestStdBigintMatchesMathBig differentially checks elisacore_bigint.elisa against
// Go's math/big over random and boundary operands: parse/print round trip, add,
// sub, mul, truncated divmod (Knuth D and the single-limb path), addmul/submul by a
// small factor and in-place small division.
func TestStdBigintMatchesMathBig(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	runtimeDir, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std"))
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(20261013))
	var program, expected strings.Builder
	fmt.Fprintf(&program, "include %q\ninclude %q\n", filepath.Join(runtimeDir, "elisacore_runtime.elisa"), filepath.Join(runtimeDir, "elisacore_bigint.elisa"))
	program.WriteString(`extern printf(format: cstr, ...) -> i32 can[Console, Unsafe.RawExtern]

def check(out: mutable darray[u8]&, left_text: sview, right_text: sview, factor: u64) -> void can[Abort.Panic, Memory.Allocate]:
    a: mutable BigInt = bigint_zero()
    b: mutable BigInt = bigint_zero()
    work: mutable BigInt = bigint_zero()
    q: mutable BigInt = bigint_zero()
    r: mutable BigInt = bigint_zero()
    parsed_a: bool = bigint_set_decimal(&a, left_text)
    parsed_b: bool = bigint_set_decimal(&b, right_text)
    bigint_append_decimal(out, a)
    out.push(' '.u8())
    bigint_assign(&work, a)
    bigint_add_assign(&work, b)
    bigint_append_decimal(out, work)
    out.push(' '.u8())
    bigint_assign(&work, a)
    bigint_sub_assign(&work, b)
    bigint_append_decimal(out, work)
    out.push(' '.u8())
    bigint_mul(&work, a, b)
    bigint_append_decimal(out, work)
    out.push(' '.u8())
    if not bigint_is_zero(b):
        bigint_divmod(&q, &r, a, b)
        bigint_append_decimal(out, q)
        out.push(' '.u8())
        bigint_append_decimal(out, r)
        out.push(' '.u8())
    bigint_assign(&work, a)
    bigint_addmul_small(&work, b, factor)
    bigint_append_decimal(out, work)
    out.push(' '.u8())
    bigint_assign(&work, a)
    bigint_submul_small(&work, b, factor)
    bigint_append_decimal(out, work)
    out.push(' '.u8())
    bigint_assign(&work, a)
    rest: mutable u64 = bigint_divmod_small_assign(&work, factor)
    bigint_append_decimal(out, work)
    out.push(' '.u8())
    out.push('0'.u8()) if rest == 0
    while rest > 0 |rest, out|:
        out.push('0'.u8() + (rest % 10).u8())
        rest <- rest / 10
    out.push(' '.u8())
    out.push('1'.u8() if bigint_compare(a, b) > 0 else ('0'.u8() if bigint_compare(a, b) == 0 else '-'.u8()))
    out.push('\n'.u8())

def main() -> i32 can[Memory.Allocate, Abort.Panic, Console, Unsafe.RawExtern, Unsafe.PointerCast]:
    out: mutable darray[u8] = []
`)
	sizes := []int{0, 1, 2, 3, 5, 9, 17, 40}
	for i := 0; i < 160; i++ {
		a := bigintTestValue(rng, sizes[rng.Intn(len(sizes))])
		b := bigintTestValue(rng, sizes[rng.Intn(len(sizes))])
		if i%4 == 0 {
			// Same-size and nearly-equal operands: quotient digits near the estimate limit.
			b = new(big.Int).Add(a, big.NewInt(int64(rng.Intn(3)-1)))
			if rng.Intn(2) == 0 {
				b.Rsh(b, 32)
			}
		}
		factor := uint64(rng.Uint32()>>uint(rng.Intn(32))) | 1
		if rng.Intn(3) == 0 {
			factor = rng.Uint64()>>uint(rng.Intn(64)) | 1
		}
		fmt.Fprintf(&program, "    check(&out, %q, %q, %d)\n", a.String(), b.String(), factor)

		k := new(big.Int).SetUint64(factor)
		fields := []string{a.String(), new(big.Int).Add(a, b).String(), new(big.Int).Sub(a, b).String(), new(big.Int).Mul(a, b).String()}
		if b.Sign() != 0 {
			q, r := new(big.Int).QuoRem(a, b, new(big.Int))
			fields = append(fields, q.String(), r.String())
		}
		fields = append(fields, new(big.Int).Add(a, new(big.Int).Mul(b, k)).String(), new(big.Int).Sub(a, new(big.Int).Mul(b, k)).String())
		qs, rs := new(big.Int).QuoRem(a, k, new(big.Int))
		rs.Abs(rs)
		fields = append(fields, qs.String(), reverseDigits(rs.String()))
		switch a.Cmp(b) {
		case 1:
			fields = append(fields, "1")
		case 0:
			fields = append(fields, "0")
		default:
			fields = append(fields, "-")
		}
		expected.WriteString(strings.Join(fields, " ") + "\n")
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
		t.Fatalf("bigint program failed: %v\n%s", err, out)
	}
	got := strings.Split(string(out), "\n")
	want := strings.Split(expected.String(), "\n")
	if len(got) != len(want) {
		t.Fatalf("line count: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("case %d mismatch:\n got  %s\n want %s", i, got[i], want[i])
		}
	}
}

func reverseDigits(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}
