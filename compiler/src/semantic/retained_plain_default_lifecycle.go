package semantic

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
)

// These persistent products are immutable after capture. They belong to the
// canonical RetainedAnalysis (never an Analyzer worker or specialization).
// Header snapshots retain strings/counts only, not the scratch AST/products.
type retainedPlainHeaderContribution struct {
	Certificate     [32]byte
	Rows            map[string]int
	Parts           []plainHeaderCertificatePart
	Header          string
	SourcePositions map[string]lexer.Pos
}
type retainedPlainOwnerBaseline struct {
	Syntax      string
	Nodes       []ast.Node
	Invalidated bool
}

type retainedPlainDefaultLifecycle struct {
	Seen       map[*ast.FuncDecl]bool
	Candidates map[*ast.FuncDecl]retainedPlainOwnerBaseline
	Declines   map[*ast.FuncDecl]string
	Updating   bool
	Poisoned   bool
}

func samePlainOwnerNodes(left, right []ast.Node) bool {
	if len(left) != len(right) {
		return false
	}
	for i, node := range left {
		if node != right[i] {
			return false
		}
	}
	return true
}

func (r *RetainedAnalysis) refreshPlainDefaultProducts() {
	r.refreshPlainDefaultProductsAfterBodyCleanup(nil)
}

func (r *RetainedAnalysis) refreshPlainDefaultProductsAfterBodyCleanup(rebound map[*ast.FuncDecl]bool) {
	if r == nil || r.a == nil {
		return
	}
	prior := r.plainDefaults
	state := &retainedPlainDefaultLifecycle{Seen: map[*ast.FuncDecl]bool{}, Candidates: map[*ast.FuncDecl]retainedPlainOwnerBaseline{}, Declines: map[*ast.FuncDecl]string{}}
	for _, decl := range r.a.file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		state.Seen[fn] = true
		if err := plainDefaultProductShape(r.a, fn); err != nil {
			continue
		}
		syntax := newSpecializationSyntax()
		syntax.prepare(reflect.ValueOf(fn))
		if err := syntax.validate(); err != nil {
			state.Declines[fn] = err.Error()
			continue
		}
		baseline := retainedPlainOwnerBaseline{Syntax: unparse.FormatDecl(fn), Nodes: append([]ast.Node(nil), syntax.prepared...)}
		if prior != nil {
			if prior.Seen[fn] {
				if _, tracked := prior.Candidates[fn]; !tracked {
					baseline.Invalidated = true
					state.Declines[fn] = "previous unsupported source history requires full analysis"
				}
			}
			if old, tracked := prior.Candidates[fn]; tracked {
				if old.Invalidated || (!rebound[fn] && (old.Syntax != baseline.Syntax || !samePlainOwnerNodes(old.Nodes, baseline.Nodes))) {
					// This tranche does not claim historical body cleanup. Remember the
					// capability gap instead of forgetting old ownership during refresh.
					baseline = old
					baseline.Invalidated = true
					state.Declines[fn] = "owner source changed since fresh baseline; historical body contribution cleanup requires full analysis"
				}
			}
		}
		state.Candidates[fn] = baseline
	}
	r.plainDefaults = state
}

