package semantic

import (
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
	if !direct || decl == nil || !tr.a.callResultStateIndependent(decl) {
		return "", false
	}
	sym, ok := tr.a.symbolForFuncDecl(decl)
	if !ok || sym == nil {
		return "", false
	}
	fn, ok := sym.Type.(*FuncType)
	args := proofCallArgs(call)
	if !ok || fn == nil || len(args) != len(fn.Params) || len(args) != len(decl.Params) {
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
	var key strings.Builder
	fmt.Fprintf(&key, "%p|%s", decl, resultSort)
	var sorts, terms []string
	for i, arg := range args {
		if arg == nil || decl.Params[i].Mutable {
			return "", false
		}
		param := fn.Params[i]
		if _, ref := StripAggregateStateType(param).(*RefType); ref {
			return "", false
		}
		switch {
		case isStringViewType(param):
			if _, exact := param.(*SViewType); !exact {
				return "", false
			}
			// Only an authorized immutable byte-array binding names a stable view.
			// Different view bindings get different functions, conservatively losing
			// cross-view congruence. A binder-dependent view must never enter the name.
			id, bare := stripOptimizationParens(arg).(*ast.Ident)
			if !bare {
				return "", false
			}
			actualType := tr.a.exprTypes[arg]
			if actualType == nil && env[id.Name] == "" && tr.a.currentScope != nil {
				if actual, found := tr.a.currentScope.Lookup(id.Name); found && actual != nil {
					actualType = actual.Type
				}
			}
			if _, exact := actualType.(*SViewType); !exact {
				return "", false
			}
			array, ok := tr.arrayTermEnv(arg, env)
			if !ok || smtTermMentionsQuantifierBinder(array) {
				return "", false
			}
			fmt.Fprintf(&key, "|view%d:%d:%s", i, len(array), array)
		case IsBoolType(param):
			term, ok := tr.boolTerm(arg, env)
			if !ok {
				return "", false
			}
			sorts = append(sorts, "Bool")
			terms = append(terms, term)
		default:
			// Ref, container, aggregate and generic parameters decline here.
			if _, _, numeric := smtIntWidthSign(param); !numeric {
				return "", false
			}
			term, ok := tr.termEnv(arg, env)
			if !ok {
				return "", false
			}
			sorts = append(sorts, "Int")
			terms = append(terms, term)
		}
	}
	if tr.scalarCallUFNames == nil {
		tr.scalarCallUFNames = map[string]string{}
	}
	name, declared := tr.scalarCallUFNames[key.String()]
	if !declared {
		name = "|@scalar_call_" + strconv.Itoa(len(tr.scalarCallUFNames)) + "_" + resultSort + "|"
		tr.scalarCallUFNames[key.String()] = name
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

func (tr *smtTranslator) linkScalarCallUF(call *ast.CallExpr, env map[string]string, result, sort string) {
	if app, ok := tr.scalarCallUF(call, env, sort); ok {
		tr.auxDecls = append(tr.auxDecls, "(assert (= "+result+" "+app+"))\n")
	}
}
