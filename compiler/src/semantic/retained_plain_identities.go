package semantic

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
)

// Canonical source identities and complete associated values, not counter-name
// normalization. The raw allocation spelling never authorizes retraction: every
// active fact reference is inventoried again before removing a mapping.
type retainedPlainIdentityLedger struct {
	Entries map[string]retainedPlainIdentityEntry
}
type retainedPlainIdentityEntry struct {
	Value    string
	Owners   map[string]bool
	Residual bool
}

func plainDefaultBodyOwnerField(name string) bool {
	switch name {
	case "functionAnalyses", "progressSummaries", "ambientFillAnalyzed", "returnBorrowFuncAnalyzed", "callEdgeCache":
		return true
	}
	return false
}

func (e *plainDefaultProductEncoder) independentIdentityValue(symbol *Symbol, expr ast.Expr) (string, string, error) {
	fresh := *e
	fresh.seen = map[specializationPointer]int{}
	// This nested encoder inventories value identity but must not add observations
	// to the product while an observation map is being finalized.
	fresh.product = &retainedPlainDefaultProduct{Owner: e.product.Owner, Symbols: map[*Symbol]string{}, ExprIdentities: map[ast.Expr]string{}, GlobalDependencies: map[string]string{}, IdentityObservations: map[string]string{}}
	if symbol != nil {
		key, err := fresh.symbolIdentity(symbol)
		if err != nil {
			return "", "", err
		}
		value, err := fresh.encode(reflect.ValueOf(symbol).Elem())
		if err != nil {
			return "", "", err
		}
		if declaration, ok := symbol.Node.(ast.Decl); ok {
			value += ";source=" + unparse.FormatDecl(declaration)
		}
		return key, value, nil
	}
	anchor, known := fresh.anchors[expr]
	if !known {
		return "", "", fmt.Errorf("identity expression has no exact source anchor")
	}
	typ, present := fresh.analyzer.lookupExprType(expr)
	if !present {
		return "", "", fmt.Errorf("identity expression has no analyzed type")
	}
	value, err := fresh.encode(reflect.ValueOf(typ))
	if err != nil {
		return "", "", err
	}
	return "expression:" + anchor, "source=" + unparse.FormatExpr(expr) + ";type=" + value, nil
}

func finalizePlainIdentityObservations(e *plainDefaultProductEncoder) error {
	// Header and full owner products retain all symbol observations, including
	// flags/type/source, and every actually referenced expression identity.
	for symbol, token := range e.product.Symbols {
		if token == "" {
			continue
		}
		key, value, err := e.independentIdentityValue(symbol, nil)
		if err != nil {
			return err
		}
		if old, known := e.product.IdentityObservations[key]; known && old != value {
			return fmt.Errorf("ambiguous source identity %s", key)
		}
		e.product.IdentityObservations[key] = value
	}
	for expr := range e.product.ExprIdentities {
		key, value, err := e.independentIdentityValue(nil, expr)
		if err != nil {
			return err
		}
		if old, known := e.product.IdentityObservations[key]; known && old != value {
			return fmt.Errorf("ambiguous expression identity %s", key)
		}
		e.product.IdentityObservations[key] = value
	}
	return nil
}

