package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// resolvedTypeNameContributions separates declaration/header resolutions from
// body-local resolutions. The maps contain only strings, so comparisonState
// does not retain the scratch AST.
type resolvedTypeNameContributions struct {
	functions    map[string]resolvedTypeNameFunctionContribution
	declarations map[string]string
	failure      string
	walkWork     int
}

type resolvedTypeNameFunctionContribution struct {
	header string
	body   string
}

// cloneResolvedTypeNameContributions keeps the shared baseline immutable while
// an accepted fast body edit replaces its own owners' body rows.
func cloneResolvedTypeNameContributions(in resolvedTypeNameContributions) resolvedTypeNameContributions {
	out := in
	out.functions = make(map[string]resolvedTypeNameFunctionContribution, len(in.functions))
	for key, contribution := range in.functions {
		out.functions[key] = contribution
	}
	out.declarations = make(map[string]string, len(in.declarations))
	for key, contribution := range in.declarations {
		out.declarations[key] = contribution
	}
	return out
}

type resolvedTypeNameLocation struct {
	owner     string
	region    string
	firstPath string
	morePaths map[string]bool
	ambiguous bool
}

// resolvedTypeNameSourceOwner records source ownership before body analysis
// can attach shared declaration TypeExpr nodes beneath local rewrite wrappers.
// Source ownership reuses the captured location records. Interpretation remains
// separate: multiple paths within one owner are valid contribution locations,
// but cannot authorize exclusive source-node retirement or body rebinding.
type resolvedTypeNameSourceOwner = resolvedTypeNameLocation

func (owner resolvedTypeNameLocation) sourceAmbiguous() bool {
	return owner.ambiguous || owner.firstPath == "" || len(owner.morePaths) != 0
}

func (owner resolvedTypeNameLocation) sourcePath() string {
	if len(owner.morePaths) != 0 {
		return ""
	}
	return owner.firstPath
}

type resolvedTypeNamePointer struct {
	typ reflect.Type
	ptr uintptr
}

type resolvedTypeNameOwnershipField struct {
	pathSuffix string
	index      int
	name       string
	published  bool
}

var (
	resolvedTypeNameASTPackage    = reflect.TypeOf((*ast.File)(nil)).Elem().PkgPath()
	resolvedTypeNamePositionType  = reflect.TypeOf(lexer.Pos{})
	resolvedTypeNameASTFieldCache sync.Map
	resolvedTypeNameOwnershipPlan sync.Map
)

func retainedPreBodyFunctionKeySet(decls []scopedDecl) map[string]bool {
	keys := retainedPreBodyFunctionKeys(decls)
	out := make(map[string]bool, len(keys))
	for _, key := range keys {
		out[key] = true
	}
	return out
}

// captureResolvedTypeNameContributions assigns source AST keys to exact owners.
// An analyzer-created key with no source-node identity is not authorized by a
// matching position; admission declines it, while cleanup preserves it.
func captureResolvedTypeNameContributions(rows map[ast.TypeExpr]string, decls []scopedDecl, functionKeys map[*ast.FuncDecl]string, failUnknown bool) (certificate resolvedTypeNameContributions, locations map[ast.TypeExpr]resolvedTypeNameLocation) {
	return captureResolvedTypeNameContributionsWithNested(rows, decls, functionKeys, failUnknown, nil)
}

