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
	plainHeaders    map[string]retainedPlainHeaderContribution
	plainGlobal     string
	plainIdentities *retainedPlainIdentityLedger
	plainInputs     string
	plainAdmission  bool // invocation-local; never persisted in comparisonState

	hasProofReport               bool
	proofPrefix                  []proofContribution // immutable declaration-only journal captured before body analysis
	proofPrefixError             string
	classification               map[string]string                            // complete per-function classifier decisions
	fingerprint                  map[string]string                            // stable function key -> body-dependent results
	global                       string                                       // program-wide results (struct flags, table sizes)
	resolvedTypeNames            resolvedTypeNameContributions                // exact per-owner pre-body type-name facts
	resolvedTypeNameSourceOwners map[ast.TypeExpr]resolvedTypeNameSourceOwner // scratch-only pre-body ownership rows
	resolvedTypeNameRows         map[string]map[ast.TypeExpr]string           // scratch-only body rows to import with prepared bodies
	diags                        []Diagnostic                                 // diagnostics the phase emitted
	keyOf                        map[*ast.FuncDecl]string                     // each prepared function's fingerprint key
	prepared                     map[string]*ast.FuncDecl                     // position key -> function as prepared for body analysis
	// Per prepared function, the declaration-phase tables an added function
	// carries into the live analysis (plain values, no analyzer-owned types).
	targets     map[*ast.FuncDecl][]map[int]bool
	regionIndex map[*ast.FuncDecl]map[int]bool
}

// comparisonState retains only the declaration-phase baseline needed by the
// next update. The new scratch phase owns preparation tables while that update
// runs; keeping its prepared functions afterwards roots an unused scratch AST.
// Return a separate state so a caller-owned RetainedScratch is not modified.
func (p *preBodyState) comparisonState() *preBodyState {
	if p == nil || p.keyOf == nil && p.prepared == nil && p.targets == nil && p.regionIndex == nil && p.resolvedTypeNameSourceOwners == nil && p.resolvedTypeNameRows == nil && !p.plainAdmission {
		return p
	}
	return &preBodyState{classification: p.classification, fingerprint: p.fingerprint, global: p.global, resolvedTypeNames: p.resolvedTypeNames, diags: p.diags, plainHeaders: p.plainHeaders, plainGlobal: p.plainGlobal, plainInputs: p.plainInputs, plainIdentities: p.plainIdentities, hasProofReport: p.hasProofReport}
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
		hasProofReport: len(a.proofReport) != 0,
		classification: map[string]string{},
		fingerprint:    map[string]string{},
		keyOf:          map[*ast.FuncDecl]string{},
		prepared:       map[string]*ast.FuncDecl{},
		diags:          append([]Diagnostic(nil), a.diagnostics...),
		targets:        map[*ast.FuncDecl][]map[int]bool{},
		regionIndex:    map[*ast.FuncDecl]map[int]bool{},
	}
	functionKeys := retainedPreBodyFunctionKeys(decls)
	for _, scoped := range decls {
		for _, d := range retainedWalkDecls(&ast.File{Decls: []ast.Decl{scoped.Decl}}) {
			fn, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			key := functionKeys[fn]
			if key == "" {
				continue
			}
			p.fingerprint[key] = a.preBodyFunctionFingerprint(fn)
			p.classification[key] = a.preBodyFunctionClassification(fn)
			p.keyOf[fn] = key
			p.prepared[retainedLineKey(fn.Pos())+"|"+fn.Name] = fn
			if t, ok := a.paramStoreTargets[fn]; ok {
				p.targets[fn] = t
			}
			if idx, ok := a.regionParamCalleeParamIndex[fn.Name]; ok {
				p.regionIndex[fn] = idx
			}
		}
	}
	var resolvedTypeNameLocations map[ast.TypeExpr]resolvedTypeNameLocation
	p.resolvedTypeNames, resolvedTypeNameLocations = captureResolvedTypeNameContributions(a.resolvedTypeNames, decls, functionKeys, true)
	p.resolvedTypeNameSourceOwners = resolvedTypeNameSourceOwners(resolvedTypeNameLocations)
	p.resolvedTypeNameRows = resolvedTypeNameBodyRows(a.resolvedTypeNames, resolvedTypeNameLocations)
	if a.retained != nil {
		a.retained.resolvedTypeNameSourceOwners = p.resolvedTypeNameSourceOwners
		a.retained.resolvedTypeNameSourceOwnersValid = p.resolvedTypeNames.failure == ""
	}
	p.proofPrefix, p.proofPrefixError = a.captureProofPrefix()
	p.global = a.preBodyGlobalFingerprint()
	p.plainInputs = plainDefaultProgramInputs(a)
	p.plainHeaders, p.plainGlobal, p.plainIdentities = capturePlainHeaderContributions(a, p.keyOf)
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