// This first admission tranche closes declaration-phase side effects by source
// grammar as well as fact schema. It is intentionally not a claim that arbitrary
// compiler programs have a complete function-product/residue implementation.
func plainDefaultProgramInputs(a *Analyzer) string {
	if a == nil || a.file == nil || a.exprTypeOverlay != nil || a.exprFactsOverlay != nil || a.iterBinderElementOverlay != nil || a.noStorageOverlay != nil || a.optimizationSymbolIdentityOverlay != nil || a.storeScanBinderOverlay != nil || a.storeScanParamFactsOverlay != nil || a.parallel != nil || a.currentFuncDecl != nil || a.currentIndexProofContext || a.hasLiveProofContributions() {
		return ""
	}
	var literal func(ast.Expr) bool
	literal = func(expr ast.Expr) bool {
		switch value := expr.(type) {
		case nil, *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
			return true
		case *ast.ListLitExpr:
			if value.Brace || value.Owner != nil || len(value.Keys) != 0 {
				return false
			}
			for _, spread := range value.Spreads {
				if spread {
					return false
				}
			}
			for _, item := range value.Elems {
				if !literal(item) {
					return false
				}
			}
			return true
		default:
			return false
		}
	}
	var inputs strings.Builder
	for _, decl := range a.file.Decls {
		var name string
		var value ast.Expr
		switch current := decl.(type) {
		case *ast.FuncDecl:
			if err := plainDefaultProductShape(a, current); err != nil {
				return ""
			}
			continue
		case *ast.GlobalDecl:
			name, value = current.Name, current.Value
		case *ast.ConstDecl:
			name, value = current.Name, current.Value
		default:
			return ""
		}
		if plainDefaultIdentityToken.MatchString(name) {
			return ""
		}
		if !literal(value) {
			return ""
		}
		symbol := a.globalScope.Symbols[name]
		if symbol == nil || symbol.Node != decl {
			return ""
		}
		switch typ := symbol.Type.(type) {
		case *BuiltinType:
			if !plainDefaultBuiltinName(typ.Name) {
				return ""
			}
		case *ArrayType:
			elem, ok := typ.Elem.(*BuiltinType)
			if !ok || !plainDefaultBuiltinName(elem.Name) || !typ.HasConstSize || !plainDefaultArrayElementSyntax(typ) {
				return ""
			}
		default:
			return ""
		}
		fmt.Fprintf(&inputs, "%T:%s\n", decl, unparse.FormatDecl(decl))
	}
	// Include a nonempty domain tag even when there are no globals.
	return "plain-default-program-v1\n" + inputs.String()
}

func capturePlainHeaderContributions(a *Analyzer, keys map[*ast.FuncDecl]string) (map[string]retainedPlainHeaderContribution, string, *retainedPlainIdentityLedger) {
	if plainDefaultProgramInputs(a) == "" {
		return nil, "", nil
	}
	products := map[string]retainedPlainHeaderContribution{}
	complete := map[string]*retainedPlainDefaultProduct{}
	owned := map[ast.Node]string{}
	for fn, key := range keys {
		product, err := captureRetainedPlainDefaultProductPhase(a, fn, true)
		if err != nil {
			plainDefaultCaptureDebug("header %s: %v", fn.Name, err)
			continue
		} // Unsupported owners keep their original global checks.
		for _, node := range product.Nodes {
			if prior, shared := owned[node]; shared && prior != key {
				return nil, "", nil
			}
			owned[node] = key
		}
		products[key] = retainedPlainHeaderContribution{Certificate: product.Certificate, Rows: product.Rows, Parts: product.HeaderParts, Header: product.Syntax, SourcePositions: plainHeaderSourcePositions(fn)}
		complete[key] = product
	}
	var prefixRows map[string]retainedPlainIdentityEntry
	if len(complete) == len(keys) {
		var err error
		prefixRows, err = capturePlainGlobalHeaderRows(a, complete)
		if err != nil {
			plainDefaultCaptureDebug("global prefix rows: %v", err)
			return nil, "", nil
		}
	}
	ledger, err := capturePlainIdentityLedger(a, complete)
	if err != nil {
		plainDefaultCaptureDebug("identity ledger: %v", err)
		return nil, "", nil
	}
	for key, entry := range prefixRows {
		ledger.Entries[key] = entry
	}
	return products, plainHeaderResidue(a.preBodyGlobalFingerprint(), products, ledger), ledger
}

