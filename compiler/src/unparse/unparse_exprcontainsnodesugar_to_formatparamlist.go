package unparse

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func exprContainsNodeSugar(expr ast.Expr) bool {
	switch n := expr.(type) {
	case nil:
		return false
	case *ast.AllocExpr:
		return n.NodeSugar || exprContainsNodeSugar(n.Owner) || exprContainsNodeSugar(n.Value) || exprContainsNodeSugar(n.NodeSpan)
	case *ast.CanExpr:
		return exprContainsNodeSugar(n.Expr)
	case *ast.BinaryExpr:
		return exprContainsNodeSugar(n.Left) || exprContainsNodeSugar(n.Right)
	case *ast.UnaryExpr:
		return exprContainsNodeSugar(n.Operand)
	case *ast.CallExpr:
		if exprContainsNodeSugar(n.Func) {
			return true
		}
		if exprContainsNodeSugar(n.SafeReceiver) {
			return true
		}
		for _, arg := range n.Args {
			if exprContainsNodeSugar(arg) {
				return true
			}
		}
		return false
	case *ast.FieldExpr:
		return exprContainsNodeSugar(n.Object)
	case *ast.IndexExpr:
		return exprContainsNodeSugar(n.Object) || exprContainsNodeSugar(n.Index) || exprContainsNodeSugar(n.Fallback)
	case *ast.SliceExpr:
		return exprContainsNodeSugar(n.Object) || exprContainsNodeSugar(n.Start) || exprContainsNodeSugar(n.End)
	case *ast.CastExpr:
		return exprContainsNodeSugar(n.Operand)
	case *ast.ListLitExpr:
		for _, item := range n.Elems {
			if exprContainsNodeSugar(item) {
				return true
			}
		}
		return false
	case *ast.StructLitExpr:
		for _, arg := range n.Args {
			if exprContainsNodeSugar(arg) {
				return true
			}
		}
		return false
	case *ast.RecordUpdateExpr:
		if exprContainsNodeSugar(n.Base) {
			return true
		}
		for _, arg := range n.Args {
			if exprContainsNodeSugar(arg) {
				return true
			}
		}
		return false
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			if exprContainsNodeSugar(elem) {
				return true
			}
		}
		return false
	case *ast.ParenExpr:
		return exprContainsNodeSugar(n.Inner)
	case *ast.TernaryExpr:
		return exprContainsNodeSugar(n.Value) || exprContainsNodeSugar(n.Cond) || exprContainsNodeSugar(n.Alt)
	case *ast.AddrOfExpr:
		return exprContainsNodeSugar(n.Operand)
	case *ast.SpecializeExpr:
		return exprContainsNodeSugar(n.Operand)
	case *ast.TryExpr:
		return exprContainsNodeSugar(n.Value) || exprContainsNodeSugar(n.Fallback)
	case *ast.CatchExpr:
		return exprContainsNodeSugar(n.Value)
	case *ast.UnwrapElseExpr:
		return exprContainsNodeSugar(n.Value) || exprContainsNodeSugar(n.Fallback)
	case *ast.OptionalBindExpr:
		return exprContainsNodeSugar(n.Value)
	default:
		return false
	}
}
func formatSingleStmtCanBlock(stmt *ast.CanStmt) (string, bool) {
	if stmt == nil || len(stmt.Body) != 1 {
		return "", false
	}
	return formatInlineCanStmt(stmt.Body[0], stmt.Permissions)
}
func (f *formatter) writeStmt(level int, stmt ast.Stmt) {
	if stmt == nil {
		return
	}
	switch n := stmt.(type) {
	case *ast.AssignStmt:
		op := " <- "
		if n.Optional {
			op = " ?= "
		}
		f.writePrefixedMultiline(level, "", formatExpr(n.Target)+op+formatExpr(n.Value))
	case *ast.AugAssignStmt:
		f.writePrefixedMultiline(level, "", formatExpr(n.Target)+" "+lexer.TokenName(n.Op)+" "+formatExpr(n.Value))
	case *ast.AsRefAssignStmt:
		line := formatExpr(n.Target) + " as"
		if n.AsKind != "" {
			line += " " + n.AsKind
		}
		line += " <- " + formatExpr(n.Value)
		f.writePrefixedMultiline(level, "", line)
	case *ast.VarDeclStmt:
		if n.Type == nil {
			f.writePrefixedMultiline(level, "", n.Name+" = "+formatExpr(n.Value))
			return
		}
		line := n.Name + ": "
		if n.Mutable {
			line += "mutable "
		}
		line += formatTypeExpr(n.Type)
		if n.Value != nil {
			line += " = " + formatExpr(n.Value)
		}
		f.writePrefixedMultiline(level, "", line)
	case *ast.LocalParamsStmt:
		f.writeLine(level, "bundle "+n.Name+" explicit:")
		for _, param := range n.Params {
			f.writeLine(level+1, formatParamDecl(param))
		}
	case *ast.LetDestructureStmt:
		f.writePrefixedMultiline(level, "", "let "+formatMoveBindPattern(n.Pattern)+" = "+formatExpr(n.Value))
	case *ast.TupleBindStmt:
		names := make([]string, 0, len(n.Names))
		for _, name := range n.Names {
			names = append(names, name.Name)
		}
		op := " <- "
		if n.Declare {
			op = " = "
		}
		f.writePrefixedMultiline(level, "", strings.Join(names, ", ")+op+formatExpr(n.Value))
	case *ast.MoveBindStmt:
		line := formatExpr(n.Value)
		if _, ok := n.Value.(*ast.MoveExpr); !ok {
			line = "move " + line
		}
		if n.Store != nil {
			line += " in " + formatExpr(n.Store)
		}
		line += " as " + formatMoveBindPattern(n.Pattern)
		f.writePrefixedMultiline(level, "", line)
	case *ast.DeferStmt:
		mode := "block"
		if n.Mode == ast.DeferModeFunction {
			mode = "function"
		}
		f.writeLine(level, "defer "+mode+":")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.ReturnStmt:
		if n.Value == nil {
			f.writeLine(level, "return")
			return
		}
		f.writePrefixedMultiline(level, "return ", formatExpr(n.Value))
	case *ast.IfStmt:
		f.writeLine(level, formatIfHeader("if", n.Hint, n.Cond))
		for _, stmt := range n.Then {
			f.writeStmt(level+1, stmt)
		}
		for _, elif := range n.Elifs {
			f.writeLine(level, formatIfHeader("elif", elif.Hint, elif.Cond))
			for _, stmt := range elif.Body {
				f.writeStmt(level+1, stmt)
			}
		}
		if len(n.Else) > 0 {
			f.writeLine(level, "else:")
			for _, stmt := range n.Else {
				f.writeStmt(level+1, stmt)
			}
		}
	case *ast.WhileStmt:
		f.writeLine(level, formatIfHeader("while", n.Hint, n.Cond))
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.ForStmt:
		line := "for "
		if n.Reverse {
			line += "rev "
		}
		line += n.Name + " in " + formatExpr(n.Start) + " " + lexer.TokenName(n.Op) + " " + formatExpr(n.End)
		if n.Step != nil {
			line += " .. " + formatExpr(n.Step)
		}
		line += ":"
		f.writeLine(level, line)
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.IterForStmt:
		line := "for "
		switch n.Mode {
		case ast.IterBindRef:
			line += "ref "
		case ast.IterBindMutableRef:
			line += "mutable ref "
		}
		sourceText := formatExpr(n.Source)
		var wherePredicate ast.Expr
		if source, predicate, ok := iterForWhereSource(n.Source); ok {
			sourceText = formatExpr(source)
			wherePredicate = predicate
		}
		if n.Reverse {
			sourceText = "rev(" + sourceText + ")"
		}
		line += formatMoveBindPattern(n.Pattern) + " in " + sourceText
		if wherePredicate != nil {
			line += " where " + formatExpr(wherePredicate)
		}
		if n.Filter != nil {
			line += " if " + formatExpr(n.Filter)
		}
		line += ":"
		f.writeLine(level, line)
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.ParallelForStmt:
		line := "parallel for " + n.Name
		if n.IndexName != "" {
			line += " at " + n.IndexName
		}
		line += " in " + formatExpr(n.Source) + ":"
		f.writeLine(level, line)
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.MatchStmt:
		f.writeLine(level, formatMatchHeader("match", n.Value, n.Store))
		f.writeMatchArms(level, n.Arms)
	case *ast.ExpectPatternStmt:
		patterns := make([]string, 0, len(n.Patterns))
		for _, pattern := range n.Patterns {
			patterns = append(patterns, formatMatchPattern(pattern))
		}
		f.writeLine(level, "expect "+formatExpr(n.Value)+" as "+strings.Join(patterns, " | "))
	case *ast.InStoreStmt:
		f.writeLine(level, "in "+formatExpr(n.Store)+":")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.CanStmt:
		if line, ok := formatSingleStmtCanBlock(n); ok {
			f.writePrefixedMultiline(level, "", line)
			return
		}
		f.writeLine(level, "can "+formatPermissionRefSurfaceList(n.Permissions)+":")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.WithStmt:
		f.writeLine(level, "with "+formatWithValueClause(n.Bundles, n.Args, n.WithItemOrder)+":")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.ArgsScopeStmt:
		f.writeLine(level, "with args("+formatArgsScopeClause(n.ParamPacks, n.Args, n.ItemOrder)+"):")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.CascadeStmt:
		f.writeLine(level, "cascade "+formatExpr(n.Target)+":")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.ScopeStmt:
		f.writeLine(level, "scope "+formatExpr(n.Guard)+":")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.PoolStmt:
		f.writeLine(level, "pool "+n.Name+"("+formatExpr(n.Workers)+"):")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.LockStmt:
		f.writeLine(level, "lock "+formatExpr(n.Mutex)+" as "+n.GuardName+":")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.PassStmt:
		f.writeLine(level, "pass")
	case *ast.SignalStmt:
		f.writeLine(level, "signal "+formatPermissionRefSurfaceList(n.Permissions))
	case *ast.PanicStmt:
		f.writePrefixedMultiline(level, "", "panic("+formatExpr(n.Message)+")")
	case *ast.ExprStmt:
		f.writePrefixedMultiline(level, "", formatExpr(n.Expr))
	case *ast.StaticIfStmt:
		f.writeLine(level, "static if "+formatExpr(n.Cond)+":")
		for _, stmt := range n.Then {
			f.writeStmt(level+1, stmt)
		}
		for _, elif := range n.Elifs {
			f.writeLine(level, "static elif "+formatExpr(elif.Cond)+":")
			for _, stmt := range elif.Body {
				f.writeStmt(level+1, stmt)
			}
		}
		if len(n.Else) > 0 {
			f.writeLine(level, "static else:")
			for _, stmt := range n.Else {
				f.writeStmt(level+1, stmt)
			}
		}
	case *ast.StaticErrorStmt:
		f.writePrefixedMultiline(level, "", "static error("+formatExpr(n.Message)+")")
	case *ast.DiscardStmt:
		f.writePrefixedMultiline(level, "", "_ = "+formatExpr(n.Value))
	case *ast.RegionStmt:
		if len(n.Body) != 0 || n.OwnerName != "" {
			line := "with arena " + n.Name
			if n.Capacity != nil {
				line += "(" + formatExpr(n.Capacity) + ")"
			}
			line += " as " + n.OwnerName + ":"
			f.writeLine(level, line)
			for _, stmt := range n.Body {
				f.writeStmt(level+1, stmt)
			}
			return
		}
		line := "region " + n.Name
		if n.Capacity != nil {
			line += "(" + formatExpr(n.Capacity) + ")"
		}
		f.writeLine(level, line)
	case *ast.DestroyStmt:
		f.writeLine(level, "destroy "+n.Name)
	case *ast.MarkStmt:
		f.writeLine(level, "mark "+n.RegionName+" as "+n.Name)
	case *ast.CheckpointStmt:
		line := "checkpoint " + n.Name + " = " + formatExpr(n.Target)
		if len(n.Body) != 0 {
			line += ":"
		}
		f.writeLine(level, line)
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.GroupedCheckpointStmt:
		parts := make([]string, 0, len(n.Targets))
		for _, target := range n.Targets {
			parts = append(parts, formatExpr(target))
		}
		f.writeLine(level, "checkpoint "+strings.Join(parts, ", ")+":")
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.RestoreStmt:
		f.writeLine(level, "restore "+n.RegionName+" from "+n.MarkName)
	case *ast.RestoreCheckpointStmt:
		f.writeLine(level, "restore "+n.Name)
	case *ast.ResetStmt:
		f.writeLine(level, "reset "+n.Name)
	}
}

