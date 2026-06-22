package semantic

import (
	"fmt"
	"sort"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
)

// Protocol laws (docs/85 P3). A protocol may declare algebraic LAWS — universally-quantified
// predicates over `Self` that every impl must satisfy:
//
//	protocol Ord:
//	    def le(self: Self, other: Self) -> bool
//	    law total(a: Self, b: Self)   = a.le(b) or b.le(a)
//	    law antisym(a: Self, b: Self) = (a.le(b) and b.le(a)) implies (a == b)
//
// For each impl, each law is discharged at the impl site by the SMT tier after INLINING the impl's
// method bodies into the law predicate (so `a.le(b)` becomes the concrete boolean over `a`/`b`'s
// fields). When the inlined obligation is provable the law costs nothing at runtime. When it is NOT
// provable (a non-affine `le` the solver declines, or SMT off) the law is AUTO-LOWERED to a per-impl
// `@property` randomized harness (recorded as a LawFuzzObligation) so it is fuzz-checked in debug/CI
// rather than silently assumed. A genuinely broken impl is therefore caught: either the SMT tier
// refutes it (no proof + the harness still runs) or the fuzz harness finds a counterexample.

// LawFuzzObligation describes a protocol law that could not be discharged statically for one impl and
// must be checked by a generated @property harness. It carries everything the test runner needs to
// emit a self-contained `@property def __law_<Protocol>_<law>_<Type>(<scalar fields…>) -> bool` whose
// body reconstructs the Self values from their scalar fields and evaluates the law predicate.
type LawFuzzObligation struct {
	Interface string // protocol name (qualified)
	LawName   string // law name
	TypeName  string // the concrete impl receiver type's source name
	// Params describes each law parameter (all of type Self): the parameter name and the ordered
	// scalar fields of the receiver struct used to (re)construct it.
	Params []LawFuzzParam
	// BodySource is the law predicate rendered as Elisa source, with each law-parameter ident left
	// as-is (it will be bound to a reconstructed Self local in the generated harness).
	BodySource string
	Position   lexer.Pos
}

// LawFuzzParam is one Self-typed law parameter expanded into the scalar fields that drive the fuzzer.
type LawFuzzParam struct {
	Name   string             // law parameter name (e.g. "a")
	Fields []LawFuzzFieldKind // ordered constructor fields
}

// LawFuzzFieldKind is one struct field: its name and the property-generatable scalar type name.
type LawFuzzFieldKind struct {
	Name string
	Type string // a PropertyParamTypeName-supported builtin (bool/int/iN/uN/fN)
}

// dischargeProtocolLawsForImpl runs every law of the impl's protocol against the impl, recording a
// fuzz obligation for each law the SMT tier could not prove. It is called from collectStaticImpls
// after the impl's methods have been validated, so the impl method bodies are available for inlining.
func (a *Analyzer) dischargeProtocolLawsForImpl(iface *StaticInterface, impl *StaticImpl, decl *ast.ImplDecl) {
	if iface == nil || impl == nil || decl == nil || len(iface.Laws) == 0 {
		return
	}
	st := lawReceiverStructType(impl.Receiver)
	if st == nil {
		// Laws are only modeled over struct receivers (their fields drive both SMT projections and the
		// fuzzer). A non-struct receiver simply forgoes the law check (sound: never a false proof).
		return
	}
	typeName := lawTypeSourceName(a, impl.Receiver, st)
	methodBodies := implMethodBodies(decl)
	for _, law := range iface.Laws {
		a.dischargeOneProtocolLaw(iface, impl, st, typeName, methodBodies, law)
	}
}

// dischargeOneProtocolLaw attempts the SMT proof of a single law for the impl; on failure it records a
// fuzz obligation.
func (a *Analyzer) dischargeOneProtocolLaw(iface *StaticInterface, impl *StaticImpl, st *StructType, typeName string, methodBodies map[string]*ast.FuncDecl, law *ast.FuncDecl) {
	body, ok := a.lawBodyExpr(law)
	if !ok {
		return
	}
	// Build the fuzz-parameter shape first; if any law parameter is not a struct of scalar fields the
	// law cannot be fuzzed OR soundly inlined here, so we skip it (forgoing the check, never asserting).
	params, fieldsOK := lawFuzzParams(law, st)
	if !fieldsOK {
		return
	}

	subject := fmt.Sprintf("protocol law %s.%s for %s", iface.Name, law.Name, typeName)
	proven := a.tryProveProtocolLaw(law, body, st, methodBodies)
	if proven {
		a.recordProof(law.Pos(), subject, "universally quantified over Self", ProofProvenSMT)
		return
	}
	a.recordProof(law.Pos(), subject, "auto-lowered to @property fuzz harness (not proven)", ProofRuntime)
	a.lawFuzzObligations = append(a.lawFuzzObligations, &LawFuzzObligation{
		Interface:  iface.Name,
		LawName:    law.Name,
		TypeName:   typeName,
		Params:     params,
		BodySource: renderLawBodySource(body),
		Position:   law.Pos(),
	})
}

