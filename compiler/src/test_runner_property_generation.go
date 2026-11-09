package main

import (
	"elisacore/src/semantic"
	"fmt"
	"strings"
)

// lockstepDefaultSteps is the default number of steps a @lockstep oracle advances
// both steppers per fuzzed seed before giving up. Overridable with @lockstep(N).
const lockstepDefaultSteps = 64

// lockstepCases is the number of distinct fuzzed seeds a @lockstep oracle tries.
// Each seed is stepped up to the step count, so total work is cases * steps; we keep
// the seed count modest because the per-seed step loop already explores deeply.
const lockstepCases = 64

// lockstepStepCount returns the per-seed step bound for a @lockstep function: the
// optional positive-integer @lockstep(N) argument, or lockstepDefaultSteps.
func lockstepStepCount(fn *semantic.AnnotatedFunc) int {
	for _, ann := range fn.Annotations {
		if ann.Name != "lockstep" || len(ann.Args) != 1 {
			continue
		}
		if n, ok := semantic.PropertyCaseCount(ann.Args[0]); ok && n > 0 {
			return n
		}
	}
	return lockstepDefaultSteps
}

// lockstepSeedParams returns the seed (non-step) parameter type names and labels of a
// @lockstep function. The trailing `step` parameter is excluded; the analyzer has
// already guaranteed it is present, named, and integer-typed.
func lockstepSeedParams(fn *semantic.AnnotatedFunc) (types, labels []string) {
	n := len(fn.Signature.Params) - 1 // drop trailing step param
	for j := 0; j < n; j++ {
		t, _ := semantic.PropertyParamTypeName(fn.Signature.Params[j])
		types = append(types, t)
		label := fmt.Sprintf("arg%d", j)
		if j < len(fn.Signature.ExplicitParamNames) && fn.Signature.ExplicitParamNames[j] != "" {
			label = fn.Signature.ExplicitParamNames[j]
		}
		labels = append(labels, label)
	}
	return types, labels
}

// lockstepHelperSource emits two pure helper functions for a @lockstep oracle:
//   - `__lockstep_<name>_div(seeds...) -> i64` returns the first step index (0..<N) at
//     which the impl projection diverges from the `<name>__ref` projection, or -1 if
//     they agree for all N steps. This is the comparable scalar the seed-shrinker uses
//     (a seed is "interesting" iff div >= 0) and is also how the step index is reported.
//
// Both the impl and reference are called as `f(seeds..., step)`; the reference must be
// declared by the program (or a compile error naming `<name>__ref` results — that is
// the intended, sound way to require the partner stepper).
func lockstepHelperSource(fn *semantic.AnnotatedFunc) string {
	seedTypes, seedLabels := lockstepSeedParams(fn)
	_ = seedLabels
	steps := lockstepStepCount(fn)
	var b strings.Builder
	// Build the seed parameter list with stable synthetic names.
	params := make([]string, len(seedTypes))
	seedArgs := make([]string, len(seedTypes))
	for j, t := range seedTypes {
		params[j] = fmt.Sprintf("__ls_s%d: %s", j, t)
		seedArgs[j] = fmt.Sprintf("__ls_s%d", j)
	}
	stepType, _ := semantic.PropertyParamTypeName(fn.Signature.Params[len(fn.Signature.Params)-1])
	fmt.Fprintf(&b, "\ndef __lockstep_%s_div(%s) -> i64:\n", fn.Name, strings.Join(params, ", "))
	fmt.Fprintf(&b, "\tfor __ls_step in 0..<%d:\n", steps)
	// Convert the loop counter to the stepper's step-parameter type.
	stepExpr := lockstepStepConv(stepType, "__ls_step")
	implCall := fmt.Sprintf("%s(%s)", fn.Name, lockstepArgs(seedArgs, stepExpr))
	refCall := fmt.Sprintf("%s%s(%s)", fn.Name, semantic.LockstepRefSuffix, lockstepArgs(seedArgs, stepExpr))
	fmt.Fprintf(&b, "\t\tif %s != %s:\n", implCall, refCall)
	b.WriteString("\t\t\treturn __ls_step.i64()\n")
	b.WriteString("\treturn (0 - 1).i64()\n")
	return b.String()
}