func iterForWhereSource(expr ast.Expr) (ast.Expr, ast.Expr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call == nil || len(call.Args) != 2 {
		return nil, nil, false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident.Name != "where" {
		return nil, nil, false
	}
	return call.Args[0], call.Args[1], true
}
func (f *formatter) writeMatchArms(level int, arms []ast.MatchArm) {
	for _, arm := range arms {
		f.writeLine(level+1, formatMatchPattern(arm.Pattern)+":")
		for _, stmt := range arm.Body {
			f.writeStmt(level+2, stmt)
		}
	}
}
func formatAnnotation(annotation ast.Annotation) string {
	if len(annotation.Args) == 0 {
		return "@" + annotation.Name
	}
	return "@" + annotation.Name + "(" + strings.Join(annotation.Args, ", ") + ")"
}
func formatGenericParams(genericParams []ast.GenericParam, typeParams []string, refStorageParams []string, refStateParams []string, regionParams []string, permissionParams []string) string {
	parts := make([]string, 0, len(genericParams)+len(typeParams)+len(refStorageParams)+len(refStateParams)+len(regionParams)+len(permissionParams))
	if len(genericParams) != 0 {
		seenRegion := map[string]bool{}
		seenPermission := map[string]bool{}
		for _, param := range genericParams {
			switch param.Kind {
			case ast.GenericParamRefStorage:
				parts = append(parts, "refstorage "+param.Name)
			case ast.GenericParamRefState:
				parts = append(parts, "refstate "+param.Name)
			case ast.GenericParamRegion:
				seenRegion[param.Name] = true
				parts = append(parts, "region "+param.Name)
			case ast.GenericParamPermission:
				seenPermission[param.Name] = true
				parts = append(parts, "permission "+param.Name)
			default:
				if param.InterfaceBound != "" {
					parts = append(parts, param.Name+": "+param.InterfaceBound)
				} else {
					parts = append(parts, param.Name)
				}
			}
		}
		for _, name := range regionParams {
			if !seenRegion[name] {
				parts = append(parts, "region "+name)
			}
		}
		for _, name := range permissionParams {
			if !seenPermission[name] {
				parts = append(parts, "permission "+name)
			}
		}
	} else {
		parts = append(parts, typeParams...)
		for _, name := range refStorageParams {
			parts = append(parts, "refstorage "+name)
		}
		for _, name := range refStateParams {
			parts = append(parts, "refstate "+name)
		}
		for _, name := range regionParams {
			parts = append(parts, "region "+name)
		}
		for _, name := range permissionParams {
			parts = append(parts, "permission "+name)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
func formatAggregateStateSuffix(hasStateParam bool, stateParamCount int) string {
	count := stateParamCount
	if count == 0 && hasStateParam {
		count = 1
	}
	if count <= 0 {
		return ""
	}
	parts := make([]string, count)
	for i := range parts {
		parts[i] = "?"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
func formatFuncHeader(name string, genericParams []ast.GenericParam, typeParams []string, refStorageParams []string, refStateParams []string, regionParams []string, permissionParams []string, params []ast.ParamDecl, paramPacks []ast.ParamPackUse, paramItemOrder []ast.ParamSigItem, implicitParams []ast.ParamDecl, implicitBundles []string, implicitItemOrder []ast.ImplicitSigItem, retType ast.TypeExpr, effectAlias string, effects []ast.SignatureEffectItem, permissions []ast.PermissionRef, ensures []ast.EnsuresClause, variadic bool) string {
	line := formatImplMethodHeader(name, genericParams, typeParams, refStorageParams, refStateParams, regionParams, permissionParams, params, paramPacks, paramItemOrder, implicitParams, implicitBundles, implicitItemOrder, retType, effectAlias, effects, permissions, ensures, variadic)
	line += ":"
	return line
}
func formatImplMethodHeader(name string, genericParams []ast.GenericParam, typeParams []string, refStorageParams []string, refStateParams []string, regionParams []string, permissionParams []string, params []ast.ParamDecl, paramPacks []ast.ParamPackUse, paramItemOrder []ast.ParamSigItem, implicitParams []ast.ParamDecl, implicitBundles []string, implicitItemOrder []ast.ImplicitSigItem, retType ast.TypeExpr, effectAlias string, effects []ast.SignatureEffectItem, permissions []ast.PermissionRef, ensures []ast.EnsuresClause, variadic bool) string {
	line := "def " + name
	line += formatGenericParams(genericParams, typeParams, refStorageParams, refStateParams, regionParams, permissionParams)
	line += "(" + formatExplicitParamList(params, paramPacks, paramItemOrder, variadic) + ")"
	line += formatWithSignatureClause(implicitBundles, implicitParams, implicitItemOrder)
	if retType != nil {
		line += " -> " + formatTypeExpr(retType)
	}
	line += formatSignatureEffects(effectAlias, effects)
	if effectAlias == "" && len(effects) == 0 {
		line += formatPermissionRefs(permissions)
	}
	line += formatEnsuresClauses(ensures)
	return line
}
func formatExternFuncHeader(name string, genericParams []ast.GenericParam, typeParams []string, refStorageParams []string, refStateParams []string, regionParams []string, permissionParams []string, params []ast.ParamDecl, paramPacks []ast.ParamPackUse, paramItemOrder []ast.ParamSigItem, implicitParams []ast.ParamDecl, implicitBundles []string, implicitItemOrder []ast.ImplicitSigItem, retType ast.TypeExpr, effectAlias string, effects []ast.SignatureEffectItem, permissions []ast.PermissionRef, ensures []ast.EnsuresClause, variadic bool) string {
	line := "extern " + name
	line += formatGenericParams(genericParams, typeParams, refStorageParams, refStateParams, regionParams, permissionParams)
	line += "(" + formatExplicitParamList(params, paramPacks, paramItemOrder, variadic) + ")"
	line += formatWithSignatureClause(implicitBundles, implicitParams, implicitItemOrder)
	if retType != nil {
		line += " -> " + formatTypeExpr(retType)
	}
	line += formatSignatureEffects(effectAlias, effects)
	if effectAlias == "" && len(effects) == 0 {
		line += formatPermissionRefs(permissions)
	}
	line += formatEnsuresClauses(ensures)
	return line
}
func formatExportFuncHeader(n *ast.ExportFuncDecl) string {
	line := "export func " + n.Name + "(" + formatExplicitParamList(n.Params, nil, nil, false) + ")"
	if n.ReturnType != nil {
		line += " -> " + formatTypeExpr(n.ReturnType)
	}
	line += " = " + n.TargetName
	if len(n.TargetTypeArgs) > 0 {
		parts := make([]string, 0, len(n.TargetTypeArgs))
		for _, arg := range n.TargetTypeArgs {
			parts = append(parts, formatTypeExpr(arg))
		}
		line += "[" + strings.Join(parts, ", ") + "]"
	}
	return line
}
func formatExplicitParamList(params []ast.ParamDecl, paramPacks []ast.ParamPackUse, order []ast.ParamSigItem, variadic bool) string {
	parts := make([]string, 0, len(params)+len(paramPacks)+1)
	if len(order) != 0 {
		for _, item := range order {
			if item.IsPack {
				parts = append(parts, formatSignatureParamPackUse(item.Pack))
				continue
			}
			parts = append(parts, formatParamDecl(item.Param))
		}
	} else {
		for _, param := range params {
			parts = append(parts, formatParamDecl(param))
		}
	}
	if variadic {
		parts = append(parts, "...")
	}
	return strings.Join(parts, ", ")
}
func formatParamDecl(param ast.ParamDecl) string {
	line := ""
	if param.Mutable {
		line += "mutable "
	}
	line += param.Name + ": " + formatTypeExpr(param.Type)
	if param.DefaultValue != nil {
		line += " = " + formatExpr(param.DefaultValue)
	}
	return line
}
func formatParamList(params []ast.ParamDecl) string {
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(parts, formatParamDecl(param))
	}
	return strings.Join(parts, ", ")
}