// Subtract only inventoried AST-keyed rows of certified owners. Registrations,
// type-name rows, observational telemetry and existing exprTypes default policy
// retain their independent original guards. Borrowed identity rows are subtracted
// only through the complete certified observation ledger; every surviving
// nonowner reference and its complete associated value remain residue.
func plainHeaderResidue(global string, products map[string]retainedPlainHeaderContribution, ledger *retainedPlainIdentityLedger) string {
	rows := map[string]int{}
	for _, product := range products {
		for name, count := range product.Rows {
			if name == "exprTypes" || name == "indexBoundsRuntimeSites" || addedFunctionTables[name] || addedSignatureTables[name] {
				continue
			}
			rows[name] += count
		}
	}
	for name, count := range ledger.ownedRows() {
		rows[name] += count
	}
	lines := strings.Split(global, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(strings.TrimPrefix(line, "#"), "=")
		if !ok {
			continue
		}
		count, err := strconv.Atoi(value)
		if err != nil {
			return ""
		}
		if count < rows[name] {
			return ""
		}
		if rows[name] != 0 {
			lines[i] = fmt.Sprintf("#%s=%d", name, count-rows[name])
		}
	}
	return strings.Join(lines, "\n")
}

func plainHeaderResidueDifference(old, cur *preBodyState, g retainedGrowth, replaced map[string]bool) (string, bool) {
	if old == nil || cur == nil {
		plainDefaultCaptureDebug("residue: missing pre-body state")
		return "", false
	}
	if old.plainGlobal == "" || cur.plainGlobal == "" || old.plainInputs == "" || old.plainInputs != cur.plainInputs {
		plainDefaultCaptureDebug("residue: unavailable/domain change oldGlobal=%t newGlobal=%t oldInputs=%t sameInputs=%t oldHeaders=%d newHeaders=%d", old.plainGlobal != "", cur.plainGlobal != "", old.plainInputs != "", old.plainInputs == cur.plainInputs, len(old.plainHeaders), len(cur.plainHeaders))
		return "", false
	}
	if old.plainIdentities == nil || cur.plainIdentities == nil {
		plainDefaultCaptureDebug("residue: identity ledger missing")
		return "", false
	}
	if before, after := old.plainIdentities.residualCertificate(), cur.plainIdentities.residualCertificate(); before != after {
		plainDefaultCaptureDebug("residue: residual identity values differ: %s", plainDefaultDebugDifference(before, after))
		return "", false
	}
	for key, before := range old.plainIdentities.Entries {
		if after, observed := cur.plainIdentities.Entries[key]; observed && before.Value != after.Value {
			plainDefaultCaptureDebug("residue: identity %s value differs: %s", key, plainDefaultDebugDifference(before.Value, after.Value))
			return "", false
		}
	}
	if g.addedFuncs+g.removedFuncs == 0 || g.addedConsts+g.removedConsts+g.addedGlobals != 0 || len(g.addedStructs)+len(g.removedStructs) != 0 || g.usings {
		plainDefaultCaptureDebug("residue: unsupported declaration growth domain")
		return "", false
	}
	// Every surviving certified contribution must have identical actual analyzed
	// facts. Body/header edits combined with declaration growth conservatively
	// decline if their contribution cannot be proved unchanged by this tranche.
	for key, before := range old.plainHeaders {
		if replaced[key] {
			continue
		}
		if _, survives := cur.fingerprint[key]; !survives {
			continue
		}
		after, known := cur.plainHeaders[key]
		if !known || (before.Certificate != after.Certificate && !samePlainHeaderUnderSourceRebind(before, after)) {
			plainDefaultCaptureDebug("residue: surviving header %s differs known=%t oldRows=%v newRows=%v oldCert=%x newCert=%x", key, known, before.Rows, after.Rows, before.Certificate, after.Certificate)
			return "", false
		}
	}
	for key := range cur.plainHeaders {
		if replaced[key] {
			continue
		}
		if _, survives := old.fingerprint[key]; survives {
			if _, known := old.plainHeaders[key]; !known {
				plainDefaultCaptureDebug("residue: surviving header %s lacked old certificate", key)
				return "", false
			}
		}
	}
	difference := globalGrowthDifference(old.plainGlobal, cur.plainGlobal, g)
	if difference != "" {
		plainDefaultCaptureDebug("residue: certified global residue differs%s; %s", difference, plainDefaultDebugDifference(old.plainGlobal, cur.plainGlobal))
	}
	return difference, true
}

