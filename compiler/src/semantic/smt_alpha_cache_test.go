package semantic

import (
	"crypto/sha256"
	"elisacore/src/smt"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestSMTAlphaProofCache(t *testing.T) {
	c := &smtProofCache{directory: t.TempDir(), identity: "toolchain"}
	first := "(declare-const |sview_length_old_0123| Int)(assert (not (= |sview_length_old_0123| |sview_length_old_0123|)))"
	moved := strings.ReplaceAll(first, "old_0123", "new_4567")
	if c.key(first) == c.key(moved) {
		t.Fatal("exact keys must stay exact")
	}
	c.publish(first)
	if !c.contains(moved) {
		t.Fatal("relocated equivalent proof missed")
	}
	if c.contains("(declare-const |sview_length_new_4567| Int)(assert (= |sview_length_new_4567| 17))") {
		t.Fatal("different formula hit")
	}
	other := &smtProofCache{directory: c.directory, identity: "other toolchain"}
	if other.contains(moved) {
		t.Fatal("toolchain identity lost")
	}
	key, ok := c.alphaKey(first)
	if !ok {
		t.Fatal("missing alpha key")
	}
	if err := os.WriteFile(c.path(key), []byte(`{"Schema":"broken"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if c.contains(moved) {
		t.Fatal("corrupt alpha record trusted")
	}
	if !c.contains(first) {
		t.Fatal("valid exact record lost")
	}
}

func TestSMTAlphaDeclinesRemainExact(t *testing.T) {
	c := &smtProofCache{directory: t.TempDir(), identity: "test"}
	prior := smtDeclineCacheEnabled
	smtDeclineCacheEnabled = true
	defer func() { smtDeclineCacheEnabled = prior }()
	q := "(declare-const v_old Int)(assert (> v_old 0))"
	c.publishDeclined(q, smt.Sat, "v_old = 0")
	moved := strings.ReplaceAll(q, "v_old", "v_new")
	if _, ok := c.declined(moved); ok {
		t.Fatal("renamed counterexample hit")
	}
	if c.contains(moved) {
		t.Fatal("decline became proof")
	}
}

func TestSMTAlphaStructure(t *testing.T) {
	same := [][2]string{
		{"(declare-const v_a Int)(assert (= v_a 1))", "(declare-const v_b Int)(assert (= |v_b| 1))"},
		{"(declare-fun |ghost_uf_a| (Int) Int)(assert (= (|ghost_uf_a| 3) 4))", "(declare-fun ghost_uf_b (Int) Int)(assert (= (ghost_uf_b 3) 4))"},
		{"(declare-const v_a Int)(assert (forall ((v_a Int)) (= v_a v_a)))", "(declare-const v_b Int)(assert (forall ((v_b Int)) (= v_b v_b)))"},
	}
	different := [][2]string{
		{"(declare-const v_a Int)(assert (= v_a 1))", "(declare-const v_a Int)(assert (= v_a 2))"},
		{"(declare-const v_a Int)(assert (= v_a 1))", "(declare-const v_a Bool)(assert v_a)"},
		{"(declare-const v_a Int)(declare-const v_b Int)(assert (= v_a v_b))", "(declare-const v_a Int)(declare-const v_b Int)(assert (= v_a v_a))"},
		{`(declare-const v_a String)(assert (= v_a "v_a"))`, `(declare-const v_b String)(assert (= v_b "v_b"))`},
		{"(declare-const v_a Int)(assert (forall ((v_b Int)) (= v_a v_b)))", "(declare-const v_a Int)(assert (forall ((v_a Int)) (= v_a v_a)))"},
	}
	for group, pairs := range [][][2]string{same, different} {
		for _, p := range pairs {
			a, ok := smtAlphaQuery(p[0])
			if !ok {
				t.Fatal(p[0])
			}
			b, ok := smtAlphaQuery(p[1])
			if !ok {
				t.Fatal(p[1])
			}
			if (a == b) != (group == 0) {
				t.Fatalf("wrong structural equivalence:\n%s\n%s", a, b)
			}
		}
	}
}

func TestSMTAlphaLexicalBoundaries(t *testing.T) {
	for _, q := range []string{"(declare-const v_x Int", "(declare-const |v_x Int)", `(declare-const v_x String)(assert (= v_x "unterminated))`, "(declare-const v_x Int)(declare-const |v_x| Int)", "(declare-const |v_\\bad| Int)", ")(declare-const v_x Int)("} {
		if _, ok := smtAlphaQuery(q); ok {
			t.Fatalf("malformed input admitted: %q", q)
		}
	}
	for _, name := range []string{"v_bad:name", "v_bad#name", "|v_bad\x00name|"} {
		if _, ok := smtAlphaQuery("(declare-const " + name + " Int)(assert false)"); ok {
			t.Fatalf("invalid identifier normalized: %q", name)
		}
	}
	if _, ok := smtAlphaQuery("(declare-const |v_a:b| Int)(assert (= v_a:b 0))"); ok {
		t.Fatal("invalid unquoted use was repaired by normalization")
	}
	for _, bad := range []string{"; bad\x00comment\n", "; bad\xffcomment\n", "; comment\r"} {
		if _, ok := smtAlphaQuery(bad + "(declare-const v_x Int)(assert false)"); ok {
			t.Fatal("invalid/comment line boundary was normalized")
		}
	}
	if _, ok := smtAlphaQuery("(declare-const x Int)(assert (= x x))"); ok {
		t.Fatal("ordinary query changed")
	}
	q := "(declare-const v_x Int)(assert (forall ((__elisa_proof_alpha_0 Int)) (= v_x __elisa_proof_alpha_0)))"
	got, ok := smtAlphaQuery(q)
	if !ok || !strings.Contains(got, "|__elisa_proof_alpha__0|") || !strings.Contains(got, "__elisa_proof_alpha_0") {
		t.Fatalf("binder capture: %s", got)
	}
}

func TestSMTAlphaSolverParity(t *testing.T) {
	if _, err := exec.LookPath(smt.ResolveBinary("")); err != nil {
		t.Skip("solver unavailable")
	}
	solver, err := smt.Open(smt.Options{PerQueryTimeoutMillis: 2000})
	if err != nil {
		t.Fatal(err)
	}
	defer solver.Close()
	for _, q := range []string{
		"(declare-const v_x Int)(assert (not (= v_x v_x)))",
		"(declare-const v_x Int)(declare-const v_y Int)(assert (> v_x v_y))",
		"(declare-fun ghost_uf_a (Int) Int)(assert (= (ghost_uf_a 3) 4))",
		"(declare-const v_x Int)(assert (forall ((v_x Int)) (= v_x v_x)))",
		"(declare-const v_x Int)(assert (forall ((__elisa_proof_alpha_0 Int)) (= v_x __elisa_proof_alpha_0)))",
		`(declare-const v_x String)(assert (= v_x "quoted ""v_x"" text"))`,
		"; v_x in comment\n(declare-const |v_x| Int)(assert (let ((v_x 3)) (= v_x 3)))",
	} {
		canonical, ok := smtAlphaQuery(q)
		if !ok {
			t.Fatal(q)
		}
		before, _ := solver.Check(q)
		after, _ := solver.Check(canonical)
		if before == smt.Unknown || after != before {
			t.Fatalf("solver mismatch %v/%v:\n%s\n%s", before, after, q, canonical)
		}
	}
}

func BenchmarkSMTAlphaQuery(b *testing.B) {
	name := "|sview_length_" + strings.Repeat("0123abcdef", 40) + "|"
	q := "(declare-const " + name + " Int)(assert (>= " + name + " 0))"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, ok := smtAlphaQuery(q); !ok {
			b.Fatal("not admitted")
		}
	}
}

func TestSMTAlphaStreamingCanonicalForm(t *testing.T) {
	const base = "__elisa_proof_alpha_"
	collision := base + "__0"
	query := "(declare-const |v_old| Int); comment\n(assert (= v_old |" + collision + "|))"
	canonical, ok := smtAlphaQuery(query)
	renamed := "|" + base + "___0|"
	expected := "( declare-const " + renamed + " Int ) ( assert ( = " + renamed + " |" + collision + "| ) )"
	if !ok || canonical != expected {
		t.Fatalf("canonical form changed: %q, %v", canonical, ok)
	}
}

func BenchmarkSMTAlphaLargeQuery(b *testing.B) {
	name := "|sview_length_" + strings.Repeat("0123abcdef", 40) + "|"
	query := "(declare-const " + name + " Int)" + strings.Repeat("(assert (>= "+name+" 0))", 1000)
	b.SetBytes(int64(len(query)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := smtAlphaQuery(query); !ok {
			b.Fatal("not admitted")
		}
	}
}

func BenchmarkSMTAlphaLargeKey(b *testing.B) {
	name := "|sview_length_" + strings.Repeat("0123abcdef", 40) + "|"
	query := "(declare-const " + name + " Int)" + strings.Repeat("(assert (>= "+name+" 0))", 1000)
	c := &smtProofCache{identity: "toolchain"}
	b.SetBytes(int64(len(query)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := c.alphaKey(query); !ok {
			b.Fatal("not admitted")
		}
	}
}

func FuzzSMTAlphaKeyMatchesCanonicalText(f *testing.F) {
	for _, query := range []string{
		"(declare-const v_old Int)(assert (= v_old 1))",
		"(declare-const |v_old| Int); comment\n(assert (not (= v_old |v_old|)))",
		"(declare-const v_a Int)(declare-const |__elisa_proof_alpha_0| Int)(assert (= v_a 1))",
		"(assert true)", "(declare-const v_x Int", "(declare-const |v_bad\\name| Int)",
	} {
		f.Add(query)
	}
	f.Fuzz(func(t *testing.T, query string) {
		c := &smtProofCache{identity: "compatibility"}
		canonical, wantOK := smtAlphaQuery(query)
		key, gotOK := c.alphaKey(query)
		if gotOK != wantOK {
			t.Fatal("streaming changed query admission")
		}
		if wantOK && key != c.key("alpha-declared-v1\x00"+canonical) {
			t.Fatal("streaming changed proof-cache key")
		}
	})
}

func BenchmarkSMTAlphaKeySizes(b *testing.B) {
	for _, size := range []int{1, 1000} {
		query := "(declare-const v_x Int)" + strings.Repeat("(assert (>= v_x 0))", size)
		c := &smtProofCache{identity: "toolchain"}
		for _, streaming := range []bool{false, true} {
			label := "legacy"
			if streaming {
				label = "streaming"
			}
			b.Run(fmt.Sprintf("terms=%d/%s", size, label), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if streaming {
						if _, ok := c.alphaKey(query); !ok {
							b.Fatal("not admitted")
						}
					} else {
						canonical, ok := smtAlphaQuery(query)
						if !ok {
							b.Fatal("not admitted")
						}
						_ = c.key("alpha-declared-v1\x00" + canonical)
					}
				}
			})
		}
	}
}

func BenchmarkSMTExactQueryKey(b *testing.B) {
	c := &smtProofCache{identity: "toolchain"}
	for _, terms := range []int{1, 1000} {
		query := "(declare-const v_x Int)" + strings.Repeat("(assert (>= v_x 0))", terms)
		b.Run(fmt.Sprintf("terms=%d", terms), func(b *testing.B) {
			b.SetBytes(int64(len(query)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = c.key(query)
			}
		})
	}
}

func TestSMTExactQueryKeyKeepsLegacyDigest(t *testing.T) {
	c := &smtProofCache{identity: "compiler\x00solver\x00options"}
	for _, size := range []int{0, 1, 4095, 4096, 4097, 8193, 128 * 1024} {
		query := strings.Repeat("x", size)
		if size >= 3 {
			query = query[:size-3] + "\x00λ"
		}
		digest := sha256.Sum256([]byte(c.identity + "\x00" + query))
		if got, want := c.key(query), hex.EncodeToString(digest[:]); got != want {
			t.Fatalf("size %d changed key: %s != %s", size, got, want)
		}
	}
}
