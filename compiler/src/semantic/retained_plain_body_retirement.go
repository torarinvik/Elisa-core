package semantic

import (
	"fmt"
	"reflect"

	"elisacore/src/ast"
)

// Invocation-local plan. Full product certification happens before any mutation;
// only nodes exclusively in the old body are deletion ownership. The stable
// declaration/header and its registration remain live through ordinary reanalysis.
type retainedPlainBodyRetirement struct {
	Product *retainedPlainDefaultProduct
	Nodes   []ast.Node
}

func (r *RetainedAnalysis) preflightPlainBodyRetirement(fn *ast.FuncDecl) (*retainedPlainBodyRetirement, error) {
	products, err := r.preflightPlainDefaultRemoval([]*ast.FuncDecl{fn})
	if err != nil {
		return nil, err
	}
	product := products[0]
	header := newSpecializationSyntax()
	header.prepare(reflect.ValueOf(fn.Params))
	header.prepare(reflect.ValueOf(fn.ReturnType))
	if err := header.validate(); err != nil {
		return nil, err
	}
	stable := map[ast.Node]bool{fn: true}
	for _, node := range header.prepared {
		stable[node] = true
	}
	body := newSpecializationSyntax()
	body.prepare(reflect.ValueOf(fn.Body))
	if err := body.validate(); err != nil {
		return nil, err
	}
	retirement := &retainedPlainBodyRetirement{Product: product}
	for _, node := range body.prepared {
		if stable[node] {
			return nil, fmt.Errorf("body shares source syntax with stable header")
		}
		retirement.Nodes = append(retirement.Nodes, node)
	}
	// Every nonheader owned syntax node must be in the retired body; a future
	// source lowering cannot silently fall outside the cleanup frontier.
	owned := map[ast.Node]bool{}
	for _, node := range retirement.Nodes {
		owned[node] = true
	}
	for _, node := range product.Nodes {
		if !stable[node] && !owned[node] {
			return nil, fmt.Errorf("owner has syntax outside header/body ownership")
		}
	}
	return retirement, nil
}

func (r *RetainedAnalysis) cleanupPlainBodyRetirement(retirement *retainedPlainBodyRetirement) error {
	if r == nil || r.a == nil || r.result == nil || retirement == nil || r.plainDefaults == nil || !r.plainDefaults.Updating {
		return fmt.Errorf("body retirement requires active canonical transaction")
	}
	// The complete old product was certified before mutations. Rechecking syntax
	// after source relocation would confuse coordinate changes with lost authority.
	for _, node := range retirement.Nodes {
		r.deletePlainBodyNodeRows(node)
	}
	owner := reflect.ValueOf(retirement.Product.Owner)
	fields := reflect.ValueOf(r.a).Elem()
	for i := 0; i < fields.NumField(); i++ {
		field := fields.Type().Field(i)
		if !plainDefaultBodyOwnerField(field.Name) {
			continue
		}
		table := settable(fields.Field(i))
		if table.Kind() != reflect.Map || !owner.Type().AssignableTo(table.Type().Key()) {
			return fmt.Errorf("body owner field %s changed schema", field.Name)
		}
		if !table.IsNil() {
			table.SetMapIndex(owner, reflect.Value{})
		}
	}
	// Body parameter observations are replaced, not accumulated. Plain defaults
	// cannot reference parameters, and the complete foreign-reference preflight
	// proves these local symbols are not an obligation of another live owner.
	for symbol := range retirement.Product.Symbols {
		if symbol == nil || symbol.Node != retirement.Product.Owner || symbol.Kind == SymbolFunc {
			continue
		}
		for i := 0; i < fields.NumField(); i++ {
			table := settable(fields.Field(i))
			if table.Kind() == reflect.Map && table.Type().Key() == reflect.TypeFor[*Symbol]() && !table.IsNil() {
				table.SetMapIndex(reflect.ValueOf(symbol), reflect.Value{})
			}
		}
	}
	return nil
}

func (r *RetainedAnalysis) deletePlainBodyNodeRows(node ast.Node) {
	key := reflect.ValueOf(node)
	for _, path := range specializationASTMapPaths() {
		table := specializationField(reflect.ValueOf(r.a).Elem(), path)
		if !table.IsNil() && key.Type().AssignableTo(table.Type().Key()) {
			table.SetMapIndex(key, reflect.Value{})
		}
	}
	fields := reflect.ValueOf(r.result).Elem()
	for i := 0; i < fields.NumField(); i++ {
		table := fields.Field(i)
		if fields.Type().Field(i).IsExported() && table.Kind() == reflect.Map && table.Type().Key().Implements(astNodeType) && !table.IsNil() && key.Type().AssignableTo(table.Type().Key()) {
			table.SetMapIndex(key, reflect.Value{})
		}
	}
}

func (r *RetainedAnalysis) verifyPlainBodyRetirement(retirement *retainedPlainBodyRetirement) error {
	// Reuse the exact raw-map absence oracle with only the retired body frontier;
	// the live function declaration and header are deliberately retained.
	frontier := *retirement.Product
	frontier.Nodes = retirement.Nodes
	return r.verifyPlainDefaultRemoval([]*retainedPlainDefaultProduct{&frontier})
}
