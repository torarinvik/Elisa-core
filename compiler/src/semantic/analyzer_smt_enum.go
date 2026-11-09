package semantic

import (
	"elisacore/src/ast"
	"encoding/hex"
	"strconv"
	"strings"
)

// Enum values in the SMT fragment (docs/90 datatype extension).
//
// A (non-const) enum value is modeled by two families of symbols:
//
//   - its TAG: an Int constrained to the declared variant tags, keyed by the value's stable
//     projection (`e`, `self.kind`, or the caller-side projection a substituted formal resolves to);
//   - its PAYLOAD: one symbol per (value, variant, payload field[, struct field path]). A payload
//     symbol is only meaningful under its tag guard; outside it the symbol is unconstrained, so every
//     fact that mentions it is guarded by the tag test that makes it the real payload.
//
// A variant constructor lowers to its constant tag (and its payload arguments). This keeps the
// representation a proper tagged sum — the shape later dependent-type and cost refinements over enum
// values need — rather than an opaque token, while staying inside linear integer arithmetic.
//
// Soundness invariants:
//   - a tag symbol is always constrained to the enum's declared tags (no phantom variants);
//   - payload equalities for pattern binders are emitted ONLY for a top-level path fact (the arm of
//     a statement `match`, or an `is` guard taken), never inside a goal or under negation, where an
//     unconstrained binder term could otherwise make a false goal provable;
//   - a match expression's final arm is a default only when the arms provably cover every variant
//     (or it is a wildcard); otherwise the term declines.

type smtEnumValue struct {
	et   *EnumType
	key  string            // stable projection key; "" for a constructor
	ctor *EnumVariant      // non-nil when the expression is a variant constructor
	args []ast.Expr        // constructor payload arguments (positional)
	env  map[string]string // environment the constructor arguments translate under
	// tag, when set, is the Int variant-index term of a payload-free enum read that has no
	// projection key (a field of a container element, `xs[i].kind`).
	tag string
}

type smtEnumBinding struct {
	expr ast.Expr
	env  map[string]string
}

func smtEnumQuote(s string) string {
	s = strings.ReplaceAll(s, "|", "_")
	return strings.ReplaceAll(s, "\\", "_")
}

func (tr *smtTranslator) enumTypeOf(expr ast.Expr) *EnumType {
	if tr == nil || tr.a == nil || expr == nil {
		return nil
	}
	t := tr.a.exprType(expr)
	if t == nil {
		if id, ok := stripOptimizationParens(expr).(*ast.Ident); ok && tr.a.currentScope != nil {
			if sym, found := tr.a.currentScope.Lookup(id.Name); found && sym != nil {
				t = sym.Type
			}
		}
	}
	if t == nil {
		return nil
	}
	et, _, ok := resolveMatchableEnumType(stripRefForBounds(t))
	if !ok || et == nil || len(et.Variants) == 0 {
		return nil
	}
	return et
}

// enumConstructor recognizes `E.V` and `E.V(args)` for a (non-const) enum.
func (tr *smtTranslator) enumConstructor(expr ast.Expr) (*EnumType, *EnumVariant, []ast.Expr, bool) {
	expr = stripOptimizationParens(expr)
	var args []ast.Expr
	if call, ok := expr.(*ast.CallExpr); ok && call != nil {
		if !tr.a.isEnumVariantConstructorCall(call) {
			return nil, nil, nil, false
		}
		expr = call.Func
		args = call.Args
		for _, arg := range args {
			if arg == nil {
				return nil, nil, nil, false
			}
		}
	}
	field, ok := isEnumVariantExpr(expr)
	if !ok {
		return nil, nil, nil, false
	}
	et, variant, ok := tr.a.enumConstructorInfoFromFieldExpr(field)
	if !ok || et == nil || variant == nil {
		return nil, nil, nil, false
	}
	if args != nil && len(args) != len(variant.Payload) {
		return nil, nil, nil, false
	}
	if args == nil && len(variant.Payload) != 0 {
		// A bare reference to a payload-carrying variant is a constructor function, not a value.
		return nil, nil, nil, false
	}
	return et, variant, args, true
}