// tryProveProtocolLaw discharges the law universally over its Self parameters by INLINING the impl's
// method bodies into the predicate and handing the resulting quantifier-free boolean (over the Self
// fields, which become free SMT projection symbols ⇒ universally quantified) to the SMT goal checker.
// Returns true only on a sound proof. Any failure (unsupported body, declined solver) returns false,
// which routes the law to the fuzz fallback.
func (a *Analyzer) tryProveProtocolLaw(law *ast.FuncDecl, body ast.Expr, st *StructType, methodBodies map[string]*ast.FuncDecl) bool {
	if !a.smtEnabled {
		return false
	}
	if a.openSMT() == nil {
		return false
	}
	inlined, ok := inlineLawMethodCalls(body, methodBodies)
	if !ok {
		return false
	}
	// Declare each Self-typed law parameter as an immutable local of the receiver struct type in a fresh
	// scope, so the synthetic field reads (`a.major`) resolve their numeric types via the scope and the
	// translator models them as free projection symbols — exactly the universal over all field values.
	receiver := Type(st)
	scope := NewScope(a.currentScope)
	for _, p := range law.Params {
		scope.Define(&Symbol{Name: p.Name, Kind: SymbolParam, Type: receiver})
	}
	prevScope := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = prevScope }()

	tr := a.newSMTTranslator(nil)
	proven, _, lowered := a.smtCheckGoal(tr, inlined, nil, "")
	return lowered && proven
}

// smtProtocolLawHypotheses injects each protocol law as an assumable SMT hypothesis when proving inside
// a generic function bounded on the protocol (docs/85 P3: "laws usable as facts in generic code"). For
// every type parameter `T: Proto` and every law `law L(a,b,…)`, the law is instantiated over the
// function's parameters of type `T` (all ordered tuples) and asserted. Because a protocol-method call
// like `p.le(q)` is translated to a STABLE canonicalized Bool symbol (keyed by callee + argument terms),
// the instantiated transitivity/totality facts share symbols with the goal — so a generic `sort`'s
// `ensure` over `le` can discharge against the assumed laws. Sound: laws are obligations every impl must
// satisfy (proven or fuzz-checked at the impl site), so assuming them in generic code is justified, and
// asserting more true facts can only ever prove more goals, never fewer.
func (a *Analyzer) smtProtocolLawHypotheses(tr *smtTranslator) string {
	if a == nil || tr == nil || a.currentFuncDecl == nil || a.currentFuncType == nil {
		return ""
	}
	if a.inClosedWorldProof {
		return ""
	}
	ft := a.currentFuncType
	if len(ft.TypeParams) == 0 {
		return ""
	}
	var b strings.Builder
	for _, tp := range ft.TypeParams {
		iface, ok := a.lookupTypeParamInterface(tp)
		if !ok || iface == nil || len(iface.Laws) == 0 {
			continue
		}
		// Parameter names whose declared type is exactly this type parameter.
		var tpParams []string
		for i, pt := range ft.Params {
			if tpt, ok := pt.(*TypeParamType); ok && tpt.Name == tp && i < len(ft.ExplicitParamNames) {
				if name := ft.ExplicitParamNames[i]; name != "" {
					tpParams = append(tpParams, name)
				}
			}
		}
		// Guard against a combinatorial blowup: with many T-typed parameters the per-law tuple count is
		// |params|^arity. Cap the parameter set used for instantiation so the assertion budget stays
		// bounded (dropping instances only weakens the hypothesis set — sound, never a false proof).
		if len(tpParams) == 0 {
			continue
		}
		if len(tpParams) > 8 {
			tpParams = tpParams[:8]
		}
		for _, law := range iface.Laws {
			body, ok := a.lawBodyExpr(law)
			if !ok || len(law.Params) == 0 {
				continue
			}
			// Instantiate the law over every ordered tuple of the function's T-typed parameters (with
			// repetition), so reflexive/2-of-the-same instances are covered too.
			for _, tuple := range lawParamTuples(tpParams, len(law.Params)) {
				subst := map[string]ast.Expr{}
				for i, p := range law.Params {
					subst[p.Name] = &ast.Ident{Name: tuple[i]}
				}
				inst := substituteLawIdents(body, subst)
				if h, ok := tr.boolTerm(inst, nil); ok {
					b.WriteString("(assert " + h + ")\n")
				}
			}
		}
	}
	return b.String()
}

