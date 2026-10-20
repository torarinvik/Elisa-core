package semantic

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"elisacore/src/ast"
)

// The declaration phase (everything analyzeWithLookupRecorder does before
// analyzeDecls) is not only about declarations: several of its passes read or
// rewrite function bodies — classifier folding, collection-append preparation,
// region-parameter inference, store-target and region-polymorphism summaries,
// append-only checks. A retained update therefore runs that phase again on a
// scratch parse of the edited source, takes the changed bodies from it in their
// prepared form, and requires every body-dependent result of the phase to be
// unchanged; otherwise it falls back to a full analysis.

type preBodyState struct {
	fingerprint map[string]string        // stable function key -> body-dependent results
	global      string                   // program-wide results (struct flags, table sizes)
	diags       []Diagnostic             // diagnostics the phase emitted
	prepared    map[string]*ast.FuncDecl // position key -> function as prepared for body analysis
	// Per prepared function, the declaration-phase tables an added function
	// carries into the live analysis (plain values, no analyzer-owned types).
	targets     map[*ast.FuncDecl][]map[int]bool
	regionIndex map[*ast.FuncDecl]map[int]bool
}

var pendingPreBody *preBodyState

func takePendingPreBody() *preBodyState {
	p := pendingPreBody
	pendingPreBody = nil
	return p
}

func (p *preBodyState) capture(a *Analyzer, decls []scopedDecl) {
	*p = *capturePreBody(a, decls)
}

// scratchPreBody runs the declaration phase on file and returns its results.
// The caller holds pendingRetainedMu.
func scratchPreBody(file *ast.File, options AnalyzeOptions) *preBodyState {
	probe := &preBodyState{}
	pendingPreBody = probe
	AnalyzeWithOptions(file, options)
	pendingPreBody = nil
	if probe.fingerprint == nil {
		return nil
	}
	return probe
}

func capturePreBody(a *Analyzer, decls []scopedDecl) *preBodyState {
	p := &preBodyState{
		fingerprint: map[string]string{},
		prepared:    map[string]*ast.FuncDecl{},
		diags:       append([]Diagnostic(nil), a.diagnostics...),
		targets:     map[*ast.FuncDecl][]map[int]bool{},
		regionIndex: map[*ast.FuncDecl]map[int]bool{},
	}
	occurrences := map[string]int{}
	for _, scoped := range decls {
		for _, d := range retainedWalkDecls(&ast.File{Decls: []ast.Decl{scoped.Decl}}) {
			fn, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			base := scoped.Namespace + "::" + fn.Name
			key := fmt.Sprintf("%s#%d", base, occurrences[base])
			occurrences[base]++
			p.fingerprint[key] = a.preBodyFunctionFingerprint(fn)
			p.prepared[retainedLineKey(fn.Pos())+"|"+fn.Name] = fn
			if t, ok := a.paramStoreTargets[fn]; ok {
				p.targets[fn] = t
			}
			if idx, ok := a.regionParamCalleeParamIndex[fn.Name]; ok {
				p.regionIndex[fn] = idx
			}
		}
	}
	p.global = a.preBodyGlobalFingerprint()
	return p
}

func (a *Analyzer) preBodyFunctionFingerprint(fn *ast.FuncDecl) string {
	var b strings.Builder
	b.WriteString(retainedHeader(fn))
	fmt.Fprintf(&b, "\x00targets=%v", a.paramStoreTargets[fn])
	fmt.Fprintf(&b, "\x00regionIndex=%v", a.regionParamCalleeParamIndex[fn.Name])
	if sym := a.funcDeclSymbols[fn]; sym != nil {
		if ft, ok := sym.Type.(*FuncType); ok {
			fmt.Fprintf(&b, "\x00type=%s", ft.String())
			fmt.Fprintf(&b, "\x00callerArena=%v", a.regionPolyReturnsCallerArena[ft])
			// Only the roots: the value is one witness name, picked by map order.
			roots := make([]string, 0, len(a.packedStoreBuilders[ft]))
			for root := range a.packedStoreBuilders[ft] {
				roots = append(roots, root)
			}
			sort.Strings(roots)
			fmt.Fprintf(&b, "\x00packedBuilders=%v", roots)
		}
	}
	return retainedNormalized(b.String())
}

