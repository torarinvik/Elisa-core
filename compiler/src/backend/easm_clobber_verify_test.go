package backend

import (
	"testing"

	"elisacore/src/easm"
)

func TestVerifyEASMConstraintsCoverClobbers(t *testing.T) {
	cases := []struct {
		name        string
		clobbers    []string
		constraints string
		wantErr     bool
	}{
		// The 'flags' synonym must lower to ~{cc}; without it LLVM keeps flags live.
		{"flags clobber covered by cc", []string{"flags"}, "=r,r,~{cc}", false},
		{"flags clobber missing cc rejected", []string{"flags"}, "=r,r", true},
		{"flags lowered to literal flags is rejected", []string{"flags"}, "=r,r,~{flags}", true},
		{"cc clobber covered", []string{"cc"}, "=r,~{cc}", false},
		{"cc clobber dropped rejected", []string{"cc"}, "=r,r", true},

		// memory clobber must reach LLVM as ~{memory}.
		{"memory clobber covered", []string{"memory"}, "=r,~{memory}", false},
		{"memory clobber dropped rejected", []string{"memory"}, "=r,r", true},

		// GPR clobbers may be covered by a clobber or a bound operand.
		{"gpr clobber via tilde", []string{"rbx"}, "=r,r,~{rbx}", false},
		{"gpr clobber dropped rejected", []string{"rbx"}, "=r,r", true},
		{"gpr clobber covered by bound output", []string{"rbx"}, "={rbx},r", false},
		{"gpr clobber covered by bound input", []string{"rbx"}, "=r,{rbx}", false},

		// Width-insensitive matching (eax/rax are the same physical register).
		{"eax clobber vs rax constraint", []string{"eax"}, "=r,~{rax}", false},
		{"rax clobber vs eax constraint", []string{"rax"}, "=r,~{eax}", false},

		// Multiple clobbers: every one must be covered.
		{"comma separated clobbers covered", []string{"rbx, r12"}, "=r,~{rbx},~{r12}", false},
		{"one of several dropped rejected", []string{"rbx, r12"}, "=r,~{rbx}", true},
		{"multiple effect kinds", []string{"memory", "cc", "rbx"}, "=r,~{memory},~{cc},~{rbx}", false},

		// Registers we do not canonicalize (vector/x87) are passed through and not
		// reconciled here, so they never produce a false positive.
		{"unmodeled clobber not checked", []string{"xmm0"}, "=r,r", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fn := &easm.Function{Name: "test_fn", Clobbers: tc.clobbers}
			err := verifyEASMConstraintsCoverClobbers(fn, tc.constraints)
			if tc.wantErr && err == nil {
				t.Fatalf("clobbers=%v constraints=%q: expected error, got nil", tc.clobbers, tc.constraints)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("clobbers=%v constraints=%q: unexpected error: %v", tc.clobbers, tc.constraints, err)
			}
		})
	}
}

func TestCanonicalEASMClobberRegister(t *testing.T) {
	groups := map[string][]string{
		"rax": {"rax", "eax", "ax", "al", "ah", "%rax", "  RAX "},
		"rbx": {"rbx", "ebx", "bx", "bl"},
		"rsp": {"rsp", "esp", "sp", "spl"},
		"r12": {"r12", "r12d", "r12w", "r12b"},
		"r15": {"r15", "r15d", "r15w", "r15b"},
	}
	for want, names := range groups {
		for _, in := range names {
			if got := canonicalEASMClobberRegister(in); got != want {
				t.Errorf("canonicalEASMClobberRegister(%q) = %q, want %q", in, got, want)
			}
		}
	}
	for _, in := range []string{"memory", "cc", "flags", "xmm0", "st0", "", "rax2", "r16"} {
		if got := canonicalEASMClobberRegister(in); got != "" {
			t.Errorf("canonicalEASMClobberRegister(%q) = %q, want empty", in, got)
		}
	}
}

func TestEASMConstraintCoverage(t *testing.T) {
	regs, hasMem, hasCC := easmConstraintCoverage("={rax},{rdi},~{rbx},~{memory},~{cc},~{r12}")
	if !hasMem {
		t.Error("expected memory coverage")
	}
	if !hasCC {
		t.Error("expected cc coverage")
	}
	for _, r := range []string{"rax", "rdi", "rbx", "r12"} {
		if !regs[r] {
			t.Errorf("expected %s in coverage, got %v", r, regs)
		}
	}
	if regs["rsi"] {
		t.Error("rsi should not be covered")
	}
}