// Collect every textual occurrence in complete live values, including Extent,
// alias/flow metadata and symbol facts. We do not reconstruct selected flags.
// Coordinates are not optimization identities; neither are the allocation map
// itself or RetainedAnalysis bookkeeping. All actual Analyzer/public facts are
// scanned, and AST-keyed rows are excluded only for exact scheduled owner nodes.
func plainLiveIdentityReferences(a *Analyzer, result *Result, owned map[ast.Node]bool, symbols map[*Symbol]bool) (map[string]bool, error) {
	if _, err := a.canonicalOptimizationSymbolIdentities(); err != nil {
		return nil, err
	}
	refs := map[string]bool{}
	seen := map[specializationPointer]bool{}
	seenSlices := map[specializationPointer]int{}
	allocationMaps := map[uintptr]bool{}
	for _, name := range []string{canonicalOptimizationSymbolIdentityTableName(), "optimizationExprIDs"} {
		table := settable(reflect.ValueOf(a).Elem().FieldByName(name))
		if !table.IsNil() {
			allocationMaps[table.Pointer()] = true
		}
	}
	var visit func(reflect.Value)
	visit = func(value reflect.Value) {
		if !value.IsValid() {
			return
		}
		value = settable(value)
		if value.Type() == reflect.TypeFor[lexer.Pos]() || value.Type() == reflect.TypeFor[*RetainedAnalysis]() {
			return
		}
		switch value.Kind() {
		case reflect.Interface:
			if !value.IsNil() {
				visit(value.Elem())
			}
			return
		case reflect.Pointer:
			if value.IsNil() {
				return
			}
			key := specializationPointer{value.Type(), value.Pointer()}
			if seen[key] {
				return
			}
			seen[key] = true
			visit(value.Elem())
		case reflect.Struct:
			for i := 0; i < value.NumField(); i++ {
				if value.Type() == reflect.TypeFor[ast.File]() && value.Type().Field(i).Name == "Filename" {
					continue
				}
				visit(settable(value.Field(i)))
			}
		case reflect.Map:
			if value.IsNil() || allocationMaps[value.Pointer()] {
				return
			}
			key := specializationPointer{value.Type(), value.Pointer()}
			if seen[key] {
				return
			}
			seen[key] = true
			iter := value.MapRange()
			for iter.Next() {
				key := settable(iter.Key())
				if key.CanInterface() {
					if node, ok := key.Interface().(ast.Node); ok && owned[node] {
						continue
					}
					if symbol, ok := key.Interface().(*Symbol); ok && symbols[symbol] {
						continue
					}
				}
				visit(key)
				visit(iter.Value())
			}
		case reflect.Slice:
			if value.IsNil() || value.Len() == 0 {
				return
			}
			key := specializationPointer{value.Type(), value.Pointer()}
			from := seenSlices[key]
			if from >= value.Len() {
				return
			}
			seenSlices[key] = value.Len()
			for i := from; i < value.Len(); i++ {
				visit(value.Index(i))
			}
		case reflect.Array:
			for i := 0; i < value.Len(); i++ {
				visit(value.Index(i))
			}
		case reflect.String:
			for _, token := range plainDefaultIdentityToken.FindAllString(value.String(), -1) {
				refs[token] = true
			}
		}
	}
	visit(reflect.ValueOf(a))
	if result != nil {
		visit(reflect.ValueOf(result))
	}
	return refs, nil
}

func plainValidateIdentityRegistrations(a *Analyzer) error {
	registrations := map[string]bool{}
	identities, err := a.canonicalOptimizationSymbolIdentities()
	if err != nil {
		return err
	}
	for _, token := range identities {
		if token == "" || registrations[token] {
			return fmt.Errorf("invalid or aliased symbol identity registration")
		}
		registrations[token] = true
	}
	for _, token := range a.optimizationExprIDs {
		if token == "" || registrations[token] {
			return fmt.Errorf("invalid or aliased expression identity registration")
		}
		registrations[token] = true
	}
	return nil
}