// enumValue resolves an enum-valued expression to a constructor or a stable projection.
func (tr *smtTranslator) enumValue(expr ast.Expr, env map[string]string) (smtEnumValue, bool) {
	expr = stripOptimizationParens(expr)
	if et, variant, args, ok := tr.enumConstructor(expr); ok {
		return smtEnumValue{et: et, ctor: variant, args: args, env: env}, true
	}
	switch n := expr.(type) {
	case *ast.Ident:
		if env != nil {
			if bound, ok := env[n.Name]; ok {
				if b, ok := tr.enumBindings[bound]; ok {
					return tr.enumValue(b.expr, b.env)
				}
				if strings.HasPrefix(bound, dtTokenPrefix) {
					et := tr.enumTypeOf(n)
					if et == nil {
						return smtEnumValue{}, false
					}
					return smtEnumValue{et: et, key: strings.TrimPrefix(bound, dtTokenPrefix)}, true
				}
				return smtEnumValue{}, false
			}
		}
		if tr.calleeBodyDepth > 0 {
			return smtEnumValue{}, false
		}
		et := tr.enumTypeOf(n)
		if et == nil || tr.a.currentScope == nil {
			return smtEnumValue{}, false
		}
		sym, found := tr.a.currentScope.Lookup(n.Name)
		if !found || sym == nil || (sym.Kind != SymbolLocal && sym.Kind != SymbolParam) {
			return smtEnumValue{}, false
		}
		return smtEnumValue{et: et, key: n.Name}, true
	case *ast.FieldExpr:
		if env != nil {
			if _, ok := env[smtProjectionName(n)]; ok {
				// A field bound by a struct substitution is not an enum projection we can key.
				return smtEnumValue{}, false
			}
		}
		root := n.Object
		for {
			if f, ok := stripOptimizationParens(root).(*ast.FieldExpr); ok {
				root = f.Object
				continue
			}
			break
		}
		id, ok := stripOptimizationParens(root).(*ast.Ident)
		if !ok || id == nil {
			// A payload-free enum field of a stable container element is its variant index.
			if et := tr.enumTypeOf(n); et != nil && enumHasNoPayload(et) {
				if tag, _, ok := tr.recordElementFieldTerm(n, env, "Int"); ok {
					return smtEnumValue{et: et, tag: tag}, true
				}
			}
			return smtEnumValue{}, false
		}
		if env != nil {
			if _, bound := env[id.Name]; bound {
				return smtEnumValue{}, false
			}
		}
		if tr.calleeFreeProjection(n, env) {
			return smtEnumValue{}, false
		}
		et := tr.enumTypeOf(n)
		if et == nil {
			return smtEnumValue{}, false
		}
		return smtEnumValue{et: et, key: smtProjectionName(n)}, true
	}
	return smtEnumValue{}, false
}

func enumVariantIndex(et *EnumType, v *EnumVariant) int {
	for i, cand := range et.Variants {
		if cand == v {
			return i
		}
	}
	return -1
}

func (tr *smtTranslator) enumTagSym(v smtEnumValue) string {
	sym := "|etag_" + smtEnumQuote(v.key) + "|"
	if _, ok := tr.enumDecls[sym]; !ok {
		var b strings.Builder
		b.WriteString("(declare-const " + sym + " Int)\n")
		opts := make([]string, 0, len(v.et.Variants))
		for i := range v.et.Variants {
			opts = append(opts, "(= "+sym+" "+strconv.Itoa(i)+")")
		}
		if len(opts) == 1 {
			b.WriteString("(assert " + opts[0] + ")\n")
		} else {
			b.WriteString("(assert (or " + strings.Join(opts, " ") + "))\n")
		}
		tr.enumDecls[sym] = b.String()
	}
	return sym
}

// enumTagTerm is the Int term for an enum value's variant index.
func (tr *smtTranslator) enumTagTerm(v smtEnumValue) string {
	if v.ctor != nil {
		return strconv.Itoa(enumVariantIndex(v.et, v.ctor))
	}
	if v.tag != "" {
		return v.tag
	}
	return tr.enumTagSym(v)
}

// enumVariantGuard is the tag test `value is variant`.
func (tr *smtTranslator) enumVariantGuard(v smtEnumValue, variant *EnumVariant) string {
	idx := enumVariantIndex(v.et, variant)
	if idx < 0 {
		return "false"
	}
	if v.ctor != nil {
		if v.ctor == variant {
			return "true"
		}
		return "false"
	}
	return "(= " + tr.enumTagTerm(v) + " " + strconv.Itoa(idx) + ")"
}

