package semantic

import (
	"strings"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

type PackedVariantGuard struct {
	EnumName    string
	VariantName string
}

type GuardFactSet struct {
	NonNull        map[string]bool
	Null           map[string]bool
	PackedVariants map[string]PackedVariantGuard
	Leq            map[string]map[string]bool
}

func NewGuardFactSet() GuardFactSet {
	return GuardFactSet{}
}

func (g GuardFactSet) Clone() GuardFactSet {
	cloned := GuardFactSet{}
	if len(g.NonNull) != 0 {
		cloned.NonNull = make(map[string]bool, len(g.NonNull))
		for key, value := range g.NonNull {
			cloned.NonNull[key] = value
		}
	}
	if len(g.Null) != 0 {
		cloned.Null = make(map[string]bool, len(g.Null))
		for key, value := range g.Null {
			cloned.Null[key] = value
		}
	}
	if len(g.PackedVariants) != 0 {
		cloned.PackedVariants = make(map[string]PackedVariantGuard, len(g.PackedVariants))
		for key, value := range g.PackedVariants {
			cloned.PackedVariants[key] = value
		}
	}
	if len(g.Leq) != 0 {
		cloned.Leq = make(map[string]map[string]bool, len(g.Leq))
		for left, rights := range g.Leq {
			if len(rights) == 0 {
				continue
			}
			clonedRights := make(map[string]bool, len(rights))
			for right, value := range rights {
				clonedRights[right] = value
			}
			cloned.Leq[left] = clonedRights
		}
	}
	return cloned
}

func GuardFactsForCondition(expr ast.Expr, truthy bool) GuardFactSet {
	facts := NewGuardFactSet()
	facts.addCondition(expr, truthy)
	return facts
}

func (g *GuardFactSet) MergeFrom(other GuardFactSet) {
	if g == nil {
		return
	}
	for key := range other.NonNull {
		g.ensureNonNull()[key] = true
		if g.Null != nil {
			delete(g.Null, key)
		}
	}
	for key := range other.Null {
		g.ensureNull()[key] = true
		if g.NonNull != nil {
			delete(g.NonNull, key)
		}
	}
	for key, value := range other.PackedVariants {
		g.ensurePackedVariants()[key] = value
	}
	for left, rights := range other.Leq {
		for right := range rights {
			g.addLEKeys(left, right)
		}
	}
}

func (g *GuardFactSet) AddNonNull(expr ast.Expr) {
	if g == nil {
		return
	}
	key := guardFactExprKey(expr)
	if key == "" {
		return
	}
	g.ensureNonNull()[key] = true
	if g.Null != nil {
		delete(g.Null, key)
	}
}

func (g *GuardFactSet) AddNull(expr ast.Expr) {
	if g == nil {
		return
	}
	key := guardFactExprKey(expr)
	if key == "" {
		return
	}
	g.ensureNull()[key] = true
	if g.NonNull != nil {
		delete(g.NonNull, key)
	}
}

func (g *GuardFactSet) AddPackedVariant(expr ast.Expr, viewType *PackedVariantViewType) {
	if g == nil || viewType == nil || viewType.Enum == nil || viewType.Variant == nil {
		return
	}
	g.AddVariantProof(expr, viewType.Enum.Name, viewType.Variant.Name)
}

func (g *GuardFactSet) AddVariantProof(expr ast.Expr, typeName string, variantName string) {
	if g == nil || typeName == "" || variantName == "" {
		return
	}
	key := guardFactExprKey(expr)
	if key == "" {
		return
	}
	g.ensurePackedVariants()[key] = PackedVariantGuard{EnumName: typeName, VariantName: variantName}
}

func (g *GuardFactSet) AddLE(left ast.Expr, right ast.Expr) {
	if g == nil {
		return
	}
	leftKey := guardFactExprKey(left)
	rightKey := guardFactExprKey(right)
	g.addLEKeys(leftKey, rightKey)
}

func (g GuardFactSet) ProvesNonNull(expr ast.Expr) bool {
	key := guardFactExprKey(expr)
	if key == "" {
		return false
	}
	return g.NonNull[key]
}

func (g GuardFactSet) ProvesNull(expr ast.Expr) bool {
	key := guardFactExprKey(expr)
	if key == "" {
		return false
	}
	return g.Null[key]
}

func (g GuardFactSet) PackedVariant(expr ast.Expr) (PackedVariantGuard, bool) {
	key := guardFactExprKey(expr)
	if key == "" || len(g.PackedVariants) == 0 {
		return PackedVariantGuard{}, false
	}
	guard, ok := g.PackedVariants[key]
	return guard, ok
}

func (g GuardFactSet) ProveLE(left ast.Expr, right ast.Expr) bool {
	leftKey := guardFactExprKey(left)
	rightKey := guardFactExprKey(right)
	return g.proveLEKeys(leftKey, rightKey)
}

func (g GuardFactSet) CheckFieldAccess(expr ast.Expr, objType Type, field string) bool {
	if field == "" || objType == nil {
		return false
	}
	if fieldInfo, ok := dstrSyntheticField(objType, field); ok {
		return fieldInfo.Type != nil
	}
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull && !g.ProvesNonNull(expr) {
			return false
		}
		objType = ref.Elem
	}
	objType = StripAggregateStateType(objType)
	if _, ok := packedStoreSyntheticField(objType, field); ok {
		return true
	}
	if viewType, ok := objType.(*PackedVariantViewType); ok {
		_, ok := viewType.Field(field)
		return ok
	}
	if viewType, ok := objType.(*TreeVariantViewType); ok {
		_, ok := viewType.Field(field)
		return ok
	}
	if enumType, ok := objType.(*EnumType); ok && enumType != nil && enumType.Packed {
		if _, ok := enumType.Common[field]; ok {
			return true
		}
		guard, ok := g.PackedVariant(expr)
		if !ok || guard.EnumName != enumType.Name {
			return false
		}
		variant, ok := enumType.Variant(guard.VariantName)
		if !ok || variant == nil {
			return false
		}
		_, ok = variant.PayloadIndex(field)
		return ok
	}
	if categoryType, ok := objType.(*TreeCategoryType); ok && categoryType != nil {
		if _, ok := categoryType.Common[field]; ok {
			return true
		}
		guard, ok := g.PackedVariant(expr)
		if !ok || guard.EnumName != categoryType.Name {
			return false
		}
		variant, ok := categoryType.Variant(guard.VariantName)
		if !ok || variant == nil {
			return false
		}
		_, ok = variant.PayloadIndex(field)
		return ok
	}
	switch objType.(type) {
	case *DArrayViewType:
		return field == "data" || field == "len" || field == "elem_size"
	case *SViewType:
		return field == "data" || field == "len"
	}
	switch t := objType.(type) {
	case *StructType:
		_, ok := t.Fields[field]
		return ok
	case *TreeBlockType:
		_, ok := t.Fields[field]
		return ok
	case *TreeStructType:
		_, ok := t.Fields[field]
		return ok
	case *GenericInstanceType:
		baseStruct, ok := t.Base.(*StructType)
		if !ok || baseStruct == nil {
			return false
		}
		_, ok = baseStruct.Fields[field]
		return ok
	default:
		return false
	}
}

