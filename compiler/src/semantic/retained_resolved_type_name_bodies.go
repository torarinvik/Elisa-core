package semantic

import (
	"crypto/sha256"
	"elisacore/src/ast"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
)

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
			fields := reflectFields(typ)
			for i := 0; i < value.NumField(); i++ {
				field := &fields[i]
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
			sourceDigest, sourceErr := functionSyntaxDigest(sourceExpr)
			preparedDigest, preparedErr := functionSyntaxDigest(preparedExpr)
			if sourceErr != nil || preparedErr != nil || sourceDigest != preparedDigest {
				return nil, fmt.Errorf("fast-prepared body changes TypeExpr syntax")
			}
			owners[preparedExpr] = resolvedTypeNameSourceOwner{owner: "fn:" + key, region: "body", firstPath: path}
		}
	}
	return owners, nil
}

// resolvedTypeNameSyntaxKey names one TypeExpr's pointer-free syntax. Two
// occurrences in one function resolve through the same declaration scope, so
// equal keys may be rebound onto one another by a body edit.
func resolvedTypeNameSyntaxKey(expr ast.TypeExpr) (string, error) {
	if expr == nil {
		return "", fmt.Errorf("missing type expression")
	}
	digest, err := functionSyntaxDigest(expr)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\x00%s", reflect.TypeOf(expr), hex.EncodeToString(digest[:])), nil
}

// resolvedTypeNameSyntaxKeysOf collects the syntax keys of every TypeExpr in an
// AST value. It is the witness inventory a body edit is matched against.
func resolvedTypeNameSyntaxKeysOf(value any) (map[string]bool, error) {
	keys := map[string]bool{}
	if err := visitResolvedTypeNameSyntax(value, func(key string, _ ast.TypeExpr) { keys[key] = true }); err != nil {
		return nil, err
	}
	return keys, nil
}

