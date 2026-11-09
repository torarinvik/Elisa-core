package main

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"strings"
)

// propertyIterations is the default number of random inputs each @property is
// checked against; override per-property with @property(N).
const propertyIterations = 256

// propertyCaseCount returns the case count for a property: the optional
// positive-integer @property(N) argument, or propertyIterations by default.
func propertyCaseCount(fn *semantic.AnnotatedFunc) int {
	for _, ann := range fn.Annotations {
		if (ann.Name != "property" && ann.Name != "differential") || len(ann.Args) != 1 {
			continue
		}
		if n, ok := semantic.PropertyCaseCount(ann.Args[0]); ok && n > 0 {
			return n
		}
	}
	return propertyIterations
}

// buildPropertyDrivers synthesizes, for every selected @property function, a
// parameterless void driver `__property_<name>` that feeds it `propertyIterations`
// deterministic pseudo-random inputs (xorshift64, name-seeded so runs are
// reproducible) and panics on the first counterexample. Each driver is returned
// as appended Elisa source plus a synthetic test case so it runs like a @test.
func buildPropertyDrivers(result *semantic.Result, filter string) (string, []selectedTestCase) {
	props := selectAnnotatedFunctions(result, "property", filter)
	// @differential functions (a reusable bit-exact-vs-reference falsification harness) reuse the
	// exact same fuzz-input generation and counterexample machinery as @property; the only
	// difference is the report wording ("differential ... counterexample"). We track which selected
	// funcs are differential so propertyDriverSource can label them accordingly.
	differentialNames := map[string]bool{}
	for _, fn := range selectAnnotatedFunctions(result, "differential", filter) {
		if fn != nil {
			differentialNames[fn.Name] = true
			props = append(props, fn)
		}
	}
	// Protocol laws (docs/85 P3) the analyzer could not statically prove are auto-lowered to a
	// @property fuzz harness. Their source is injected separately (buildLawFuzzHarnesses); here we add
	// their synthetic @property AnnotatedFuncs so the property-driver pass generates a randomized driver
	// for each (with name-filter applied, like authored @property functions).
	for _, fn := range lawFuzzAnnotatedFuncs(result) {
		if fn != nil && matchesNameFilter(fn.Name, filter) {
			props = append(props, fn)
		}
	}
	// @lockstep functions are STATE-LEVEL differential oracles: a step-and-project
	// stepper plus a same-signature reference named `<name>__ref`. We lower each to (a)
	// a synthetic bool "agree over all steps" @property whose seeds the existing fuzz +
	// shrink machinery drives, and (b) extra source/report wiring that names the first
	// diverging step index. The synthetic agree-predicate AnnotatedFuncs are appended to
	// `props` so they flow through the normal property-driver pass; the lockstep-specific
	// helper source + step-reporting driver is emitted alongside.
	lockstepFns := selectAnnotatedFunctions(result, "lockstep", filter)

	if len(props) == 0 && len(lockstepFns) == 0 {
		return "", nil
	}
	var src strings.Builder
	// Prologue: libc snprintf/write aliased via @link_name so a counterexample can
	// report the failing input values without touching the runtime arenas (which are
	// not initialized in the standalone test binary). Distinct Elisa names avoid
	// colliding with any snprintf the program itself declares.
	src.WriteString(propertyReportExterns)
	// Boundary-biased input generation: emit one per-integer-type draw helper that,
	// with ~50% probability, returns a structured edge-case value (0/1/2, type-MAX
	// and near-MAX, powers of two, masks, page-aligned ±1) instead of a uniform draw.
	// This closes the "sparse domain" blindness (e.g. random u64 is essentially never
	// page-aligned) while preserving the uniform component for broad coverage. The
	// helper is a PURE function of the already-advanced generator state `s`, so the
	// input sequence stays deterministic and the shrinker still converges.
	usedBiasTypes := map[string]bool{}
	for _, fn := range props {
		if fn == nil || fn.Signature == nil {
			continue
		}
		for _, p := range fn.Signature.Params {
			if t, _ := semantic.PropertyParamTypeName(p); biasedDrawTypes[t] {
				usedBiasTypes[t] = true
			}
		}
	}
	for _, t := range []string{"i8", "i16", "i32", "i64", "int", "u8", "u16", "u32", "u64"} {
		if usedBiasTypes[t] {
			src.WriteString(biasedDrawHelperSource(t))
		}
	}
	cases := make([]selectedTestCase, 0, len(props)+len(lockstepFns))

	// Emit the lockstep helpers + step-reporting drivers. Each contributes one test case
	// (the step-aware driver) that subsumes the seed-shrink + step report end to end.
	for _, fn := range lockstepFns {
		if fn == nil || fn.Signature == nil {
			continue
		}
		src.WriteString(lockstepHelperSource(fn))
		driverName := "__property_" + fn.Name
		src.WriteString(lockstepDriverSource(driverName, fn))
		cases = append(cases, selectedTestCase{Func: &semantic.AnnotatedFunc{
			Name: driverName,
			Signature: &semantic.FuncType{
				Return:         result.NamedTypes["void"],
				PermissionRefs: []ast.PermissionRef{{Name: "Abort", Member: "Panic"}},
			},
		}})
	}

	for _, fn := range props {
		if fn == nil || fn.Signature == nil {
			continue
		}
		driverName := "__property_" + fn.Name
		kind := "property"
		if differentialNames[fn.Name] {
			kind = "differential"
		}
		src.WriteString(propertyDriverSource(driverName, fn, kind))
		cases = append(cases, selectedTestCase{Func: &semantic.AnnotatedFunc{
			Name: driverName,
			// The driver calls panic() on a counterexample; surface Abort.Panic so
			// the dispatch wrapper grants it at the call site (no warning).
			Signature: &semantic.FuncType{
				Return:         result.NamedTypes["void"],
				PermissionRefs: []ast.PermissionRef{{Name: "Abort", Member: "Panic"}},
			},
		}})
	}
	return src.String(), cases
}