func (g *GuardFactSet) addCondition(expr ast.Expr, truthy bool) {
	if g == nil || expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		g.addCondition(n.Inner, truthy)
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			g.addCondition(n.Operand, !truthy)
		}
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			if truthy {
				g.addCondition(n.Left, true)
				g.addCondition(n.Right, true)
			}
		case lexer.TOKEN_OR:
			if !truthy {
				g.addCondition(n.Left, false)
				g.addCondition(n.Right, false)
			}
		case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
			targetExpr, nonNull, ok := refinedExprNullState(n, truthy)
			if ok {
				if nonNull {
					g.AddNonNull(targetExpr)
				} else {
					g.AddNull(targetExpr)
				}
			}
		case lexer.TOKEN_LT, lexer.TOKEN_LTEQ:
			if truthy {
				g.AddLE(n.Left, n.Right)
			}
		case lexer.TOKEN_GT, lexer.TOKEN_GTEQ:
			if truthy {
				g.AddLE(n.Right, n.Left)
			}
		case lexer.TOKEN_IS:
			if truthy {
				if targetExpr, guard, ok := guardFactPackedVariantTest(n); ok {
					g.ensurePackedVariants()[guardFactExprKey(targetExpr)] = guard
				}
			}
		}
	}
}

func guardFactPackedVariantTest(expr *ast.BinaryExpr) (ast.Expr, PackedVariantGuard, bool) {
	if expr == nil || expr.Op != lexer.TOKEN_IS {
		return nil, PackedVariantGuard{}, false
	}
	enumName, variantName, ok := guardFactVariantTarget(expr.Right)
	if !ok {
		return nil, PackedVariantGuard{}, false
	}
	key := guardFactExprKey(expr.Left)
	if key == "" {
		return nil, PackedVariantGuard{}, false
	}
	return expr.Left, PackedVariantGuard{EnumName: enumName, VariantName: variantName}, true
}