func captureResolvedTypeNameContributionsWithNested(rows map[ast.TypeExpr]string, decls []scopedDecl, functionKeys map[*ast.FuncDecl]string, failUnknown bool, nested map[ast.Decl][]*ast.FuncDecl) (certificate resolvedTypeNameContributions, locations map[ast.TypeExpr]resolvedTypeNameLocation) {
	if functionKeys == nil {
		functionKeys = retainedPreBodyFunctionKeys(decls)
	}
	certificate = resolvedTypeNameContributions{
		functions:    make(map[string]resolvedTypeNameFunctionContribution, len(functionKeys)),
		declarations: map[string]string{},
	}
	locations = make(map[ast.TypeExpr]resolvedTypeNameLocation, len(rows))
	ambiguous := map[ast.TypeExpr]bool{}
	active := map[any]bool{}
	work := 0
	defer func() { certificate.walkWork = work }()

	// Keep path components until a type-expression location actually needs
	// a stable string. Siblings reuse both buffers.
	rootPath := make([]resolvedTypeNamePathPart, 0, 64)
	pathText := make([]byte, 0, 512)
	var walk func(reflect.Value, string, string, []resolvedTypeNamePathPart, bool, int) error
	walk = func(value reflect.Value, owner, region string, path []resolvedTypeNamePathPart, skipNestedFunctions bool, depth int) error {
		work++
		if work > 32_000_000 {
			return fmt.Errorf("AST ownership walk work budget exceeded (%d)", work)
		}
		if depth > 2048 {
			return fmt.Errorf("AST ownership walk depth exceeded")
		}
		if !value.IsValid() {
			return nil
		}
		// AST child fields are commonly interfaces wrapping a pointer. Charge
		// both nodes to the traversal limits while keeping them in one frame.
		if value.Kind() == reflect.Interface {
			if value.IsNil() {
				return nil
			}
			work++
			if work > 32_000_000 {
				return fmt.Errorf("AST ownership walk work budget exceeded (%d)", work)
			}
			depth++
			if depth > 2048 {
				return fmt.Errorf("AST ownership walk depth exceeded")
			}
			value = value.Elem()
		}
		switch value.Kind() {
		case reflect.Pointer:
			if value.IsNil() {
				return nil
			}
			node := value.Interface()
			// These leaves cannot own type-expression keys or contain cycles.
			// Avoid a reflective descent and cycle-table updates for each one.
			switch node.(type) {
			case *ast.Ident, *ast.IntLit, *ast.FloatLit, *ast.StringLit,
				*ast.CharLit, *ast.BoolLit, *ast.NullLit, *ast.ZeroedLit:
				return nil
			}
			typ := value.Type()
			if typ.Elem().PkgPath() != resolvedTypeNameASTPackage {
				return nil
			}
			if skipNestedFunctions && typ == reflect.TypeOf((*ast.FuncDecl)(nil)) {
				return nil
			}
			// The boxed AST pointer already carries its concrete type and identity.
			if active[node] {
				return fmt.Errorf("cyclic AST ownership graph")
			}
			active[node] = true
			if expr, ok := node.(ast.TypeExpr); ok {
				pathText = appendResolvedTypeNamePath(pathText[:0], path)
				location := locations[expr]
				if location.owner != "" && (location.owner != owner || location.region != region) {
					ambiguous[expr] = true
				}
				if location.firstPath == "" {
					location = resolvedTypeNameLocation{owner: owner, region: region, firstPath: string(pathText)}
				} else if location.firstPath != string(pathText) {
					if location.morePaths == nil {
						location.morePaths = map[string]bool{}
					}
					location.morePaths[string(pathText)] = true
				}
				locations[expr] = location
			}
			err := walk(value.Elem(), owner, region, path, skipNestedFunctions, depth+1)
			delete(active, node)
			return err
		case reflect.Struct:
			typ := value.Type()
			for _, field := range resolvedTypeNameOwnershipFields(typ) {
				if !field.published {
					return fmt.Errorf("unpublished AST ownership field %s.%s", typ, field.name)
				}
				if err := walk(value.Field(field.index), owner, region, append(path, resolvedTypeNamePathPart{fieldOrKey: field.pathSuffix, index: resolvedTypeNamePathField}), skipNestedFunctions, depth+1); err != nil {
					return err
				}
			}
		case reflect.Slice, reflect.Array:
			if value.Len() > 1_000_000 {
				return fmt.Errorf("AST ownership slice budget exceeded")
			}
			for i := 0; i < value.Len(); i++ {
				if err := walk(value.Index(i), owner, region, append(path, resolvedTypeNamePathPart{index: i}), skipNestedFunctions, depth+1); err != nil {
					return err
				}
			}
		case reflect.Map:
			if value.IsNil() {
				return nil
			}
			keyType := value.Type().Key()
			if keyType.Kind() != reflect.String {
				return fmt.Errorf("unsupported AST ownership map key %s", keyType)
			}
			keys := value.MapKeys()
			sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
			for _, key := range keys {
				keyPart := resolvedTypeNamePathPart{fieldOrKey: key.String(), index: resolvedTypeNamePathMapKey}
				if err := walk(value.MapIndex(key), owner, region, append(path, keyPart), skipNestedFunctions, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}

	functionOwners := make(map[string]bool, len(functionKeys))
	functionPaths := make(map[*ast.FuncDecl]bool, len(functionKeys))
	declarationOccurrence := map[string]int{}
	for _, scoped := range decls {
		root := scoped.Decl
		if root == nil {
			certificate.failure = "missing scoped declaration"
			return certificate, locations
		}
		if fn, ok := root.(*ast.FuncDecl); ok {
			key := functionKeys[fn]
			if key == "" {
				certificate.failure = "function declaration lacks stable owner key"
				return certificate, locations
			}
			if functionOwners[key] {
				certificate.failure = "duplicate function owner key"
				return certificate, locations
			}
			functionOwners[key], functionPaths[fn] = true, true
			if err := collectResolvedTypeNameFunctionPaths(walk, fn, key, rootPath); err != nil {
				certificate.failure = err.Error()
				return certificate, locations
			}
			continue
		}
		base, err := resolvedTypeNameDeclarationBase(scoped.Namespace, root)
		if err != nil {
			certificate.failure = "unsupported declaration syntax: " + err.Error()
			return certificate, locations
		}
		ordinal := declarationOccurrence[base]
		declarationOccurrence[base]++
		owner := fmt.Sprintf("decl:%q#%d", base, ordinal)
		if err := walk(reflect.ValueOf(root), owner, "declaration", rootPath, true, 0); err != nil {
			certificate.failure = err.Error()
			return certificate, locations
		}
		for _, fn := range retainedNestedFunctions(root, nested) {
			if functionPaths[fn] {
				continue
			}
			key := functionKeys[fn]
			if key == "" || functionOwners[key] {
				certificate.failure = "function declaration lacks a unique stable owner key"
				return certificate, locations
			}
			functionOwners[key], functionPaths[fn] = true, true
			if err := collectResolvedTypeNameFunctionPaths(walk, fn, key, rootPath); err != nil {
				certificate.failure = err.Error()
				return certificate, locations
			}
		}
	}
	if failUnknown && len(ambiguous) != 0 {
		certificate.failure = "TypeExpr syntax is shared across declaration owners or header/body"
		return certificate, locations
	}

	// Every certified key must be an actual source TypeExpr pointer. Position
	// similarity cannot establish which declaration produced a synthetic key.
	type ownerRegion struct{ owner, region string }
	rowsByOwner := map[ownerRegion][]string{}
	for expr, canonical := range rows {
		typ := reflect.TypeOf(expr)
		if expr == nil || typ == nil || typ.Kind() != reflect.Pointer || reflect.ValueOf(expr).IsNil() {
			if failUnknown {
				certificate.failure = "resolved type name has a non-pointer or nil AST key"
				return certificate, locations
			}
			continue // Keep keys without exact source-node identity out of cleanup authority.
		}
		location, found := locations[expr]
		if !found {
			if failUnknown {
				certificate.failure = "resolved type name key has no exact source-node owner"
				return certificate, locations
			}
			continue // Preserve unowned keys during cleanup; never retire by offset.
		}
		if ambiguous[expr] {
			location.ambiguous = true
			locations[expr] = location
			if failUnknown {
				certificate.failure = "resolved type name key is shared across declaration owners or header/body"
				return certificate, locations
			}
			continue // Preserve an ambiguous key; it has no exclusive owner.
		}
		digest, err := functionSyntaxDigest(expr)
		if err != nil {
			certificate.failure = "unsupported resolved type name syntax: " + err.Error()
			return certificate, locations
		}
		typeDigest := hex.EncodeToString(digest[:])
		pathList := []string{location.firstPath}
		if len(location.morePaths) != 0 {
			for path := range location.morePaths {
				pathList = append(pathList, path)
			}
			sort.Strings(pathList)
		}
		pathBytes, _ := json.Marshal(pathList)
		row := string(pathBytes) + "\x00" + typ.String() + "\x00" + typeDigest + "\x00" + canonical
		key := ownerRegion{owner: location.owner, region: location.region}
		rowsByOwner[key] = append(rowsByOwner[key], row)
	}
	for key, ownerRows := range rowsByOwner {
		sort.Strings(ownerRows)
		encoded, err := json.Marshal(ownerRows)
		if err != nil {
			certificate.failure = "could not encode resolved type name contribution"
			return certificate, locations
		}
		owner, region := key.owner, key.region
		if strings.HasPrefix(owner, "fn:") {
			key := strings.TrimPrefix(owner, "fn:")
			contribution := certificate.functions[key]
			switch region {
			case "header":
				contribution.header = string(encoded)
			case "body":
				contribution.body = string(encoded)
			default:
				certificate.failure = "unknown function type-name contribution region"
				return certificate, locations
			}
			certificate.functions[key] = contribution
		} else if region == "declaration" {
			certificate.declarations[owner] = string(encoded)
		} else {
			certificate.failure = "unknown declaration type-name contribution region"
			return certificate, locations
		}
	}
	for key := range functionOwners {
		if _, ok := certificate.functions[key]; !ok {
			certificate.functions[key] = resolvedTypeNameFunctionContribution{}
		}
	}
	return certificate, locations
}

var resolvedTypeNameFunctionHeaderFields = sync.OnceValue(func() []resolvedTypeNameOwnershipField {
	typ := reflect.TypeOf(ast.FuncDecl{})
	var plan []resolvedTypeNameOwnershipField
	for i, field := range reflectFields(typ) {
		if field.Name != "Body" && resolvedTypeNameFieldMayContainAST(field.Type) {
			plan = append(plan, resolvedTypeNameOwnershipField{
				index: i, name: field.Name, pathSuffix: "." + field.Name, published: field.IsExported(),
			})
		}
	}
	return plan
})

func collectResolvedTypeNameFunctionPaths(walk func(reflect.Value, string, string, []resolvedTypeNamePathPart, bool, int) error, fn *ast.FuncDecl, key string, rootPath []resolvedTypeNamePathPart) error {
	owner := "fn:" + key
	value := reflect.ValueOf(fn).Elem()
	for _, field := range resolvedTypeNameFunctionHeaderFields() {
		if !field.published {
			return fmt.Errorf("unpublished function header field %s.%s", value.Type(), field.name)
		}
		if err := walk(value.Field(field.index), owner, "header", append(rootPath, resolvedTypeNamePathPart{fieldOrKey: field.pathSuffix, index: resolvedTypeNamePathField}), false, 0); err != nil {
			return err
		}
	}
	if err := walk(reflect.ValueOf(fn.Body), owner, "body", rootPath, false, 0); err != nil {
		return err
	}
	return nil
}

func resolvedTypeNameOwnershipFields(typ reflect.Type) []resolvedTypeNameOwnershipField {
	if cached, ok := resolvedTypeNameOwnershipPlan.Load(typ); ok {
		return cached.([]resolvedTypeNameOwnershipField)
	}
	var plan []resolvedTypeNameOwnershipField
	if typ.PkgPath() != resolvedTypeNameASTPackage {
		resolvedTypeNameOwnershipPlan.LoadOrStore(typ, plan)
		return nil
	}
	for i, field := range reflectFields(typ) {
		published := field.IsExported()
		if !published || resolvedTypeNameFieldMayContainAST(field.Type) {
			plan = append(plan, resolvedTypeNameOwnershipField{index: i, name: field.Name, pathSuffix: "." + field.Name, published: published})
		}
	}
	actual, _ := resolvedTypeNameOwnershipPlan.LoadOrStore(typ, plan)
	return actual.([]resolvedTypeNameOwnershipField)
}

func resolvedTypeNameFieldMayContainAST(typ reflect.Type) bool {
	if cached, ok := resolvedTypeNameASTFieldCache.Load(typ); ok {
		return cached.(bool)
	}
	seen := map[reflect.Type]bool{}
	var contains func(reflect.Type, int) bool
	contains = func(current reflect.Type, depth int) bool {
		if current == nil || depth > 32 || seen[current] {
			return false
		}
		if current.PkgPath() == resolvedTypeNameASTPackage {
			return true
		}
		seen[current] = true
		switch current.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			return contains(current.Elem(), depth+1)
		case reflect.Map:
			return contains(current.Key(), depth+1) || contains(current.Elem(), depth+1)
		case reflect.Interface:
			return current.PkgPath() == resolvedTypeNameASTPackage
		}
		return false
	}
	result := contains(typ, 0)
	actual, _ := resolvedTypeNameASTFieldCache.LoadOrStore(typ, result)
	return actual.(bool)
}

func resolvedTypeNameIndexPath(path string, index int) string {
	return path + "[" + strconv.Itoa(index) + "]"
}

func resolvedTypeNameDeclarationBase(namespace string, decl ast.Decl) (string, error) {
	name := ""
	value := reflect.ValueOf(decl)
	if value.IsValid() && value.Kind() == reflect.Pointer && !value.IsNil() && value.Elem().Kind() == reflect.Struct {
		field := value.Elem().FieldByName("Name")
		if field.IsValid() && field.Kind() == reflect.String && field.CanInterface() {
			name = field.String()
		}
	}
	if name == "" {
		// Position moves when an earlier function body grows. A syntax digest is
		// stable across that shift and still changes when an unnamed declaration
		// itself changes.
		identity := decl
		if impl, ok := decl.(*ast.ImplDecl); ok {
			header := *impl
			header.Members = nil
			identity = &header
		}
		digest, err := functionSyntaxDigest(identity)
		if err != nil {
			return "", err
		}
		name = "@" + hex.EncodeToString(digest[:])
	}
	return fmt.Sprintf("%s|%T|%s", namespace, decl, name), nil
}

func compareResolvedTypeNameContributions(old, current resolvedTypeNameContributions, oldFunctionKeys, currentFunctionKeys map[string]bool, addedFunctions, removedFunctions int, replaced, reanalyzedBodies map[string]bool) string {
	if old.failure != "" || current.failure != "" {
		return "ownership certificate unavailable: " + firstNonEmpty(old.failure, current.failure)
	}
	if len(oldFunctionKeys) != len(old.functions) || len(currentFunctionKeys) != len(current.functions) {
		return "function owner set does not match declaration inventory"
	}
	if len(old.declarations) != len(current.declarations) {
		return "non-function declaration owner set changed"
	}
	for owner, previous := range old.declarations {
		now, ok := current.declarations[owner]
		if !ok || previous != now {
			return "non-function declaration contribution changed"
		}
	}
	added, removed := 0, 0
	for key := range currentFunctionKeys {
		if !oldFunctionKeys[key] {
			added++
		}
	}
	for key := range oldFunctionKeys {
		if !currentFunctionKeys[key] {
			removed++
		}
	}
	replacements := 0
	for key := range replaced {
		if oldFunctionKeys[key] && currentFunctionKeys[key] {
			replacements++
		}
	}
	if added+replacements != addedFunctions || removed+replacements != removedFunctions {
		return "function owner additions/removals do not match declaration plan"
	}
	for key, previous := range old.functions {
		now, ok := current.functions[key]
		if !ok {
			if !currentFunctionKeys[key] {
				continue // explicit removed function
			}
			return "function type-name contribution is missing"
		}
		if !oldFunctionKeys[key] || replaced[key] {
			continue // newly declared or explicitly replaced function
		}
		if previous.header != now.header {
			return "function header type-name contribution changed for " + key
		}
		if previous.body != now.body && !reanalyzedBodies[key] {
			return "function body type-name contribution changed outside reanalysis for " + key
		}
	}
	for key := range current.functions {
		if !oldFunctionKeys[key] && currentFunctionKeys[key] {
			continue // explicit added function
		}
		if !oldFunctionKeys[key] && !currentFunctionKeys[key] {
			return "unowned function type-name contribution"
		}
	}
	return ""
}

func resolvedTypeNameBodyRows(rows map[ast.TypeExpr]string, locations map[ast.TypeExpr]resolvedTypeNameLocation) map[string]map[ast.TypeExpr]string {
	byFunction := map[string]map[ast.TypeExpr]string{}
	for expr, value := range rows {
		location, ok := locations[expr]
		if !ok || location.ambiguous || location.region != "body" || !strings.HasPrefix(location.owner, "fn:") {
			continue
		}
		key := strings.TrimPrefix(location.owner, "fn:")
		if byFunction[key] == nil {
			byFunction[key] = map[ast.TypeExpr]string{}
		}
		byFunction[key][expr] = value
	}
	return byFunction
}

// resolvedTypeNameSourceOwners transfers the captured location map to its
// immutable source-ownership role. Callers may read locations afterwards, but
// updates must clone the map, as retained body replay already does.
func resolvedTypeNameSourceOwners(locations map[ast.TypeExpr]resolvedTypeNameLocation) map[ast.TypeExpr]resolvedTypeNameSourceOwner {
	if locations == nil {
		return map[ast.TypeExpr]resolvedTypeNameSourceOwner{}
	}
	return locations
}

// importResolvedTypeNameBodyRows carries certified facts onto the body AST
// that will be analyzed. The caller has already matched the rows to this owner.
func importResolvedTypeNameBodyRows(a *Analyzer, rows map[ast.TypeExpr]string) error {
	if a == nil {
		return fmt.Errorf("missing live analyzer")
	}
	for expr, value := range rows {
		a.recordResolvedTypeName(expr, value)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "unknown ownership error"
}
