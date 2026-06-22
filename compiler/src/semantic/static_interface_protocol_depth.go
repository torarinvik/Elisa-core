package semantic

import (
	"elisacore/src/ast"
)

// foldInterfaceBases implements PROTOCOL INHERITANCE (`protocol Ord: Eq:`): every base protocol's
// members (associated types + methods, including their default bodies) are folded into the derived
// protocol's member set. The effect is that a single `impl Ord for T` must satisfy the union of Eq's
// and Ord's members, and Eq's methods become resolvable on an Ord receiver — without the impl having
// to name Eq separately. Folding runs after all interfaces are collected (so a base may be declared
// in any order) and resolves each base name within the deriving interface's own resolution context.
//
// Cycles (`protocol A: B` / `protocol B: A`) are reported and broken so folding terminates.
func (a *Analyzer) foldInterfaceBases(decls []scopedDecl) {
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.InterfaceDecl)
		if !ok || len(decl.Bases) == 0 {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			qualifiedName := joinQualifiedName(scoped.Namespace, decl.Name)
			iface, ok := a.staticInterfaces[qualifiedName]
			if !ok || iface == nil {
				return
			}
			// Resolve base names to their canonical qualified names up front so the fold pass and
			// later conformance share one identity for each base.
			resolved := make([]string, 0, len(decl.Bases))
			for _, baseName := range decl.Bases {
				base, baseQualified, ok := a.lookupVisibleStaticInterface(baseName)
				if !ok || base == nil {
					a.errorf(decl.Pos(), "%s", UnknownInterfaceMessage(baseName))
					continue
				}
				if baseQualified == qualifiedName {
					a.errorf(decl.Pos(), "protocol %q cannot inherit from itself", decl.Name)
					continue
				}
				resolved = append(resolved, baseQualified)
			}
			iface.Bases = resolved
		})
	}
	// Transitively fold members from bases into each interface. Done as a fixpoint over the now-
	// resolved Bases so a chain A: B: C contributes C's members to A. A visiting set per root
	// guards against inheritance cycles.
	for name, iface := range a.staticInterfaces {
		if iface == nil {
			continue
		}
		visiting := map[string]bool{}
		a.foldInterfaceBaseMembers(name, iface, visiting)
	}
}

// foldInterfaceBaseMembers recursively merges each base interface's methods and associated types
// into iface. Own members win over inherited ones (an override at the deriving protocol shadows the
// base). Returns once iface has the transitive closure of its bases' members.
func (a *Analyzer) foldInterfaceBaseMembers(name string, iface *StaticInterface, visiting map[string]bool) {
	if iface == nil || len(iface.Bases) == 0 {
		return
	}
	if visiting[name] {
		// Cycle already reported by foldInterfaceBases' self-check at one hop; deeper cycles are
		// broken silently here to keep folding total.
		return
	}
	visiting[name] = true
	defer delete(visiting, name)
	for _, baseName := range iface.Bases {
		base, ok := a.staticInterfaces[baseName]
		if !ok || base == nil {
			continue
		}
		a.foldInterfaceBaseMembers(baseName, base, visiting)
		for assocName, assoc := range base.AssociatedTypes {
			if _, exists := iface.AssociatedTypes[assocName]; !exists {
				iface.AssociatedTypes[assocName] = assoc
			}
		}
		for methodName, method := range base.Methods {
			if _, exists := iface.Methods[methodName]; !exists {
				iface.Methods[methodName] = method
			}
		}
		// Fold inherited LAWS (docs/85 P3) the same way as methods: a derived protocol owes its
		// bases' algebraic obligations too, so an `impl Hash for T` must discharge Eq's laws, and a
		// generic `[T: Hash]` may assume them. Dedup by FuncDecl identity so DIAMOND inheritance
		// (two bases sharing a base) contributes each shared law exactly once.
		for _, law := range base.Laws {
			if law == nil || ifaceHasLaw(iface, law) {
				continue
			}
			iface.Laws = append(iface.Laws, law)
		}
	}
}

// ifaceHasLaw reports whether the interface's law set already contains this exact law decl (pointer
// identity). Used to dedup laws folded up a diamond so a shared base's law is not double-counted.
func ifaceHasLaw(iface *StaticInterface, law *ast.FuncDecl) bool {
	for _, existing := range iface.Laws {
		if existing == law {
			return true
		}
	}
	return false
}

