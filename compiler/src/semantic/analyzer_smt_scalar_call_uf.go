package semantic

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"elisacore/src/ast"
)

// scalarCallUF models a deterministic call as a function of its scalar arguments.
// A bound argument remains inside the application, so no quantifier-dependent
// result equality needs to be hoisted out of its binder. Callee clauses are not
// assumed here; an arbitrary function of these inputs overapproximates the call.
func (tr *smtTranslator) scalarCallUF(call *ast.CallExpr, env map[string]string, resultSort string) (string, bool) {
	if tr == nil || tr.a == nil || call == nil || call.Safe || call.SafeReceiver != nil {
		return "", false
	}
	decl, direct := tr.a.resolveDirectCallFuncDecl(call)
	decl = tr.a.readDecl(decl)
	if !direct || decl == nil || !tr.a.callResultStateIndependent(decl) {
		return "", false
	}
	sym, ok := tr.a.symbolForFuncDecl(decl)
	if !ok || sym == nil {
		return "", false
	}
	fn, ok := sym.Type.(*FuncType)
	if !ok || fn == nil || len(proofCallArgs(call)) != len(fn.Params) || len(proofCallArgs(call)) != len(decl.Params) {
		return "", false
	}
	if _, ref := StripAggregateStateType(fn.Return).(*RefType); ref {
		return "", false
	}
	if resultSort == "Bool" {
		if !IsBoolType(fn.Return) {
			return "", false
		}
	} else {
		if _, bits, ok := smtIntWidthSign(fn.Return); !ok || bits <= 0 || bits > 64 {
			return "", false
		}
	}
	key, sorts, terms, ok := tr.scalarCallArgTerms(call, decl, fn, env)
	if !ok {
		return "", false
	}
	key = fmt.Sprintf("%p|%s", decl, resultSort) + key
	if tr.scalarCallUFNames == nil {
		tr.scalarCallUFNames = map[string]string{}
	}
	name, declared := tr.scalarCallUFNames[key]
	if !declared {
		name = "|@scalar_call_" + strconv.Itoa(len(tr.scalarCallUFNames)) + "_" + resultSort + "|"
		tr.scalarCallUFNames[key] = name
		tr.auxDecls = append(tr.auxDecls, "(declare-fun "+name+" ("+strings.Join(sorts, " ")+") "+resultSort+")\n")
	}
	app := name
	if len(terms) != 0 {
		app = "(" + name + " " + strings.Join(terms, " ") + ")"
	}
	if resultSort == "Int" {
		signed, bits, _ := smtIntWidthSign(fn.Return)
		app = smtSameWidthReinterpret(app, signed, bits)
	}
	return app, true
}