func (a *Analyzer) preBodyGlobalFingerprint() string {
	var parts []string
	for name, t := range a.namedTypes {
		if st, ok := t.(*StructType); ok {
			parts = append(parts, fmt.Sprintf("%s:%v%v%v", name, st.AppendOnly, st.Affine, st.Droppable))
		}
	}
	// Safety net for body-dependent tables this file does not name: their
	// sizes must not change either.
	v := reflect.ValueOf(a).Elem()
	for i := 0; i < v.NumField(); i++ {
		switch f := v.Field(i); f.Kind() {
		case reflect.Map, reflect.Slice:
			name := v.Type().Field(i).Name
			if name == "diagnostics" || name == "reportedDiagnostics" || name == "warnOnceSeen" {
				continue
			}
			parts = append(parts, fmt.Sprintf("#%s=%d", name, f.Len()))
		}
	}
	sort.Strings(parts)
	return retainedNormalized(strings.Join(parts, "\n"))
}

// preBodyDifference names the first body-dependent declaration-phase result
// that differs, or returns "". Old diagnostics are compared after the edit's
// line shifts.
func preBodyDifference(old, cur *preBodyState, marks map[string][][2]int, added int) string {
	if old == nil || cur == nil {
		return "declaration phase results unavailable"
	}
	if old.global != cur.global {
		if diff := globalGrowthDifference(old.global, cur.global, added); diff != "" {
			return "program-wide declaration results changed" + diff
		}
	}
	if len(old.fingerprint)+added != len(cur.fingerprint) {
		return "function set changed"
	}
	keys := make([]string, 0, len(cur.fingerprint))
	for key := range cur.fingerprint {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fresh := 0
	for _, key := range keys {
		prev, ok := old.fingerprint[key]
		if !ok {
			fresh++ // an added function: checked against the live declaration
			continue
		}
		if prev != cur.fingerprint[key] {
			return "declaration results of " + key + " changed" + firstFieldDifference(prev, cur.fingerprint[key])
		}
	}
	if fresh != added {
		return "function set changed"
	}
	render := func(ds []Diagnostic, shift bool) string {
		out := make([]string, len(ds))
		for i, d := range ds {
			if shift {
				d.Pos = retainedShiftPos(d.Pos, marks)
			}
			out[i] = d.String()
		}
		sort.Strings(out)
		return strings.Join(out, "\n")
	}
	if render(old.diags, true) != render(cur.diags, false) {
		return "declaration phase diagnostics changed"
	}
	return ""
}

func firstLineDifference(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) && i < len(bl); i++ {
		if al[i] != bl[i] {
			return fmt.Sprintf(" (%s -> %s)", al[i], bl[i])
		}
	}
	return ""
}

// firstFieldDifference shows the first fingerprint field that differs, trimmed.
func firstFieldDifference(a, b string) string {
	af, bf := strings.Split(a, "\x00"), strings.Split(b, "\x00")
	trim := func(s string) string {
		if len(s) > 160 {
			return s[:160] + "..."
		}
		return s
	}
	for i := 0; i < len(af) && i < len(bf); i++ {
		if af[i] != bf[i] {
			return fmt.Sprintf(" (%s -> %s)", trim(af[i]), trim(bf[i]))
		}
	}
	return ""
}

// addedFunctionTables are the declaration tables that hold one entry per
// function; adding k functions grows each by exactly k.
var addedFunctionTables = map[string]bool{
	"funcDeclSymbols": true, "funcDeclUsings": true, "functionTypes": true,
	"paramStoreTargets": true, "ufcsFunctionsByName": true,
}

// globalGrowthDifference accepts a global fingerprint change only when the sole
// differences are per-function table sizes grown by added.
func globalGrowthDifference(old, cur string, added int) string {
	if added == 0 {
		return firstLineDifference(old, cur)
	}
	ol, cl := strings.Split(old, "\n"), strings.Split(cur, "\n")
	if len(ol) != len(cl) {
		return firstLineDifference(old, cur)
	}
	for i := range ol {
		if ol[i] == cl[i] {
			continue
		}
		var name string
		var before, after int
		ok1, _ := fmt.Sscanf(strings.Replace(ol[i], "=", " ", 1), "#%s %d", &name, &before)
		var name2 string
		ok2, _ := fmt.Sscanf(strings.Replace(cl[i], "=", " ", 1), "#%s %d", &name2, &after)
		if ok1 != 2 || ok2 != 2 || name != name2 || !addedFunctionTables[name] || after > before+added || after < before {
			return fmt.Sprintf(" (%s -> %s)", ol[i], cl[i])
		}
	}
	return ""
}