func capturePlainIdentityLedger(a *Analyzer, products map[string]*retainedPlainDefaultProduct) (*retainedPlainIdentityLedger, error) {
	if err := plainValidateIdentityRegistrations(a); err != nil {
		return nil, err
	}
	ledger := &retainedPlainIdentityLedger{Entries: map[string]retainedPlainIdentityEntry{}}
	nodes := map[ast.Node]bool{}
	symbols := map[*Symbol]bool{}
	tokens := map[string]string{}
	// Only complete observed values of exact certified owners can authorize
	// subtracting one borrowed allocation row; all other rows remain residue.
	for owner, product := range products {
		for _, node := range product.Nodes {
			nodes[node] = true
		}
		for symbol := range product.Symbols {
			if symbol.Node == product.Owner {
				symbols[symbol] = true
			}
		}
		for key, value := range product.IdentityObservations {
			if strings.HasPrefix(key, "owner-symbol:") || strings.HasPrefix(key, "expression:owner-node:") {
				key = "owner:" + owner + ":" + key
			}
			entry, known := ledger.Entries[key]
			if known && entry.Value != value {
				return nil, fmt.Errorf("identity %s has inconsistent complete values", key)
			}
			if !known {
				entry = retainedPlainIdentityEntry{Value: value, Owners: map[string]bool{}}
			}
			entry.Owners[owner] = true
			ledger.Entries[key] = entry
		}
		for symbol, token := range product.Symbols {
			if token == "" {
				continue
			}
			encoder := plainIdentityEncoderForProduct(a, product)
			key, _, err := encoder.independentIdentityValue(symbol, nil)
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(key, "owner-symbol:") || strings.HasPrefix(key, "expression:owner-node:") {
				key = "owner:" + owner + ":" + key
			}
			if old, known := tokens[token]; known && old != key {
				return nil, fmt.Errorf("identity token aliases distinct source keys")
			}
			tokens[token] = key
		}
		for expr, token := range product.ExprIdentities {
			encoder := plainIdentityEncoderForProduct(a, product)
			key, _, err := encoder.independentIdentityValue(nil, expr)
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(key, "owner-symbol:") || strings.HasPrefix(key, "expression:owner-node:") {
				key = "owner:" + owner + ":" + key
			}
			if old, known := tokens[token]; known && old != key {
				return nil, fmt.Errorf("expression token aliases distinct source keys")
			}
			tokens[token] = key
		}
	}
	residual, err := plainLiveIdentityReferences(a, nil, nodes, symbols)
	if err != nil {
		return nil, err
	}
	for token, key := range tokens {
		entry := ledger.Entries[key]
		entry.Residual = residual[token]
		ledger.Entries[key] = entry
	}
	return ledger, nil
}

func plainIdentityEncoderForProduct(a *Analyzer, product *retainedPlainDefaultProduct) *plainDefaultProductEncoder {
	// Recreate the same exact source anchors without copying any semantic rows.
	encoder := &plainDefaultProductEncoder{analyzer: a, product: product, anchors: map[ast.Node]string{}, owned: map[ast.Node]bool{}, tokens: map[string]string{}, tokenSymbols: map[string]*Symbol{}, tokenExprs: map[string]ast.Expr{}, seen: map[specializationPointer]int{}}
	for i, node := range product.Nodes {
		encoder.anchors[node] = fmt.Sprintf("owner-node:%d:%T", i, node)
		encoder.owned[node] = true
	}
	for name, symbol := range a.globalScope.Symbols {
		if symbol == nil || symbol.Node == nil {
			continue
		}
		switch symbol.Node.(type) {
		case *ast.GlobalDecl, *ast.ConstDecl:
			syntax := newSpecializationSyntax()
			syntax.prepare(reflect.ValueOf(symbol.Node))
			if syntax.validate() != nil {
				continue
			}
			for i, node := range syntax.prepared {
				if !encoder.owned[node] {
					encoder.anchors[node] = fmt.Sprintf("global:%s:%d:%T", name, i, node)
				}
			}
		}
	}
	identities, err := a.canonicalOptimizationSymbolIdentities()
	if err != nil {
		encoder.canonicalIdentityError = err
		return encoder
	}
	for symbol, token := range identities {
		if key, err := encoder.symbolIdentity(symbol); err == nil {
			encoder.tokens[token] = key
			encoder.tokenSymbols[token] = symbol
		}
	}
	for expr, token := range a.optimizationExprIDs {
		if key, known := encoder.anchors[expr]; known {
			encoder.tokens[token] = key
			encoder.tokenExprs[token] = expr
		}
	}
	return encoder
}

func (ledger *retainedPlainIdentityLedger) ownedRows() map[string]int {
	rows := map[string]int{}
	if ledger == nil {
		return rows
	}
	for key, entry := range ledger.Entries {
		if entry.Residual || len(entry.Owners) == 0 {
			continue
		}
		if strings.HasPrefix(key, "prebody-global-row:") {
			field, _, known := strings.Cut(strings.TrimPrefix(key, "prebody-global-row:"), ":")
			if known && (field == "exprTypes" || field == "exprFacts") && strings.HasPrefix(entry.Value, "present:") {
				rows[field]++
			}
			continue
		}
		if strings.HasPrefix(key, "global-symbol:") {
			rows[canonicalOptimizationSymbolIdentityTableName()]++
		}
		if strings.HasPrefix(key, "expression:global:") {
			rows["optimizationExprIDs"]++
		}
	}
	return rows
}

