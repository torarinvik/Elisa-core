package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"sort"
	"strings"
)

func (a *Analyzer) resolveMatchPatternArgsWithOptions(pattern *ast.MatchVariantPattern, variant *EnumVariant, qualified string, nested bool, allowPartialNamed bool) []*ast.MatchPatternArg {
	ordered := make([]*ast.MatchPatternArg, len(variant.Payload))
	if len(pattern.Args) == 0 {
		pattern.ResolvedArgs = ordered
		return ordered
	}
	namedCount := 0
	for i := range pattern.Args {
		if pattern.Args[i].Name != "" {
			namedCount++
		}
	}
	if namedCount != 0 && namedCount != len(pattern.Args) {
		a.errorf(pattern.Pos(), "%s cannot mix positional and named payload patterns", matchPatternContext(qualified, nested))
	}
	if namedCount == 0 {
		if len(pattern.Args) != len(variant.Payload) {
			a.errorf(pattern.Pos(), "%s expects %d payload patterns, got %d", matchPatternContext(qualified, nested), len(variant.Payload), len(pattern.Args))
		}
		limit := len(pattern.Args)
		if len(ordered) < limit {
			limit = len(ordered)
		}
		for i := 0; i < limit; i++ {
			ordered[i] = &pattern.Args[i]
		}
		pattern.ResolvedArgs = ordered
		return ordered
	}
	if !variant.HasNamedPayloads() {
		a.errorf(pattern.Pos(), "%s does not declare named payload fields", matchPatternContext(qualified, nested))
		pattern.ResolvedArgs = ordered
		return ordered
	}
	seen := map[int]lexer.Pos{}
	for i := range pattern.Args {
		arg := &pattern.Args[i]
		index, ok := variant.PayloadIndex(arg.Name)
		if !ok {
			a.errorf(arg.Position, "%s has no payload field %q", matchPatternContext(qualified, nested), arg.Name)
			continue
		}
		if prev, exists := seen[index]; exists {
			a.errorf(arg.Position, "%s payload field %q is matched more than once (first at %s:%d:%d)", matchPatternContext(qualified, nested), arg.Name, prev.File, prev.Line, prev.Col)
			continue
		}
		seen[index] = arg.Position
		ordered[index] = arg
	}
	missing := make([]string, 0)
	for i := range ordered {
		if ordered[i] == nil {
			missing = append(missing, variant.PayloadLabel(i))
		}
	}
	if len(missing) > 0 && !allowPartialNamed {
		sort.Strings(missing)
		a.errorf(pattern.Pos(), "%s is missing named payload patterns for: %s", matchPatternContext(qualified, nested), strings.Join(missing, ", "))
	}
	pattern.ResolvedArgs = ordered
	return ordered
}
func matchPatternContext(qualified string, nested bool) string {
	if nested {
		return "nested match arm " + strconvQuote(qualified)
	}
	return "match arm " + strconvQuote(qualified)
}
func (a *Analyzer) matchCoversAllVariants(variantBase Type, covered map[string]bool, hasWildcard bool) bool {
	if hasWildcard {
		return true
	}
	switch tt := variantBase.(type) {
	case *EnumType:
		if tt == nil {
			return false
		}
		if enumIsHierarchical(tt) {
			// docs/77: a hierarchy node's case-set is the union of its refinements' leaves; check
			// coverage by qualified Owner.Variant key (distinct refinements never collide).
			for _, leaf := range enumDescendantLeaves(tt) {
				if !covered[leaf.Qualified] {
					return false
				}
			}
			return true
		}
		for _, variant := range tt.Variants {
			if !covered[variant.Name] {
				return false
			}
		}
		return true
	case *ConstEnumType:
		if tt == nil {
			return false
		}
		for _, member := range tt.Members {
			if !covered[member.Name] {
				return false
			}
		}
		return true
	case *ErrorSetType:
		if tt == nil {
			return false
		}
		for _, tag := range tt.Tags {
			if !covered[tag] {
				return false
			}
		}
		return true
	case *TreeCategoryType:
		if tt == nil {
			return false
		}
		for _, item := range treeCategoryVariantsInTagOrder(tt) {
			if !covered[item.QualifiedName] {
				return false
			}
		}
		return true
	default:
		return false
	}
}