// Preflight the entire owner set before cleanup. Foreign caller/type/default
// aliases are deliberately unsupported until their complete contribution closure
// is wired; existing caller reanalysis alone does not make stale rows safe.
func (r *RetainedAnalysis) preflightPlainDefaultRemoval(fns []*ast.FuncDecl) ([]*retainedPlainDefaultProduct, error) {
	if r.plainDefaults == nil || r.plainDefaults.Poisoned || r.plainDefaults.Updating {
		return nil, fmt.Errorf("plain default contribution baseline unavailable")
	}
	if r.a.hasLiveProofContributions() {
		return nil, fmt.Errorf("report-bearing declaration transactions require complete report contribution rebinding")
	}
	if err := plainValidateIdentityRegistrations(r.a); err != nil {
		return nil, err
	}
	products := make([]*retainedPlainDefaultProduct, 0, len(fns))
	for _, fn := range fns {
		baseline, tracked := r.plainDefaults.Candidates[fn]
		if !tracked || baseline.Invalidated {
			return nil, fmt.Errorf("unsupported removed function %s: %s", fn.Name, r.plainDefaults.Declines[fn])
		}
		if unparse.FormatDecl(fn) != baseline.Syntax {
			return nil, fmt.Errorf("owner %s changed since baseline", fn.Name)
		}
		product, err := captureRetainedPlainDefaultProduct(r.a, fn)
		if err != nil {
			return nil, err
		}
		if !samePlainOwnerNodes(product.Nodes, baseline.Nodes) {
			return nil, fmt.Errorf("owner %s syntax identity changed since baseline", fn.Name)
		}
		if len(product.AuxiliaryRows) != 0 {
			return nil, fmt.Errorf("unsupported auxiliary cleanup closure in %s: %v", fn.Name, product.AuxiliaryRows)
		}
		if err := preflightRetainedPlainDefaultCleanup(r.a, product); err != nil {
			return nil, fmt.Errorf("removed %s: %w", fn.Name, err)
		}
		products = append(products, product)
	}
	return products, nil
}

// Require all owned raw rows to have disappeared before committing the graph.
// Public projected parity is insufficient: it intentionally ignores detached AST.
func (r *RetainedAnalysis) verifyPlainDefaultRemoval(products []*retainedPlainDefaultProduct) error {
	for _, product := range products {
		for _, node := range product.Nodes {
			key := reflect.ValueOf(node)
			for _, path := range specializationASTMapPaths() {
				table := specializationField(reflect.ValueOf(r.a).Elem(), path)
				if !table.IsNil() && key.Type().AssignableTo(table.Type().Key()) && table.MapIndex(key).IsValid() {
					return fmt.Errorf("removed %s retains raw %s row", product.Owner.Name, reflect.TypeOf(*r.a).FieldByIndex(path).Name)
				}
			}
			values := reflect.ValueOf(r.result).Elem()
			for i := 0; i < values.NumField(); i++ {
				table := values.Field(i)
				if values.Type().Field(i).IsExported() && table.Kind() == reflect.Map && table.Type().Key().Implements(astNodeType) && !table.IsNil() && key.Type().AssignableTo(table.Type().Key()) && table.MapIndex(key).IsValid() {
					return fmt.Errorf("removed %s retains public %s row", product.Owner.Name, values.Type().Field(i).Name)
				}
			}
		}
	}
	return nil
}

