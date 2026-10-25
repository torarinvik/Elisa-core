package semantic

// Restricted plain-function contributions. Header residue and complete idle
// cleanup are separate phases; unsupported closures retain full-analysis fallback.
import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
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
	source := unparse.FormatDecl(fn)
	if header {
		nodes = append([]ast.Node{fn}, nodes...)
		source = retainedHeader(fn)
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
	for i := 0; i < analyzerFields.NumField(); i++ {
		table := settable(analyzerFields.Field(i))
		if table.Kind() != reflect.Map || table.Type().Key() != reflect.TypeFor[*Symbol]() || table.IsNil() {
			continue
		}
		iter := table.MapRange()
		for iter.Next() {
			symbol := iter.Key().Interface().(*Symbol)
			if symbol == nil || symbol.Node != fn {
				continue
			}
			name := analyzerFields.Type().Field(i).Name
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
		for _, path := range specializationASTMapPaths() {
			table := specializationField(reflect.ValueOf(a).Elem(), path)
			if table.IsNil() || !key.Type().AssignableTo(table.Type().Key()) {
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
		name := value.Type().Field(i).Name
		if allowed[name] {
			continue
		}
		if (field.Kind() == reflect.Slice || field.Kind() == reflect.Map) && field.Len() == 0 {
			continue
		}
		if !field.IsZero() {
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

func (encoder *plainDefaultProductEncoder) encode(value reflect.Value) (string, error) {
	if encoder.canonicalIdentityError != nil {
		return "", encoder.canonicalIdentityError
	}
	if !value.IsValid() {
		return "invalid", nil
	}
	value = settable(value)
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return "nil-interface", nil
		}
		return encoder.encode(value.Elem())
	}
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return "nil:" + value.Type().String(), nil
	}
	if value.CanInterface() {
		if node, ok := value.Interface().(ast.Node); ok {
			if anchor, present := encoder.anchors[node]; present {
				return anchor, nil
			}
			return "", fmt.Errorf("unowned or synthetic AST %T", node)
		}
		if symbol, ok := value.Interface().(*Symbol); ok {
			identity, err := encoder.symbolIdentity(symbol)
			if err != nil {
				return "", err
			}
			encoder.product.Symbols[symbol], _ = encoder.analyzer.lookupOptimizationSymbolIdentity(symbol)
			encoder.rememberGlobalDependency(symbol)
			fields, err := encoder.encode(reflect.ValueOf(symbol).Elem())
			if err != nil {
				return "", err
			}
			return identity + ":" + fields, nil
		}
		if site, ok := value.Interface().(indexBoundsRuntimeSite); ok {
			owner, err := encoder.encode(reflect.ValueOf(site.Owner))
			if err != nil {
				return "", err
			}
			origin, err := encoder.encode(reflect.ValueOf(site.Origin))
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("runtime-site:%s:%v:%s", owner, site.Header, origin), nil
		}
		if _, ok := value.Interface().(*Scope); ok {
			return "", fmt.Errorf("scope ownership unsupported")
		}
		if typ, ok := value.Interface().(Type); ok {
			switch typ := typ.(type) {
			case *BuiltinType:
				return "builtin:" + typ.Name, nil
			case *ArrayType:
				if _, ok := typ.Elem.(*BuiltinType); !ok || !typ.HasConstSize {
					return "", fmt.Errorf("unsupported array type")
				}
			case *FuncType:
				if typ.Name != encoder.product.Owner.Name || len(typ.TypeParams)+len(typ.GenericParams)+len(typ.RegionParams)+len(typ.PermissionParams) != 0 {
					return "", fmt.Errorf("unsupported callable type")
				}
			default:
				return "", fmt.Errorf("unsupported semantic type %T", typ)
			}
		}
	}
	if value.Type() == reflect.TypeFor[lexer.Pos]() {
		pos := value.Interface().(lexer.Pos)
		if encoder.headerPositions != nil && encoder.positionSourceField != "" {
			field := encoder.positionSourceField
			expected, known := plainHeaderSourcePositions(encoder.product.Owner)[field]
			if !known || expected != pos {
				return "", fmt.Errorf("header position lacks exact inventoried producer")
			}
			index := len(*encoder.headerPositions)
			*encoder.headerPositions = append(*encoder.headerPositions, plainHeaderPositionBinding{field, pos})
			return fmt.Sprintf("\x00position:%d\x00", index), nil
		}
		if encoder.headerPositions != nil && !pos.IsZero() {
			return "", fmt.Errorf("uninventoried header position producer")
		}
		return fmt.Sprintf("pos:%#v", pos), nil
	}
	if err := validateSparseFactValueType(value); err != nil {
		return "", err
	}
	switch value.Kind() {
	case reflect.Pointer:
		key := specializationPointer{value.Type(), value.Pointer()}
		if ordinal, seen := encoder.seen[key]; seen {
			return fmt.Sprintf("reference:%d", ordinal), nil
		}
		encoder.seen[key] = len(encoder.seen)
		nested, err := encoder.encode(value.Elem())
		return "pointer:" + value.Type().String() + ":" + nested, err
	case reflect.Struct:
		var out strings.Builder
		fmt.Fprintf(&out, "%s{", value.Type())
		for i := 0; i < value.NumField(); i++ {
			prior := encoder.positionSourceField
			encoder.positionSourceField = ""
			if encoder.headerPositions != nil {
				field, err := encoder.headerPositionField(value, value.Type().Field(i).Name)
				if err != nil {
					encoder.positionSourceField = prior
					return "", err
				}
				encoder.positionSourceField = field
			}
			item, err := encoder.encode(settable(value.Field(i)))
			encoder.positionSourceField = prior
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&out, "%s=%s;", value.Type().Field(i).Name, item)
		}
		out.WriteByte('}')
		return out.String(), nil
	case reflect.Slice, reflect.Array:
		var out strings.Builder
		fmt.Fprintf(&out, "%s[%d:", value.Type(), value.Len())
		for i := 0; i < value.Len(); i++ {
			item, err := encoder.encode(value.Index(i))
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&out, "%s;", item)
		}
		out.WriteByte(']')
		return out.String(), nil
	case reflect.Map:
		if value.IsNil() {
			return "nil-map:" + value.Type().String(), nil
		}
		identity := specializationPointer{value.Type(), value.Pointer()}
		if ordinal, seen := encoder.seen[identity]; seen {
			return fmt.Sprintf("reference:%d", ordinal), nil
		}
		encoder.seen[identity] = len(encoder.seen)
		// Restrict map keys to scalar values or anchored AST. Pointer-order map
		// iteration must never assign canonical graph ordinals.
		keys := value.MapKeys()
		ordered := make([]string, len(keys))
		byKey := map[string]reflect.Value{}
		for i, key := range keys {
			if key.Kind() != reflect.String && key.Kind() != reflect.Int && key.Kind() != reflect.Uint && !key.Type().Implements(astNodeType) {
				return "", fmt.Errorf("unsupported map key %s", key.Type())
			}
			item, err := encoder.encode(key)
			if err != nil {
				return "", err
			}
			if _, exists := byKey[item]; exists {
				return "", fmt.Errorf("ambiguous canonical map key")
			}
			ordered[i] = item
			byKey[item] = key
		}
		sort.Strings(ordered)
		var out strings.Builder
		fmt.Fprintf(&out, "%s{", value.Type())
		for _, key := range ordered {
			item, err := encoder.encode(value.MapIndex(byKey[key]))
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&out, "%s=%s;", key, item)
		}
		out.WriteByte('}')
		return out.String(), nil
	case reflect.String:
		text := value.String()
		var unknown string
		text = plainDefaultIdentityToken.ReplaceAllStringFunc(text, func(token string) string {
			stable, ok := encoder.tokens[token]
			if !ok {
				unknown = token
				return token
			}
			if symbol := encoder.tokenSymbols[token]; symbol != nil {
				encoder.product.Symbols[symbol] = token
				encoder.rememberGlobalDependency(symbol)
			}
			if expr := encoder.tokenExprs[token]; expr != nil {
				encoder.product.ExprIdentities[expr] = token
			}
			return "[identity:" + stable + "]"
		})
		if unknown != "" {
			return "", fmt.Errorf("unbound optimization identity %s", unknown)
		}
		return strconv.Quote(text), nil
	case reflect.Bool:
		return strconv.FormatBool(value.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%s:%d", value.Type(), value.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("%s:%d", value.Type(), value.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return fmt.Sprintf("%s:%g", value.Type(), value.Float()), nil
	default:
		return "", fmt.Errorf("unsupported fact value %s", value.Type())
	}
}

// Rebinding validates a newly analyzed destination contribution. It never
// copies stale source fact pointers into another Analyzer. Changed signatures
// require a fresh capture/new declaration transaction, not this replay API.
func rebindRetainedPlainDefaultProduct(prior *retainedPlainDefaultProduct, a *Analyzer, fn *ast.FuncDecl) (*retainedPlainDefaultProduct, error) {
	if prior == nil {
		return nil, fmt.Errorf("missing prior product")
	}
	current, err := captureRetainedPlainDefaultProduct(a, fn)
	if err != nil {
		return nil, err
	}
	if current.Syntax != prior.Syntax || current.Certificate != prior.Certificate {
		return nil, fmt.Errorf("default/function contribution changed; fresh declaration required")
	}
	return current, nil
}

// Preflight is conservative about shared ownership. Shared global symbols and
// their identity tokens are borrowed and preserved. A live foreign source/fact
// referring to owner-local syntax/symbol/token declines rather than being
// silently deleted or rebound. This component does not unregister declarations.
func preflightRetainedPlainDefaultCleanup(a *Analyzer, product *retainedPlainDefaultProduct) error {
	if a == nil || product == nil || product.Owner == nil {
		return fmt.Errorf("missing cleanup owner")
	}
	current, err := captureRetainedPlainDefaultProduct(a, product.Owner)
	if err != nil {
		return err
	}
	if current.Certificate != product.Certificate || len(current.Nodes) != len(product.Nodes) {
		return fmt.Errorf("product facts changed after capture")
	}
	for i, node := range current.Nodes {
		if node != product.Nodes[i] {
			return fmt.Errorf("product ownership changed after capture")
		}
	}
	if unparse.FormatDecl(product.Owner) != product.Syntax {
		return fmt.Errorf("product is stale after an untracked syntax edit")
	}
	for name, expected := range product.GlobalDependencies {
		symbol, ok := a.globalScope.Lookup(name)
		if !ok || symbol == nil {
			return fmt.Errorf("global dependency %s removed", name)
		}
		source, ok := symbol.Node.(ast.Decl)
		if !ok {
			return fmt.Errorf("global dependency %s has no declaration", name)
		}
		observed := fmt.Sprintf("kind=%v mutable=%v type=%s source=%s", symbol.Kind, symbol.Mutable, symbol.Type.String(), unparse.FormatDecl(source))
		if observed != expected {
			return fmt.Errorf("global dependency %s changed", name)
		}
	}
	var ownerType *FuncType
	if symbol := a.funcDeclSymbols[product.Owner]; symbol != nil {
		ownerType, _ = symbol.Type.(*FuncType)
	}
	if ownerType == nil {
		return fmt.Errorf("owner has no canonical function type")
	}
	owned := map[ast.Node]bool{}
	for _, node := range product.Nodes {
		owned[node] = true
	}
	localSymbols := map[*Symbol]bool{}
	localTokens := map[string]bool{}
	for symbol, token := range product.Symbols {
		if symbol.Node == product.Owner {
			localSymbols[symbol] = true
			if token != "" {
				localTokens[token] = true
			}
		}
	}
	var mentions func(reflect.Value, map[specializationPointer]bool) bool
	mentions = func(value reflect.Value, seen map[specializationPointer]bool) bool {
		if !value.IsValid() {
			return false
		}
		value = settable(value)
		if value.Kind() == reflect.Interface {
			return !value.IsNil() && mentions(value.Elem(), seen)
		}
		if value.Kind() == reflect.Pointer && value.IsNil() {
			return false
		}
		if value.CanInterface() {
			if typ, ok := value.Interface().(*FuncType); ok && typ == ownerType {
				return true
			}
			if node, ok := value.Interface().(ast.Node); ok && owned[node] {
				return true
			}
			if symbol, ok := value.Interface().(*Symbol); ok && (localSymbols[symbol] || symbol.Node == product.Owner) {
				return true
			}
		}
		switch value.Kind() {
		case reflect.Pointer:
			key := specializationPointer{value.Type(), value.Pointer()}
			if seen[key] {
				return false
			}
			seen[key] = true
			return mentions(value.Elem(), seen)
		case reflect.Struct:
			if value.Type() == reflect.TypeFor[lexer.Pos]() {
				return false
			}
			for i := 0; i < value.NumField(); i++ {
				if mentions(settable(value.Field(i)), seen) {
					return true
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < value.Len(); i++ {
				if mentions(value.Index(i), seen) {
					return true
				}
			}
		case reflect.Map:
			if value.IsNil() {
				return false
			}
			key := specializationPointer{value.Type(), value.Pointer()}
			if seen[key] {
				return false
			}
			seen[key] = true
			iter := value.MapRange()
			for iter.Next() {
				if mentions(iter.Key(), seen) || mentions(iter.Value(), seen) {
					return true
				}
			}
		case reflect.String:
			for _, token := range plainDefaultIdentityToken.FindAllString(value.String(), -1) {
				if localTokens[token] {
					return true
				}
			}
		}
		return false
	}
	check := func(value reflect.Value) bool { return mentions(value, map[specializationPointer]bool{}) }
	for _, decl := range a.file.Decls {
		if decl != product.Owner && check(reflect.ValueOf(decl)) {
			return fmt.Errorf("shared source AST requires broader ownership")
		}
	}
	for _, path := range specializationASTMapPaths() {
		table := specializationField(reflect.ValueOf(a).Elem(), path)
		if table.IsNil() {
			continue
		}
		iter := table.MapRange()
		for iter.Next() {
			key, _ := iter.Key().Interface().(ast.Node)
			if owned[key] {
				continue
			}
			if check(iter.Value()) {
				return fmt.Errorf("foreign %s row shares owner syntax/identity", reflect.TypeOf(*a).FieldByIndex(path).Name)
			}
		}
	}
	// Check live function-value/type aliases outside the owner registration.
	for _, symbol := range a.globalScope.Symbols {
		if symbol != nil && symbol.Node != product.Owner && check(reflect.ValueOf(symbol)) {
			return fmt.Errorf("live global/type alias shares removed function contribution")
		}
	}
	fields := reflect.ValueOf(a).Elem()
	for i := 0; i < fields.NumField(); i++ {
		table := settable(fields.Field(i))
		if table.Kind() != reflect.Map || table.Type().Key() != reflect.TypeFor[*Symbol]() || table.IsNil() {
			continue
		}
		iter := table.MapRange()
		for iter.Next() {
			symbol := iter.Key().Interface().(*Symbol)
			if symbol != nil && symbol.Node == product.Owner {
				continue
			}
			if check(iter.Value()) {
				return fmt.Errorf("foreign %s symbol contribution shares owner identity", fields.Type().Field(i).Name)
			}
		}
	}
	return nil
}

// Stage-A raw observation cleanup. The enclosing transaction MUST separately
// unregister the function and rebuild declaration/caller indexes through the
// normal semantic path. This API alone cannot authorize or complete Update.
func cleanupRetainedPlainDefaultProduct(a *Analyzer, result *Result, product *retainedPlainDefaultProduct) error {
	if result == nil || result.analyzer != a || result.specializationRoot != nil || result.shared != nil {
		return fmt.Errorf("cleanup requires the idle matching canonical result")
	}
	if err := preflightRetainedPlainDefaultCleanup(a, product); err != nil {
		return err
	}
	for _, node := range product.Nodes {
		key := reflect.ValueOf(node)
		for _, path := range specializationASTMapPaths() {
			// The normal unregister operation still needs the symbol to remove
			// functionTypes/region registrations. Do not destroy it early.
			if reflect.TypeOf(*a).FieldByIndex(path).Name == "funcDeclSymbols" {
				continue
			}
			table := specializationField(reflect.ValueOf(a).Elem(), path)
			if !table.IsNil() && key.Type().AssignableTo(table.Type().Key()) {
				table.SetMapIndex(key, reflect.Value{})
			}
		}
		if result != nil {
			fields := reflect.ValueOf(result).Elem()
			for i := 0; i < fields.NumField(); i++ {
				table := fields.Field(i)
				if fields.Type().Field(i).IsExported() && table.Kind() == reflect.Map && table.Type().Key().Implements(astNodeType) && key.Type().AssignableTo(table.Type().Key()) && !table.IsNil() {
					table.SetMapIndex(key, reflect.Value{})
				}
			}
		}
	}
	// Delete owner-local symbol observations only. Borrowed global identities stay
	// stable even when their removed default was the first observer of the symbol.
	symbols := map[*Symbol]bool{}
	for symbol := range product.Symbols {
		if symbol.Node == product.Owner {
			symbols[symbol] = true
		}
	}
	root := settable(reflect.ValueOf(a).Elem())
	for i := 0; i < root.NumField(); i++ {
		table := settable(root.Field(i))
		if table.Kind() != reflect.Map || table.Type().Key() != reflect.TypeFor[*Symbol]() || table.IsNil() {
			continue
		}
		for symbol := range symbols {
			table.SetMapIndex(reflect.ValueOf(symbol), reflect.Value{})
		}
	}
	a.callDeclsByName = nil
	a.declaredFuncNames = nil // rebuild from the transaction's final declaration graph
	if result != nil {
		result.ElisionSummary.ArrayBounds = a.currentBoundsElisionCounts()
	}
	return nil
}

func (encoder *plainDefaultProductEncoder) rememberGlobalDependency(symbol *Symbol) {
	if symbol == nil || symbol.Node == encoder.product.Owner {
		return
	}
	source, ok := symbol.Node.(ast.Decl)
	if !ok {
		return
	}
	encoder.product.GlobalDependencies[symbol.Name] = fmt.Sprintf("kind=%v mutable=%v type=%s source=%s", symbol.Kind, symbol.Mutable, symbol.Type.String(), unparse.FormatDecl(source))
}

func (encoder *plainDefaultProductEncoder) checkAnchoredAST(value reflect.Value, seen map[specializationPointer]bool) error {
	if !value.IsValid() {
		return nil
	}
	value = settable(value)
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		return encoder.checkAnchoredAST(value.Elem(), seen)
	}
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil
	}
	if value.CanInterface() {
		if node, ok := value.Interface().(ast.Node); ok {
			if _, known := encoder.anchors[node]; !known {
				return fmt.Errorf("unanchored auxiliary syntax %T", node)
			}
			return nil
		}
		switch value.Interface().(type) {
		case Type, *Symbol, *Scope:
			return nil
		case *Analyzer, *RetainedAnalysis, *parallelWorkerState, *specializationContext, *sharedCodegen:
			return fmt.Errorf("auxiliary fact retains invocation resource %s", value.Type())
		}
	}
	switch value.Kind() {
	case reflect.Pointer:
		key := specializationPointer{value.Type(), value.Pointer()}
		if seen[key] {
			return nil
		}
		seen[key] = true
		return encoder.checkAnchoredAST(value.Elem(), seen)
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if err := encoder.checkAnchoredAST(settable(value.Field(i)), seen); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if err := encoder.checkAnchoredAST(value.Index(i), seen); err != nil {
				return err
			}
		}
	case reflect.Map:
		if value.IsNil() {
			return nil
		}
		key := specializationPointer{value.Type(), value.Pointer()}
		if seen[key] {
			return nil
		}
		seen[key] = true
		it := value.MapRange()
		for it.Next() {
			if err := encoder.checkAnchoredAST(it.Key(), seen); err != nil {
				return err
			}
			if err := encoder.checkAnchoredAST(it.Value(), seen); err != nil {
				return err
			}
		}
	case reflect.Func, reflect.Chan, reflect.UnsafePointer, reflect.Uintptr:
		return fmt.Errorf("auxiliary fact has unsupported resource %s", value.Type())
	}
	return nil
}

func (product *retainedPlainDefaultProduct) admissionDecline() string {
	return "contribution alone is not admission authority; require the retained header-residue and idle-cleanup transaction"
}

func plainDefaultBuiltinName(name string) bool {
	switch name {
	case "bool", "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "usize", "isize", "f32", "f64":
		return true
	}
	return false
}
func plainDefaultArrayElementSyntax(array *ArrayType) bool {
	if array.ElemTypeExpr == nil {
		return false
	}
	named, ok := array.ElemTypeExpr.(*ast.NamedType)
	return ok && plainDefaultBuiltinName(named.Name)
}