type treeCategoryVariantItem struct {
	Category      *TreeCategoryType
	Variant       *EnumVariant
	QualifiedName string
}

func treeCategoryVariantsInTagOrder(category *TreeCategoryType) []treeCategoryVariantItem {
	if category == nil || category.Family == nil || category.Decl == nil {
		return nil
	}
	items := make([]treeCategoryVariantItem, 0, len(category.Variants))
	var visit func(decl *ast.TreeCategoryDecl)
	visit = func(decl *ast.TreeCategoryDecl) {
		if decl == nil {
			return
		}
		memberType, _ := category.Family.Member(decl.Name)
		memberCategory, _ := memberType.(*TreeCategoryType)
		if memberCategory != nil {
			for _, variant := range memberCategory.Variants {
				items = append(items, treeCategoryVariantItem{
					Category:      memberCategory,
					Variant:       variant,
					QualifiedName: memberCategory.Name + "." + variant.Name,
				})
			}
		}
		for i := range decl.Nested {
			visit(&decl.Nested[i])
		}
	}
	visit(category.Decl)
	return items
}
func strconvQuote(s string) string {
	return "\"" + s + "\""
}
func (a *Analyzer) reportNonExhaustiveMatch(pos lexer.Pos, variantBase Type, covered map[string]bool, hasWildcard bool) {
	if hasWildcard {
		return
	}
	switch tt := variantBase.(type) {
	case *EnumType:
		if tt == nil {
			return
		}
		missing := make([]string, 0)
		if enumIsHierarchical(tt) {
			// docs/77: report missing leaves across the whole refinement subtree, by qualified name.
			for _, leaf := range enumDescendantLeaves(tt) {
				if !covered[leaf.Qualified] {
					missing = append(missing, leaf.Qualified)
				}
			}
		} else {
			for _, variant := range tt.Variants {
				if !covered[variant.Name] {
					missing = append(missing, tt.Name+"."+variant.Name)
				}
			}
		}
		if len(missing) == 0 {
			return
		}
		a.errorf(pos, "non-exhaustive match over %q; missing variants: %s", tt.Name, strings.Join(missing, ", "))
	case *ConstEnumType:
		if tt == nil {
			return
		}
		missing := make([]string, 0)
		for _, member := range tt.Members {
			if !covered[member.Name] {
				missing = append(missing, tt.Name+"."+member.Name)
			}
		}
		if len(missing) == 0 {
			return
		}
		a.errorf(pos, "non-exhaustive match over %q; missing members: %s", tt.Name, strings.Join(missing, ", "))
	case *ErrorSetType:
		if tt == nil {
			return
		}
		missing := make([]string, 0)
		for _, tag := range tt.Tags {
			if !covered[tag] {
				missing = append(missing, tag)
			}
		}
		if len(missing) == 0 {
			return
		}
		a.errorf(pos, "non-exhaustive match over %q; missing tags: %s", tt.Name, strings.Join(missing, ", "))
	case *TreeCategoryType:
		if tt == nil {
			return
		}
		missing := make([]string, 0)
		for _, variant := range tt.Variants {
			if !covered[variant.Name] {
				missing = append(missing, tt.Name+"."+variant.Name)
			}
		}
		if len(missing) == 0 {
			return
		}
		a.errorf(pos, "non-exhaustive match over %q; missing variants: %s", tt.Name, strings.Join(missing, ", "))
	}
}
func (a *Analyzer) reportNonExhaustiveStringMatchExpr(pos lexer.Pos, hasWildcard bool) {
	if hasWildcard {
		return
	}
	a.errorf(pos, "non-exhaustive string match expression; add a final _ arm")
}