func (r *RetainedAnalysis) prunePlainDefaultRetainedOwners(products []*retainedPlainDefaultProduct) {
	gone := map[*ast.FuncDecl]bool{}
	types := map[*FuncType]bool{}
	for _, product := range products {
		gone[product.Owner] = true
		for symbol := range product.Symbols {
			if symbol != nil && symbol.Node == product.Owner && symbol.Kind == SymbolFunc {
				if typ, ok := symbol.Type.(*FuncType); ok {
					types[typ] = true
				}
			}
		}
	}
	// Direct owner-keyed retained records, including empty diagnostic windows,
	// must not root a removed declaration after the new baseline is committed.
	fields := reflect.ValueOf(r).Elem()
	for i := 0; i < fields.NumField(); i++ {
		table := settable(fields.Field(i))
		if table.Kind() != reflect.Map || table.IsNil() || table.Type().Key() != reflect.TypeFor[*ast.FuncDecl]() {
			continue
		}
		for fn := range gone {
			table.SetMapIndex(reflect.ValueOf(fn), reflect.Value{})
		}
	}
	for _, deps := range r.rbDeps {
		for fn := range gone {
			delete(deps, fn)
		}
	}
	for _, deps := range r.rbStaleDeps {
		for fn := range gone {
			delete(deps, fn)
		}
	}
	for fn, callers := range r.rbCallers {
		kept := make([]*ast.FuncDecl, 0, len(callers))
		for _, caller := range callers {
			if !gone[caller] {
				kept = append(kept, caller)
			}
		}
		r.rbCallers[fn] = kept
	}
	keep := func(functions []*ast.FuncDecl) []*ast.FuncDecl {
		out := make([]*ast.FuncDecl, 0, len(functions))
		for _, fn := range functions {
			if !gone[fn] {
				out = append(out, fn)
			}
		}
		return out
	}
	r.rbChanged = keep(r.rbChanged)
	// Permission maps and Analyzer memo/semantic rows keyed by this exact now-dead
	// function identity can be removed without touching borrowed global identities.
	pruneTypes := func(value reflect.Value) {
		for i := 0; i < value.NumField(); i++ {
			table := settable(value.Field(i))
			if table.Kind() != reflect.Map || table.IsNil() {
				continue
			}
			if table.Type().Key() == reflect.TypeFor[*ast.FuncDecl]() {
				for fn := range gone {
					table.SetMapIndex(reflect.ValueOf(fn), reflect.Value{})
				}
				continue
			}
			if table.Type().Key() == reflect.TypeFor[*FuncType]() {
				for typ := range types {
					table.SetMapIndex(reflect.ValueOf(typ), reflect.Value{})
				}
			} else if table.Type().Key() == reflect.TypeFor[Type]() {
				for typ := range types {
					key := reflect.ValueOf(typ)
					if key.Type().AssignableTo(table.Type().Key()) {
						table.SetMapIndex(key, reflect.Value{})
					}
				}
			}
		}
	}
	pruneTypes(reflect.ValueOf(&r.perms).Elem())
	pruneTypes(reflect.ValueOf(r.a).Elem())
	// Existing declaration transactions already require an independent fresh fast
	// baseline. Keep that invalidation explicit even if fast preparation is disabled
	// in this process; never serialize stale classifier/digest ownership for later use.
	r.invalidateFastPrepareBaseline("plain default declaration transaction requires a fresh fast baseline")
}

// Reuse the existing invocation-observer mode for capture diagnostics. This is
// deliberately not an admission escape or a new semantic/environment setting.
func plainDefaultCaptureDebug(format string, args ...any) {
	if os.Getenv("ELISACORE_ANALYSIS_CACHE_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "[plain-default] "+format+"\n", args...)
	}
}

func plainDefaultDebugDifference(before, after string) string {
	at := 0
	for at < len(before) && at < len(after) && before[at] == after[at] {
		at++
	}
	start := at - 80
	if start < 0 {
		start = 0
	}
	leftEnd, rightEnd := at+160, at+160
	if leftEnd > len(before) {
		leftEnd = len(before)
	}
	if rightEnd > len(after) {
		rightEnd = len(after)
	}
	return fmt.Sprintf("byte=%d old=%q new=%q", at, before[start:leftEnd], after[start:rightEnd])
}