// enumPayloadValue describes payload field `i` of `variant` for an enum value: either the
// constructor's own argument, or a keyed payload projection.
func (tr *smtTranslator) enumPayloadKey(v smtEnumValue, variant *EnumVariant, i int) string {
	return v.key + "#" + variant.Name + "#" + strconv.Itoa(i)
}

// enumArrayPayloadLengthStable restricts length-only payload identity to immutable
// local/parameter subjects in call-free functions. A call could mutate an aliased
// payload between two match expressions, so reusing its length across the function
// would otherwise assert a stale heap fact.
func (tr *smtTranslator) enumArrayPayloadLengthStable(v smtEnumValue) bool {
	if tr == nil || tr.a == nil || tr.a.currentFuncDecl == nil || tr.a.currentScope == nil || v.key == "" || strings.Contains(v.key, ".") {
		return false
	}
	sym, ok := tr.a.currentScope.Lookup(v.key)
	if !ok || sym == nil || (sym.Kind != SymbolParam && sym.Kind != SymbolLocal) || sym.Mutable || symbolRootMutable(sym) {
		return false
	}
	stable := true
	tr.a.storeScanEachNode(tr.a.currentFuncDecl, func(node any) {
		if _, isCall := node.(*ast.CallExpr); isCall {
			stable = false
		}
	})
	return stable
}

// enumPayloadIntSym declares (once) an Int payload symbol carrying its type bounds.
func (tr *smtTranslator) enumPayloadIntSym(key string, t Type) string {
	sym := "|epay_" + smtEnumQuote(key) + "|"
	if _, ok := tr.enumDecls[sym]; !ok {
		var b strings.Builder
		b.WriteString("(declare-const " + sym + " Int)\n")
		if nt, ok := smtNumericValueType(t); ok {
			if signed, bits, ok := smtIntWidthSign(nt); ok && bits > 0 && bits <= 64 {
				if signed {
					bias := smtPow2(bits - 1)
					b.WriteString("(assert (>= " + sym + " (- " + bias + ")))\n")
					b.WriteString("(assert (< " + sym + " " + bias + "))\n")
				} else {
					b.WriteString("(assert (>= " + sym + " 0))\n")
					b.WriteString("(assert (< " + sym + " " + smtPow2(bits) + "))\n")
				}
			}
		}
		tr.enumDecls[sym] = b.String()
	}
	return sym
}

func (tr *smtTranslator) enumPayloadBoolSym(key string) string {
	sym := "|epayb_" + smtEnumQuote(key) + "|"
	tr.boolDecls[sym] = true
	return sym
}