func (ledger *retainedPlainIdentityLedger) residualCertificate() string {
	if ledger == nil {
		return ""
	}
	var entries []string
	for key, entry := range ledger.Entries {
		if entry.Residual {
			entries = append(entries, key+"="+entry.Value)
		}
	}
	sort.Strings(entries)
	return strings.Join(entries, "\n")
}

func (r *RetainedAnalysis) prunePlainDeadIdentities(products []*retainedPlainDefaultProduct) error {
	refs, err := plainLiveIdentityReferences(r.a, r.result, nil, nil)
	if err != nil {
		return err
	}
	identities, err := r.a.canonicalOptimizationSymbolIdentities()
	if err != nil {
		return err
	}
	for _, product := range products {
		for symbol := range product.Symbols {
			if symbol == nil || symbol.Node == product.Owner {
				continue
			}
			token, present := r.a.lookupOptimizationSymbolIdentity(symbol)
			if present && !refs[token] {
				if err := r.a.deleteCanonicalOptimizationSymbolIdentity(symbol); err != nil {
					return err
				}
			}
		}
		for expr := range product.ExprIdentities {
			token, present := r.a.optimizationExprIDs[expr]
			if present && !refs[token] {
				delete(r.a.optimizationExprIDs, expr)
			}
		}
	}
	registrations := map[string]bool{}
	for _, token := range identities {
		if registrations[token] {
			return fmt.Errorf("symbol identity mapping aliases another source key")
		}
		registrations[token] = true
	}
	for _, token := range r.a.optimizationExprIDs {
		if registrations[token] {
			return fmt.Errorf("expression identity mapping aliases another source key")
		}
		registrations[token] = true
	}
	for token := range refs {
		if !registrations[token] {
			return fmt.Errorf("live fact refers to retracted identity %s", token)
		}
	}
	return nil
}

