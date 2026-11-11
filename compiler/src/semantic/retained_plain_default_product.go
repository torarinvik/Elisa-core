package semantic

import (
	"crypto/sha256"
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

type retainedPlainDefaultProduct struct {
	Owner                *ast.FuncDecl
	Nodes                []ast.Node
	Rows                 map[string]int
	AuxiliaryRows        map[string]int
	Symbols              map[*Symbol]string
	ExprIdentities       map[ast.Expr]string
	IdentityObservations map[string]string
	GlobalDependencies   map[string]string
	Certificate          [32]byte
	HeaderParts          []plainHeaderCertificatePart
	Syntax               string
}

type plainDefaultProductEncoder struct {
	analyzer               *Analyzer
	product                *retainedPlainDefaultProduct
	anchors                map[ast.Node]string
	owned                  map[ast.Node]bool
	tokens                 map[string]string
	tokenSymbols           map[string]*Symbol
	tokenExprs             map[string]ast.Expr
	seen                   map[specializationPointer]int
	headerPositions        *[]plainHeaderPositionBinding
	positionSourceField    string
	canonicalIdentityError error
}

func captureRetainedPlainDefaultProduct(a *Analyzer, fn *ast.FuncDecl) (*retainedPlainDefaultProduct, error) {
	return captureRetainedPlainDefaultProductPhase(a, fn, false)
}

func captureRetainedPlainDefaultProductPhase(a *Analyzer, fn *ast.FuncDecl, header bool) (*retainedPlainDefaultProduct, error) {
	if a == nil || fn == nil {
		return nil, fmt.Errorf("plain default product requires analyzer and declaration")
	}
	for _, diagnostic := range a.diagnostics {
		if diagnostic.Severity == DiagnosticSeverityError {
			return nil, fmt.Errorf("error-bearing analysis is unsupported")
		}
	}
	if a.exprTypeOverlay != nil || a.exprFactsOverlay != nil || a.iterBinderElementOverlay != nil || a.noStorageOverlay != nil || a.optimizationSymbolIdentityOverlay != nil || a.storeScanBinderOverlay != nil || a.storeScanParamFactsOverlay != nil {
		return nil, fmt.Errorf("contribution capture requires canonical expression facts")
	}
	if why := a.proofReportCheckpointError(); why != "" {
		return nil, fmt.Errorf("contribution capture has active/invalid proof resource: %s", why)
	}
	if a.parallel != nil || a.currentFuncDecl != nil || a.currentIndexProofContext {
		return nil, fmt.Errorf("capture requires a completed serial/merged owner boundary")
	}
	if err := plainDefaultProductShape(a, fn); err != nil {
		return nil, err
	}
	if err := validateSparseFactSchema(); err != nil {
		return nil, err
	}
	syntax := newSpecializationSyntax()
	if header {
		syntax.prepare(reflect.ValueOf(fn.Params))
		syntax.prepare(reflect.ValueOf(fn.ReturnType))
	} else {
		syntax.prepare(reflect.ValueOf(fn))
	}
	if err := syntax.validate(); err != nil {
		return nil, err
	}
	nodes := append([]ast.Node(nil), syntax.prepared...)
	var source string
	if header {
		nodes = append([]ast.Node{fn}, nodes...)
		source = retainedHeader(fn)
	} else {
		source = unparse.FormatDecl(fn)
	}
	product := &retainedPlainDefaultProduct{Owner: fn, Nodes: nodes, Rows: map[string]int{}, AuxiliaryRows: map[string]int{}, Symbols: map[*Symbol]string{}, ExprIdentities: map[ast.Expr]string{}, IdentityObservations: map[string]string{}, GlobalDependencies: map[string]string{}, Syntax: source}
	encoder := &plainDefaultProductEncoder{analyzer: a, product: product, anchors: map[ast.Node]string{}, owned: map[ast.Node]bool{}, tokens: map[string]string{}, tokenSymbols: map[string]*Symbol{}, tokenExprs: map[string]ast.Expr{}, seen: map[specializationPointer]int{}}
	if header {
		encoder.headerPositions = new([]plainHeaderPositionBinding)
	}
	for _, node := range product.Nodes {
		if expr, ok := node.(ast.Expr); ok {
			if typ, present := a.lookupExprType(expr); (!present || typ == nil || IsInvalidType(typ)) && !header {
				return nil, fmt.Errorf("owned expression %T has no valid analyzed type", expr)
			}
		}
	}
	for i, node := range product.Nodes {
		encoder.owned[node] = true
		encoder.anchors[node] = fmt.Sprintf("owner-node:%d:%T", i, node)
	}
	// Foreign source is an explicit dependency, never deletion ownership. Globals
	// are stable name/type/value dependencies; no source-position guessing.
	for name, symbol := range a.globalScope.Symbols {
		if symbol == nil || symbol.Node == nil || symbol.Node == fn {
			continue
		}
		switch symbol.Node.(type) {
		case *ast.GlobalDecl, *ast.ConstDecl:
			foreign := newSpecializationSyntax()
			foreign.prepare(reflect.ValueOf(symbol.Node))
			if err := foreign.validate(); err != nil {
				return nil, err
			}
			for i, node := range foreign.prepared {
				if !encoder.owned[node] {
					encoder.anchors[node] = fmt.Sprintf("global:%s:%d:%T", name, i, node)
				}
			}
		}
	}
	canonicalIdentities, err := a.canonicalOptimizationSymbolIdentities()
	if err != nil {
		return nil, err
	}
	for symbol, token := range canonicalIdentities {
		identity, err := encoder.symbolIdentity(symbol)
		if err != nil {
			continue
		}
		encoder.tokens[token] = identity
		encoder.tokenSymbols[token] = symbol
	}
	// This source tranche owns only ordinary optimization symbol observations.
	// New/current semantic symbol tables are not guessed to be disposable.
	analyzerFields := reflect.ValueOf(a).Elem()
	for _, field := range analyzerStorageFields {
		table := settable(analyzerFields.FieldByIndex(field.Index))
		if table.Kind() != reflect.Map || table.Type().Key() != reflect.TypeFor[*Symbol]() || table.IsNil() {
			continue
		}
		iter := table.MapRange()
		for iter.Next() {
			symbol := iter.Key().Interface().(*Symbol)
			if symbol == nil || symbol.Node != fn {
				continue
			}
			name := field.Name
			if name != canonicalOptimizationSymbolIdentityTableName() && name != "symbolFacts" {
				return nil, fmt.Errorf("unsupported owner symbol contribution %s", name)
			}
			product.Symbols[symbol], _ = a.lookupOptimizationSymbolIdentity(symbol)
		}
	}
	for expr, token := range a.optimizationExprIDs {
		if anchor, ok := encoder.anchors[expr]; ok {
			encoder.tokens[token] = anchor
			encoder.tokenExprs[token] = expr
		}
	}
	// Fixed-point row closure is deliberately closed here: this first source
	// shape cannot create lowerings or private synthetic syntax. Any fact-held AST
	// not anchored in owned source or a registered global declines in encode().
	var certificate strings.Builder
	for _, node := range product.Nodes {
		key := reflect.ValueOf(node)
		for _, path := range specializationASTMapPathsForNode(key.Type()) {
			table := specializationField(analyzerFields, path)
			if table.IsNil() {
				continue
			}
			value := table.MapIndex(key)
			if !value.IsValid() {
				continue
			}
			name := reflect.TypeOf(*a).FieldByIndex(path).Name
			if header && (plainDefaultBodyOwnerField(name) || name == "fillMayAdoptSet") {
				continue
			}
			product.Rows[name]++
			// Declaration-produced param-store targets and the body progress
			// summary are actual facts, not ignorable auxiliary cardinality.
			// Encode their entire values under the same ownership/resource checks.
			// computeFillMayAdopt seeds only ambient growers/unknown calls and
			// closes named edges backwards. This closed scalar-return body has
			// no seed or edge; its actual syntactic classification is false.
			if name == "fillMayAdoptSet" && value.Bool() {
				return nil, fmt.Errorf("plain scalar body has non-false fill-adoption producer")
			}
			if name == "callEdgeCache" && (!value.IsNil() || value.Len() != 0) {
				return nil, fmt.Errorf("plain no-call body has nonempty call-edge producer")
			}
			semanticField := name == "fillMayAdoptSet" || name == "callEdgeCache" || name == "paramStoreTargets" || name == "progressSummaries" || name == "ambientFillAnalyzed" || name == "returnBorrowFuncAnalyzed" || name == "returnElementStmtUnknown" || name == "resolvedGlobalStorage" || name == "funcDeclSymbols" || name == "funcDeclUsings" || name == "indexBoundsRuntimeSites" || name == "indexBoundsProofOwners" || name == "indexBoundsProofHeaders" || name == "indexBoundsProofOrigins" || name == "optimizationExprIDs"
			for _, policy := range sparseSpecializationMaps {
				if policy.analyzer == name {
					semanticField = true
				}
			}
			if !semanticField {
				// Auxiliary caches are inventoried/removed, never copied or
				// accepted as replay authority by this Stage-A component.
				if err := encoder.checkAnchoredAST(value, map[specializationPointer]bool{}); err != nil {
					return nil, fmt.Errorf("auxiliary %s: %w", name, err)
				}
				product.AuxiliaryRows[name]++
				if header {
					return nil, fmt.Errorf("unsupported header auxiliary fact %s", name)
				}
				continue
			}
			encoded, err := encoder.encode(value)
			if err != nil {
				return nil, fmt.Errorf("plain default product %s/%s: %w", fn.Name, name, err)
			}
			fmt.Fprintf(&certificate, "%s:%s=%s\n", encoder.anchors[node], name, encoded)
		}
	}
	// Owner-local symbol facts are semantic observations too. Preserve their
	// complete values in the certificate, independently of expression rows.
	var ownerSymbols []*Symbol
	for symbol := range product.Symbols {
		if symbol.Node == fn {
			ownerSymbols = append(ownerSymbols, symbol)
		}
	}
	sort.Slice(ownerSymbols, func(i, j int) bool {
		if ownerSymbols[i].ParamIndex != ownerSymbols[j].ParamIndex {
			return ownerSymbols[i].ParamIndex < ownerSymbols[j].ParamIndex
		}
		return ownerSymbols[i].Name < ownerSymbols[j].Name
	})
	for _, symbol := range ownerSymbols {
		if facts, present := a.symbolFacts[symbol]; present {
			encoded, err := encoder.encode(reflect.ValueOf(facts))
			if err != nil {
				return nil, err
			}
			identity, err := encoder.symbolIdentity(symbol)
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(&certificate, "symbol-facts:%s=%s\n", identity, encoded)
		}
	}
	// The whole source and each global dependency are semantic inputs, not only
	// table cardinalities. Token counters never appear in the certificate.
	fmt.Fprintf(&certificate, "source=%q\n", product.Syntax)
	names := make([]string, 0, len(product.GlobalDependencies))
	for name := range product.GlobalDependencies {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&certificate, "dependency:%s=%s\n", name, product.GlobalDependencies[name])
	}
	if err := finalizePlainIdentityObservations(encoder); err != nil {
		return nil, err
	}
	identities := make([]string, 0, len(product.IdentityObservations))
	for key := range product.IdentityObservations {
		identities = append(identities, key)
	}
	sort.Strings(identities)
	for _, key := range identities {
		fmt.Fprintf(&certificate, "identity:%s=%s\n", key, product.IdentityObservations[key])
	}
	if header {
		parts, err := splitPlainHeaderCertificate(certificate.String(), *encoder.headerPositions)
		if err != nil {
			return nil, err
		}
		product.HeaderParts = parts
		product.Certificate, err = plainHeaderCertificateHash(parts, nil)
		if err != nil {
			return nil, err
		}
	} else {
		product.Certificate = sha256.Sum256([]byte(certificate.String()))
	}
	return product, nil
}