// scalarCallArgTerms models the arguments of a deterministic direct call: the UF identity
// suffix (literal and binding-specific parts) plus the sorts and terms of the applied inputs.
func (tr *smtTranslator) scalarCallArgTerms(call *ast.CallExpr, decl *ast.FuncDecl, fn *FuncType, env map[string]string) (string, []string, []string, bool) {
	args := proofCallArgs(call)
	var key strings.Builder
	var sorts, terms []string
	for i, arg := range args {
		if arg == nil || decl.Params[i].Mutable {
			return "", nil, nil, false
		}
		param := fn.Params[i]
		// A stable record root is one fixed snapshot for the query, identified by its root.
		if token, ok := tr.stableRecordRootToken(arg, env); ok {
			if ref, isRef := StripAggregateStateType(param).(*RefType); isRef && (ref.Mutable || ref.Linear) {
				return "", nil, nil, false
			}
			key.WriteString("|record:")
			key.WriteString(token)
			continue
		}
		if _, ref := StripAggregateStateType(param).(*RefType); ref && !isDArrayTypeMaybeRef(stripRefForBounds(param)) {
			return "", nil, nil, false
		}
		switch {
		case isStringViewType(param):
			if _, exact := param.(*SViewType); !exact {
				return "", nil, nil, false
			}
			if literal, ok := stripOptimizationParens(arg).(*ast.StringLit); ok && literal != nil {
				// A literal view is immutable and has exact contents. Keep it in
				// the UF identity so repeated calls on the same literal agree.
				key.WriteString("|view-literal:")
				key.WriteString(hex.EncodeToString([]byte(literal.Value)))
				continue
			}
			if id, bare := stripOptimizationParens(arg).(*ast.Ident); bare && id != nil {
				if data, offset, length, ok := smtIndexedSViewArgTermsFromEnv(id.Name, env); ok {
					key.WriteString("|view-indexed")
					sorts = append(sorts, "(Array Int Int)", "Int", "Int")
					terms = append(terms, data, offset, length)
					continue
				}
			}
			if inner, ok := stripOptimizationParens(arg).(*ast.CallExpr); ok && inner != nil {
				if data, offset, length, ok := tr.sviewCallUFTerms(inner, env); ok {
					key.WriteString("|view-call")
					sorts = append(sorts, "(Array Int Int)", "Int", "Int")
					terms = append(terms, data, offset, length)
					continue
				}
			}
			if data, offset, length, ok := tr.indexedSViewCallTerms(arg, env); ok {
				// The binder stays in the application arguments. Unlike
				// sviewWindowArrayTerm, this creates no top-level axiom containing
				// a free quantified symbol.
				key.WriteString("|view-indexed")
				sorts = append(sorts, "(Array Int Int)", "Int", "Int")
				terms = append(terms, data, offset, length)
				continue
			}
			if data, offset, length, ok := tr.sviewContentParts(arg, env); ok && !smtTermMentionsQuantifierBinder(data) {
				key.WriteString("|view-parts")
				sorts = append(sorts, "(Array Int Int)", "Int", "Int")
				terms = append(terms, data, offset, length)
				continue
			}
			// Only an authorized immutable byte-array binding names a stable view.
			// Different view bindings get different functions, conservatively losing
			// cross-view congruence. A binder-dependent view must never enter the name.
			id, bare := stripOptimizationParens(arg).(*ast.Ident)
			if !bare {
				return "", nil, nil, false
			}
			actualType := tr.a.exprTypes[arg]
			if actualType == nil && env[id.Name] == "" && tr.a.currentScope != nil {
				if actual, found := tr.a.currentScope.Lookup(id.Name); found && actual != nil {
					actualType = actual.Type
				}
			}
			if _, exact := actualType.(*SViewType); !exact {
				return "", nil, nil, false
			}
			array, ok := tr.arrayTermEnv(arg, env)
			if !ok || smtTermMentionsQuantifierBinder(array) {
				return "", nil, nil, false
			}
			fmt.Fprintf(&key, "|view%d:%d:%s", i, len(array), array)
		case IsBoolType(param):
			term, ok := tr.boolTerm(arg, env)
			if !ok {
				return "", nil, nil, false
			}
			sorts = append(sorts, "Bool")
			terms = append(terms, term)
		case isDArrayTypeMaybeRef(stripRefForBounds(param)):
			// An immutable container argument is identified by its stable base, as in ghost-call
			// congruence: the same container is the same contents within one query.
			if ref, isRef := param.(*RefType); isRef && (ref.Mutable || ref.Linear) {
				return "", nil, nil, false
			}
			inner := stripOptimizationParens(arg)
			if addr, isAddr := inner.(*ast.AddrOfExpr); isAddr && addr != nil {
				inner = addr.Operand
			}
			base, ok := tr.containerBase(inner, env)
			if !ok || smtTermMentionsQuantifierBinder(base) {
				return "", nil, nil, false
			}
			key.WriteString("|container:")
			key.WriteString(base)
		case isStableContainerRecordArg(tr, param, arg):
			// A record read from a stable container is identified by the container and
			// index, the same identity the container theory gives its field reads.
			base, index, ok := tr.containerViewIndexTerm(stripOptimizationParens(arg).(*ast.IndexExpr), "", env)
			if !ok {
				return "", nil, nil, false
			}
			key.WriteString("|record-elem:")
			key.WriteString(base)
			sorts = append(sorts, "Int")
			terms = append(terms, index)
		default:
			// Ref, container, aggregate and generic parameters decline here.
			if _, _, numeric := smtIntWidthSign(param); !numeric {
				return "", nil, nil, false
			}
			term, ok := tr.termEnv(arg, env)
			if !ok {
				return "", nil, nil, false
			}
			sorts = append(sorts, "Int")
			terms = append(terms, term)
		}
	}
	return key.String(), sorts, terms, true
}