// bindEnumPatternBinders extends env so each binder of a variant pattern denotes the matched value's
// payload: numeric/bool binders map to payload symbols (or constructor argument terms), enum binders
// to a nested enum value, struct binders to per-field payload symbols. Sub-patterns other than a
// plain binder or `_` decline (sound: the caller then declines the whole term).
func (tr *smtTranslator) bindEnumPatternBinders(v smtEnumValue, variant *EnumVariant, p *ast.MatchVariantPattern, env map[string]string) (map[string]string, bool) {
	out := make(map[string]string, len(env)+len(p.Args))
	for k, val := range env {
		out[k] = val
	}
	args := enumPatternArgs(p, variant)
	if args == nil && len(p.Args) != 0 {
		return nil, false
	}
	for i, arg := range args {
		if arg == nil {
			continue
		}
		switch sub := arg.Pattern.(type) {
		case *ast.MatchWildcardPattern:
			continue
		case *ast.MatchBindPattern:
			if sub.Binder != "" || sub.Name == "" || sub.Name == "_" || i >= len(variant.Payload) {
				return nil, false
			}
			if !tr.bindEnumPayloadName(out, sub.Name, v, variant, i) {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return out, true
}

func (tr *smtTranslator) bindEnumPayloadName(env map[string]string, name string, v smtEnumValue, variant *EnumVariant, i int) bool {
	pt := stripRefForBounds(variant.Payload[i])
	if v.ctor != nil {
		// A constructor's payload is its argument: translate it in the constructor's environment.
		arg := v.args[i]
		if IsBoolType(pt) {
			t, ok := tr.boolTerm(arg, v.env)
			if !ok {
				return false
			}
			env[name] = t
			return true
		}
		if _, ok := smtNumericValueType(pt); ok {
			t, ok := tr.termEnv(arg, v.env)
			if !ok {
				return false
			}
			env[name] = t
			return true
		}
		if tr.enumTypeOf(arg) != nil {
			token := dtTokenPrefix + "ctorarg#" + strconv.Itoa(len(tr.enumBindings))
			tr.enumBindings[token] = smtEnumBinding{expr: arg, env: v.env}
			env[name] = token
			return true
		}
		return false
	}
	key := tr.enumPayloadKey(v, variant, i)
	if IsBoolType(pt) {
		env[name] = tr.enumPayloadBoolSym(key)
		return true
	}
	if _, ok := smtNumericValueType(pt); ok {
		env[name] = tr.enumPayloadIntSym(key, pt)
		return true
	}
	if et, _, ok := resolveMatchableEnumType(pt); ok && et != nil {
		env[name] = dtTokenPrefix + key
		return true
	}
	// An unknown array payload has a stable length for this enum projection, but its
	// elements are not modeled. Give it a length-only token so repeated matches can
	// reason about `.count` without inventing an SMT array for the payload contents.
	// Constructor arguments use the branch above and must keep their actual length.
	if _, ok := pt.(*DArrayType); ok && tr.enumArrayPayloadLengthStable(v) {
		base := smtVar("enum_payload_len_" + hex.EncodeToString([]byte(key)))
		if tr.lengthOnly == nil {
			tr.lengthOnly = map[string]bool{}
		}
		tr.lengthOnly[base] = true
		env[name] = base
		return true
	}
	if st, ok := pt.(*StructType); ok && st != nil {
		bound := false
		for _, field := range sortedStructFieldNames(st) {
			ft := stripRefForBounds(st.Fields[field].Type)
			proj := name + smtFieldSeparator + field
			fkey := key + "." + field
			if IsBoolType(ft) {
				env[proj] = tr.enumPayloadBoolSym(fkey)
				bound = true
			} else if _, ok := smtNumericValueType(ft); ok {
				env[proj] = tr.enumPayloadIntSym(fkey, ft)
				bound = true
			}
		}
		// The struct binder itself has no scalar term; its scalar fields resolve through env.
		env[name] = dtTokenPrefix + key
		return bound || len(st.Fields) == 0
	}
	return false
}

// enumPatternGuard is the tag guard of a top-level enum match pattern (variant or alternation of
// binder-free variants). Binders are bound into env by the caller.
func (tr *smtTranslator) enumPatternGuard(v smtEnumValue, pattern ast.MatchPattern) (string, *EnumVariant, bool) {
	switch p := pattern.(type) {
	case *ast.MatchVariantPattern:
		variant, ok := tr.enumPatternVariant(v.et, p)
		if !ok {
			return "", nil, false
		}
		return tr.enumVariantGuard(v, variant), variant, true
	case *ast.MatchOrPattern:
		var guards []string
		for _, option := range p.Options {
			vp, ok := option.(*ast.MatchVariantPattern)
			if !ok || len(vp.Args) != 0 {
				return "", nil, false
			}
			variant, ok := tr.enumPatternVariant(v.et, vp)
			if !ok {
				return "", nil, false
			}
			guards = append(guards, tr.enumVariantGuard(v, variant))
		}
		if len(guards) == 0 {
			return "", nil, false
		}
		return "(or " + strings.Join(guards, " ") + ")", nil, true
	}
	return "", nil, false
}

func (tr *smtTranslator) enumPatternVariant(et *EnumType, p *ast.MatchVariantPattern) (*EnumVariant, bool) {
	if p == nil || p.As != "" {
		return nil, false
	}
	variant, ok := et.Variant(p.Variant)
	if !ok || variant == nil {
		return nil, false
	}
	if p.EnumName != "" {
		base, _, ok := tr.a.lookupVisibleType(p.EnumName)
		if !ok {
			return nil, false
		}
		if bt, ok := base.(*EnumType); !ok || bt != et {
			return nil, false
		}
	}
	return variant, true
}
