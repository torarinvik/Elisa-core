package main

import (
	"bytes"
	"elisacore/src/ast"
	"elisacore/src/backend"
	"elisacore/src/semantic"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"
)

type isolatedTestTiming struct {
	RunnerSource time.Duration
	Compile      time.Duration
	Run          time.Duration
	Native       nativeBuildTiming
	Analyze      time.Duration
	ShimWrite    time.Duration
	Total        time.Duration
}
type timingField struct {
	textKey   string
	textValue string
	jsonKey   string
	jsonValue any
}

func testTimingMode() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("ELISACORE_TEST_TIMING")))
}
func testTimingEnabled() bool {
	mode := testTimingMode()
	return mode != "" && mode != "0" && mode != "false" && mode != "off"
}
func testTimingJSONEnabled() bool {
	mode := testTimingMode()
	return mode == "json" || mode == "jsonl"
}
func testPhaseDebugEnabled() bool {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("ELISACORE_TEST_PHASE_DEBUG")))
	return mode != "" && mode != "0" && mode != "false" && mode != "off"
}
func writeTestPhaseLine(w io.Writer, phase string, detail string) {
	if w == nil || !testPhaseDebugEnabled() {
		return
	}
	phase = strings.TrimSpace(phase)
	detail = strings.TrimSpace(detail)
	if detail == "" {
		fmt.Fprintf(w, "[ phase    ] %s\n", phase)
		return
	}
	fmt.Fprintf(w, "[ phase    ] %s %s\n", phase, detail)
}
func durationTimingField(key string, value time.Duration) timingField {
	return timingField{
		textKey:   key,
		textValue: value.Round(time.Millisecond).String(),
		jsonKey:   key + "_ms",
		jsonValue: float64(value) / float64(time.Millisecond),
	}
}
func boolTimingField(key string, value bool) timingField {
	return timingField{
		textKey:   key,
		textValue: fmt.Sprintf("%t", value),
		jsonKey:   key,
		jsonValue: value,
	}
}
func writeTestTimingLine(w io.Writer, label string, fields ...timingField) {
	if w == nil || !testTimingEnabled() {
		return
	}
	if testTimingJSONEnabled() {
		event := map[string]any{"label": label}
		for _, field := range fields {
			if field.jsonKey == "" {
				continue
			}
			event[field.jsonKey] = field.jsonValue
		}
		_ = json.NewEncoder(w).Encode(event)
		return
	}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		if field.textKey == "" {
			continue
		}
		parts = append(parts, field.textKey+"="+field.textValue)
	}
	if len(parts) == 0 {
		fmt.Fprintf(w, "[ timing   ] %s\n", label)
		return
	}
	fmt.Fprintf(w, "[ timing   ] %s %s\n", label, strings.Join(parts, " "))
}
func selectedTestPermissionRefs(tests []*semantic.AnnotatedFunc) []ast.PermissionRef {
	refs := []ast.PermissionRef{{Name: "Console", Member: "Write"}}
	seen := map[string]bool{"Console.Write": true}
	for _, testFn := range tests {
		if testFn == nil || testFn.Signature == nil {
			continue
		}
		fnRefs := testFn.Signature.PermissionRefs
		if len(fnRefs) == 0 {
			for _, family := range testFn.Signature.Permissions {
				fnRefs = append(fnRefs, ast.PermissionRef{Name: family})
			}
		}
		for _, ref := range fnRefs {
			key := semantic.PermissionRefString(ref)
			if seen[key] {
				continue
			}
			seen[key] = true
			refs = append(refs, ref)
		}
	}
	return refs
}
func permissionGrantString(refs []ast.PermissionRef) string {
	if len(refs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		parts = append(parts, semantic.PermissionRefString(ref))
	}
	return strings.Join(parts, ", ")
}

type selectedTestCase struct {
	Func       *semantic.AnnotatedFunc
	SkipReason string
}