func (tr *smtTranslator) linkScalarCallUF(call *ast.CallExpr, env map[string]string, result, sort string) {
	if app, ok := tr.scalarCallUF(call, env, sort); ok {
		tr.auxDecls = append(tr.auxDecls, "(assert (= "+result+" "+app+"))\n")
	}
}

// isStableContainerRecordArg reports a by-value record argument `xs[i]` read from a container
// the current function never writes, so every read of xs[i] in one query sees the same record.
func isStableContainerRecordArg(tr *smtTranslator, param Type, arg ast.Expr) bool {
	if _, record := StripAggregateStateType(param).(*StructType); !record {
		return false
	}
	idx, ok := stripOptimizationParens(arg).(*ast.IndexExpr)
	if !ok || !validContainerViewIndex(idx) || tr.a.currentFuncDecl == nil || !tr.a.sviewFrameWriteFree(tr.a.currentFuncDecl) {
		return false
	}
	_, record := StripAggregateStateType(tr.containerElementType(idx.Object)).(*StructType)
	return record
}

// sviewCallUFTerms models a deterministic call that returns a view as three functions of
// its arguments: the view's bytes, start offset and length.
func (tr *smtTranslator) sviewCallUFTerms(call *ast.CallExpr, env map[string]string) (data, offset, length string, ok bool) {
	if tr == nil || tr.a == nil || call == nil || call.Safe || call.SafeReceiver != nil {
		return "", "", "", false
	}
	decl, direct := tr.a.resolveDirectCallFuncDecl(call)
	decl = tr.a.readDecl(decl)
	if !direct || decl == nil || !tr.a.callResultStateIndependent(decl) {
		return "", "", "", false
	}
	sym, found := tr.a.symbolForFuncDecl(decl)
	if !found || sym == nil {
		return "", "", "", false
	}
	fn, isFn := sym.Type.(*FuncType)
	if !isFn || fn == nil || len(proofCallArgs(call)) != len(fn.Params) || len(proofCallArgs(call)) != len(decl.Params) {
		return "", "", "", false
	}
	if _, exact := fn.Return.(*SViewType); !exact {
		return "", "", "", false
	}
	suffix, sorts, terms, ok := tr.scalarCallArgTerms(call, decl, fn, env)
	if !ok {
		return "", "", "", false
	}
	key := fmt.Sprintf("%p|view", decl) + suffix
	if tr.scalarCallUFNames == nil {
		tr.scalarCallUFNames = map[string]string{}
	}
	name, declared := tr.scalarCallUFNames[key]
	if !declared {
		name = "@view_call_" + strconv.Itoa(len(tr.scalarCallUFNames))
		tr.scalarCallUFNames[key] = name
		domain := strings.Join(sorts, " ")
		tr.auxDecls = append(tr.auxDecls,
			"(declare-fun |"+name+"$data| ("+domain+") (Array Int Int))\n",
			"(declare-fun |"+name+"$lo| ("+domain+") Int)\n",
			"(declare-fun |"+name+"$len| ("+domain+") Int)\n")
	}
	apply := func(part string) string {
		if len(terms) == 0 {
			return "|" + name + "$" + part + "|"
		}
		return "(|" + name + "$" + part + "| " + strings.Join(terms, " ") + ")"
	}
	length = apply("len")
	if !smtTermMentionsQuantifierBinder(length) {
		tr.auxDecls = append(tr.auxDecls, "(assert (>= "+length+" 0))\n")
	}
	return apply("data"), apply("lo"), length, true
}