// appendLawFuzzHarnessSource appends the generated protocol-law @property harness functions to the
// program source (a newline-safe concatenation), so they are recompiled and analyzed alongside it.
func appendLawFuzzHarnessSource(source []byte, result *semantic.Result) []byte {
	harness := buildLawFuzzHarnesses(result)
	if harness == "" {
		return source
	}
	if len(source) != 0 && source[len(source)-1] != '\n' {
		source = append(source, '\n')
	}
	return append(source, []byte(harness)...)
}

// buildLawFuzzHarnesses synthesizes a `@property` function for each protocol-law obligation the
// analyzer could not statically prove (docs/85 P3). Each harness takes the scalar struct fields of the
// law's Self parameters, reconstructs the Self values, and returns the law predicate — so the existing
// @property machinery fuzzes the law and reports a counterexample if the impl violates it. Emitting
// these as ordinary source (recompiled with the program) means they flow through normal analysis and
// the property-driver pass with no special-casing.
func buildLawFuzzHarnesses(result *semantic.Result) string {
	if result == nil || len(result.LawFuzzObligations) == 0 {
		return ""
	}
	var b strings.Builder
	for _, ob := range result.LawFuzzObligations {
		if ob == nil {
			continue
		}
		name := lawHarnessName(ob)
		// Flatten every Self parameter's fields into distinct scalar harness parameters.
		params := make([]string, 0)
		type recon struct {
			param string
			args  []string
		}
		recons := make([]recon, 0, len(ob.Params))
		for _, p := range ob.Params {
			r := recon{param: p.Name}
			for _, f := range p.Fields {
				pn := "__law_" + p.Name + "_" + f.Name
				params = append(params, pn+": "+f.Type)
				// Brace-form field initializer: `field: value`. Default construction must
				// use `Type{...}` (paren-form positional construction is rejected).
				r.args = append(r.args, f.Name+": "+pn)
			}
			recons = append(recons, r)
		}
		fmt.Fprintf(&b, "\n@property\ndef %s(%s) -> bool:\n", name, strings.Join(params, ", "))
		for _, r := range recons {
			fmt.Fprintf(&b, "\t%s: %s = %s{%s}\n", r.param, ob.TypeName, ob.TypeName, strings.Join(r.args, ", "))
		}
		fmt.Fprintf(&b, "\treturn %s\n", ob.BodySource)
	}
	return b.String()
}

// lawFuzzAnnotatedFuncs builds the synthetic @property AnnotatedFunc for each law harness so the
// property-driver pass generates a randomized driver that invokes it. The signature mirrors the
// generated harness: one scalar (BuiltinType) parameter per Self field, bool return. Field labels are
// carried in ExplicitParamNames so a counterexample names the failing field.
func lawFuzzAnnotatedFuncs(result *semantic.Result) []*semantic.AnnotatedFunc {
	if result == nil || len(result.LawFuzzObligations) == 0 {
		return nil
	}
	out := make([]*semantic.AnnotatedFunc, 0, len(result.LawFuzzObligations))
	for _, ob := range result.LawFuzzObligations {
		if ob == nil {
			continue
		}
		var params []semantic.Type
		var labels []string
		for _, p := range ob.Params {
			for _, f := range p.Fields {
				params = append(params, &semantic.BuiltinType{Name: f.Type})
				labels = append(labels, p.Name+"."+f.Name)
			}
		}
		out = append(out, &semantic.AnnotatedFunc{
			Name:        lawHarnessName(ob),
			Annotations: []ast.Annotation{{Name: "property"}},
			Signature: &semantic.FuncType{
				Params:             params,
				ExplicitParamNames: labels,
				Return:             &semantic.BuiltinType{Name: "bool"},
			},
		})
	}
	return out
}

// lawHarnessName is the canonical `__law_<Protocol>_<law>_<Type>` harness name. Non-identifier
// characters in the (possibly-qualified) protocol/type names are sanitized to underscores so the
// generated Elisa identifier is always valid.
func lawHarnessName(ob *semantic.LawFuzzObligation) string {
	clean := func(s string) string {
		var out strings.Builder
		for _, r := range s {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
				out.WriteRune(r)
			} else {
				out.WriteByte('_')
			}
		}
		return out.String()
	}
	return "__law_" + clean(ob.Interface) + "_" + clean(ob.LawName) + "_" + clean(ob.TypeName)
}