// Keep classifier dependencies separate from signature checks for newly added
// functions, whose prepared bodies already come from the full scratch phase.
func (a *Analyzer) preBodyFunctionClassification(fn *ast.FuncDecl) string {
	// Named contracts are specifications, not callable values. Symbol collection
	// deliberately skips them, so no callable classifier row can exist. Their
	// full specification is independently fingerprinted and contract edits still
	// follow the existing strict declaration protocol.
	if fn != nil && fn.IsContract && a.funcDeclSymbols[fn] == nil {
		return "non-callable named contract"
	}

	if a.preBodyClassify == nil {
		return "unavailable"
	}
	sym, found := a.symbolForFuncDecl(fn)
	if !found || sym == nil {
		return "unavailable"
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok {
		return "unavailable"
	}
	decision, known := a.preBodyClassify.preparation[fn]
	if !known {
		return "unavailable"
	}
	var needs []string
	for name := range a.preBodyClassify.storeNeeds[ft] {
		needs = append(needs, name)
	}
	sort.Strings(needs)
	return fmt.Sprintf("storeNeeds=%q; preparation=%+v", needs, decision)
}

func (a *Analyzer) preBodyGlobalFingerprint() string {
	var parts []string
	// These classifier inputs are program-wide values, not table lengths.
	var backed, exports []string
	if a.preBodyClassify != nil {
		for name, enabled := range a.preBodyClassify.regionBacked {
			if enabled {
				backed = append(backed, name)
			}
		}
		for name, enabled := range a.preBodyClassify.exportTarget {
			if enabled {
				exports = append(exports, name)
			}
		}
	}
	sort.Strings(backed)
	sort.Strings(exports)
	parts = append(parts, fmt.Sprintf("classification-region-backed=%q", backed), fmt.Sprintf("classification-export-targets=%q", exports))
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
			if name == "indexBoundsRuntimeSites" {
				// Pure observational telemetry, never a declaration/codegen input.
				// Its exact lexical owner/header/origin ledger is independently
				// checked and invalidated by Update; header counts may change on
				// function removal without changing any surviving declaration.
				continue
			}
			if name == "diagnostics" || name == "reportedDiagnostics" || name == "warnOnceSeen" || retainedTypeMemoNames[name] {
				continue // memos: their sizes count lookups, not results
			}
			size := f.Len()
			if name == "exprTypes" {
				// A parameter default is typed with its function's header, which the
				// function's own fingerprint covers; editing one is not a program-wide change.
				size -= a.preBodyParamDefaultTypedExprs()
			}
			parts = append(parts, fmt.Sprintf("#%s=%d", name, size))
		}
	}
	sort.Strings(parts)
	return retainedNormalized(strings.Join(parts, "\n"))
}

// preBodyParamDefaultTypedExprs counts the exprTypes entries of nodes inside function parameter
// and struct field defaults.
func (a *Analyzer) preBodyParamDefaultTypedExprs() int {
	nodes := map[ast.Expr]bool{}
	for fn := range a.funcDeclSymbols {
		for i := range fn.Params {
			if fn.Params[i].DefaultValue != nil {
				walkStoreScanNodes([]ast.Stmt{&ast.ExprStmt{Expr: fn.Params[i].DefaultValue}}, func(node any) {
					if e, ok := node.(ast.Expr); ok {
						nodes[e] = true
					}
				})
			}
		}
	}
	// A struct field default is typed with its struct's declaration, which an edit to that
	// struct replaces as a whole.
	if a.file != nil {
		for _, sd := range a.flattenScopedDecls(a.file.Decls, "", nil) {
			if st, ok := sd.Decl.(*ast.StructDecl); ok {
				for i := range st.Fields {
					if st.Fields[i].DefaultValue != nil {
						walkStoreScanNodes([]ast.Stmt{&ast.ExprStmt{Expr: st.Fields[i].DefaultValue}}, func(node any) {
							if e, ok := node.(ast.Expr); ok {
								nodes[e] = true
							}
						})
					}
				}
			}
		}
	}
	count := 0
	for e := range nodes {
		if _, ok := a.lookupExprType(e); ok {
			count++
		}
	}
	return count
}