func visitResolvedTypeNameSyntax(value any, visit func(string, ast.TypeExpr)) error {
	active := map[resolvedTypeNamePointer]bool{}
	work := 0
	var walk func(reflect.Value, int) error
	walk = func(value reflect.Value, depth int) error {
		work++
		if work > 1_000_000 {
			return fmt.Errorf("AST type syntax work budget exceeded")
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
				return walk(value.Elem(), depth+1)
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
				key, err := resolvedTypeNameSyntaxKey(expr)
				if err != nil {
					return err
				}
				visit(key, expr)
			}
			err := walk(value.Elem(), depth+1)
			delete(active, ptr)
			return err
		case reflect.Struct:
			if typ.PkgPath() != resolvedTypeNameASTPackage {
				return nil
			}
			fields := reflectFields(typ)
			for i := 0; i < value.NumField(); i++ {
				field := &fields[i]
				if !resolvedTypeNameFieldMayContainAST(field.Type) {
					continue
				}
				if !value.Field(i).CanInterface() {
					return fmt.Errorf("unpublished AST field %s.%s", typ, field.Name)
				}
				if err := walk(value.Field(i), depth+1); err != nil {
					return err
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < value.Len(); i++ {
				if err := walk(value.Index(i), depth+1); err != nil {
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
				if err := walk(value.MapIndex(key), depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(reflect.ValueOf(value), 0)
}

// resolvedTypeNameHeaderSyntaxKeys is the source header half of a function's
// witness inventory. The comparison projection keeps headers.
func resolvedTypeNameHeaderSyntaxKeys(fn *ast.FuncDecl) (map[string]bool, error) {
	keys := map[string]bool{}
	if fn == nil {
		return nil, fmt.Errorf("missing function")
	}
	value := reflect.ValueOf(fn).Elem()
	typ := value.Type()
	fields := reflectFields(typ)
	for i := 0; i < value.NumField(); i++ {
		field := &fields[i]
		if field.Name == "Body" || !resolvedTypeNameFieldMayContainAST(field.Type) {
			continue
		}
		if !value.Field(i).CanInterface() {
			return nil, fmt.Errorf("unpublished function header field %s.%s", typ, field.Name)
		}
		fieldKeys, err := resolvedTypeNameSyntaxKeysOf(value.Field(i).Interface())
		if err != nil {
			return nil, err
		}
		for key := range fieldKeys {
			keys[key] = true
		}
	}
	return keys, nil
}

// resolvedTypeNameBodySyntaxInventory encodes the source body's syntax-key set
// for comparison states whose bodies are projected away.
func resolvedTypeNameBodySyntaxInventory(fn *ast.FuncDecl) (string, error) {
	if fn == nil {
		return "", fmt.Errorf("missing function")
	}
	keys, err := resolvedTypeNameSyntaxKeysOf(fn.Body)
	if err != nil {
		return "", err
	}
	return encodeResolvedTypeNameSyntaxKeys(keys)
}

func encodeResolvedTypeNameSyntaxKeys(keys map[string]bool) (string, error) {
	sorted := make([]string, 0, len(keys))
	for key := range keys {
		sorted = append(sorted, key)
	}
	sort.Strings(sorted)
	encoded, err := json.Marshal(sorted)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func decodeResolvedTypeNameSyntaxKeys(encoded string) (map[string]bool, error) {
	if encoded == "" {
		return nil, fmt.Errorf("missing type-syntax inventory")
	}
	var sorted []string
	if err := json.Unmarshal([]byte(encoded), &sorted); err != nil {
		return nil, fmt.Errorf("type-syntax inventory: %w", err)
	}
	keys := make(map[string]bool, len(sorted))
	for _, key := range sorted {
		keys[key] = true
	}
	return keys, nil
}

// resolvedTypeNamePreparedBodyContribution re-encodes the declaration-phase
// body rows the fast preparation just recorded on this body. Rows owned by a
// shared declaration node are excluded exactly as captureResolvedTypeNameContributions
// excludes them.
func resolvedTypeNamePreparedBodyContribution(a *Analyzer, functionKey string, sourceOwners map[ast.TypeExpr]resolvedTypeNameSourceOwner, body []ast.Stmt) (string, error) {
	if a == nil || functionKey == "" {
		return "", fmt.Errorf("missing prepared body owner")
	}
	locations, err := resolvedTypeNameBodyTypeExprPaths(body)
	if err != nil {
		return "", err
	}
	ownerKey := "fn:" + functionKey
	rows := make([]string, 0)
	for expr, paths := range locations {
		canonical := a.resolvedTypeNames[expr]
		if canonical == "" {
			continue
		}
		if owner, known := sourceOwners[expr]; known {
			if owner.sourceAmbiguous() || owner.owner != ownerKey || owner.region != "body" {
				continue
			}
		}
		digest, err := functionSyntaxDigest(expr)
		if err != nil {
			return "", fmt.Errorf("prepared body type syntax: %w", err)
		}
		sort.Strings(paths)
		pathBytes, _ := json.Marshal(paths)
		rows = append(rows, fmt.Sprintf("%s\x00%s\x00%s\x00%s", string(pathBytes), reflect.TypeOf(expr), hex.EncodeToString(digest[:]), canonical))
	}
	sort.Strings(rows)
	encoded, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
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
		digest, err := functionSyntaxDigest(expr)
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
		if sourceOwner.sourceAmbiguous() || sourceOwner.sourcePath() == "" {
			return nil, fmt.Errorf("old body TypeExpr has no unique source path")
		}
		newExpr := newPaths[sourceOwner.sourcePath()]
		if newExpr == nil {
			return nil, fmt.Errorf("prepared body lost a resolved type-name path")
		}
		oldDigest, oldErr := functionSyntaxDigest(expr)
		newDigest, newErr := functionSyntaxDigest(newExpr)
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
		if !ok || owner.sourceAmbiguous() {
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
