package semantic

import "elisacore/src/ast"

// Opaque calls currently produce constants, not SMT function applications. A
// constant cannot represent a result that changes with a quantifier's binder:
// even dropping binder-dependent callee facts would leave an underapproximation.
// Exact array/dict operations bypass this gate and keep their dependent terms.
func (tr *smtTranslator) opaqueCallQuantifierIndependent(call *ast.CallExpr, env map[string]string) bool {
	if !smtEnvHasQuantifierBinder(env) {
		return true
	}
	// Inspect substitutions before translation: a nested unsupported operation
	// could otherwise replace a binder-dependent argument with another fresh aux.
	depends := func(expr ast.Expr) bool {
		return tr.a.walkStaticExpr(expr, func(node ast.Expr) bool {
			if id, ok := node.(*ast.Ident); ok {
				if smtTermMentionsQuantifierBinder(env[id.Name]) || smtTermMentionsQuantifierBinder(env["\x00string:"+id.Name]) {
					return true
				}
			}
			return smtTermMentionsQuantifierBinder(env[smtProjectionName(node)])
		})
	}
	// Surface method calls may keep the receiver outside their resolved arguments.
	if depends(call.Func) || depends(call.SafeReceiver) {
		return false
	}
	for _, arg := range proofCallArgs(call) {
		if depends(arg) {
			return false
		}
		key, ok := tr.callCanonArgKeyEnv(arg, env)
		if !ok || smtTermMentionsQuantifierBinder(key) {
			return false
		}
	}
	return true
}