// lockstepArgs joins seed args with the step expression for a stepper call.
func lockstepArgs(seedArgs []string, stepExpr string) string {
	all := make([]string, 0, len(seedArgs)+1)
	all = append(all, seedArgs...)
	all = append(all, stepExpr)
	return strings.Join(all, ", ")
}

// lockstepStepConv converts the i64 loop counter to the declared step-parameter type.
func lockstepStepConv(stepType, counter string) string {
	switch stepType {
	case "int":
		return counter
	case "i64":
		return counter + ".i64()"
	case "u32":
		return counter + ".u32()"
	case "u64":
		return counter + ".u64()"
	default:
		return counter
	}
}

// lockstepDriverSource emits the void test driver for a @lockstep oracle: it fuzzes the
// seed inputs (deterministic xorshift64, reusing the property draw helpers), calls the
// `__lockstep_<name>_div` helper to find the first diverging step, greedily shrinks the
// seeds while they still diverge, and on the first divergence reports the step index,
// shrunk seeds, and panics — reusing the @property counterexample report machinery.
func lockstepDriverSource(driverName string, fn *semantic.AnnotatedFunc) string {
	seedTypes, seedLabels := lockstepSeedParams(fn)
	seed := propertySeed(fn.Name)
	steps := lockstepStepCount(fn)
	divFn := fmt.Sprintf("__lockstep_%s_div", fn.Name)
	var b strings.Builder
	b.WriteString("\ndef ")
	b.WriteString(driverName)
	b.WriteString("() -> void:\n")
	b.WriteString("\tcan Abort.Panic:\n")
	fmt.Fprintf(&b, "\t\t__prop_s: mutable u64 = %d\n", seed)
	b.WriteString("\t\t__prop_buf: mutable u8[128] = zeroed\n")
	b.WriteString("\t\t__prop_n: mutable int = 0\n")
	fmt.Fprintf(&b, "\t\tfor __prop_i in 0..<%d:\n", lockstepCases)
	argNames := make([]string, len(seedTypes))
	for j, t := range seedTypes {
		argNames[j] = fmt.Sprintf("__prop_a%d", j)
		b.WriteString("\t\t\t__prop_s <- __prop_s ^ (__prop_s << 13)\n")
		b.WriteString("\t\t\t__prop_s <- __prop_s ^ (__prop_s >> 7)\n")
		b.WriteString("\t\t\t__prop_s <- __prop_s ^ (__prop_s << 17)\n")
		fmt.Fprintf(&b, "\t\t\t%s: %s = %s\n", argNames[j], t, propertyDrawExpr(t, "__prop_s"))
	}
	// A seed is interesting iff the steppers diverge at some step (div >= 0).
	fmt.Fprintf(&b, "\t\t\tif %s(%s) >= 0:\n", divFn, strings.Join(argNames, ", "))
	mutNames := make([]string, len(argNames))
	for j := range argNames {
		mutNames[j] = fmt.Sprintf("__prop_m%d", j)
		fmt.Fprintf(&b, "\t\t\t\t%s: mutable %s = %s\n", mutNames[j], seedTypes[j], argNames[j])
	}
	// Greedy coordinate-descent shrink: drive each seed toward zero while the steppers
	// still diverge for SOME step, so the reported seed is minimal-ish.
	fmt.Fprintf(&b, "\t\t\t\tfor __prop_r in 0..<%d:\n", propertyShrinkRounds)
	b.WriteString("\t\t\t\t\t_ = __prop_r\n")
	for j := range mutNames {
		t := seedTypes[j]
		zero := propertyZeroExpr(t)
		fmt.Fprintf(&b, "\t\t\t\t\tif %s(%s) >= 0:\n", divFn, strings.Join(lockstepReplace(mutNames, j, zero), ", "))
		fmt.Fprintf(&b, "\t\t\t\t\t\t%s <- %s\n", mutNames[j], zero)
		if t != "bool" {
			half := propertyHalfExpr(t, mutNames[j])
			fmt.Fprintf(&b, "\t\t\t\t\tif %s != %s and %s(%s) >= 0:\n", mutNames[j], half, divFn, strings.Join(lockstepReplace(mutNames, j, half), ", "))
			fmt.Fprintf(&b, "\t\t\t\t\t\t%s <- %s\n", mutNames[j], half)
		}
	}
	// Report: first diverging step index, then each shrunk seed.
	header := fmt.Sprintf(">>> lockstep %s counterexample (case %%lld, shrunk):\\n", fn.Name)
	b.WriteString(propertyReportStmt(header, "__prop_i.i64()"))
	stepLine := "      first diverging step = %lld\\n"
	b.WriteString(propertyReportStmt(stepLine, fmt.Sprintf("%s(%s)", divFn, strings.Join(mutNames, ", "))))
	for j := range mutNames {
		verb, conv := propertyReportArg(seedTypes[j], mutNames[j])
		line := fmt.Sprintf("      %s (%s) = %s\\n", seedLabels[j], seedTypes[j], verb)
		b.WriteString(propertyReportStmt(line, conv))
	}
	msg := elisacoreStringLiteral(fmt.Sprintf("lockstep %q diverged (deterministic seed; %d cases x %d steps)", fn.Name, lockstepCases, steps))
	fmt.Fprintf(&b, "\t\t\t\tpanic(%s)\n", msg)
	return b.String()
}