func (tc selectedTestCase) skipped() bool {
	return strings.TrimSpace(tc.SkipReason) != ""
}
func selectTestCases(result *semantic.Result, filter string) []selectedTestCase {
	selected := selectAnnotatedFunctions(result, "test", filter)
	if len(selected) == 0 {
		return nil
	}
	cases := make([]selectedTestCase, 0, len(selected))
	for _, fn := range selected {
		skipReason, _ := skipReasonForAnnotatedFunc(fn)
		cases = append(cases, selectedTestCase{Func: fn, SkipReason: skipReason})
	}
	return cases
}
func skipReasonForAnnotatedFunc(fn *semantic.AnnotatedFunc) (string, bool) {
	if fn == nil {
		return "", false
	}
	for _, annotation := range fn.Annotations {
		switch strings.ToLower(strings.TrimSpace(annotation.Name)) {
		case "skip", "ignore":
			reason := strings.TrimSpace(strings.Join(annotation.Args, ", "))
			if reason == "" {
				reason = "annotation-requested"
			}
			return reason, true
		}
	}
	return "", false
}
func matchesNameFilter(name string, filter string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	name = strings.ToLower(strings.TrimSpace(name))
	patterns := strings.Split(strings.ToLower(filter), ",")
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if strings.ContainsAny(pattern, "*?[") {
			matched, err := path.Match(pattern, name)
			if err == nil && matched {
				return true
			}
			continue
		}
		if strings.Contains(name, pattern) {
			return true
		}
	}
	return false
}
func selectAnnotatedFunctions(result *semantic.Result, annotationName string, filter string) []*semantic.AnnotatedFunc {
	if result == nil || annotationName == "" {
		return nil
	}
	selected := make([]*semantic.AnnotatedFunc, 0)
	for _, fn := range result.AnnotatedFuncs {
		if fn == nil || !hasAnnotation(fn, annotationName) {
			continue
		}
		if !matchesNameFilter(fn.Name, filter) {
			continue
		}
		selected = append(selected, fn)
	}
	return selected
}
func generateTestRunnerSource(inputFile string, result *semantic.Result, filter string) (string, error) {
	source, err := readSourceWithIncludes(inputFile, map[string]bool{})
	if err != nil {
		return "", err
	}
	source = appendLawFuzzHarnessSource(source, result)
	driverSource, propertyCases := buildPropertyDrivers(result, filter)
	if driverSource != "" {
		if len(source) != 0 && source[len(source)-1] != '\n' {
			source = append(source, '\n')
		}
		source = append(source, []byte(driverSource)...)
	}
	cases := append(selectTestCases(result, filter), propertyCases...)
	return buildTestRunnerSource(source, cases, filter), nil
}

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
	if len(props) == 0 {
		return "", nil
	}
	var src strings.Builder
	// Prologue: libc snprintf/write aliased via @link_name so a counterexample can
	// report the failing input values without touching the runtime arenas (which are
	// not initialized in the standalone test binary). Distinct Elisa names avoid
	// colliding with any snprintf the program itself declares.
	src.WriteString(propertyReportExterns)
	cases := make([]selectedTestCase, 0, len(props))
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
				r.args = append(r.args, pn)
			}
			recons = append(recons, r)
		}
		fmt.Fprintf(&b, "\n@property\ndef %s(%s) -> bool:\n", name, strings.Join(params, ", "))
		for _, r := range recons {
			fmt.Fprintf(&b, "\t%s: %s = %s(%s)\n", r.param, ob.TypeName, ob.TypeName, strings.Join(r.args, ", "))
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
		fmt.Fprintf(&b, "\t\t\t%s: %s = %s\n", arg, typeName, propertyDrawExpr(typeName, "__prop_s"))
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
func buildTestRunnerSource(source []byte, cases []selectedTestCase, filter string) string {
	runnable := runnableTests(cases)
	skipped := countSkippedTests(cases)
	permissionRefs := selectedTestPermissionRefs(runnable)
	grant := permissionGrantString(permissionRefs)
	bodyIndent := "\t"

	var out strings.Builder
	out.Write(source)
	if len(source) == 0 || source[len(source)-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	if !strings.Contains(string(source), "extern puts(") {
		out.WriteString("extern puts(text: u8&) -> int can[Console.Write]\n\n")
	}
	out.WriteString("def ctx_test_main() -> int")
	out.WriteString(semantic.PermissionRefsString(permissionRefs))
	out.WriteString(":\n")
	if grant != "" && len(cases) != 0 {
		out.WriteString("\tcan ")
		out.WriteString(grant)
		out.WriteString(":\n")
		bodyIndent = "\t\t"
	}

	if len(cases) == 0 {
		message := elisacoreStringLiteral(fmt.Sprintf("[ NO TESTS ] no @test functions matched filter %q", strings.TrimSpace(filter)))
		out.WriteString(bodyIndent)
		out.WriteString("puts(")
		out.WriteString(message)
		out.WriteString(".cast[u8&]) can Console.Write\n")
		out.WriteString(bodyIndent)
		out.WriteString("return 1\n\n")
		out.WriteString("export func main() -> int = ctx_test_main\n")
		return out.String()
	}

	for _, testCase := range cases {
		if testCase.Func == nil {
			continue
		}
		if testCase.skipped() {
			skippedLine := elisacoreStringLiteral(formatTestLine("SKIPPED", testCase.Func.Name, fmt.Sprintf(" (%s)", testCase.SkipReason)))
			out.WriteString("\tputs(")
			out.WriteString(skippedLine)
			out.WriteString(".cast[u8&])\n")
			continue
		}
		runLine := elisacoreStringLiteral(formatTestLine("RUN", testCase.Func.Name, ""))
		okLine := elisacoreStringLiteral(formatTestLine("OK", testCase.Func.Name, ""))
		out.WriteString(bodyIndent)
		out.WriteString("puts(")
		out.WriteString(runLine)
		out.WriteString(".cast[u8&])\n")
		out.WriteString(bodyIndent)
		out.WriteString(testCase.Func.Name)
		out.WriteString("()\n")
		out.WriteString(bodyIndent)
		out.WriteString("puts(")
		out.WriteString(okLine)
		out.WriteString(".cast[u8&])\n")
	}

	summaryLine := elisacoreStringLiteral(fmt.Sprintf("[ SUMMARY  ] %d test(s) selected; runnable=%d skipped=%d failed=0", len(cases), len(runnable), skipped))
	out.WriteString(bodyIndent)
	out.WriteString("puts(")
	out.WriteString(summaryLine)
	out.WriteString(".cast[u8&])\n")
	out.WriteString(bodyIndent)
	out.WriteString("return 0\n\n")
	out.WriteString("export func main() -> int = ctx_test_main\n")
	return out.String()
}
func buildIsolatedTestRunnerSource(source []byte, testCase selectedTestCase) string {
	permissionRefs := testCasePermissionRefs(testCase.Func)
	grant := permissionGrantString(permissionRefs)

	var out strings.Builder
	out.Write(source)
	if len(source) == 0 || source[len(source)-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	out.WriteString("def ctx_test_main() -> int")
	out.WriteString(semantic.PermissionRefsString(permissionRefs))
	out.WriteString(":\n")
	if testCase.Func != nil {
		out.WriteString("\t")
		out.WriteString(testCase.Func.Name)
		out.WriteString("()")
		if grant != "" {
			out.WriteString(" can ")
			out.WriteString(grant)
		}
		out.WriteString("\n")
	}
	out.WriteString("\t")
	out.WriteString("return 0\n\n")
	out.WriteString("export func main() -> int = ctx_test_main\n")
	return out.String()
}
func testCaseExportName(index int) string {
	return fmt.Sprintf("ctx_test_case_%d", index)
}
func testCaseInternalName(index int) string {
	return fmt.Sprintf("ctx_test_case_impl_%d", index)
}
func testCasePermissionRefs(testFn *semantic.AnnotatedFunc) []ast.PermissionRef {
	if testFn == nil || testFn.Signature == nil {
		return nil
	}
	if len(testFn.Signature.PermissionRefs) != 0 {
		return testFn.Signature.PermissionRefs
	}
	refs := make([]ast.PermissionRef, 0, len(testFn.Signature.Permissions))
	for _, family := range testFn.Signature.Permissions {
		refs = append(refs, ast.PermissionRef{Name: family})
	}
	return refs
}
func buildDispatchTestRunnerSource(source []byte, cases []selectedTestCase) string {
	var out strings.Builder
	out.Write(source)
	if len(source) == 0 || source[len(source)-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	for index, testCase := range cases {
		if testCase.Func == nil || testCase.skipped() {
			continue
		}
		exportName := testCaseExportName(index)
		internalName := testCaseInternalName(index)
		permissionRefs := testCasePermissionRefs(testCase.Func)
		grant := permissionGrantString(permissionRefs)
		out.WriteString("def ")
		out.WriteString(internalName)
		out.WriteString("() -> int")
		out.WriteString(semantic.PermissionRefsString(permissionRefs))
		out.WriteString(":\n")
		out.WriteString("\t")
		out.WriteString(testCase.Func.Name)
		out.WriteString("()")
		if grant != "" {
			out.WriteString(" can ")
			out.WriteString(grant)
		}
		out.WriteString("\n")
		out.WriteString("\t")
		out.WriteString("return 0\n\n")
		out.WriteString("export func ")
		out.WriteString(exportName)
		out.WriteString("() -> int = ")
		out.WriteString(internalName)
		out.WriteString("\n\n")
	}
	return out.String()
}
func testRunnerDispatchShimSource(cases []selectedTestCase) string {
	var out strings.Builder
	out.WriteString(testRunnerRuntimeShimSource())
	out.WriteString("\n#include <stdio.h>\n\nextern int strcmp(const char* lhs, const char* rhs);\n\n")
	for index, testCase := range cases {
		if testCase.Func == nil || testCase.skipped() {
			continue
		}
		out.WriteString("long long ")
		out.WriteString(testCaseExportName(index))
		out.WriteString("(void);\n")
	}
	out.WriteString("\nint main(int argc, char **argv) {\n")
	out.WriteString("    if (argc < 2) {\n")
	out.WriteString("        return 2;\n")
	out.WriteString("    }\n")
	out.WriteString("    const char *test_name = argv[1];\n")
	out.WriteString("    fprintf(stderr, \"[ ACTIVE   ] %s\\n\", test_name);\n")
	out.WriteString("    fflush(stderr);\n")
	for index, testCase := range cases {
		if testCase.Func == nil || testCase.skipped() {
			continue
		}
		out.WriteString("    if (strcmp(test_name, ")
		out.WriteString(strconv.Quote(testCase.Func.Name))
		out.WriteString(") == 0) {\n")
		out.WriteString("        return (int)")
		out.WriteString(testCaseExportName(index))
		out.WriteString("();\n")
		out.WriteString("    }\n")
	}
	out.WriteString("    return 2;\n")
	out.WriteString("}\n")
	return out.String()
}
func runnableTestCases(cases []selectedTestCase) []selectedTestCase {
	out := make([]selectedTestCase, 0, len(cases))
	for _, testCase := range cases {
		if testCase.Func == nil || testCase.skipped() {
			continue
		}
		out = append(out, testCase)
	}
	return out
}
func toCachedTestCases(cases []selectedTestCase) []cachedTestCase {
	out := make([]cachedTestCase, 0, len(cases))
	for _, tc := range cases {
		if tc.Func == nil {
			continue
		}
		out = append(out, cachedTestCase{Name: tc.Func.Name, SkipReason: tc.SkipReason})
	}
	return out
}

// runTestExecutableCases runs each non-skipped case against the (already built or
// cached) test executable and reports pass/skip/fail. Shared by the normal build
// path and the early-cache hit path, so both emit identical [ RUN ]/[ OK ]/... lines.
func runTestExecutableCases(exePath string, cases []cachedTestCase, targetTriple string, stdout io.Writer, stderr io.Writer) (passed int, skipped int, failed int, runTotal time.Duration) {
	writeTestPhaseLine(stderr, "selected_tests", "run_cases")
	for _, tc := range cases {
		if strings.TrimSpace(tc.SkipReason) != "" {
			skipped++
			fmt.Fprintln(stdout, formatTestLine("SKIPPED", tc.Name, fmt.Sprintf(" (%s)", tc.SkipReason)))
			continue
		}
		fmt.Fprintln(stdout, formatTestLine("RUN", tc.Name, ""))
		testStart := time.Now()
		var testStdout bytes.Buffer
		var testStderr bytes.Buffer
		runCmd := nativeExecCommand(exePath, targetTriple, tc.Name)
		runCmd.Stdout = &testStdout
		runCmd.Stderr = &testStderr
		runStart := time.Now()
		runErr := runCmd.Run()
		runDur := time.Since(runStart)
		runTotal += runDur
		writeTestTimingLine(stderr, tc.Name,
			durationTimingField("total", time.Since(testStart)),
			durationTimingField("run", runDur),
		)
		if runErr == nil {
			passed++
			fmt.Fprintln(stdout, formatTestLine("OK", tc.Name, ""))
			continue
		}
		failed++
		status, detail := classifyTestExecutionError(runErr)
		fmt.Fprintln(stdout, formatTestLine(status, tc.Name, detail))
		writeCapturedTestOutput(stdout, tc.Name, testStdout.String(), testStderr.String())
	}
	return passed, skipped, failed, runTotal
}

func executeSelectedTests(inputFile string, result *semantic.Result, filter string, foreignFiles []string, linkFlags []string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, targetTriple string, debugInfo bool, traceInfo bool, stdout io.Writer, stderr io.Writer) int {
	suiteStart := time.Now()
	writeTestPhaseLine(stderr, "selected_tests", "read_source")
	source, err := readSourceWithIncludes(inputFile, map[string]bool{})
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	writeTestPhaseLine(stderr, "selected_tests", "select_cases")
	source = appendLawFuzzHarnessSource(source, result)
	testCases := selectTestCases(result, filter)
	driverSource, propertyCases := buildPropertyDrivers(result, filter)
	if driverSource != "" {
		if len(source) != 0 && source[len(source)-1] != '\n' {
			source = append(source, '\n')
		}
		source = append(source, []byte(driverSource)...)
		testCases = append(testCases, propertyCases...)
	}
	if len(testCases) == 0 {
		fmt.Fprintf(stdout, "[ NO TESTS ] no @test functions matched filter %q\n", strings.TrimSpace(filter))
		return 1
	}
	runnableCases := runnableTestCases(testCases)

	clangPath, err := exec.LookPath("clang")
	if err != nil {
		fmt.Fprintf(stderr, "error: clang is required to execute tests: %s\n", err)
		return 1
	}

	passed := 0
	skipped := 0
	failed := 0
	var compileTotal time.Duration
	var runTotal time.Duration
	var analyzeTotal time.Duration
	var nativeObjectTotal time.Duration
	var nativeHeaderGenTotal time.Duration
	var nativeHeaderWriteTotal time.Duration
	var nativeLinkTotal time.Duration
	var cacheLookupTotal time.Duration
	var cachePublishTotal time.Duration
	cacheHit := false
	compileStarted := false
	var exePath string
	cleanup := func() {}
	if len(runnableCases) > 0 {
		compileStarted = true
		writeTestPhaseLine(stderr, "selected_tests", "compile_dispatch")
		runnerSourceStart := time.Now()
		runnerSource := buildDispatchTestRunnerSource(source, runnableCases)
		runnerSourceElapsed := time.Since(runnerSourceStart)
		compileStart := time.Now()
		dispatchShim := testRunnerDispatchShimSource(runnableCases)
		var nativeTiming nativeBuildTiming
		var analyzeTime time.Duration
		var shimWriteTime time.Duration
		exePath, cleanup, nativeTiming, analyzeTime, shimWriteTime, err = compileTestRunnerExecutableWithShim(clangPath, runnerSource, dispatchShim, result.EASMModules, foreignFiles, linkFlags, optLevel, packedProfile, targetTriple, debugInfo, traceInfo, stderr)
		compileTotal = time.Since(compileStart)
		analyzeTotal = analyzeTime
		nativeObjectTotal = nativeTiming.ObjectWrite
		nativeHeaderGenTotal = nativeTiming.HeaderGen
		nativeHeaderWriteTotal = nativeTiming.HeaderWrite
		nativeLinkTotal = nativeTiming.Link
		cacheLookupTotal = nativeTiming.CacheLookup
		cachePublishTotal = nativeTiming.CachePublish
		cacheHit = nativeTiming.CacheHit
		if err != nil {
			cleanup()
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		// ELISA_KEEP_TEST_BINARY keeps the built test binary (and its .dSYM) instead of
		// deleting it, and prints the path -- so it can be debugged under lldb (e.g. to
		// inspect a fault that the test harness otherwise just reports as a panic).
		if os.Getenv("ELISA_KEEP_TEST_BINARY") != "" {
			fmt.Fprintf(stderr, "[ keep     ] test binary: %s\n", exePath)
			cleanup = func() {}
		}
		writeTestTimingLine(stderr, "compile",
			durationTimingField("build_runner", runnerSourceElapsed),
			durationTimingField("compile", compileTotal),
			durationTimingField("analyze", analyzeTime),
			durationTimingField("shim", shimWriteTime),
			durationTimingField("obj", nativeTiming.ObjectWrite),
			durationTimingField("header_gen", nativeTiming.HeaderGen),
			durationTimingField("header_write", nativeTiming.HeaderWrite),
			durationTimingField("link", nativeTiming.Link),
			durationTimingField("cache_lookup", nativeTiming.CacheLookup),
			durationTimingField("cache_publish", nativeTiming.CachePublish),
			boolTimingField("cache_hit", nativeTiming.CacheHit),
		)
		defer cleanup()
	}
	cachedCases := toCachedTestCases(testCases)
	// Publish the early (source-level) cache so the next unchanged run skips the
	// whole front-end (parse+analyze) and jumps straight to running this binary.
	if exePath != "" {
		if key, keyErr := earlyTestCacheKey(source, filter, result.EASMModules, foreignFiles, linkFlags, optLevel, packedProfile, targetTriple, debugInfo, traceInfo); keyErr == nil {
			publishEarlyTestCache(key, exePath, cachedCases)
		}
	}
	passed, skipped, failed, runTotal = runTestExecutableCases(exePath, cachedCases, targetTriple, stdout, stderr)
	fmt.Fprintf(stdout, "[ SUMMARY  ] %d test(s) selected; passed=%d skipped=%d failed=%d\n", len(testCases), passed, skipped, failed)
	writeTestTimingLine(stderr, "suite",
		durationTimingField("total", time.Since(suiteStart)),
		durationTimingField("compile", compileTotal),
		durationTimingField("run", runTotal),
		durationTimingField("analyze", analyzeTotal),
		durationTimingField("obj", nativeObjectTotal),
		durationTimingField("header_gen", nativeHeaderGenTotal),
		durationTimingField("header_write", nativeHeaderWriteTotal),
		durationTimingField("link", nativeLinkTotal),
		durationTimingField("cache_lookup", cacheLookupTotal),
		durationTimingField("cache_publish", cachePublishTotal),
		boolTimingField("cache_hit", cacheHit),
		boolTimingField("compiled", compileStarted),
	)
	if failed > 0 {
		return 1
	}
	return 0
}
func runnableTests(cases []selectedTestCase) []*semantic.AnnotatedFunc {
	runnable := make([]*semantic.AnnotatedFunc, 0, len(cases))
	for _, testCase := range cases {
		if testCase.Func == nil || testCase.skipped() {
			continue
		}
		runnable = append(runnable, testCase.Func)
	}
	return runnable
}
