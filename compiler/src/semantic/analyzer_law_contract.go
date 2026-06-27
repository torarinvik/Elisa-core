package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// checkLawContract enforces what makes a `law` a sound predicate rather than an arbitrary
// function (docs/85 §2, §9.5): it must return `bool`, take at least one value parameter (its
// subject), and be PURE — no effects. Purity is checked via the function's inferred effect set
// (the same set `@hot` is judged against), so a law cannot observe time/IO/randomness or mutate;
// that is what lets the compiler treat the predicate as a reorderable, cacheable fact and lets
// `is` apply it freely in type, flow, and contract position. Totality is covered by the existing
// progress/recursion checks that run over every function body.
func (a *Analyzer) checkLawContract(fn *ast.FuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil || !fn.IsLaw {
		return
	}
	// A FRAME law (docs/88) is a named `changes`/`preserves` set, not a bool predicate — it has no
	// body to be pure or total. Validate its shape here; its paths are validated like any frame
	// clause when it is applied (expandFulfills). A law may not be both frame and predicate.
	if isFrameLaw(fn) {
		if len(fn.Body) != 0 {
			a.errorf(fn.Pos(), "frame law %q has a `changes`/`preserves` clause and cannot also have a predicate body", fn.Name)
		}
		if len(fnType.Params) == 0 {
			a.errorf(fn.Pos(), "frame law %q needs a subject reference parameter (conventionally `self`)", fn.Name)
		}
		return
	}
	// An EFFECT law (docs/85 §4, Stage 4) is a named set of forbidden effects — a function-level
	// discharge class with no value subject and no body. Validate its shape here; conformance is
	// discharged against each fulfilling function's effect set (checkEffectFulfills).
	if isEffectLaw(fn) {
		if len(fn.Body) != 0 {
			a.errorf(fn.Pos(), "effect law %q has a `forbids` clause and cannot also have a predicate body", fn.Name)
		}
		if len(fnType.Params) != 0 {
			a.errorf(fn.Pos(), "effect law %q constrains the whole function and takes no subject parameter", fn.Name)
		}
		return
	}
	// A COMPOSITE law (docs/85 §6) unions its members. Validate: no body, no subject, every member is
	// a known function-level law (effect / shape / composite — not value or frame), and the include
	// graph is acyclic.
	if isCompositeLaw(fn) {
		if len(fn.Body) != 0 {
			a.errorf(fn.Pos(), "composite law %q has an `includes` clause and cannot also have a predicate body", fn.Name)
		}
		if len(fnType.Params) != 0 {
			a.errorf(fn.Pos(), "composite law %q constrains the whole function and takes no subject parameter", fn.Name)
		}
		for _, m := range fn.Includes {
			if isBuiltinShapeLaw(m) {
				continue
			}
			// docs/85 §8 hard rule: measure laws are emergent and non-local, so they are NEVER a
			// composable premise — including one in a law is a class error, not just unsupported.
			if isBuiltinMeasureLaw(m) {
				a.errorf(fn.Pos(), "composite law %q includes %q, a measure law; measure laws are not composable (docs/85 §8) — verify them with `fulfills %s` directly", fn.Name, m, m)
				continue
			}
			decl, _, ok := a.lookupLaw(m)
			if !ok || decl == nil {
				a.errorf(fn.Pos(), "composite law %q includes %q, which is not a law", fn.Name, m)
				continue
			}
			if !isEffectLaw(decl) && !isCompositeLaw(decl) {
				a.errorf(fn.Pos(), "composite law %q includes %q, which is a %s law; `includes` composes only function-level laws (effect, shape, composite)", fn.Name, m, lawClassName(decl))
			}
		}
		if a.compositeLawHasCycle(fn.Name, map[string]bool{}) {
			a.errorf(fn.Pos(), "composite law %q is cyclic: it transitively includes itself", fn.Name)
		}
		return
	}
	if !IsBoolType(fnType.Return) {
		a.errorf(fn.Pos(), "law %q must be a predicate returning bool, got %s", fn.Name, typeString(fnType.Return))
	}
	if len(fnType.Params) == 0 {
		a.errorf(fn.Pos(), "law %q needs a subject: give it at least one value parameter (conventionally `self`)", fn.Name)
	}
	// Purity: a law's inferred effect set must be empty. PermissionRefs carries the transitive
	// effects (the same set the @hot contract is judged against); any entry means the predicate
	// is impure and cannot be a sound, freely-applicable fact.
	for _, ref := range fnType.PermissionRefs {
		a.errorf(fn.Pos(), "law %q must be pure but uses the `%s` effect; a predicate may not perform effects (no IO, allocation, mutation, time, or randomness)", fn.Name, lawEffectName(ref))
		break
	}
}

