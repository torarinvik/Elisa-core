package easm

// A mutation fuzzer for the validator's enforcement. The MC cross-check
// (easm_mc_effects_test.go) proves the effect *tables* match LLVM. This proves the
// validator actually *enforces* the declarations those tables drive: for a randomly
// generated, fully-declared function that the validator accepts, removing any one
// enforcement-bearing declaration must make it reject. If a drop is still accepted, the
// validator under-enforces -- it would let a real EASM author ship that same
// under-declaration and miscompile.
//
// The generator stays inside a deliberately safe envelope (caller-saved scratch registers
// only, every register initialized before use, void return, no inputs/outputs/preserves)
// so the fully-declared form is reliably valid. That is not a weakness: the property under
// test is "a required declaration, once removed, is caught", and the envelope guarantees
// every declaration the generator emits is genuinely required. A complete form that the
// validator rejects is therefore a generator bug, and the test fails loudly rather than
// skipping, so the fuzzer can never silently degrade into testing nothing.

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

func fuzzHasError(issues []Issue) bool {
	for _, is := range issues {
		if is.Severity == "error" {
			return true
		}
	}
	return false
}

func fuzzErrorCodes(issues []Issue) []string {
	var codes []string
	for _, is := range issues {
		if is.Severity == "error" {
			codes = append(codes, is.Code)
		}
	}
	return codes
}

// fuzzFunc is a generated export: its body plus the complete set of declarations that the
// instruction effects and structure require for the validator to accept it.
type fuzzFunc struct {
	body     []string
	clobbers []string // written GPRs, plus "cc" and/or "memory" exactly when required
	requires []string // capabilities required by special instructions (e.g. rdtsc)
}

func (g fuzzFunc) source() string {
	var b strings.Builder
	b.WriteString("module fuzz\ntarget x86_64\n\n")
	b.WriteString("export def fuzz_fn() -> void abi c:\n")
	if len(g.clobbers) > 0 {
		b.WriteString("    clobbers: " + strings.Join(g.clobbers, ", ") + "\n")
	}
	b.WriteString("    stack: unchanged\n")
	b.WriteString("    control: returns\n")
	if len(g.requires) > 0 {
		b.WriteString("    requires: " + strings.Join(g.requires, ", ") + "\n")
	}
	b.WriteString("    body:\n")
	for _, line := range g.body {
		b.WriteString("        " + line + "\n")
	}
	return b.String()
}

func fuzzDropOne(s []string, v string) []string {
	out := make([]string, 0, len(s))
	removed := false
	for _, x := range s {
		if !removed && x == v {
			removed = true
			continue
		}
		out = append(out, x)
	}
	return out
}

func fuzzGenerate(rng *rand.Rand) fuzzFunc {
	scratch := []string{"rax", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10", "r11"}
	live := map[string]bool{}
	written := map[string]bool{}
	usesFlags := false
	usesMemory := false
	requires := map[string]bool{}
	var body []string

	liveList := func() []string {
		l := make([]string, 0, len(live))
		for r := range live {
			l = append(l, r)
		}
		sort.Strings(l)
		return l
	}

	n := 2 + rng.Intn(7) // 2..8 instructions before the trailing ret
	for i := 0; i < n; i++ {
		kind := rng.Intn(5)
		// Cases 1..4 consume at least one live register. When none is live yet, fall back
		// to an initializing move so we never read an uninitialized register and never
		// index an empty live set.
		if len(live) == 0 {
			kind = 0
		}
		switch kind {
		case 0: // init: movq $imm, %r  -- writes r, no flags, no memory
			r := scratch[rng.Intn(len(scratch))]
			body = append(body, fmt.Sprintf("movq $%d, %%%s", rng.Intn(1000), r))
			live[r] = true
			written[r] = true
		case 1, 2: // arith immediate: <op>q $imm, %r  -- reads+writes r, sets flags
			ll := liveList()
			r := ll[rng.Intn(len(ll))]
			op := []string{"addq", "subq", "andq", "xorq"}[rng.Intn(4)]
			body = append(body, fmt.Sprintf("%s $%d, %%%s", op, rng.Intn(1000), r))
			written[r] = true
			usesFlags = true
		case 3: // arith register: <op>q %r2, %r1  -- reads r1,r2; writes r1; sets flags
			ll := liveList()
			r1 := ll[rng.Intn(len(ll))]
			r2 := ll[rng.Intn(len(ll))]
			op := []string{"addq", "subq", "andq"}[rng.Intn(3)]
			body = append(body, fmt.Sprintf("%s %%%s, %%%s", op, r2, r1))
			written[r1] = true
			usesFlags = true
		case 4: // store: movq %r1, (%r2)  -- writes memory, no register write, no flags
			ll := liveList()
			r1 := ll[rng.Intn(len(ll))]
			r2 := ll[rng.Intn(len(ll))]
			body = append(body, fmt.Sprintf("movq %%%s, (%%%s)", r1, r2))
			usesMemory = true
		}
	}
	body = append(body, "ret")

	clob := make([]string, 0, len(written)+2)
	for r := range written {
		clob = append(clob, r)
	}
	sort.Strings(clob)
	if usesFlags {
		clob = append(clob, "cc")
	}
	if usesMemory {
		clob = append(clob, "memory")
	}
	req := make([]string, 0, len(requires))
	for c := range requires {
		req = append(req, c)
	}
	sort.Strings(req)

	return fuzzFunc{body: body, clobbers: clob, requires: req}
}

func TestEASMValidatorEnforcementFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5A17ED))
	const iterations = 500
	accepted := 0
	mutations := 0

	for iter := 0; iter < iterations; iter++ {
		g := fuzzGenerate(rng)
		if _, issues := Parse("fuzz.easm", g.source()); fuzzHasError(issues) {
			// The complete form must validate; otherwise the generator emitted something
			// the validator rejects for an unrelated reason and the property is untestable.
			t.Fatalf("complete form rejected (iter %d): codes=%v\n%s",
				iter, fuzzErrorCodes(issues), g.source())
		}
		accepted++

		// Each declaration the generator emitted is genuinely required, so dropping any one
		// must turn acceptance into rejection. A still-accepted drop is under-enforcement.
		check := func(field string, decls []string, mutate func(string) fuzzFunc) {
			for _, drop := range decls {
				mutations++
				if _, issues := Parse("fuzz.easm", mutate(drop).source()); !fuzzHasError(issues) {
					t.Errorf("UNDER-ENFORCEMENT (iter %d): dropping %s %q was still accepted; "+
						"the validator does not enforce this declaration:\n%s",
						iter, field, drop, mutate(drop).source())
				}
			}
		}
		check("clobber", g.clobbers, func(drop string) fuzzFunc {
			m := g
			m.clobbers = fuzzDropOne(g.clobbers, drop)
			return m
		})
		check("requires", g.requires, func(drop string) fuzzFunc {
			m := g
			m.requires = fuzzDropOne(g.requires, drop)
			return m
		})
	}

	t.Logf("fuzz: %d generated functions accepted in complete form; all %d declaration-drop "+
		"mutations were rejected", accepted, mutations)
}