// lockstepReplace renders the div-helper seed arguments with the j-th replaced by a
// candidate expression (for shrink trial calls).
func lockstepReplace(mutNames []string, j int, candidate string) []string {
	parts := make([]string, len(mutNames))
	copy(parts, mutNames)
	parts[j] = candidate
	return parts
}

// propertyReportExterns declares libc snprintf/write under distinct Elisa names so
// the property drivers can print failing inputs without the runtime arenas.
const propertyReportExterns = `
@link_name("snprintf")
extern __prop_snprintf(buf: mutable u8&?, bufsize: usize, fmt: u8&, ...) -> int
@link_name("write")
extern __prop_write(fd: int, buf: void&, count: usize) -> isize
`

// propertyReportStmt emits an indented snprintf-into-__prop_buf + write(2) pair for
// one report line. fmtLiteral is the printf-style format (with escaped \n), arg is
// the single Elisa value expression substituted for its verb.
func propertyReportStmt(fmtLiteral, arg string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\t\t\t\t__prop_n <- __prop_snprintf(&__prop_buf[0], 128, \"%s\", %s)\n", fmtLiteral, arg)
	b.WriteString("\t\t\t\tif __prop_n > 0:\n")
	b.WriteString("\t\t\t\t\t_ = __prop_write(2, (&__prop_buf[0]).cast[void&], __prop_n.usize())\n")
	return b.String()
}

// propertyShrinkRounds bounds the greedy coordinate-descent shrink applied to a
// failing input before it is reported. 40 rounds is comfortably more than the
// log2 of the draw ranges, so halving converges well before the cap.
const propertyShrinkRounds = 40

// propertyCallWith renders a predicate call over the mutable shrink locals with the
// j-th argument replaced by a candidate expression.
func propertyCallWith(fnName string, mutNames []string, j int, candidate string) string {
	parts := make([]string, len(mutNames))
	copy(parts, mutNames)
	parts[j] = candidate
	return fmt.Sprintf("%s(%s)", fnName, strings.Join(parts, ", "))
}