func plainDefaultClosedFields(value reflect.Value, allowed map[string]bool) error {
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.IsZero() || (field.Kind() == reflect.Slice || field.Kind() == reflect.Map) && field.Len() == 0 {
			continue
		}
		name := value.Type().Field(i).Name
		if !allowed[name] {
			return fmt.Errorf("unsupported nonzero %s.%s", value.Type(), name)
		}
	}
	return nil
}

// Product capture additionally requires canonical ownership. Independent parsed
// syntax can be checked structurally without pretending it belongs to that graph.
func plainDefaultProductShape(a *Analyzer, fn *ast.FuncDecl) error {
	if fn == nil {
		return fmt.Errorf("missing plain declaration")
	}
	found := false
	for _, decl := range a.file.Decls {
		if decl == fn {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("only direct top-level function owners are supported")
	}
	return plainDefaultFunctionSyntaxShape(a, fn)
}

func plainDefaultFunctionSyntaxShape(a *Analyzer, fn *ast.FuncDecl) error {
	if fn == nil {
		return fmt.Errorf("missing plain declaration")
	}
	if plainDefaultIdentityToken.MatchString(fn.Name) {
		return fmt.Errorf("source name collides with opaque identity grammar")
	}
	for _, parameter := range fn.Params {
		if plainDefaultIdentityToken.MatchString(parameter.Name) {
			return fmt.Errorf("parameter name collides with opaque identity grammar")
		}
	}
	if err := plainDefaultClosedFields(reflect.ValueOf(fn).Elem(), map[string]bool{"Position": true, "Name": true, "Params": true, "ReturnType": true, "Body": true}); err != nil {
		return err
	}
	for _, parameter := range fn.Params {
		if err := plainDefaultClosedFields(reflect.ValueOf(parameter), map[string]bool{"Position": true, "Name": true, "Type": true, "DefaultValue": true}); err != nil {
			return err
		}
	}
	if !retainedPlainFunction(fn) || len(fn.Annotations)+len(fn.RegionParams)+len(fn.PermissionParams)+len(fn.Ensures)+len(fn.Requires)+len(fn.EnsureValues)+len(fn.Changes)+len(fn.Fulfills)+len(fn.Permissions)+len(fn.Uses) != 0 || fn.Static {
		return fmt.Errorf("unsupported non-plain/default header")
	}
	scalar := func(expr ast.TypeExpr) bool {
		named, ok := expr.(*ast.NamedType)
		if !ok {
			return false
		}
		if !plainDefaultBuiltinName(named.Name) {
			return false
		}
		_, ok = a.namedTypes[named.Name].(*BuiltinType)
		return ok
	}
	if !scalar(fn.ReturnType) {
		return fmt.Errorf("unsupported nonscalar return")
	}
	parameters := map[string]bool{}
	var defaultExpr func(ast.Expr) error
	defaultExpr = func(expr ast.Expr) error {
		switch node := expr.(type) {
		case nil:
			return nil
		case *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
			return nil
		case *ast.Ident:
			symbol, ok := a.globalScope.Lookup(node.Name)
			if !ok || symbol == nil {
				return fmt.Errorf("default identifier is not an exact root global")
			}
			switch symbol.Node.(type) {
			case *ast.GlobalDecl, *ast.ConstDecl:
			default:
				return fmt.Errorf("unsupported default identifier owner")
			}
			switch typ := symbol.Type.(type) {
			case *BuiltinType:
				return nil
			case *ArrayType:
				if _, ok := typ.Elem.(*BuiltinType); ok && typ.HasConstSize && plainDefaultArrayElementSyntax(typ) {
					return nil
				}
			}
			return fmt.Errorf("unsupported default global type")
		case *ast.IndexExpr:
			if node.Fallback != nil || node.Index2 != nil || node.AsSpecialize != nil || node.AsOverlayCall != nil || node.AsBuiltinDictGet != nil {
				return fmt.Errorf("unsupported index lowering/recovery")
			}
			object, ok := node.Object.(*ast.Ident)
			if !ok {
				return fmt.Errorf("unsupported indexed object")
			}
			symbol, ok := a.globalScope.Lookup(object.Name)
			if !ok {
				return fmt.Errorf("missing indexed global")
			}
			array, ok := symbol.Type.(*ArrayType)
			if !ok || !array.HasConstSize {
				return fmt.Errorf("only fixed global array defaults are supported")
			}
			if err := defaultExpr(node.Object); err != nil {
				return err
			}
			return defaultExpr(node.Index)
		default:
			return fmt.Errorf("unsupported default syntax %T", expr)
		}
	}
	for _, parameter := range fn.Params {
		if !scalar(parameter.Type) {
			return fmt.Errorf("unsupported parameter type")
		}
		parameters[parameter.Name] = true
		if err := defaultExpr(parameter.DefaultValue); err != nil {
			return err
		}
	}
	if len(fn.Body) != 1 {
		return fmt.Errorf("only a single scalar return body is supported")
	}
	ret, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok {
		return fmt.Errorf("unsupported body statement")
	}
	switch value := ret.Value.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
	case *ast.Ident:
		if !parameters[value.Name] {
			return fmt.Errorf("only scalar parameter return is supported")
		}
	default:
		return fmt.Errorf("unsupported body expression %T", ret.Value)
	}
	return nil
}