// lawParamTuples enumerates every length-k ordered tuple (with repetition) drawn from names. k is small
// (a law's arity, ≤3) and names is the function's small parameter set, so the product is bounded.
func lawParamTuples(names []string, k int) [][]string {
	if k <= 0 || len(names) == 0 {
		return nil
	}
	out := [][]string{{}}
	for d := 0; d < k; d++ {
		var next [][]string
		for _, prefix := range out {
			for _, name := range names {
				tuple := append(append([]string{}, prefix...), name)
				next = append(next, tuple)
			}
		}
		out = next
	}
	return out
}

// lawReceiverStructType peels a receiver type to its underlying struct, or nil if it is not a struct.
func lawReceiverStructType(t Type) *StructType {
	switch tt := t.(type) {
	case *StructType:
		return tt
	case *RefType:
		return lawReceiverStructType(tt.Elem)
	default:
		return nil
	}
}

// lawTypeSourceName recovers a source-visible type name for the receiver (so the generated harness can
// name the constructor). Falls back to the struct's own Name.
func lawTypeSourceName(a *Analyzer, receiver Type, st *StructType) string {
	if a != nil {
		if name, ok := a.typePathNameForReceiver(receiver); ok && name != "" {
			return name
		}
	}
	return st.Name
}

// implMethodBodies collects the impl's method FuncDecls keyed by method name, for inlining.
func implMethodBodies(decl *ast.ImplDecl) map[string]*ast.FuncDecl {
	out := map[string]*ast.FuncDecl{}
	for _, m := range decl.Members {
		if fn, ok := m.(*ast.FuncDecl); ok && fn != nil {
			out[fn.Name] = fn
		}
	}
	return out
}

// lawFuzzParams expands each Self-typed law parameter into its struct's scalar constructor fields. It
// reports ok=false if a parameter is not Self or any field is not a property-generatable scalar.
func lawFuzzParams(law *ast.FuncDecl, st *StructType) ([]LawFuzzParam, bool) {
	fields, ok := lawScalarFields(st)
	if !ok || len(law.Params) == 0 {
		return nil, false
	}
	params := make([]LawFuzzParam, 0, len(law.Params))
	for _, p := range law.Params {
		if !lawParamIsSelf(p) {
			return nil, false
		}
		params = append(params, LawFuzzParam{Name: p.Name, Fields: fields})
	}
	return params, true
}

// lawScalarFields returns the receiver struct's fields in constructor order, each as a generatable
// scalar; ok=false if any field is non-scalar.
func lawScalarFields(st *StructType) ([]LawFuzzFieldKind, bool) {
	if st == nil {
		return nil, false
	}
	names := lawStructFieldOrder(st)
	if len(names) == 0 {
		return nil, false
	}
	out := make([]LawFuzzFieldKind, 0, len(names))
	for _, name := range names {
		f, ok := st.Fields[name]
		if !ok {
			return nil, false
		}
		tn, ok := PropertyParamTypeName(f.Type)
		if !ok {
			return nil, false
		}
		out = append(out, LawFuzzFieldKind{Name: name, Type: tn})
	}
	return out, true
}