// ProofOutcome classifies how a refinement obligation was discharged (docs/85 discharge ladder).
type ProofOutcome string

const (
	ProofProvenFlow     ProofOutcome = "proven (flow)"     // entailed by a branch-condition range fact
	ProofProvenLinear   ProofOutcome = "proven (linear)"   // entailed by tier-2 bounded linear arithmetic (docs/86)
	ProofProvenConst    ProofOutcome = "proven (const)"    // entailed by constant evaluation
	ProofProvenSMT      ProofOutcome = "proven (smt)"      // entailed by an SMT solver — nonlinear / rich-boolean (docs/90)
	ProofProvenContract ProofOutcome = "proven (contract)" // a function-level law (effect/shape/composite) discharged by analysis (docs/89)
	ProofAssumedExtern  ProofOutcome = "assumed (extern boundary)"
	ProofMeasured       ProofOutcome = "measured (-Wperf)" // a measure law verified post-codegen, surfaced as a warning (docs/89 Stage 5)
	ProofRefuted        ProofOutcome = "refuted"           // provably violated — a compile error
	ProofRuntime        ProofOutcome = "runtime"           // unprovable — debug runtime check / -strict error
)

type ProofDischargeClass string

const (
	ProofClassFlow      ProofDischargeClass = "flow"
	ProofClassLinear    ProofDischargeClass = "linear"
	ProofClassConst     ProofDischargeClass = "const"
	ProofClassSMT       ProofDischargeClass = "smt"
	ProofClassContract  ProofDischargeClass = "contract"
	ProofClassBoundary  ProofDischargeClass = "boundary"
	ProofClassScoped    ProofDischargeClass = "scoped"
	ProofClassTypestate ProofDischargeClass = "typestate"
	ProofClassRuntime   ProofDischargeClass = "runtime"
	ProofClassMeasured  ProofDischargeClass = "measured"
)

// ProofObligationCategory classifies a ProofFact into one of the four elision-telemetry buckets
// used by the --explain proof-elision summary: refinement returns, call-arg refinements, array
// bounds, and contract ensures. All other obligations (assert-by, frame laws, typestate, …) use
// ProofCatOther and are excluded from the per-category elision counts.
type ProofObligationCategory string

const (
	ProofCatReturnRefinement  ProofObligationCategory = "return-refinement"  // return value satisfies refinement type
	ProofCatCallArgRefinement ProofObligationCategory = "call-arg-refinement" // argument satisfies callee param refinement
	ProofCatArrayBounds       ProofObligationCategory = "array-bounds"        // index expression proved in-bounds
	ProofCatContractEnsures   ProofObligationCategory = "contract-ensures"    // postcondition (ensure) clause
	ProofCatOther             ProofObligationCategory = "other"               // all other obligations
)

// ProofFact is one entry in the --explain proof report: where a refinement was discharged, on what
// subject, by which law, and with what outcome.
type ProofFact struct {
	Pos              lexer.Pos
	Subject          string
	Predicate        string
	Outcome          ProofOutcome
	Class            ProofDischargeClass
	Category         ProofObligationCategory // elision-telemetry bucket; zero value treated as ProofCatOther
	KnownFacts       []string
	ClosedWorldFacts []string
	Missing          string
}

// recordProof appends one discharge decision to the proof report (docs/85 observability).
// It consumes and clears a.currentProofCategory so callers can set a category before entering
// the discharge ladder without threading it through every helper.
func (a *Analyzer) recordProof(pos lexer.Pos, subject, predicate string, outcome ProofOutcome) {
	a.recordProofWithClass(pos, subject, predicate, outcome, defaultProofClass(outcome), nil, "")
}

// recordProofCat is like recordProof but explicitly stamps the elision-telemetry category.
// Use it when the category is known at the call site (e.g. return-refinement, call-arg-refinement,
// contract-ensures). For call paths that go through tryDischargeRefinementStatically set
// a.currentProofCategory before the call; recordProofWithClass will consume and clear it.
func (a *Analyzer) recordProofCat(pos lexer.Pos, subject, predicate string, outcome ProofOutcome, cat ProofObligationCategory) {
	var known []string
	if a != nil {
		known = append([]string(nil), a.inScopeKnownFacts()...)
	}
	a.proofReport = append(a.proofReport, ProofFact{
		Pos:        pos,
		Subject:    subject,
		Predicate:  predicate,
		Outcome:    outcome,
		Class:      defaultProofClass(outcome),
		Category:   cat,
		KnownFacts: known,
	})
}