func (encoder *plainDefaultProductEncoder) symbolIdentity(symbol *Symbol) (string, error) {
	if symbol == nil {
		return "nil", nil
	}
	if symbol.Node == encoder.product.Owner {
		if symbol.Kind != SymbolParam && symbol.Kind != SymbolFunc {
			return "", fmt.Errorf("unsupported owner symbol kind")
		}
		return fmt.Sprintf("owner-symbol:%s:%v:%d", symbol.Name, symbol.Kind, symbol.ParamIndex), nil
	}
	if encoder.analyzer.globalScope.Symbols[symbol.Name] != symbol {
		return "", fmt.Errorf("unregistered or alias symbol")
	}
	switch symbol.Node.(type) {
	case *ast.GlobalDecl, *ast.ConstDecl:
	default:
		return "", fmt.Errorf("unsupported foreign symbol")
	}
	switch typ := symbol.Type.(type) {
	case *BuiltinType:
	case *ArrayType:
		if _, ok := typ.Elem.(*BuiltinType); !ok || !typ.HasConstSize {
			return "", fmt.Errorf("unsupported global array type")
		}
	default:
		return "", fmt.Errorf("unsupported global symbol type")
	}
	return "global-symbol:" + symbol.Name, nil
}

var plainDefaultIdentityToken = regexp.MustCompile(`\b(?:symbol|expr)@[0-9]+\b`)