func guardFactVariantTarget(expr ast.Expr) (string, string, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return guardFactVariantTarget(n.Inner)
	case *ast.FieldExpr:
		if objectName, ok := qualifiedTypePathFromExpr(n.Object); ok && objectName != "" && n.Field != "" {
			return objectName, n.Field, true
		}
	case *ast.TypeExprExpr:
		named, ok := n.Type.(*ast.NamedType)
		if !ok || named == nil {
			return "", "", false
		}
		idx := strings.LastIndex(named.Name, ".")
		if idx <= 0 || idx+1 >= len(named.Name) {
			return "", "", false
		}
		return named.Name[:idx], named.Name[idx+1:], true
	}
	return "", "", false
}

func (g *GuardFactSet) addLEKeys(leftKey string, rightKey string) {
	if g == nil || leftKey == "" || rightKey == "" {
		return
	}
	if g.Leq == nil {
		g.Leq = map[string]map[string]bool{}
	}
	rights := g.Leq[leftKey]
	if rights == nil {
		rights = map[string]bool{}
		g.Leq[leftKey] = rights
	}
	rights[rightKey] = true
}

func (g GuardFactSet) proveLEKeys(leftKey string, rightKey string) bool {
	if leftKey == "" || rightKey == "" {
		return false
	}
	if leftKey == rightKey {
		return true
	}
	if len(g.Leq) == 0 {
		return false
	}
	seen := map[string]bool{leftKey: true}
	queue := []string{leftKey}
	for len(queue) != 0 {
		current := queue[0]
		queue = queue[1:]
		for next := range g.Leq[current] {
			if next == rightKey {
				return true
			}
			if seen[next] {
				continue
			}
			seen[next] = true
			queue = append(queue, next)
		}
	}
	return false
}

func (g *GuardFactSet) ensureNonNull() map[string]bool {
	if g.NonNull == nil {
		g.NonNull = map[string]bool{}
	}
	return g.NonNull
}

func (g *GuardFactSet) ensureNull() map[string]bool {
	if g.Null == nil {
		g.Null = map[string]bool{}
	}
	return g.Null
}

func (g *GuardFactSet) ensurePackedVariants() map[string]PackedVariantGuard {
	if g.PackedVariants == nil {
		g.PackedVariants = map[string]PackedVariantGuard{}
	}
	return g.PackedVariants
}

func guardFactExprKey(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	if key, ok := exprRefinementKey(expr); ok && key != "" {
		return key
	}
	return optimizationExprString(expr)
}