// preBodyDifference names the first body-dependent declaration-phase result
// that differs, or returns "". Old diagnostics are compared after the edit's
// line shifts.
// retainedGrowth is what an accepted edit adds to or removes from the declaration phase.
type retainedGrowth struct {
	addedFuncs, removedFuncs   int
	addedConsts, removedConsts int // consts and payload-less enum variants
	addedGlobals               int
	addedStructs               []string // qualified names
	removedStructs             []string // qualified names
	usings                     bool     // a wildcard `using` line was added or removed
}

func (g retainedGrowth) none() bool {
	return g.addedFuncs == 0 && g.removedFuncs == 0 && g.addedConsts == 0 && g.removedConsts == 0 && g.addedGlobals == 0 && len(g.addedStructs) == 0 && len(g.removedStructs) == 0 && !g.usings
}

// preBodyDifference reports why the edited program's declaration phase differs from the live
// one's in a way the update cannot follow. It sets *warningsChanged when the only difference
// is in its warnings, which the update then takes from cur.
func preBodyDifference(old, cur *preBodyState, marks map[string][][2]int, g retainedGrowth, replaced, reanalyzedBodies map[string]bool, checkResolvedTypeNames bool, warningsChanged *bool) string {
	added, removed := g.addedFuncs, g.removedFuncs
	if old == nil || cur == nil {
		return "declaration phase results unavailable"
	}
	cur.plainAdmission = false
	resolvedTypeNamesHandled := false
	if checkResolvedTypeNames {
		oldFunctionKeys := stringSetKeys(old.fingerprint)
		currentFunctionKeys := stringSetKeys(cur.fingerprint)
		if difference := compareResolvedTypeNameContributions(old.resolvedTypeNames, cur.resolvedTypeNames, oldFunctionKeys, currentFunctionKeys, g.addedFuncs, g.removedFuncs, replaced, reanalyzedBodies); difference != "" {
			return "resolved type-name contributions changed: " + difference
		}
		resolvedTypeNamesHandled = true
	}
	if old.global != cur.global {
		oldGlobal, currentGlobal := old.global, cur.global
		if resolvedTypeNamesHandled {
			oldGlobal = retainedWithoutResolvedTypeNameCount(oldGlobal)
			currentGlobal = retainedWithoutResolvedTypeNameCount(currentGlobal)
		}
		if oldGlobal == currentGlobal {
			// The only table-size delta is explained by the exact owner-scoped
			// certificate above.
		} else if diff := globalGrowthDifference(oldGlobal, currentGlobal, g); diff != "" {
			if residual, certified := plainHeaderResidueDifference(old, cur, g, replaced); !certified || residual != "" {
				return "program-wide declaration results changed" + diff
			}
			cur.plainAdmission = true
		}
	}
	if len(old.fingerprint)+added-removed != len(cur.fingerprint) {
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
		if !ok || replaced[key] {
			fresh++ // an added or re-declared function: checked against the live declaration
			continue
		}
		if prev != cur.fingerprint[key] {
			return "declaration results of " + key + " changed" + firstFieldDifference(prev, cur.fingerprint[key])
		}
		before, hadBefore := old.classification[key]
		after, hasAfter := cur.classification[key]
		if !hadBefore || !hasAfter || before == "unavailable" || after == "unavailable" {
			return "declaration classification of " + key + " unavailable"
		}
		if before != after {
			return "declaration classification of " + key + " changed (" + before + " -> " + after + ")"
		}
	}
	if fresh != added {
		return "function set changed"
	}
	gone := 0
	for key := range old.fingerprint {
		if _, ok := cur.fingerprint[key]; !ok || replaced[key] {
			gone++
		}
	}
	if gone != removed {
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
	if before, after := render(old.diags, true), render(cur.diags, false); before != after {
		if retainedOnlyWarningsDiffer(old.diags, cur.diags, marks) {
			*warningsChanged = true
			return ""
		}
		return "declaration phase diagnostics changed" + firstLineDifference(before, after)
	}
	return ""
}

func stringSetKeys(values map[string]string) map[string]bool {
	keys := make(map[string]bool, len(values))
	for key := range values {
		keys[key] = true
	}
	return keys
}

func retainedWithoutResolvedTypeNameCount(fingerprint string) string {
	lines := strings.Split(fingerprint, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(line, "#resolvedTypeNames=") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
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

// addedFunctionTables can gain or lose at most one entry per changed function.
// Surviving functions must still pass their individual declaration fingerprints.
var addedFunctionTables = map[string]bool{
	"funcDeclSymbols": true, "funcDeclUsings": true, "functionTypes": true,
	"paramStoreTargets": true, "ufcsFunctionsByName": true,
	"declVisibility":              true, // a function in a `public:`/`private:` section
	"regionParamCalleeParamIndex": true, // copied from the scratch phase on add
	// One true-valued entry per caller-arena function. Surviving entries are
	// checked individually by preBodyFunctionFingerprint; add/remove maintains
	// the changed function's entry alongside its declaration.
	"regionPolyReturnsCallerArena": true,
}

// addedSignatureTables hold one entry per resolved type name in a signature: an added or
// removed function moves them by however many its signature has, and a `using` edit by the
// names it newly resolves or unresolves. Unchanged functions keep identical entries, and
// declaring the added function records its own.
var addedSignatureTables = map[string]bool{"resolvedTypeNames": true}

// addedStructTables hold one entry per struct type; adding k structs grows each by at most k.
var addedStructTables = map[string]bool{"namedTypes": true, "declVisibility": true, "privateTypeNames": true}

// addedConstTables hold one entry per const (declVisibility: one in a `public:`/`private:`
// section); adding k consts grows each by at most k, removing k shrinks it by at most k.
var addedConstTables = map[string]bool{"constValues": true, "declVisibility": true}

// globalGrowthDifference accepts a global fingerprint change only when the sole
// differences are table sizes moved by the added or removed functions and added consts.
func globalGrowthDifference(old, cur string, g retainedGrowth) string {
	if g.none() {
		return firstLineDifference(old, cur)
	}
	added, removed := g.addedFuncs, g.removedFuncs
	ol, cl := strings.Split(old, "\n"), strings.Split(cur, "\n")
	// Each struct type has a line of its own: an added struct's line is new, a removed one's gone.
	without := func(lines []string, names []string) []string {
		if len(names) == 0 {
			return lines
		}
		skip := map[string]bool{}
		for _, name := range names {
			skip[name+":"] = true
		}
		kept := lines[:0:0]
		for _, l := range lines {
			if k := strings.Index(l, ":"); k >= 0 && !strings.HasPrefix(l, "#") && skip[l[:k+1]] {
				continue
			}
			kept = append(kept, l)
		}
		return kept
	}
	cl = without(cl, g.addedStructs)
	ol = without(ol, g.removedStructs)
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
		grow, shrink := 0, 0
		if addedFunctionTables[name] {
			grow, shrink = added, removed
		}
		if addedConstTables[name] {
			grow += g.addedConsts
			shrink += g.removedConsts
		}
		if addedStructTables[name] {
			grow += len(g.addedStructs)
			shrink += len(g.removedStructs)
		}
		if ok1 == 2 && ok2 == 2 && name == name2 && addedSignatureTables[name] && (added+removed != 0 || g.usings) {
			continue
		}
		if ok1 != 2 || ok2 != 2 || name != name2 || grow+shrink == 0 || after > before+grow || after < before-shrink {
			return fmt.Sprintf(" (%s -> %s)", ol[i], cl[i])
		}
	}
	return ""
}

// retainedOnlyWarningsDiffer: every diagnostic in one list and not the other is a warning.
// Errors stop analysis of what they report on, so a changed error is never followed in place.
func retainedOnlyWarningsDiffer(old, cur []Diagnostic, marks map[string][][2]int) bool {
	count := map[string]int{}
	sev := map[string]DiagnosticSeverity{}
	for _, d := range old {
		d.Pos = retainedShiftPos(d.Pos, marks)
		count[d.String()]++
		sev[d.String()] = d.Severity
	}
	for _, d := range cur {
		count[d.String()]--
		sev[d.String()] = d.Severity
	}
	for k, n := range count {
		if n != 0 && sev[k] != DiagnosticSeverityWarning {
			return false
		}
	}
	return true
}