// propertyZeroExpr is the simplest ("most shrunk") value of a supported type.
func propertyZeroExpr(typeName string) string {
	switch typeName {
	case "bool":
		return "false"
	case "f32":
		return "0.0.f32()"
	case "f64":
		return "0.0"
	case "int", "i64":
		return "0"
	default: // i8/i16/i32, u8/u16/u32/u64
		return "0." + typeName + "()"
	}
}

// propertyHalfExpr halves the magnitude of a shrink local toward zero.
func propertyHalfExpr(typeName, mut string) string {
	switch typeName {
	case "f32":
		return mut + " / 2.0.f32()"
	case "f64":
		return mut + " / 2.0"
	default:
		return mut + " / 2"
	}
}

// propertyReportArg returns the printf verb and the Elisa conversion expression for
// reporting a value of the given supported property type.
func propertyReportArg(typeName, arg string) (verb, conv string) {
	switch typeName {
	case "bool":
		return "%lld", "(1 if " + arg + " else 0).i64()"
	case "u8", "u16", "u32", "u64":
		return "%llu", arg + ".u64()"
	case "f32", "f64":
		return "%g", arg + ".f64()"
	default: // signed integers and int
		return "%lld", arg + ".i64()"
	}
}

// propertyDrawExpr returns an Elisa expression that converts the current
// generator state `s` into a value of the given supported type.
func propertyDrawExpr(typeName, s string) string {
	switch typeName {
	case "bool":
		return "(" + s + " & 1) == 1"
	case "i8":
		return "((" + s + " % 256).i32() - 128).i8()"
	case "i16":
		return "((" + s + " % 65536).i32() - 32768).i16()"
	case "i32":
		return "((" + s + " % 2000001).i64() - 1000000).i32()"
	case "i64":
		return "(" + s + " % 2000000001).i64() - 1000000000"
	case "int":
		return "((" + s + " % 2000000001).i64() - 1000000000).int()"
	case "u8":
		return "(" + s + " % 256).u8()"
	case "u16":
		return "(" + s + " % 65536).u16()"
	case "u32":
		return "(" + s + " % 4000000000).u32()"
	case "u64":
		return s
	case "f64":
		// Fixed-point draw over [-1000.000, 1000.000] in 0.001 steps; covers
		// sign, zero, and fractional values without relying on bit reinterpretation.
		return "((" + s + " % 2000001).i64() - 1000000).f64() / 1000.0"
	case "f32":
		return "(((" + s + " % 2000001).i64() - 1000000).f64() / 1000.0).f32()"
	default:
		return s
	}
}

// biasedDrawTypes is the set of integer types for which boundary-biased generation
// emits a draw helper. bool and floats keep the existing uniform draw.
var biasedDrawTypes = map[string]bool{
	"i8": true, "i16": true, "i32": true, "i64": true, "int": true,
	"u8": true, "u16": true, "u32": true, "u64": true,
}

// propertyDrawSelect returns the per-param draw expression. For the integer types that
// have a boundary-biased helper it calls that helper; everything else (bool, floats)
// falls back to the uniform draw.
func propertyDrawSelect(typeName, s string) string {
	if biasedDrawTypes[typeName] {
		return fmt.Sprintf("__prop_draw_%s(%s)", typeName, s)
	}
	return propertyDrawExpr(typeName, s)
}

// biasedTypeInfo describes the unsigned MAX literal and the bit-width of an integer
// type for edge-case construction. For signed types MAX is the unsigned all-ones
// pattern of the same width; the helper produces a u64 edge value and converts it
// to the target type by truncation (.T()), so all-ones maps to the signed -1/MAX
// boundaries that matter for overflow/mask predicates.
var biasedTypeBits = map[string]int{
	"i8": 8, "i16": 16, "i32": 32, "i64": 64, "int": 64,
	"u8": 8, "u16": 16, "u32": 32, "u64": 64,
}

