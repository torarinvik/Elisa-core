package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"elisacore/src/ast"
	"elisacore/src/lexer"
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

type resolvedTypeNameLocation struct {
	owner     string
	region    string
	paths     map[string]bool
	ambiguous bool
}

// resolvedTypeNameSourceOwner records source ownership before body analysis
// can attach shared declaration TypeExpr nodes beneath local rewrite wrappers.
type resolvedTypeNameSourceOwner struct {
	owner     string
	region    string
	path      string
	ambiguous bool
}

type resolvedTypeNamePointer struct {
	typ reflect.Type
	ptr uintptr
}

var (
	resolvedTypeNameASTPackage    = reflect.TypeOf((*ast.File)(nil)).Elem().PkgPath()
	resolvedTypeNamePositionType  = reflect.TypeOf(lexer.Pos{})
	resolvedTypeNameASTFieldCache sync.Map
)

// retainedPreBodyFunctionKeys must stay in lockstep with capturePreBody's
// historical function key format: namespace/name plus same-name occurrence.
func retainedPreBodyFunctionKeys(decls []scopedDecl) map[*ast.FuncDecl]string {
	keys := make(map[*ast.FuncDecl]string)
	occurrences := map[string]int{}
	for _, scoped := range decls {
		for _, declaration := range retainedWalkDecls(&ast.File{Decls: []ast.Decl{scoped.Decl}}) {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			base := scoped.Namespace + "::" + fn.Name
			keys[fn] = fmt.Sprintf("%s#%d", base, occurrences[base])
			occurrences[base]++
		}
	}
	return keys
}

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
	certificate = resolvedTypeNameContributions{
		functions:    map[string]resolvedTypeNameFunctionContribution{},
		declarations: map[string]string{},
	}
	locations = map[ast.TypeExpr]resolvedTypeNameLocation{}
	ambiguous := map[ast.TypeExpr]bool{}
	active := map[resolvedTypeNamePointer]bool{}
	work := 0
	defer func() { certificate.walkWork = work }()
	if functionKeys == nil {
		functionKeys = retainedPreBodyFunctionKeys(decls)
	}

	var walk func(reflect.Value, string, string, string, bool, int) error
	walk = func(value reflect.Value, owner, region, path string, skipNestedFunctions bool, depth int) error {
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
		typ := value.Type()
		switch value.Kind() {
		case reflect.Interface:
			if value.IsNil() {
				return nil
			}
			return walk(value.Elem(), owner, region, path, skipNestedFunctions, depth+1)
		case reflect.Pointer:
			if value.IsNil() {
				return nil
			}
			if typ.Elem().PkgPath() != resolvedTypeNameASTPackage {
				return nil
			}
			if skipNestedFunctions && typ == reflect.TypeOf((*ast.FuncDecl)(nil)) {
				return nil
			}
			ptr := resolvedTypeNamePointer{typ: typ, ptr: value.Pointer()}
			if active[ptr] {
				return fmt.Errorf("cyclic AST ownership graph")
			}
			active[ptr] = true
			if expr, ok := value.Interface().(ast.TypeExpr); ok {
				location := locations[expr]
				if location.owner != "" && (location.owner != owner || location.region != region) {
					ambiguous[expr] = true
				} else if location.paths == nil {
					location = resolvedTypeNameLocation{owner: owner, region: region, paths: map[string]bool{}}
				}
				location.paths[path] = true
				locations[expr] = location
			}
			err := walk(value.Elem(), owner, region, path, skipNestedFunctions, depth+1)
			delete(active, ptr)
			return err
		case reflect.Struct:
			if typ.PkgPath() != resolvedTypeNameASTPackage {
				return nil
			}
			for i := 0; i < value.NumField(); i++ {
				field := typ.Field(i)
				child := value.Field(i)
				if !child.CanInterface() {
					return fmt.Errorf("unpublished AST ownership field %s.%s", typ, field.Name)
				}
				if !resolvedTypeNameFieldMayContainAST(field.Type) {
					continue
				}
				if err := walk(child, owner, region, path+"."+field.Name, skipNestedFunctions, depth+1); err != nil {
					return err
				}
			}
		case reflect.Slice, reflect.Array:
			if value.Len() > 1_000_000 {
				return fmt.Errorf("AST ownership slice budget exceeded")
			}
			for i := 0; i < value.Len(); i++ {
				if err := walk(value.Index(i), owner, region, resolvedTypeNameIndexPath(path, i), skipNestedFunctions, depth+1); err != nil {
					return err
				}
			}
		case reflect.Map:
			if value.IsNil() {
				return nil
			}
			if typ.Key().Kind() != reflect.String {
				return fmt.Errorf("unsupported AST ownership map key %s", typ.Key())
			}
			keys := value.MapKeys()
			sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
			for _, key := range keys {
				if err := walk(value.MapIndex(key), owner, region, path+"["+strconv.Quote(key.String())+"]", skipNestedFunctions, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}

	functionOwners := map[string]bool{}
	functionPaths := map[*ast.FuncDecl]bool{}
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
			if err := collectResolvedTypeNameFunctionPaths(walk, fn, key); err != nil {
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
		if err := walk(reflect.ValueOf(root), owner, "declaration", "$", true, 0); err != nil {
			certificate.failure = err.Error()
			return certificate, locations
		}
		for _, declaration := range retainedWalkDecls(&ast.File{Decls: []ast.Decl{root}}) {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || functionPaths[fn] {
				continue
			}
			key := functionKeys[fn]
			if key == "" || functionOwners[key] {
				certificate.failure = "function declaration lacks a unique stable owner key"
				return certificate, locations
			}
			functionOwners[key], functionPaths[fn] = true, true
			if err := collectResolvedTypeNameFunctionPaths(walk, fn, key); err != nil {
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
	rowsByOwner := map[string][]string{}
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
		digest, _, err := functionSyntaxFingerprint(expr)
		if err != nil {
			certificate.failure = "unsupported resolved type name syntax: " + err.Error()
			return certificate, locations
		}
		typeDigest := hex.EncodeToString(digest[:])
		pathList := make([]string, 0, len(location.paths))
		for path := range location.paths {
			pathList = append(pathList, path)
		}
		sort.Strings(pathList)
		pathBytes, _ := json.Marshal(pathList)
		row := fmt.Sprintf("%s\x00%s\x00%s\x00%s", string(pathBytes), reflect.TypeOf(expr), typeDigest, canonical)
		key := location.owner + "\x00" + location.region
		rowsByOwner[key] = append(rowsByOwner[key], row)
		locations[expr] = location
	}
	for ownerRegion, ownerRows := range rowsByOwner {
		sort.Strings(ownerRows)
		encoded, err := json.Marshal(ownerRows)
		if err != nil {
			certificate.failure = "could not encode resolved type name contribution"
			return certificate, locations
		}
		separator := strings.LastIndex(ownerRegion, "\x00")
		owner, region := ownerRegion[:separator], ownerRegion[separator+1:]
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

func collectResolvedTypeNameFunctionPaths(walk func(reflect.Value, string, string, string, bool, int) error, fn *ast.FuncDecl, key string) error {
	value := reflect.ValueOf(fn).Elem()
	typ := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := typ.Field(i)
		if field.Name == "Body" || !resolvedTypeNameFieldMayContainAST(field.Type) {
			continue
		}
		if !value.Field(i).CanInterface() {
			return fmt.Errorf("unpublished function header field %s.%s", typ, field.Name)
		}
		if err := walk(value.Field(i), "fn:"+key, "header", "$."+field.Name, false, 0); err != nil {
			return err
		}
	}
	if err := walk(reflect.ValueOf(fn.Body), "fn:"+key, "body", "$", false, 0); err != nil {
		return err
	}
	return nil
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
		digest, _, err := functionSyntaxFingerprint(identity)
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

func resolvedTypeNameSourceOwners(locations map[ast.TypeExpr]resolvedTypeNameLocation) map[ast.TypeExpr]resolvedTypeNameSourceOwner {
	owners := make(map[ast.TypeExpr]resolvedTypeNameSourceOwner, len(locations))
	for expr, location := range locations {
		owner := resolvedTypeNameSourceOwner{
			owner:     location.owner,
			region:    location.region,
			ambiguous: location.ambiguous || len(location.paths) != 1,
		}
		if len(location.paths) == 1 {
			for path := range location.paths {
				owner.path = path
			}
		}
		owners[expr] = owner
	}
	return owners
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

func resolvedTypeNameBodyTypeExprPaths(body []ast.Stmt) (map[ast.TypeExpr][]string, error) {
	locations := map[ast.TypeExpr][]string{}
	active := map[resolvedTypeNamePointer]bool{}
	work := 0
	var walk func(reflect.Value, string, int) error
	walk = func(value reflect.Value, path string, depth int) error {
		work++
		if work > 1_000_000 {
			return fmt.Errorf("AST body type syntax work budget exceeded")
		}
		if depth > 2048 {
			return fmt.Errorf("AST type syntax depth exceeded")
		}
		if !value.IsValid() {
			return nil
		}
		typ := value.Type()
		if typ == resolvedTypeNamePositionType {
			return nil
		}
		switch value.Kind() {
		case reflect.Interface:
			if !value.IsNil() {
				return walk(value.Elem(), path, depth+1)
			}
		case reflect.Pointer:
			if value.IsNil() || typ.Elem().PkgPath() != resolvedTypeNameASTPackage {
				return nil
			}
			ptr := resolvedTypeNamePointer{typ: typ, ptr: value.Pointer()}
			if active[ptr] {
				return fmt.Errorf("cyclic AST body")
			}
			active[ptr] = true
			if expr, ok := value.Interface().(ast.TypeExpr); ok {
				locations[expr] = append(locations[expr], path)
			}
			err := walk(value.Elem(), path, depth+1)
			delete(active, ptr)
			return err
		case reflect.Struct:
			if typ.PkgPath() != resolvedTypeNameASTPackage {
				return nil
			}
			for i := 0; i < value.NumField(); i++ {
				field := typ.Field(i)
				if !resolvedTypeNameFieldMayContainAST(field.Type) {
					continue
				}
				if !value.Field(i).CanInterface() {
					return fmt.Errorf("unpublished AST field %s.%s", typ, field.Name)
				}
				if err := walk(value.Field(i), path+"."+field.Name, depth+1); err != nil {
					return err
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < value.Len(); i++ {
				if err := walk(value.Index(i), resolvedTypeNameIndexPath(path, i), depth+1); err != nil {
					return err
				}
			}
		case reflect.Map:
			if typ.Key().Kind() != reflect.String {
				return fmt.Errorf("unsupported AST map key %s", typ.Key())
			}
			keys := value.MapKeys()
			sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
			for _, key := range keys {
				if err := walk(value.MapIndex(key), path+"["+strconv.Quote(key.String())+"]", depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(reflect.ValueOf(body), "$", 0); err != nil {
		return nil, err
	}
	return locations, nil
}

func resolvedTypeNameBodyExprsByPath(body []ast.Stmt) (map[string]ast.TypeExpr, error) {
	locations, err := resolvedTypeNameBodyTypeExprPaths(body)
	if err != nil {
		return nil, err
	}
	byPath := make(map[string]ast.TypeExpr, len(locations))
	for expr, paths := range locations {
		if len(paths) != 1 {
			return nil, fmt.Errorf("body TypeExpr has multiple syntax paths")
		}
		path := paths[0]
		if previous := byPath[path]; previous != nil && previous != expr {
			return nil, fmt.Errorf("body syntax path maps to multiple TypeExpr pointers")
		}
		byPath[path] = expr
	}
	return byPath, nil
}

func resolvedTypeNameFastBodySourceOwners(edits [][3]*ast.FuncDecl, preparedBodies [][]ast.Stmt, functionKeys map[*ast.FuncDecl]string) (map[ast.TypeExpr]resolvedTypeNameSourceOwner, error) {
	if len(edits) != len(preparedBodies) {
		return nil, fmt.Errorf("fast body ownership count mismatch")
	}
	owners := map[ast.TypeExpr]resolvedTypeNameSourceOwner{}
	for i, edit := range edits {
		key := functionKeys[edit[0]]
		if key == "" || edit[2] == nil {
			return nil, fmt.Errorf("fast-prepared function lacks a source owner")
		}
		sourcePaths, err := resolvedTypeNameBodyExprsByPath(edit[2].Body)
		if err != nil {
			return nil, err
		}
		preparedPaths, err := resolvedTypeNameBodyExprsByPath(preparedBodies[i])
		if err != nil {
			return nil, err
		}
		if len(sourcePaths) != len(preparedPaths) {
			return nil, fmt.Errorf("fast-prepared body changes the TypeExpr path set")
		}
		for path, sourceExpr := range sourcePaths {
			preparedExpr := preparedPaths[path]
			if preparedExpr == nil || reflect.TypeOf(sourceExpr) != reflect.TypeOf(preparedExpr) {
				return nil, fmt.Errorf("fast-prepared body changes a TypeExpr path or kind")
			}
			sourceDigest, _, sourceErr := functionSyntaxFingerprint(sourceExpr)
			preparedDigest, _, preparedErr := functionSyntaxFingerprint(preparedExpr)
			if sourceErr != nil || preparedErr != nil || sourceDigest != preparedDigest {
				return nil, fmt.Errorf("fast-prepared body changes TypeExpr syntax")
			}
			owners[preparedExpr] = resolvedTypeNameSourceOwner{owner: "fn:" + key, region: "body", path: path}
		}
	}
	return owners, nil
}

func resolvedTypeNameBodySyntaxFingerprint(fn *ast.FuncDecl) (string, error) {
	if fn == nil {
		return "", fmt.Errorf("missing function")
	}
	locations, err := resolvedTypeNameBodyTypeExprPaths(fn.Body)
	if err != nil {
		return "", err
	}
	rows := []string{}
	for expr, paths := range locations {
		digest, _, err := functionSyntaxFingerprint(expr)
		if err != nil {
			return "", err
		}
		sort.Strings(paths)
		encoded, _ := json.Marshal(paths)
		rows = append(rows, fmt.Sprintf("%s\x00%s\x00%s", string(encoded), reflect.TypeOf(expr), hex.EncodeToString(digest[:])))
	}
	sort.Strings(rows)
	encoded, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func rebindResolvedTypeNameBodyRows(a *Analyzer, sourceOwners map[ast.TypeExpr]resolvedTypeNameSourceOwner, ownerKey string, preparedBody []ast.Stmt) (map[ast.TypeExpr]string, error) {
	if a == nil || ownerKey == "" {
		return nil, fmt.Errorf("missing fast body owner")
	}
	newPaths, err := resolvedTypeNameBodyExprsByPath(preparedBody)
	if err != nil {
		return nil, err
	}
	rows := map[ast.TypeExpr]string{}
	for expr, canonical := range a.resolvedTypeNames {
		sourceOwner, owned := sourceOwners[expr]
		if !owned || sourceOwner.owner != "fn:"+ownerKey || sourceOwner.region != "body" {
			continue
		}
		if sourceOwner.ambiguous || sourceOwner.path == "" {
			return nil, fmt.Errorf("old body TypeExpr has no unique source path")
		}
		newExpr := newPaths[sourceOwner.path]
		if newExpr == nil {
			return nil, fmt.Errorf("prepared body lost a resolved type-name path")
		}
		oldDigest, _, oldErr := functionSyntaxFingerprint(expr)
		newDigest, _, newErr := functionSyntaxFingerprint(newExpr)
		if oldErr != nil || newErr != nil || oldDigest != newDigest || reflect.TypeOf(expr) != reflect.TypeOf(newExpr) {
			return nil, fmt.Errorf("prepared body changes a resolved TypeExpr path or syntax")
		}
		rows[newExpr] = canonical
	}
	return rows, nil
}

// retireResolvedTypeNameBodyRows removes all old-AST keys for bodies that will
// be replayed or removed. Header and declaration rows stay in place.
type resolvedTypeNameRetirementPlan struct {
	rows      map[ast.TypeExpr]string
	plainRows map[ast.TypeExpr]bool
}

func prepareResolvedTypeNameRetirement(a *Analyzer, sourceOwners map[ast.TypeExpr]resolvedTypeNameSourceOwner, functionKeys map[*ast.FuncDecl]string, bodyFunctions, removedFunctions []*ast.FuncDecl, plainRows map[ast.TypeExpr]bool, ownershipCertified bool) (*resolvedTypeNameRetirementPlan, error) {
	if a == nil || len(a.resolvedTypeNames) == 0 || len(bodyFunctions)+len(removedFunctions) == 0 {
		return &resolvedTypeNameRetirementPlan{plainRows: plainRows}, nil
	}
	plan := &resolvedTypeNameRetirementPlan{rows: map[ast.TypeExpr]string{}, plainRows: plainRows}
	if !ownershipCertified || sourceOwners == nil {
		return plan, nil // Preserve analyzer rows when the old source graph lacked an ownership proof.
	}
	retireBodies, retireAll := map[string]bool{}, map[string]bool{}
	for _, fn := range bodyFunctions {
		key := functionKeys[fn]
		if key != "" {
			retireBodies["fn:"+key] = true
		}
	}
	for _, fn := range removedFunctions {
		key := functionKeys[fn]
		if key != "" {
			retireAll["fn:"+key] = true
		}
	}
	for expr, value := range a.resolvedTypeNames {
		owner, ok := sourceOwners[expr]
		if !ok || owner.ambiguous {
			continue // Synthetic or shared post-body keys have no exclusive deletion authority.
		}
		if retireAll[owner.owner] || owner.region == "body" && retireBodies[owner.owner] {
			plan.rows[expr] = value
		}
	}
	return plan, nil
}

func (plan *resolvedTypeNameRetirementPlan) apply(a *Analyzer) error {
	if plan == nil || a == nil {
		return fmt.Errorf("missing resolved type-name retirement authority")
	}
	for expr, expected := range plan.rows {
		current, ok := a.resolvedTypeNames[expr]
		if ok && current != expected || !ok && !plan.plainRows[expr] {
			return fmt.Errorf("resolved type-name row changed after retirement preflight")
		}
	}
	for expr := range plan.rows {
		delete(a.resolvedTypeNames, expr)
	}
	return nil
}