// Header typing may lazily type/fact a referenced GLOBAL initializer before the
// declaration/body pass. These are prefix observations, NOT deletion ownership:
// full declaration analysis later owns the live initializer rows independently.
// The closed source domain proves typed globals/typed literal consts do not type
// their initializer in collectValueSymbols; inferred const initializers do and
// are deliberately excluded. Complete header coverage is required by the caller.
func capturePlainGlobalHeaderRows(a *Analyzer, products map[string]*retainedPlainDefaultProduct) (map[string]retainedPlainIdentityEntry, error) {
	// The table-path inventory below validates canonical storage only. Logical
	// getters supply every captured value; an overlay's local half cannot prove
	// absence in the borrowed base, even when products happens to be empty.
	if a == nil || a.exprTypeOverlay != nil || a.exprFactsOverlay != nil || a.iterBinderElementOverlay != nil || a.noStorageOverlay != nil || a.optimizationSymbolIdentityOverlay != nil || a.storeScanBinderOverlay != nil || a.storeScanParamFactsOverlay != nil || a.parallel != nil || a.currentFuncDecl != nil || a.currentIndexProofContext {
		return nil, fmt.Errorf("global header inventory requires idle canonical fact storage")
	}
	rows := map[string]retainedPlainIdentityEntry{}
	for owner, product := range products {
		encoder := plainIdentityEncoderForProduct(a, product)
		for name := range product.GlobalDependencies {
			symbol := a.globalScope.Symbols[name]
			if symbol == nil {
				return nil, fmt.Errorf("missing global prefix dependency %s", name)
			}
			var value ast.Expr
			switch decl := symbol.Node.(type) {
			case *ast.GlobalDecl:
				value = decl.Value
			case *ast.ConstDecl:
				if decl.Type == nil {
					continue
				}
				value = decl.Value
			default:
				return nil, fmt.Errorf("unsupported global prefix producer %T", symbol.Node)
			}
			syntax := newSpecializationSyntax()
			syntax.prepare(reflect.ValueOf(value))
			if err := syntax.validate(); err != nil {
				return nil, err
			}
			for _, node := range syntax.prepared {
				expr, ok := node.(ast.Expr)
				if !ok {
					continue
				}
				anchor, known := encoder.anchors[expr]
				if !known {
					return nil, fmt.Errorf("global prefix expression has no source anchor")
				}
				// Every other global-expression table retains its original residue guard.
				// An unknown populated row cannot silently be attributed to these headers.
				key := reflect.ValueOf(expr)
				for _, path := range specializationASTMapPaths() {
					table := specializationField(reflect.ValueOf(a).Elem(), path)
					if table.IsNil() || !key.Type().AssignableTo(table.Type().Key()) || !table.MapIndex(key).IsValid() {
						continue
					}
					field := reflect.TypeOf(*a).FieldByIndex(path).Name
					if field != "exprTypes" && field != "exprFacts" && field != "optimizationExprIDs" {
						return nil, fmt.Errorf("unclassified global prefix row %s/%s", name, field)
					}
				}
				for _, field := range []string{"exprTypes", "exprFacts"} {
					encoder.seen = map[specializationPointer]int{}
					observed := "absent"
					if field == "exprTypes" {
						if typ, present := a.lookupExprType(expr); present {
							encoded, err := encoder.encode(reflect.ValueOf(typ))
							if err != nil {
								return nil, err
							}
							observed = "present:" + encoded
						}
					} else if facts, present := a.lookupExprFacts(expr); present {
						encoded, err := encoder.encode(reflect.ValueOf(facts))
						if err != nil {
							return nil, err
						}
						observed = "present:" + encoded
					}
					rowKey := "prebody-global-row:" + field + ":" + anchor
					entry, present := rows[rowKey]
					if present && entry.Value != observed {
						return nil, fmt.Errorf("global prefix row %s has inconsistent complete values", rowKey)
					}
					if !present {
						entry = retainedPlainIdentityEntry{Value: observed, Owners: map[string]bool{}}
					}
					entry.Owners[owner] = true
					rows[rowKey] = entry
				}
			}
		}
		// Any identity actually referenced by a complete row joins the ordinary
		// full-value ledger. Unobserved allocations are NOT swept into ownership.
		if err := finalizePlainIdentityObservations(encoder); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

// Retire only a fully certified last-header observation. The source declaration
// remains live: after withdrawing the exact old fact, rerun its normal scalar
// literal initializer producer, which decides whether a cold-live fact exists.
// Other global facts/types are neither swept nor guessed to be disposable.
func (r *RetainedAnalysis) preparePlainGlobalFactRetirement(pre *preBodyState, products []*retainedPlainDefaultProduct) (func() error, error) {
	if r.pre == nil || r.pre.plainIdentities == nil || pre == nil || pre.plainIdentities == nil {
		return nil, nil
	}
	wanted := map[string]retainedPlainIdentityEntry{}
	for key, entry := range r.pre.plainIdentities.Entries {
		if !strings.HasPrefix(key, "prebody-global-row:exprFacts:") || !strings.HasPrefix(entry.Value, "present:") {
			continue
		}
		if next, present := pre.plainIdentities.Entries[key]; present && strings.HasPrefix(next.Value, "present:") {
			continue
		}
		wanted[key] = entry
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	if pre.plainInputs == "" || pre.plainInputs != r.pre.plainInputs || len(pre.plainHeaders) != len(pre.keyOf) {
		return nil, fmt.Errorf("global fact retirement lacks complete header/source authority")
	}
	type target struct {
		expr     ast.Expr
		decl     ast.Decl
		encoder  *plainDefaultProductEncoder
		expected string
		rebuild  bool
	}
	targets := map[string]target{}
	encode := func(encoder *plainDefaultProductEncoder, expr ast.Expr) (string, error) {
		facts, present := r.a.lookupExprFacts(expr)
		if !present {
			return "absent", nil
		}
		encoder.seen = map[specializationPointer]int{}
		value, err := encoder.encode(reflect.ValueOf(facts))
		if err != nil {
			return "", err
		}
		return "present:" + value, nil
	}
	for _, product := range products {
		encoder := plainIdentityEncoderForProduct(r.a, product)
		for name := range product.GlobalDependencies {
			symbol := r.a.globalScope.Symbols[name]
			if symbol == nil {
				return nil, fmt.Errorf("retiring global fact lost source %s", name)
			}
			var decl ast.Decl
			var expr ast.Expr
			switch node := symbol.Node.(type) {
			case *ast.GlobalDecl:
				decl, expr = node, node.Value
			case *ast.ConstDecl:
				if node.Type != nil {
					decl, expr = node, node.Value
				}
			}
			if expr == nil {
				continue
			}
			anchor, known := encoder.anchors[expr]
			if !known {
				continue
			}
			key := "prebody-global-row:exprFacts:" + anchor
			entry, retiring := wanted[key]
			if !retiring {
				continue
			}
			// A contextual fixed-array initializer does not record a root fact:
			// analyzeValueExpr dispatches directly to analyzeListLitExprWithExpected.
			// Its root fact instead came from the header's immutable-binding lookup:
			// functionValueTypeForExpr -> analyzeExpr -> deferred fact recorder.
			// Complete current header receipts prove that the final such observer
			// is gone; plainDefaultProgramInputs excludes body/global call consumers.
			// Retract only that root, leaving all element facts and types untouched.
			// Scalar contextual producers are replayed separately below.
			rebuild := true
			switch typ := symbol.Type.(type) {
			case *BuiltinType:
				literal, integral := expr.(*ast.IntLit)
				if !integral || literal.Suffix != "" || !IsIntegralStorageType(typ) {
					return nil, fmt.Errorf("global fact %s lacks a classified contextual integer producer", name)
				}
			case *ArrayType:
				list, literal := expr.(*ast.ListLitExpr)
				_, builtin := typ.Elem.(*BuiltinType)
				if !literal || !builtin || !typ.HasConstSize || list.Brace || list.Owner != nil || len(list.Keys) != 0 || uint64(len(list.Elems)) != uint64(typ.ConstSize) {
					return nil, fmt.Errorf("global fact %s lacks a classified contextual fixed-array producer", name)
				}
				for _, spread := range list.Spreads {
					if spread {
						return nil, fmt.Errorf("global fact %s has a spread initializer", name)
					}
				}
				for _, element := range list.Elems {
					switch element.(type) {
					case *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
					default:
						return nil, fmt.Errorf("global fact %s has a nonliteral array element", name)
					}
				}
				rebuild = false
			default:
				return nil, fmt.Errorf("global fact %s has an unclassified declaration producer", name)
			}
			observed, err := encode(encoder, expr)
			if err != nil {
				return nil, err
			}
			if observed != "absent" && observed != entry.Value {
				return nil, fmt.Errorf("global fact %s gained a different full-program value", name)
			}
			if old, present := targets[key]; present && old.expr != expr {
				return nil, fmt.Errorf("global fact source identity aliases distinct syntax")
			}
			targets[key] = target{expr, decl, encoder, observed, rebuild}
		}
	}
	for key := range wanted {
		if _, known := targets[key]; !known {
			return nil, fmt.Errorf("global fact %s lacks a retiring certified owner", key)
		}
	}
	return func() error {
		var rebuild []scopedDecl
		decls := map[ast.Decl]bool{}
		// Revalidate every complete value before deleting ANY row.
		for _, item := range targets {
			observed, err := encode(item.encoder, item.expr)
			if err != nil {
				return err
			}
			if observed != item.expected {
				return fmt.Errorf("global initializer fact changed after retirement preflight")
			}
		}
		for _, item := range targets {
			if item.expected != "absent" {
				r.a.deleteExprFacts(item.expr)
				delete(r.result.ExprFacts, item.expr)
			}
			if item.rebuild {
				decls[item.decl] = true
			}
		}
		for _, scoped := range r.decls {
			if decls[scoped.Decl] {
				rebuild = append(rebuild, scoped)
				delete(decls, scoped.Decl)
			}
		}
		if len(decls) != 0 {
			return fmt.Errorf("retiring global initializer no longer has a live declaration")
		}
		errorsBefore := 0
		for _, diagnostic := range r.a.diagnostics {
			if diagnostic.Severity == DiagnosticSeverityError {
				errorsBefore++
			}
		}
		if len(rebuild) != 0 {
			r.a.analyzeDecls(rebuild)
		}
		errorsAfter := 0
		for _, diagnostic := range r.a.diagnostics {
			if diagnostic.Severity == DiagnosticSeverityError {
				errorsAfter++
			}
		}
		if errorsAfter != errorsBefore {
			return fmt.Errorf("unchanged scalar global producer emitted a new semantic error")
		}
		// Canonical Result storage may be a separate projection. Publish only the
		// producer's exact target rows, leaving every unrelated global fact intact.
		for _, item := range targets {
			if facts, present := r.a.lookupExprFacts(item.expr); present {
				if r.result.ExprFacts == nil {
					r.result.ExprFacts = map[ast.Expr]OptimizationFacts{}
				}
				r.result.ExprFacts[item.expr] = facts
			} else {
				delete(r.result.ExprFacts, item.expr)
			}
		}
		return nil
	}, nil
}

// Reopen only the certified lazy root consumed by a newly declared header.
// Normal collectValueSymbols will run its producer with the real parameter-default
// owner/context. Retaining the cached root type would skip that cold producer.
func (r *RetainedAnalysis) reopenPlainHeaderGlobalProducers(pre *preBodyState, added []scopedDecl) (func() error, error) {
	if pre == nil || pre.plainIdentities == nil || len(added) == 0 {
		return nil, nil
	}
	owners := map[string]bool{}
	for _, scoped := range added {
		fn, ok := scoped.Decl.(*ast.FuncDecl)
		if !ok || pre.keyOf[fn] == "" {
			return nil, fmt.Errorf("new header has no independent declaration key")
		}
		owners[pre.keyOf[fn]] = true
	}
	product := &retainedPlainDefaultProduct{Owner: added[0].Decl.(*ast.FuncDecl), Symbols: map[*Symbol]string{}, ExprIdentities: map[ast.Expr]string{}, GlobalDependencies: map[string]string{}}
	encoder := plainIdentityEncoderForProduct(r.a, product)
	type replay struct {
		expr     ast.Expr
		expected string
		decl     ast.Decl
	}
	var pending []replay
	for _, symbol := range r.a.globalScope.Symbols {
		if symbol == nil {
			continue
		}
		var expr ast.Expr
		var source ast.Decl
		switch decl := symbol.Node.(type) {
		case *ast.GlobalDecl:
			expr, source = decl.Value, decl
		case *ast.ConstDecl:
			if decl.Type != nil {
				expr, source = decl.Value, decl
			}
		}
		anchor, known := encoder.anchors[expr]
		if !known {
			continue
		}
		entry, wanted := pre.plainIdentities.Entries["prebody-global-row:exprFacts:"+anchor]
		if !wanted || !strings.HasPrefix(entry.Value, "present:") {
			continue
		}
		if _, present := r.a.lookupExprFacts(expr); present {
			continue
		}
		if pre.plainInputs == "" || r.pre.plainInputs != pre.plainInputs || len(pre.plainHeaders) != len(pre.keyOf) || entry.Residual || len(entry.Owners) == 0 {
			return nil, fmt.Errorf("lazy global producer lacks complete source/header authority")
		}
		for owner := range entry.Owners {
			if !owners[owner] {
				return nil, fmt.Errorf("lazy global producer has a non-new observer")
			}
		}
		array, fixed := symbol.Type.(*ArrayType)
		list, literal := expr.(*ast.ListLitExpr)
		if !fixed || !literal || !array.HasConstSize || list.Brace || list.Owner != nil || len(list.Keys) != 0 || uint64(len(list.Elems)) != uint64(array.ConstSize) {
			return nil, fmt.Errorf("uncertified lazy global producer %T", expr)
		}
		if _, builtin := array.Elem.(*BuiltinType); !builtin {
			return nil, fmt.Errorf("nonbuiltin lazy global array")
		}
		for _, spread := range list.Spreads {
			if spread {
				return nil, fmt.Errorf("spread lazy global array")
			}
		}
		for _, element := range list.Elems {
			switch element.(type) {
			case *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
			default:
				return nil, fmt.Errorf("nonliteral lazy global element")
			}
		}
		cached, typed := r.a.lookupExprType(expr)
		if !typed || !SameType(cached, symbol.Type) {
			return nil, fmt.Errorf("lazy global root type changed")
		}
		pending = append(pending, replay{expr, entry.Value, source})
	}
	// All candidates are checked before changing any cache. Each root is typed
	// again by its actual new header; no child or unrelated global row is cleared.
	for _, item := range pending {
		r.a.deleteExprType(item.expr)
	}
	return func() error {
		// Replay may allocate a new identity; rebuild source-token bindings from
		// the completed live producer before comparing complete semantic values.
		encoder = plainIdentityEncoderForProduct(r.a, product)
		for _, item := range pending {
			facts, present := r.a.lookupExprFacts(item.expr)
			if !present {
				return fmt.Errorf("new header did not replay its lazy global producer")
			}
			encoder.seen = map[specializationPointer]int{}
			encoded, err := encoder.encode(reflect.ValueOf(facts))
			if err != nil {
				return err
			}
			if "present:"+encoded != item.expected {
				return fmt.Errorf("new header global fact differs from independent capture")
			}
		}
		// Cold order is declaration/header typing first, then contextual global
		// initializer analysis. The lazy root replay above inferred literal child
		// types; run the same normal declaration producer to restore its expected
		// element types. Do not normalize semantic values or rerun other globals.
		wanted := map[ast.Decl]bool{}
		for _, item := range pending {
			wanted[item.decl] = true
		}
		var contextual []scopedDecl
		for _, scoped := range r.decls {
			if wanted[scoped.Decl] {
				contextual = append(contextual, scoped)
				delete(wanted, scoped.Decl)
			}
		}
		if len(wanted) != 0 {
			return fmt.Errorf("replayed global has no canonical declaration producer")
		}
		diagnosticsBefore := len(r.a.diagnostics)
		if len(contextual) != 0 {
			r.a.analyzeDecls(contextual)
		}
		if len(r.a.diagnostics) != diagnosticsBefore {
			return fmt.Errorf("unchanged contextual global replay emitted diagnostics")
		}
		encoder = plainIdentityEncoderForProduct(r.a, product)
		for _, item := range pending {
			facts, present := r.a.lookupExprFacts(item.expr)
			if !present {
				return fmt.Errorf("contextual producer removed certified header root fact")
			}
			encoder.seen = map[specializationPointer]int{}
			encoded, err := encoder.encode(reflect.ValueOf(facts))
			if err != nil {
				return err
			}
			if "present:"+encoded != item.expected {
				return fmt.Errorf("contextual producer changed certified header root fact")
			}
			// Result may project separate maps. Publish exactly the replayed
			// initializer subtree after both real producer phases have completed.
			syntax := newSpecializationSyntax()
			syntax.prepare(reflect.ValueOf(item.expr))
			if err := syntax.validate(); err != nil {
				return err
			}
			for _, node := range syntax.prepared {
				expr, ok := node.(ast.Expr)
				if !ok {
					continue
				}
				if typ, present := r.a.lookupExprType(expr); present {
					if r.result.ExprTypes == nil {
						r.result.ExprTypes = map[ast.Expr]Type{}
					}
					r.result.ExprTypes[expr] = typ
				} else {
					delete(r.result.ExprTypes, expr)
				}
				if facts, present := r.a.lookupExprFacts(expr); present {
					if r.result.ExprFacts == nil {
						r.result.ExprFacts = map[ast.Expr]OptimizationFacts{}
					}
					r.result.ExprFacts[expr] = facts
				} else {
					delete(r.result.ExprFacts, expr)
				}
			}
		}
		return nil
	}, nil
}