func (a *Analyzer) recordProofWithClass(pos lexer.Pos, subject, predicate string, outcome ProofOutcome, class ProofDischargeClass, closedWorldFacts []string, missing string) {
	var known []string
	if a != nil {
		known = append([]string(nil), a.inScopeKnownFacts()...)
	}
	cat := a.currentProofCategory
	a.currentProofCategory = "" // consume
	a.proofReport = append(a.proofReport, ProofFact{
		Pos:              pos,
		Subject:          subject,
		Predicate:        predicate,
		Outcome:          outcome,
		Class:            class,
		Category:         cat,
		KnownFacts:       known,
		ClosedWorldFacts: append([]string(nil), closedWorldFacts...),
		Missing:          missing,
	})
}

func defaultProofClass(outcome ProofOutcome) ProofDischargeClass {
	switch outcome {
	case ProofProvenFlow:
		return ProofClassFlow
	case ProofProvenLinear:
		return ProofClassLinear
	case ProofProvenConst:
		return ProofClassConst
	case ProofProvenSMT:
		return ProofClassSMT
	case ProofProvenContract:
		return ProofClassContract
	case ProofAssumedExtern:
		return ProofClassBoundary
	case ProofMeasured:
		return ProofClassMeasured
	default:
		return ProofClassRuntime
	}
}

// ProofElisionCounts is the (elided, runtime) pair for one obligation category.
// Elided = statically proven (no runtime check needed); Runtime = fell back to a debug check.
type ProofElisionCounts struct {
	Elided  int // statically proven (ProofProven* outcomes)
	Runtime int // fell back to a runtime check (ProofRuntime outcome)
}

// ProofElisionSummary is the per-category breakdown of check elision computed from the ProofReport
// and IndexBoundsProven map. Printed as a one-liner under --explain to make the dogfooding payoff
// immediately scannable ("emulator: N bounds checks elided").
type ProofElisionSummary struct {
	ReturnRefinements  ProofElisionCounts
	CallArgRefinements ProofElisionCounts
	ArrayBounds        ProofElisionCounts
	ContractEnsures    ProofElisionCounts
}

// ComputeElisionSummary builds a ProofElisionSummary from a proof report and the proven/total
// array-bounds counts (caller computes those from IndexBoundsProven). The three refinement
// categories come from ProofFact.Category tags; the array-bounds category is passed directly so
// this function does not need to import ast.
func ComputeElisionSummary(report []ProofFact, arrayBoundsElided, arrayBoundsRuntime int) ProofElisionSummary {
	var s ProofElisionSummary
	s.ArrayBounds = ProofElisionCounts{Elided: arrayBoundsElided, Runtime: arrayBoundsRuntime}
	for _, f := range report {
		cat := f.Category
		if cat == "" {
			cat = ProofCatOther
		}
		proven := isProofProven(f.Outcome)
		runtime := f.Outcome == ProofRuntime
		switch cat {
		case ProofCatReturnRefinement:
			if proven {
				s.ReturnRefinements.Elided++
			} else if runtime {
				s.ReturnRefinements.Runtime++
			}
		case ProofCatCallArgRefinement:
			if proven {
				s.CallArgRefinements.Elided++
			} else if runtime {
				s.CallArgRefinements.Runtime++
			}
		case ProofCatContractEnsures:
			if proven {
				s.ContractEnsures.Elided++
			} else if runtime {
				s.ContractEnsures.Runtime++
			}
		}
	}
	return s
}

// isProofProven reports whether the outcome is any of the statically-proven variants.
func isProofProven(o ProofOutcome) bool {
	switch o {
	case ProofProvenFlow, ProofProvenLinear, ProofProvenConst, ProofProvenSMT, ProofProvenContract:
		return true
	}
	return false
}

// proofLint reports that a refinement obligation was not statically discharged. It is a WARNING by
// default — so the user always KNOWS where a static guarantee fell back to a runtime check — and a
// hard ERROR under `-strict` (EnforceStrictProofs), the Dafny-like prove-it-or-fail mode (docs/85).
func (a *Analyzer) proofLint(pos lexer.Pos, format string, args ...interface{}) {
	if a.enforceStrictProofs {
		a.errorf(pos, format, args...)
		return
	}
	a.warnf(pos, format, args...)
}

// lawEffectName renders an effect reference for the law-purity diagnostic.
func lawEffectName(ref ast.PermissionRef) string {
	if ref.Member != "" {
		return ref.Name + "." + ref.Member
	}
	return ref.Name
}

// typeString renders a type for diagnostics, tolerating nil.
func typeString(t Type) string {
	if t == nil {
		return "void"
	}
	return t.String()
}
