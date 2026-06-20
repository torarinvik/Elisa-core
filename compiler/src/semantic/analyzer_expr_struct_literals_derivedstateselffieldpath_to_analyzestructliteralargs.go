package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
)

func derivedStateSelfFieldPath(expr ast.Expr) ([]string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n.Name == "self" {
			return nil, true
		}
		return nil, false
	case *ast.FieldExpr:
		path, ok := derivedStateSelfFieldPath(n.Object)
		if !ok {
			return nil, false
		}
		return append(path, n.Field), true
	default:
		return nil, false
	}
}
func cloneDerivedStateExpr(expr ast.Expr) ast.Expr {
	switch n := expr.(type) {
	case *ast.Ident:
		return &ast.Ident{Position: n.Position, Name: n.Name}
	case *ast.IntLit:
		return &ast.IntLit{Position: n.Position, Value: n.Value, Suffix: n.Suffix, IsHex: n.IsHex}
	case *ast.FloatLit:
		return &ast.FloatLit{Position: n.Position, Value: n.Value, Suffix: n.Suffix}
	case *ast.StringLit:
		return &ast.StringLit{Position: n.Position, Value: n.Value}
	case *ast.CharLit:
		return &ast.CharLit{Position: n.Position, Value: n.Value}
	case *ast.BoolLit:
		return &ast.BoolLit{Position: n.Position, Value: n.Value}
	case *ast.NullLit:
		return &ast.NullLit{Position: n.Position}
	case *ast.ParenExpr:
		return &ast.ParenExpr{Position: n.Position, Inner: cloneDerivedStateExpr(n.Inner)}
	case *ast.FieldExpr:
		return &ast.FieldExpr{Position: n.Position, Object: cloneDerivedStateExpr(n.Object), Field: n.Field}
	case *ast.UnaryExpr:
		return &ast.UnaryExpr{Position: n.Position, Op: n.Op, Operand: cloneDerivedStateExpr(n.Operand)}
	case *ast.BinaryExpr:
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op, Left: cloneDerivedStateExpr(n.Left), Right: cloneDerivedStateExpr(n.Right)}
	default:
		return expr
	}
}
func allocOwnerPos(expr *ast.AllocExpr) lexer.Pos {
	if expr != nil && expr.Owner != nil {
		return expr.Owner.Pos()
	}
	if expr != nil {
		return expr.Pos()
	}
	return lexer.Pos{}
}

// dischargeStructFieldRefinement enforces a struct field's refinement (`op: u32 is UB[0,127]`) against
// the value supplied at construction — the field invariant must hold or downstream code that trusts the
// field type (a same-refinement param, a bounded index) would be unsound. Mirrors param/return
// discharge: prove statically, else proofLint (a hard error under the default -strict). A non-refined
// field is a no-op.
func (a *Analyzer) dischargeStructFieldRefinement(field ast.FieldDecl, arg ast.Expr) {
	if arg == nil || field.Type == nil {
		return
	}
	ft := field.Type
	if mt, ok := ft.(*ast.MutableType); ok && mt != nil {
		ft = mt.Elem
	}
	rt, ok := ft.(*ast.RefinementTypeExpr)
	if !ok || rt == nil {
		return
	}
	for _, pred := range rt.Preds {
		lawDecl, _, ok := a.lookupLaw(pred.Name)
		if !ok {
			continue
		}
		if a.tryDischargeRefinementStatically(arg, "the struct field value", pred, lawDecl, arg.Pos()) {
			continue
		}
		// Composition: the value is a call whose return refinement entails the field's (the decoder
		// pattern — `Sop2(sop2_op(inst))` where sop2_op returns the same Bounded). The callee enforces it.
		if a.returnCallRefinementEntails(arg, pred) {
			continue
		}
		a.proofLint(arg.Pos(), "refinement %q on field %q could not be proven statically; pass a provable value or accept the runtime check%s", pred.Name, field.Name, a.counterexampleSuffix(a.lastSMTCounterexample))
	}
}

