package semantic

import (
	"elisacore/src/ast"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Frozen reflective traversal from before the leaf shortcut. Keep authority
// errors and exact ownership paths independently comparable to the fast walk.
func captureResolvedTypeNameContributionsReference(rows map[ast.TypeExpr]string, decls []scopedDecl, functionKeys map[*ast.FuncDecl]string, failUnknown bool) (certificate resolvedTypeNameContributions, locations map[ast.TypeExpr]resolvedTypeNameLocation) {
	if functionKeys == nil {
		functionKeys = retainedPreBodyFunctionKeys(decls)
	}
	certificate = resolvedTypeNameContributions{
		functions:    make(map[string]resolvedTypeNameFunctionContribution, len(functionKeys)),
		declarations: map[string]string{},
	}
	locations = make(map[ast.TypeExpr]resolvedTypeNameLocation, len(rows))
	ambiguous := map[ast.TypeExpr]bool{}
	active := map[resolvedTypeNamePointer]bool{}
	work := 0
	defer func() { certificate.walkWork = work }()

	// Siblings reuse the traversal buffer. Only a retained type-name path
	// needs its own immutable string.
	rootPath := make([]byte, 1, 512)
	rootPath[0] = '$'
	var walk func(reflect.Value, string, string, []byte, bool, int) error
	walk = func(value reflect.Value, owner, region string, path []byte, skipNestedFunctions bool, depth int) error {
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
				}
				if location.firstPath == "" {
					location = resolvedTypeNameLocation{owner: owner, region: region, firstPath: string(path)}
				} else if location.firstPath != string(path) {
					if location.morePaths == nil {
						location.morePaths = map[string]bool{}
					}
					location.morePaths[string(path)] = true
				}
				locations[expr] = location
			}
			err := walk(value.Elem(), owner, region, path, skipNestedFunctions, depth+1)
			delete(active, ptr)
			return err
		case reflect.Struct:
			if typ.PkgPath() != resolvedTypeNameASTPackage {
				return nil
			}
			for _, field := range resolvedTypeNameOwnershipFields(typ) {
				if !field.published {
					return fmt.Errorf("unpublished AST ownership field %s.%s", typ, field.name)
				}
				if err := walk(value.Field(field.index), owner, region, append(append(path, '.'), field.name...), skipNestedFunctions, depth+1); err != nil {
					return err
				}
			}
		case reflect.Slice, reflect.Array:
			if value.Len() > 1_000_000 {
				return fmt.Errorf("AST ownership slice budget exceeded")
			}
			for i := 0; i < value.Len(); i++ {
				indexPath := strconv.AppendInt(append(path, '['), int64(i), 10)
				if err := walk(value.Index(i), owner, region, append(indexPath, ']'), skipNestedFunctions, depth+1); err != nil {
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
				keyPath := strconv.AppendQuote(append(path, '['), key.String())
				if err := walk(value.MapIndex(key), owner, region, append(keyPath, ']'), skipNestedFunctions, depth+1); err != nil {
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
			if err := collectResolvedTypeNameReferenceFunctionPaths(walk, fn, key, rootPath); err != nil {
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
			if err := collectResolvedTypeNameReferenceFunctionPaths(walk, fn, key, rootPath); err != nil {
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
		digest, _, err := functionSyntaxFingerprint(expr)
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

func collectResolvedTypeNameReferenceFunctionPaths(walk func(reflect.Value, string, string, []byte, bool, int) error, fn *ast.FuncDecl, key string, rootPath []byte) error {
	owner := "fn:" + key
	value := reflect.ValueOf(fn).Elem()
	typ := value.Type()
	fields := reflectFields(typ)
	for i := 0; i < value.NumField(); i++ {
		field := &fields[i]
		if field.Name == "Body" || !resolvedTypeNameFieldMayContainAST(field.Type) {
			continue
		}
		if !value.Field(i).CanInterface() {
			return fmt.Errorf("unpublished function header field %s.%s", typ, field.Name)
		}
		if err := walk(value.Field(i), owner, "header", append(append(rootPath, '.'), field.Name...), false, 0); err != nil {
			return err
		}
	}
	if err := walk(reflect.ValueOf(fn.Body), owner, "body", rootPath, false, 0); err != nil {
		return err
	}
	return nil
}
