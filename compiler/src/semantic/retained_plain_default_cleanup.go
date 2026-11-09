package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
	"fmt"
	"reflect"
)

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