func (a *Analyzer) analyzeStructLiteralArgs(expr *ast.StructLitExpr, base *StructType, bindings map[string]Type, regionBindings map[string]string) {
	if base == nil || base.Decl == nil {
		for _, spread := range expr.Spreads {
			a.analyzeExpr(spread)
		}
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return
	}
	if len(expr.Spreads) != 0 && !expr.Brace {
		a.errorf(expr.Pos(), "struct literal spread is only valid in brace struct literals")
	}
	if expr.NamedArgCount() != 0 {
		if expr.NamedArgCount() != len(expr.Args) {
			a.errorf(expr.Pos(), "struct literal %q cannot mix positional and named fields", expr.Name)
			for i := range expr.Args {
				expr.Args[i], _ = a.analyzeCallLikeValueExpr(expr.Args[i], nil)
			}
			return
		}
		ordered := make([]ast.Expr, len(base.Decl.Fields))
		fieldIndexes := make(map[string]int, len(base.Decl.Fields))
		for i, fieldDecl := range base.Decl.Fields {
			fieldIndexes[fieldDecl.Name] = i
		}
		seen := map[int]lexer.Pos{}
		ok := true
		for i := range expr.Args {
			name := expr.ArgName(i)
			index, exists := fieldIndexes[name]
			if !exists {
				a.errorf(expr.Args[i].Pos(), "struct literal %q has no field %q", expr.Name, name)
				expr.Args[i], _ = a.analyzeCallLikeValueExpr(expr.Args[i], nil)
				ok = false
				continue
			}
			fieldDecl := base.Decl.Fields[index]
			field, exists := base.Fields[fieldDecl.Name]
			if !exists {
				expr.Args[i], _ = a.analyzeCallLikeValueExpr(expr.Args[i], nil)
				ok = false
				continue
			}
			expected := field.Type
			if len(bindings) > 0 {
				expected = a.substituteType(expected, bindings, nil, regionBindings, nil)
			}
			arg, actual := a.analyzeCallLikeValueExpr(expr.Args[i], expected)
			expr.Args[i] = arg
			if !AssignableTo(expected, actual) {
				a.errorf(expr.Args[i].Pos(), "struct literal field %q expects %s, got %s", fieldDecl.Name, expected, actual)
			}
			a.consumeAffineValueExpr(expr.Args[i], expected, "move into struct literal field "+strconv.Quote(fieldDecl.Name))
			a.dischargeStructFieldRefinement(fieldDecl, expr.Args[i])
			if prev, exists := seen[index]; exists {
				a.errorf(expr.Args[i].Pos(), "struct literal %q field %q is specified more than once (first at %s:%d:%d)", expr.Name, fieldDecl.Name, prev.File, prev.Line, prev.Col)
				ok = false
				continue
			}
			seen[index] = expr.Args[i].Pos()
			ordered[index] = expr.Args[i]
		}
		for i, fieldDecl := range base.Decl.Fields {
			if _, exists := seen[i]; exists {
				continue
			}
			if len(expr.Spreads) != 0 {
				continue
			}
			field, exists := base.Fields[fieldDecl.Name]
			if !exists {
				a.errorf(expr.Pos(), "struct literal %q is missing field %q", expr.Name, fieldDecl.Name)
				ok = false
				continue
			}
			expected := field.Type
			if len(bindings) > 0 {
				expected = a.substituteType(expected, bindings, nil, regionBindings, nil)
			}
			defaultExpr, defaultOK := a.analyzeStructFieldDefaultExpr(base, fieldDecl, expected)
			if !defaultOK {
				a.errorf(expr.Pos(), "struct literal %q is missing field %q", expr.Name, fieldDecl.Name)
				ok = false
				continue
			}
			ordered[i] = defaultExpr
		}
		if ok {
			expr.ResolvedArgsValid = true
			expr.ResolvedArgs = ordered
		}
		return
	}
	if len(expr.Spreads) != 0 {
		if len(expr.Args) == 0 {
			expr.ResolvedArgsValid = true
			expr.ResolvedArgs = make([]ast.Expr, len(base.Decl.Fields))
			return
		}
		a.errorf(expr.Pos(), "struct literal spread requires named field overrides")
	}
	expr.ResolvedArgsValid = true
	if len(expr.Args) > len(base.Decl.Fields) {
		expr.ResolvedArgs = expr.Args
		a.errorf(expr.Pos(), "struct literal %q expects %d arguments, got %d", expr.Name, len(base.Decl.Fields), len(expr.Args))
	} else if len(expr.Args) < len(base.Decl.Fields) {
		ordered := make([]ast.Expr, len(base.Decl.Fields))
		copy(ordered, expr.Args)
		ok := true
		for i := len(expr.Args); i < len(base.Decl.Fields); i++ {
			fieldDecl := base.Decl.Fields[i]
			field, exists := base.Fields[fieldDecl.Name]
			if !exists {
				ok = false
				continue
			}
			expected := field.Type
			if len(bindings) > 0 {
				expected = a.substituteType(expected, bindings, nil, regionBindings, nil)
			}
			defaultExpr, defaultOK := a.analyzeStructFieldDefaultExpr(base, fieldDecl, expected)
			if !defaultOK {
				ok = false
				break
			}
			ordered[i] = defaultExpr
		}
		if ok {
			expr.ResolvedArgs = ordered
		} else {
			expr.ResolvedArgs = expr.Args
			a.errorf(expr.Pos(), "struct literal %q expects %d arguments, got %d", expr.Name, len(base.Decl.Fields), len(expr.Args))
		}
	} else {
		expr.ResolvedArgs = expr.Args
	}
	limit := len(expr.Args)
	if len(base.Decl.Fields) < limit {
		limit = len(base.Decl.Fields)
	}
	for i := 0; i < limit; i++ {
		fieldDecl := base.Decl.Fields[i]
		field, ok := base.Fields[fieldDecl.Name]
		if !ok {
			a.analyzeExpr(expr.Args[i])
			continue
		}
		expected := field.Type
		if len(bindings) > 0 {
			expected = a.substituteType(expected, bindings, nil, regionBindings, nil)
		}
		var actual Type
		expr.Args[i], actual = a.analyzeCallLikeValueExpr(expr.Args[i], expected)
		if !AssignableTo(expected, actual) {
			a.errorf(expr.Args[i].Pos(), "struct literal field %q expects %s, got %s", fieldDecl.Name, expected, actual)
		}
		a.consumeAffineValueExpr(expr.Args[i], expected, "move into struct literal field "+strconv.Quote(fieldDecl.Name))
		a.dischargeStructFieldRefinement(fieldDecl, expr.Args[i])
	}
	for i := limit; i < len(expr.Args); i++ {
		a.analyzeExpr(expr.Args[i])
	}
}