// propertyDriverSource emits the Elisa text for one property/differential driver.
// kind ("property" or "differential") only affects the counterexample report wording.
func propertyDriverSource(driverName string, fn *semantic.AnnotatedFunc, kind string) string {
	seed := propertySeed(fn.Name)
	cases := propertyCaseCount(fn)
	var b strings.Builder
	b.WriteString("\ndef ")
	b.WriteString(driverName)
	b.WriteString("() -> void:\n")
	b.WriteString("\tcan Abort.Panic:\n")
	fmt.Fprintf(&b, "\t\t__prop_s: mutable u64 = %d\n", seed)
	// Scratch buffer + length for the counterexample report (snprintf/write).
	b.WriteString("\t\t__prop_buf: mutable u8[128] = zeroed\n")
	b.WriteString("\t\t__prop_n: mutable int = 0\n")
	fmt.Fprintf(&b, "\t\tfor __prop_i in 0..<%d:\n", cases)
	argNames := make([]string, 0, len(fn.Signature.Params))
	argTypes := make([]string, 0, len(fn.Signature.Params))
	argLabels := make([]string, 0, len(fn.Signature.Params))
	for j, p := range fn.Signature.Params {
		typeName, _ := semantic.PropertyParamTypeName(p)
		arg := fmt.Sprintf("__prop_a%d", j)
		argNames = append(argNames, arg)
		argTypes = append(argTypes, typeName)
		label := fmt.Sprintf("arg%d", j)
		if j < len(fn.Signature.ExplicitParamNames) && fn.Signature.ExplicitParamNames[j] != "" {
			label = fn.Signature.ExplicitParamNames[j]
		}
		argLabels = append(argLabels, label)
		// Advance the generator before each draw.
		b.WriteString("\t\t\t__prop_s <- __prop_s ^ (__prop_s << 13)\n")
		b.WriteString("\t\t\t__prop_s <- __prop_s ^ (__prop_s >> 7)\n")
		b.WriteString("\t\t\t__prop_s <- __prop_s ^ (__prop_s << 17)\n")
		fmt.Fprintf(&b, "\t\t\t%s: %s = %s\n", arg, typeName, propertyDrawSelect(typeName, "__prop_s"))
	}
	fmt.Fprintf(&b, "\t\t\tif not %s(%s):\n", fn.Name, strings.Join(argNames, ", "))
	// Copy the failing tuple into mutable shrink locals.
	mutNames := make([]string, len(argNames))
	for j := range argNames {
		mutNames[j] = fmt.Sprintf("__prop_m%d", j)
		fmt.Fprintf(&b, "\t\t\t\t%s: mutable %s = %s\n", mutNames[j], argTypes[j], argNames[j])
	}
	// Greedy coordinate-descent shrink: drive each argument toward zero/false while
	// the property still fails, so the reported counterexample is minimal-ish.
	fmt.Fprintf(&b, "\t\t\t\tfor __prop_r in 0..<%d:\n", propertyShrinkRounds)
	b.WriteString("\t\t\t\t\t_ = __prop_r\n")
	for j := range mutNames {
		t := argTypes[j]
		zero := propertyZeroExpr(t)
		if t == "bool" {
			fmt.Fprintf(&b, "\t\t\t\t\tif %s and not %s:\n", mutNames[j], propertyCallWith(fn.Name, mutNames, j, zero))
			fmt.Fprintf(&b, "\t\t\t\t\t\t%s <- %s\n", mutNames[j], zero)
			continue
		}
		// Prefer an outright zero; otherwise halve the magnitude toward zero.
		fmt.Fprintf(&b, "\t\t\t\t\tif not %s:\n", propertyCallWith(fn.Name, mutNames, j, zero))
		fmt.Fprintf(&b, "\t\t\t\t\t\t%s <- %s\n", mutNames[j], zero)
		half := propertyHalfExpr(t, mutNames[j])
		fmt.Fprintf(&b, "\t\t\t\t\tif %s != %s and not %s:\n", mutNames[j], half, propertyCallWith(fn.Name, mutNames, j, half))
		fmt.Fprintf(&b, "\t\t\t\t\t\t%s <- %s\n", mutNames[j], half)
	}
	// Report the (shrunk) failing case and each input value to stderr (fd 2).
	header := fmt.Sprintf(">>> %s %s counterexample (case %%lld, shrunk):\\n", kind, fn.Name)
	b.WriteString(propertyReportStmt(header, "__prop_i.i64()"))
	for j := range mutNames {
		verb, conv := propertyReportArg(argTypes[j], mutNames[j])
		line := fmt.Sprintf("      %s (%s) = %s\\n", argLabels[j], argTypes[j], verb)
		b.WriteString(propertyReportStmt(line, conv))
	}
	msg := elisacoreStringLiteral(fmt.Sprintf("%s %q failed (deterministic seed; %d cases)", kind, fn.Name, cases))
	fmt.Fprintf(&b, "\t\t\t\tpanic(%s)\n", msg)
	return b.String()
}