// biasedDrawHelperSource emits an Elisa function `__prop_draw_<T>(s: u64) -> T` that,
// from the already-advanced generator state `s`, returns either a uniform value
// (mode bit clear, ~50%) or a structured edge-case value. The edge value is built in
// u64 space and converted to T by truncation. All entropy comes from disjoint bit
// slices of `s`, so the helper is a pure deterministic function of `s` (the driver's
// xorshift sequence is unchanged) and the shrinker still converges toward zero.
// commonShiftSet returns the deduplicated set of high-value bit-shift indices
// (byte/word/cacheline/page/2048/typical-width boundaries) clamped to a type of
// `bits` width. Used to bias the `1<<k` (pow2) and `(1<<k)-1` (mask) categories
// so a divergence localized to one specific power of two (e.g. 2048 == 1<<11)
// recurs often enough to be hit within a normal case budget.
func commonShiftSet(bits int) []int {
	candidates := []int{0, 1, 2, 3, 6, 8, 11, 12, 16, 21, 30, 31, 32, 63}
	// Drop shifts >= bits-2: 1<<(bits-1) is the sign-bit / near-MAX value that
	// categories 3/4/5 already cover, and 1<<(bits-2) is large enough that summing
	// two of them overflows a signed value of this width (e.g. 1<<30 + 1<<30 ==
	// 1<<31 on i32). Over-weighting either would manufacture operands that
	// spuriously trap checked arithmetic in holding properties (e.g. `a + b`). The
	// common set's purpose is the under-sampled MID alignment boundaries
	// (cacheline/2048/page/typical-width), not the extremes; capping at bits-3
	// keeps 1<<k below a quarter of the magnitude so paired sums stay in range.
	hi := bits - 2
	seen := map[int]bool{}
	out := []int{}
	for _, k := range candidates {
		if k >= hi {
			continue
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	if len(out) == 0 {
		out = append(out, 0)
	}
	return out
}

func biasedDrawHelperSource(typeName string) string {
	bits := biasedTypeBits[typeName]
	// all-ones mask for the width, as a u64 literal expression.
	var maxU string
	if bits >= 64 {
		maxU = "18446744073709551615" // 2^64-1
	} else {
		maxU = fmt.Sprintf("%d", (uint64(1)<<uint(bits))-1)
	}
	conv := func(uexpr string) string {
		if typeName == "u64" {
			return uexpr
		}
		return "(" + uexpr + ")." + typeName + "()"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\ndef __prop_draw_%s(s: u64) -> %s:\n", typeName, typeName)
	// Mode bit (~50%): bit 1 of s. Disjoint from low bits used by the uniform draw.
	b.WriteString("\tif ((s >> 1) & 1) == 0:\n")
	fmt.Fprintf(&b, "\t\treturn %s\n", propertyDrawExpr(typeName, "s"))
	// Edge-case construction. Category selector and sub-index from disjoint bit slices.
	fmt.Fprintf(&b, "\tmaxU: u64 = %s\n", maxU)
	// Shift index k for the pow2 (cat 6, 1<<k) and mask (cat 7, (1<<k)-1)
	// categories. The uniform `(s >> 8) % bits` alone draws any SPECIFIC power of
	// two (e.g. exactly 1<<11 == 2048) with probability <1/bits, so a divergence
	// localized to one alignment/width boundary is missed within a normal case
	// budget. With ~75% probability (a disjoint 2-bit slice of s) pick k from a
	// small high-value set of common byte/word/cacheline/page/2048/typical-width
	// boundaries; otherwise fall back to a uniform draw so arbitrary widths stay
	// reachable. Both the uniform draw and the common set are bounded to shift
	// indices < bits-2, so 1<<k / (1<<k)-1 never produce the sign-bit / near-MAX
	// magnitudes (covered by cats 3/4/5) and two pow2 draws can't sum past a signed
	// MAX of this width; this keeps holding properties like `a + b` on i32 from
	// spuriously overflowing checked arithmetic when the pow2 category is paired.
	// All entropy comes from disjoint slices of s, so the xorshift sequence is
	// unchanged and shrinking/reproduction is unaffected.
	commonK := commonShiftSet(bits)
	kmod := bits - 2
	if kmod < 1 {
		kmod = 1
	}
	fmt.Fprintf(&b, "\tku: u64 = (s >> 8) %% %d\n", kmod) // uniform shift index 0..bits-3
	b.WriteString("\tk: mutable u64 = ku\n")
	// Common-shift mode (~75%): bits 2..3 of s nonzero. Disjoint from bit 1
	// (helper mode), bit 0, and the uniform-k slice.
	b.WriteString("\tif ((s >> 2) & 3) != 0:\n")
	fmt.Fprintf(&b, "\t\tki: u64 = (s >> 24) %% %d\n", len(commonK))
	for i, ck := range commonK {
		if i == 0 {
			fmt.Fprintf(&b, "\t\tif ki == %d:\n\t\t\tk <- %d\n", i, ck)
		} else {
			fmt.Fprintf(&b, "\t\telif ki == %d:\n\t\t\tk <- %d\n", i, ck)
		}
	}
	b.WriteString("\tpage: u64 = ((s >> 20) << 12) & maxU\n")
	// Category selector. Categories 6 (pow2 1<<k) and 7 (mask (1<<k)-1) are the
	// shift-driven ones; give them extra weight so the common-shift k above recurs
	// often enough to hit a divergence localized to a single power of two (e.g.
	// 2048) within the case budget. `(s >> 4) % 22`: craw>=11 collapses onto {6,7}
	// (doubling their share), else the original 0..10 distribution is preserved.
	// Safe against holding-property overflow because k is bounded below the sign
	// bit, so the extra pow2/mask draws stay mid-magnitude.
	b.WriteString("\tcraw: u64 = (s >> 4) % 22\n")
	b.WriteString("\tcat: mutable u64 = craw\n")
	b.WriteString("\tif craw >= 11:\n\t\tcat <- 6 + (craw & 1)\n")
	b.WriteString("\tev: mutable u64 = 0\n")
	// 0,1,2 / MAX, MAX-1, MAX-k / pow2 / mask / page, page+1, page-1.
	b.WriteString("\tif cat == 0:\n\t\tev <- 0\n")
	b.WriteString("\telif cat == 1:\n\t\tev <- 1\n")
	b.WriteString("\telif cat == 2:\n\t\tev <- 2\n")
	b.WriteString("\telif cat == 3:\n\t\tev <- maxU\n")
	b.WriteString("\telif cat == 4:\n\t\tev <- maxU - 1\n")
	b.WriteString("\telif cat == 5:\n\t\tev <- (maxU - ((s >> 16) % 4)) & maxU\n")
	b.WriteString("\telif cat == 6:\n\t\tev <- (1.u64() << k) & maxU\n")
	b.WriteString("\telif cat == 7:\n\t\tev <- ((1.u64() << k) - 1) & maxU\n")
	b.WriteString("\telif cat == 8:\n\t\tev <- page\n")
	b.WriteString("\telif cat == 9:\n\t\tev <- (page + 1) & maxU\n")
	b.WriteString("\telse:\n\t\tev <- (page - 1) & maxU\n")
	fmt.Fprintf(&b, "\treturn %s\n", conv("ev"))
	return b.String()
}

// propertySeed derives a fixed nonzero xorshift64 seed from the property name so
// each property gets a distinct but reproducible input sequence (FNV-1a).
func propertySeed(name string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(name); i++ {
		h ^= uint64(name[i])
		h *= 1099511628211
	}
	// Mask to 63 bits so the value always fits a positive integer literal in
	// generated source, regardless of how the lexer first types the constant.
	h &= 0x7FFFFFFFFFFFFFFF
	if h == 0 {
		h = 88172645463325252
	}
	return h
}