func (a *Analyzer) analyzeStructFieldDefaultExpr(base *StructType, field ast.FieldDecl, expected Type) (ast.Expr, bool) {
	if field.DefaultValue == nil {
		return nil, false
	}
	defaultExpr := cloneDefaultArgExpr(field.DefaultValue)
	if defaultExpr == nil {
		a.errorf(field.Position, "default value for struct field %q uses unsupported syntax in v1", field.Name)
		return nil, false
	}
	analyze := func() bool {
		savedScope := a.currentScope
		savedReturn := a.currentReturn
		savedFuncDecl := a.currentFuncDecl
		savedFuncType := a.currentFuncType
		savedImplicitScopes := a.currentImplicitScopes
		a.currentScope = NewScope(a.globalScope)
		a.currentReturn = nil
		a.currentFuncDecl = nil
		a.currentFuncType = nil
		a.currentImplicitScopes = nil
		var actual Type = invalidType
		defaultExpr, actual = a.analyzeCallLikeValueExpr(defaultExpr, expected)
		a.currentScope = savedScope
		a.currentReturn = savedReturn
		a.currentFuncDecl = savedFuncDecl
		a.currentFuncType = savedFuncType
		a.currentImplicitScopes = savedImplicitScopes
		if !AssignableTo(expected, actual) {
			a.errorf(field.DefaultValue.Pos(), "default value for struct field %q expects %s, got %s", field.Name, expected, actual)
			a.reportShapeMismatchNotes(field.DefaultValue.Pos(), expected, actual)
			return false
		}
		return true
	}
	ok := false
	if base != nil && (base.Namespace != "" || len(base.Usings) != 0) {
		a.withResolutionContext(base.Namespace, base.Usings, func() {
			ok = analyze()
		})
	} else {
		ok = analyze()
	}
	return defaultExpr, ok
}
