package semantic

import (
	"elisacore/src/ast"
)

// Protocol DEFAULT-method contract soundness (docs P1/A4).
//
// A protocol may give a method a default body that itself carries a contract:
//
//	protocol Comparable: Ord:
//	    def max(self: Self, other: Self) -> Self:
//	        ensure self.le(result) and other.le(result)
//	        if self.le(other): return other
//	        return self
//
// The default impl is a REAL function body, so its `ensure` must be discharged against THAT body —
// exactly once, at the protocol declaration site, independent of any concrete impl. If the default
// body fails its own postcondition the protocol is itself unsound (every type that inherits the
// default would silently inherit a broken guarantee), so it is rejected here.
//
// The check models the default as the generic function it morally is: `def max[Self: P](self: Self,
// other: Self) -> Self`. Binding `Self` to a fresh type param bounded by the protocol P (its full
// folded member + law set) lets the body's `self.le(...)` resolve against P's methods and lets the
// ensure discharge assume P's laws — the same machinery a `[T: P]` generic uses. This runs after
// foldInterfaceBases so an inherited member/law used by the default (e.g. `le` from Ord) is in scope.
//
// Overriding impls are separately variance-checked: collectStaticImpls already runs
// checkProtocolImplContractVariance over every method in iface.Methods (which includes a default's
// signature+contract), so an impl that overrides `max` must refine the default's contract.
func (a *Analyzer) checkProtocolDefaultMethodContracts(decls []scopedDecl) {
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.InterfaceDecl)
		if !ok || decl == nil {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			qualifiedName := joinQualifiedName(scoped.Namespace, decl.Name)
			iface, ok := a.staticInterfaces[qualifiedName]
			if !ok || iface == nil {
				return
			}
			for _, member := range iface.Methods {
				if member == nil || member.Default == nil {
					continue
				}
				// Only methods declared ON this protocol (not folded-in base defaults) carry a body
				// whose contract is this protocol's responsibility; a base's default is checked when
				// its own InterfaceDecl is visited. iface.Methods includes inherited entries, so skip
				// any whose default body was declared on a different protocol.
				if !defaultDeclaredHere(decl, member.Default) {
					continue
				}
				a.checkOneDefaultMethodContract(qualifiedName, member.Default)
			}
		})
	}
}

// defaultDeclaredHere reports whether the default FuncDecl is a direct member of this InterfaceDecl
// (rather than one folded in from a base), so its contract is discharged exactly once at its own site.
func defaultDeclaredHere(decl *ast.InterfaceDecl, def *ast.FuncDecl) bool {
	for _, m := range decl.Members {
		if fn, ok := m.(*ast.FuncDecl); ok && fn == def {
			return true
		}
	}
	return false
}

// checkOneDefaultMethodContract discharges one default method's contract against its own body by
// re-analyzing it as a generic function with `Self` bound to the enclosing protocol. Only methods
// that actually carry a value contract (`requires`/`ensure`) are worth re-analyzing; a contract-free
// default body is already exercised when synthesized into an impl, and re-running it here would only
// duplicate diagnostics.
func (a *Analyzer) checkOneDefaultMethodContract(protocol string, def *ast.FuncDecl) {
	if def == nil || def.Body == nil {
		return
	}
	if len(def.Requires) == 0 && len(def.EnsureValues) == 0 {
		return
	}
	synth := synthesizeSelfGenericDefault(protocol, def)
	if synth == nil {
		return
	}
	// analyzeFunc resolves the function's signature via funcDeclSymbols; the synthesized default is
	// not a global, so register its FuncType (built with the `Self: protocol` generic binding) for
	// the duration of this check. The body/contract analysis then runs exactly as for any generic
	// function: method calls on `Self`-typed values resolve through the protocol bound, and the
	// ensure is discharged against the body (with the protocol's laws assumable).
	a.withGenericParams(synth.GenericParams, nil, func() {
		fnType := a.funcTypeFromDecl(protocol+".default."+synth.Name, synth.TypeParams, synth.GenericParams, synth.RegionParams, synth.PermissionParams, synth.Permissions, synth.Ensures, synth.Params, synth.ReturnType, false)
		if fnType == nil {
			return
		}
		a.funcDeclSymbols[synth] = &Symbol{Name: synth.Name, Kind: SymbolFunc, Type: fnType}
		a.analyzeFunc(synth)
		delete(a.funcDeclSymbols, synth)
	})
}

// synthesizeSelfGenericDefault produces a generic-function view of a protocol default method, binding
// its implicit `Self` to a type param bounded by the protocol. The body/params/contract are reused
// by reference (the body is analyzed, not mutated); only the GenericParams are augmented with the
// `Self: P` binding so method/law resolution on `Self`-typed values works exactly as in a `[T: P]`
// generic. Returns nil if the method already declares its own `Self` type param (defensive; protocol
// methods do not).
func synthesizeSelfGenericDefault(protocol string, def *ast.FuncDecl) *ast.FuncDecl {
	for _, gp := range def.GenericParams {
		if gp.Name == staticInterfaceSelfName {
			return nil
		}
	}
	for _, tp := range def.TypeParams {
		if tp == staticInterfaceSelfName {
			return nil
		}
	}
	selfBinding := ast.GenericParam{
		Position:       def.Position,
		Kind:           ast.GenericParamType,
		Name:           staticInterfaceSelfName,
		InterfaceBound: protocol,
	}
	clone := *def
	clone.GenericParams = append([]ast.GenericParam{selfBinding}, def.GenericParams...)
	return &clone
}