// receiverTypeExprName returns the bare head name of an impl's receiver type expression
// (`Num` for `Num`, `Box` for `Box[T]`), used to rewrite a default body's `Self`-qualified calls
// to dispatch against this type's impl methods. Returns "" for receiver shapes without a simple head.
func receiverTypeExprName(expr ast.TypeExpr) string {
	switch n := expr.(type) {
	case *ast.NamedType:
		return n.Name
	case *ast.GenericType:
		return n.Name
	default:
		return ""
	}
}

// synthesizeDefaultImplMembers implements DEFAULT METHODS: for every interface impl, each protocol
// method that carries a default body and is NOT provided by the impl gets a synthesized impl method
// whose body is a fresh clone of the default (with `Self` rewritten to the impl's receiver type in
// the signature). A type that provides the method overrides the default; one that omits it inherits
// it. Inherited defaults (from base protocols, folded by foldInterfaceBases) are handled uniformly
// because they already live in the interface's method set.
//
// This mirrors synthesizeDerivedImplMembers exactly: synthesized FuncDecl members are appended to the
// impl's member list before value-symbol collection, so they flow through the normal impl-method
// symbol/codegen path with no special casing downstream.
func (a *Analyzer) synthesizeDefaultImplMembers(decls []scopedDecl) {
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.ImplDecl)
		if !ok || decl == nil || decl.IsExtension() {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			iface, _, ok := a.lookupVisibleStaticInterface(decl.InterfaceName)
			if !ok || iface == nil {
				return
			}
			provided := implMethodNameSet(decl)
			selfReplacement := map[string]ast.TypeExpr{staticInterfaceSelfName: decl.ForType}
			// `Self` used as a VALUE in the body (the qualified-call receiver `Self.m(self, …)`)
			// is rewritten to the receiver type's name, so the synthesized body dispatches to this
			// type's impl methods (`Num.m(self, …)`). Bodyless receivers (e.g. plain `self`) are
			// untouched.
			var bodySubst map[string]ast.Expr
			if name := receiverTypeExprName(decl.ForType); name != "" {
				bodySubst = map[string]ast.Expr{staticInterfaceSelfName: &ast.Ident{Position: decl.Position, Name: name}}
			}
			synthesized := make([]ast.ImplMember, 0, len(iface.Methods))
			for methodName, method := range iface.Methods {
				if method == nil || method.Default == nil {
					continue
				}
				if provided[methodName] {
					continue
				}
				def := method.Default
				body, ok := ast.CloneBlockSubst(def.Body, bodySubst)
				if !ok {
					// A default body using a statement shape outside the clone set cannot be
					// instantiated; require the type to provide the method explicitly.
					a.errorf(decl.Pos(), "default method %q of protocol %q has a body that cannot be inherited; provide %q explicitly in this impl", methodName, iface.Name, methodName)
					continue
				}
				params := make([]ast.ParamDecl, 0, len(def.Params))
				for _, param := range def.Params {
					params = append(params, ast.ParamDecl{Position: param.Position, Name: param.Name, Mutable: param.Mutable, Type: substituteAssocTypeExpr(param.Type, selfReplacement), DefaultValue: param.DefaultValue})
				}
				synthesized = append(synthesized, &ast.FuncDecl{
					Position:         def.Position,
					Name:             def.Name,
					TypeParams:       append([]string(nil), def.TypeParams...),
					RegionParams:     append([]string(nil), def.RegionParams...),
					PermissionParams: append([]string(nil), def.PermissionParams...),
					GenericParams:    append([]ast.GenericParam(nil), def.GenericParams...),
					Permissions:      append([]ast.PermissionRef(nil), def.Permissions...),
					Ensures:          append([]ast.EnsuresClause(nil), def.Ensures...),
					Params:           params,
					ReturnType:       substituteAssocTypeExpr(def.ReturnType, selfReplacement),
					Body:             body,
				})
				provided[methodName] = true
			}
			if len(synthesized) != 0 {
				decl.Members = append(decl.Members, synthesized...)
			}
		})
	}
}
