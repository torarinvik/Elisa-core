package semantic

import (
	"elisacore/src/ast"
	"reflect"
)

// targetedSpecializationMembership contains only exact witnesses and proven
// fresh destinations. Missing entries remain unknown, never inferred private.
// The maps belong to one publication; no retained/global ownership index exists.
func targetedSpecializationMembership(c *specializationContext) (map[ast.Node]bool, map[*Symbol]bool, map[*Scope]bool) {
	scan := &specializationPublication{canonicalMembershipScan: true, owned: map[ast.Node]bool{}, rootSymbols: map[*Symbol]bool{}, rootScopes: map[*Scope]bool{}, seen: map[specializationPointer]uint8{}}
	if c.base.GlobalScope != nil {
		scan.rootScopes[c.base.GlobalScope] = true
	}
	var tables []reflect.Value
	rv := reflect.ValueOf(c.base).Elem()
	for i := 0; i < rv.NumField(); i++ {
		if rv.Type().Field(i).PkgPath != "" {
			continue
		}
		table := rv.Field(i)
		if table.Kind() == reflect.Map && table.Type().Key().Implements(astNodeType) && !table.IsNil() {
			tables = append(tables, table)
		}
	}
	if c.base.analyzer != nil {
		for _, path := range specializationASTMapPaths() {
			table := specializationField(reflect.ValueOf(c.base.analyzer).Elem(), path)
			if !table.IsNil() {
				tables = append(tables, table)
			}
		}
	}
	// Each successful lookup is an exact path from a complete-catalog root.
	// Walk both entry endpoints using its existing policy, including opaque maps,
	// unexported fields, structural types and Symbol aliases. An absent key proves
	// nothing, and nominal/Scope pruning remains exactly the catalog's policy.
	for source := range c.syntax.originals {
		key := reflect.ValueOf(source)
		for _, table := range tables {
			if !key.Type().AssignableTo(table.Type().Key()) {
				continue
			}
			value := table.MapIndex(key)
			if !value.IsValid() {
				continue
			}
			scan.walk(key, false)
			scan.walk(value, false)
		}
	}
	// nodes is written only by specializationSyntax.copy and allocateSlice. Both
	// destinations are allocated after the immutable canonical root exists:
	// reflect.New or an element of reflect.MakeSlice's new backing allocation.
	// Borrowed-return paths never register a destination. Preserve any positive
	// witness if an invariant violation exposes the same node through a root edge.
	for _, destination := range c.syntax.nodes {
		if !scan.owned[destination] {
			scan.owned[destination] = false
		}
	}
	return scan.owned, scan.rootSymbols, scan.rootScopes
}

func (p *specializationPublication) canonicalNodeMember(node ast.Node) bool {
	if p.context.base.shared == nil {
		return p.rootNodes[node]
	}
	if answer, known := p.rootNodes[node]; known {
		return answer
	}
	// Unknown derived syntax must consult the complete current-root graph. This
	// includes future/opaque metadata and detached/pristine references. Its
	// existing shared-session lock/memo remains the sole fallback authority.
	answer := canonicalSpecializationNodes(p.context.base)[node]
	p.rootNodes[node] = answer
	return answer
}

func (p *specializationPublication) canonicalIdentityMembership() (map[*Symbol]bool, map[*Scope]bool) {
	root := p.context.base
	if root.shared != nil {
		canonicalSpecializationNodes(root)
		return root.shared.canonicalSymbols, root.shared.canonicalScopes
	}
	return canonicalSpecializationIdentities(root)
}

func (p *specializationPublication) canonicalSymbolMember(symbol *Symbol) bool {
	if p.context.base.shared == nil {
		return p.rootSymbols[symbol]
	}
	if answer, known := p.rootSymbols[symbol]; known {
		return answer
	}
	// Matching the actual root map edge is a witness; matching a spelling alone
	// cannot authorize identity borrowing or pruning a Symbol's syntax edges.
	root := p.context.base
	if symbol != nil && root.GlobalScope != nil && root.GlobalScope.Symbols[symbol.Name] == symbol {
		p.rootSymbols[symbol] = true
		return true
	}
	symbols, _ := p.canonicalIdentityMembership()
	answer := symbols[symbol]
	p.rootSymbols[symbol] = answer
	return answer
}

func (p *specializationPublication) canonicalScopeMember(scope *Scope) bool {
	if p.context.base.shared == nil {
		return p.rootScopes[scope]
	}
	if answer, known := p.rootScopes[scope]; known {
		return answer
	}
	_, scopes := p.canonicalIdentityMembership()
	answer := scopes[scope]
	p.rootScopes[scope] = answer
	return answer
}