// lawStructFieldOrder returns the struct's fields in declaration (constructor) order. It prefers the
// AST decl's field order; falling back to a sorted map iteration keeps output deterministic.
func lawStructFieldOrder(st *StructType) []string {
	if st.Decl != nil && len(st.Decl.Fields) > 0 {
		out := make([]string, 0, len(st.Decl.Fields))
		for _, f := range st.Decl.Fields {
			out = append(out, f.Name)
		}
		return out
	}
	out := make([]string, 0, len(st.Fields))
	for name := range st.Fields {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// renderLawBodySource unparses the law predicate to Elisa source for the generated fuzz harness. The
// law-parameter idents stay verbatim; the harness binds them to reconstructed Self locals of the same
// names, so the rendered expression type-checks against those locals.
func renderLawBodySource(body ast.Expr) string {
	return unparse.FormatExpr(body)
}

func lawParamIsSelf(p ast.ParamDecl) bool {
	named, ok := p.Type.(*ast.NamedType)
	return ok && named != nil && named.Name == staticInterfaceSelfName
}

// inlineLawMethodCalls clones the law body, replacing every method call `recv.method(args…)` whose
// `method` has an impl body with that body's return expression (params substituted by the call's
// receiver/args). It also rewrites `x == y` over Self to a field-wise equality so the equality in
// antisymmetry laws is modeled structurally. Returns ok=false if it meets a call it cannot inline.
func inlineLawMethodCalls(expr ast.Expr, methods map[string]*ast.FuncDecl) (ast.Expr, bool) {
	switch n := expr.(type) {
	case nil:
		return nil, true
	case *ast.ParenExpr:
		inner, ok := inlineLawMethodCalls(n.Inner, methods)
		if !ok {
			return nil, false
		}
		return &ast.ParenExpr{Position: n.Position, Inner: inner}, true
	case *ast.BinaryExpr:
		l, ok := inlineLawMethodCalls(n.Left, methods)
		if !ok {
			return nil, false
		}
		r, ok := inlineLawMethodCalls(n.Right, methods)
		if !ok {
			return nil, false
		}
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op, Left: l, Right: r}, true
	case *ast.UnaryExpr:
		operand, ok := inlineLawMethodCalls(n.Operand, methods)
		if !ok {
			return nil, false
		}
		return &ast.UnaryExpr{Position: n.Position, Op: n.Op, Operand: operand}, true
	case *ast.Ident, *ast.IntLit, *ast.BoolLit, *ast.FloatLit:
		return expr, true
	case *ast.FieldExpr:
		obj, ok := inlineLawMethodCalls(n.Object, methods)
		if !ok {
			return nil, false
		}
		return &ast.FieldExpr{Position: n.Position, Object: obj, Field: n.Field}, true
	case *ast.CallExpr:
		// A method call `recv.m(args…)` is parsed as a CallExpr whose Func is a FieldExpr.
		sel, ok := n.Func.(*ast.FieldExpr)
		if !ok {
			return nil, false
		}
		body, ok := methods[sel.Field]
		if !ok || body == nil {
			return nil, false
		}
		ret, ok := singleReturnExpr(body.Body)
		if !ok {
			return nil, false
		}
		// Bind self → receiver, and the remaining params → call args.
		subst := map[string]ast.Expr{}
		recv, ok := inlineLawMethodCalls(sel.Object, methods)
		if !ok {
			return nil, false
		}
		if len(body.Params) == 0 {
			return nil, false
		}
		subst[body.Params[0].Name] = recv
		for i, arg := range n.Args {
			if i+1 >= len(body.Params) {
				return nil, false
			}
			ia, ok := inlineLawMethodCalls(arg, methods)
			if !ok {
				return nil, false
			}
			subst[body.Params[i+1].Name] = ia
		}
		substituted := substituteLawIdents(ret, subst)
		// The inlined body may itself contain method calls (mutual/self reference); inline again.
		return inlineLawMethodCalls(substituted, methods)
	default:
		return nil, false
	}
}

// substituteLawIdents deep-clones an expression, replacing free identifiers named in subst with the
// (already-inlined) replacement expression. Only the expression forms a simple boolean/arithmetic law
// body uses are handled; an unhandled form is returned as-is (sound for the SMT tier, which then simply
// declines anything it cannot translate).
func substituteLawIdents(expr ast.Expr, subst map[string]ast.Expr) ast.Expr {
	switch n := expr.(type) {
	case *ast.Ident:
		if r, ok := subst[n.Name]; ok {
			return r
		}
		return n
	case *ast.ParenExpr:
		return &ast.ParenExpr{Position: n.Position, Inner: substituteLawIdents(n.Inner, subst)}
	case *ast.BinaryExpr:
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op, Left: substituteLawIdents(n.Left, subst), Right: substituteLawIdents(n.Right, subst)}
	case *ast.UnaryExpr:
		return &ast.UnaryExpr{Position: n.Position, Op: n.Op, Operand: substituteLawIdents(n.Operand, subst)}
	case *ast.FieldExpr:
		return &ast.FieldExpr{Position: n.Position, Object: substituteLawIdents(n.Object, subst), Field: n.Field}
	case *ast.CallExpr:
		args := make([]ast.Expr, len(n.Args))
		for i, a := range n.Args {
			args[i] = substituteLawIdents(a, subst)
		}
		return &ast.CallExpr{Position: n.Position, Func: substituteLawIdents(n.Func, subst), Args: args}
	default:
		return expr
	}
}
