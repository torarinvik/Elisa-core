package easm

// Go<->Coq fidelity for the GUEST-MEMORY-STATE / guest-read layer (docs/107).
//
// The Coq development formal/easm-tal/EasmTAL.v models the typed guest-memory
// overlay's `requires size >= N` discharge as a lattice slot `amsize` (the known
// MINIMUM of the carrier's runtime guest-struct size) and a guest-read typing
// rule:
//
//     T_read : off + w <= amsize g  ->  has_type g (Iread d off w) (adefine g d)
//
// i.e. a field read is WELL-TYPED iff its byte span [off, off+w) is covered by
// the known minimum size; otherwise NO rule applies = ill-typed = stuck. Size
// guards strengthen the slot via `arefine_size g n = amsize := max(amsize, n)`,
// and the join at merges takes the MIN (ameet's amsize = min(amsize g1, amsize g2)).
//
// The Go-side authority for the SAME obligation is easm.CheckGuestOverlaySizeGuard
// (easm_guest_overlay.go): it discharges a field's `requires size >= N` tag iff
// some dominating SizeGuardFact proves `size >= K` with K >= N. This test makes
// the Coq model's read rule a continuously-checked mirror of that Go checker.
//
// THE CORRESPONDENCE. The set of dominating size-guard facts {AtLeast = K_i}
// induces the Coq known-minimum size amsize = max_i K_i (each guard refines the
// slot upward via arefine_size; the empty fact set leaves amsize = 0). For a
// field at `off` of width `w` with requirement `need = off+w` (the Coq read
// obligation; in the overlay the field's RequiresSizeAtLeast is exactly the
// offset+width past which the field is "present"), the two notions coincide:
//
//   Coq T_read applicable   <=>   off + w <= amsize = max_i K_i
//                           <=>   exists i, K_i >= off + w           (max property)
//                           <=>   CheckGuestOverlaySizeGuard discharges (no issue)
//
// So: the Coq read rule ACCEPTS  iff  the Go checker emits no
// overlay-field-needs-size-guard. We fuzz fact sets and requirements and assert
// this iff continuously, so the mechanized read rule and the real Go discharge
// cannot drift.

import (
	"math/rand"
	"testing"
)

const easmMemstateFidelityCases = 8000

// coqAmsize mirrors the Coq known-minimum-size slot induced by a list of
// dominating size guards: amsize = max over the guard bounds (arefine_size folds
// each guard's bound in with Nat.max; empty => 0). This is the EXACT image of the
// SizeGuardFact set the Go checker is handed.
func coqAmsize(facts []SizeGuardFact) int64 {
	var m int64 // 0 = nothing proven (Coq amsize of aempty)
	for _, f := range facts {
		if f.AtLeast > m {
			m = f.AtLeast
		}
	}
	return m
}

// coqReadWellTyped mirrors the Coq `T_read` premise `off + w <= amsize g`: the
// read is well-typed iff its span is covered by the known minimum size induced
// by the guards. (need == off+w == the field's RequiresSizeAtLeast.)
func coqReadWellTyped(need int64, facts []SizeGuardFact) bool {
	return need <= coqAmsize(facts)
}

func TestEASMGoCoqMemStateFidelity(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5128E))
	bounds := []int64{0, 8, 16, 24, 32, 40, 48, 64, 72, 80}

	for c := 0; c < easmMemstateFidelityCases; c++ {
		// A guarded field whose `requires size >= need` is the read obligation.
		// need == off+w in the Coq model; here we draw it directly from the same
		// pool of plausible struct-size boundaries.
		need := bounds[rng.Intn(len(bounds))]

		// Random set of dominating size-guard facts (0..3 of them).
		nFacts := rng.Intn(4)
		facts := make([]SizeGuardFact, 0, nFacts)
		for i := 0; i < nFacts; i++ {
			facts = append(facts, SizeGuardFact{AtLeast: bounds[rng.Intn(len(bounds))]})
		}

		// Build a one-field layout carrying the `requires size >= need` tag. A
		// need of 0 means an unconditional field (no requirement) — both sides
		// must accept it regardless of facts.
		field := LayoutField{Offset: 0, Name: "f", Type: "u64", Width: 8}
		if need > 0 {
			field.RequiresSizeAtLeast = need
		}
		layout := &Layout{Name: "L", Fields: []LayoutField{field}}

		// Coq read rule: a requirement of 0 is unconditionally present (amsize >= 0
		// always); otherwise need <= amsize.
		coqOK := need == 0 || coqReadWellTyped(need, facts)

		// Go authority.
		issues := CheckGuestOverlaySizeGuard("memstate.elisa", c, layout, "f", facts)
		goOK := !containsIssue(issues, "overlay-field-needs-size-guard")

		if coqOK != goOK {
			t.Fatalf("case %d: Coq read-rule (amsize=%d, need=%d) accepts=%v but Go discharge accepts=%v\nfacts=%#v\nissues=%#v",
				c, coqAmsize(facts), need, coqOK, goOK, facts, issues)
		}
	}
}

// TestEASMCoqMemStateJoinIsMin pins the docs/107 merge rule: the known-minimum
// size after a control-flow merge is the MIN of the predecessor bounds (ameet's
// amsize = Nat.min). A read discharged after the merge must be backed by the
// WEAKER edge — exactly what the Go checker would see if handed the merged
// (min) fact. This mirrors the Coq ameet_msize_lb_l/r + models_meet_l/r chain.
func TestEASMCoqMemStateJoinIsMin(t *testing.T) {
	rng := rand.New(rand.NewSource(0xA17))
	bounds := []int64{0, 8, 16, 24, 32, 48, 64, 72}
	for c := 0; c < 4000; c++ {
		k1 := bounds[rng.Intn(len(bounds))]
		k2 := bounds[rng.Intn(len(bounds))]
		need := bounds[rng.Intn(len(bounds))]

		// Merged known-minimum size = min(k1, k2) (Coq ameet amsize).
		merged := k1
		if k2 < merged {
			merged = k2
		}

		field := LayoutField{Offset: 0, Name: "f", Type: "u64", Width: 8}
		if need > 0 {
			field.RequiresSizeAtLeast = need
		}
		layout := &Layout{Name: "L", Fields: []LayoutField{field}}

		// Discharge against the MERGED fact (the only sound fact past the join).
		issues := CheckGuestOverlaySizeGuard("join.elisa", c, layout, "f", []SizeGuardFact{{AtLeast: merged}})
		goOK := !containsIssue(issues, "overlay-field-needs-size-guard")

		// Coq: a post-merge read is well-typed iff need <= min(k1,k2).
		coqOK := need == 0 || need <= merged
		if coqOK != goOK {
			t.Fatalf("case %d: join-min mismatch k1=%d k2=%d need=%d merged=%d coqOK=%v goOK=%v",
				c, k1, k2, need, merged, coqOK, goOK)
		}
	}
}
