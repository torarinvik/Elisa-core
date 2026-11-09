package semantic

import (
	"elisacore/src/ast"
	"sort"
	"strings"
)

func (a *Analyzer) resolveErrorSetExpr(expr *ast.ErrorSetExpr) Type {
	if expr == nil {
		return invalidType
	}
	if len(expr.Tags) == 0 {
		a.errorf(expr.Pos(), "error[...] requires at least one qualified error tag")
		return invalidType
	}
	if expr.HasEllipsis && containsWildcardErrorTag(expr.Tags) {
		a.errorf(expr.Pos(), "error[Set.*, ...] is no longer supported; use error[Set] instead")
		return invalidType
	}
	if expr.HasEllipsis {
		// `error[Tags, ...]` used to round the listed selections up to their whole
		// declared families — a CLOSED set, despite reading as "open to more". A bare
		// set name already means the whole family, so the suffix added nothing but
		// confusion; it is no longer supported.
		a.errorf(expr.Pos(), "the `, ...` suffix in error[...] is no longer supported; name the full sets directly (a bare set name already means the whole family)")
		return invalidType
	}
	if containsWildcardErrorTag(expr.Tags) {
		if len(expr.Tags) != 1 {
			a.errorf(expr.Pos(), "error[Set.*] cannot be mixed with explicit tags")
			return invalidType
		}
		a.errorf(expr.Pos(), "error[Set.*] is no longer supported; use error[Set] instead")
		return invalidType
	}
	// Partition: bare names that resolve to in-scope error-set generic params
	// become the set's Params component; everything else is the concrete part.
	// `error[R]` is the pure opaque placeholder; `error[R, Timeout]` carries
	// both; `error[R, S]` unions two params.
	var paramNames []string
	concreteExprTags := make([]ast.ErrorTagExpr, 0, len(expr.Tags))
	seenParams := map[string]bool{}
	for _, tag := range expr.Tags {
		if tag.Tag == "" && !tag.Family && a.lookupErrorSetParam(tag.SetName) {
			if seenParams[tag.SetName] {
				a.errorf(tag.Position, "duplicate error-set parameter %q in error[...]", tag.SetName)
				return invalidType
			}
			seenParams[tag.SetName] = true
			paramNames = append(paramNames, tag.SetName)
			continue
		}
		concreteExprTags = append(concreteExprTags, tag)
	}
	if len(paramNames) != 0 {
		if len(concreteExprTags) == 0 {
			if len(paramNames) == 1 {
				// `error[R]`: an opaque placeholder, bound at each call site.
				return &ErrorSetType{Name: paramNames[0], Params: paramNames}
			}
			return &ErrorSetType{Name: errorSetNameWithParams("", 0, paramNames), Params: paramNames}
		}
		concrete := a.resolveErrorSetExpr(&ast.ErrorSetExpr{Position: expr.Position, Tags: concreteExprTags})
		concreteSet, ok := concrete.(*ErrorSetType)
		if !ok || concreteSet == nil {
			return invalidType
		}
		if concreteSet.HasParams() {
			a.errorf(expr.Pos(), "internal error: nested error-set params in error[...]")
			return invalidType
		}
		mixed := &ErrorSetType{
			Tags:     append([]string(nil), concreteSet.Tags...),
			Payloads: concreteSet.Payloads,
			Params:   paramNames,
		}
		mixed.Name = errorSetNameWithParams(concreteSet.Name, len(mixed.Tags), paramNames)
		return mixed
	}
	// Normalize: a bare `Name` that is not a declared set resolves as a single
	// variant searched across all error sets (family-qualified on conflict). An
	// explicit `*Set` stays a whole family. Qualified `Set.Tag` is untouched.
	tags := make([]ast.ErrorTagExpr, 0, len(expr.Tags))
	for _, tag := range expr.Tags {
		normalized, ok := a.normalizeErrorTag(tag)
		if !ok {
			return invalidType
		}
		tags = append(tags, normalized)
	}
	if len(tags) == 1 && tags[0].Tag == "" {
		_, errSet := a.lookupDeclaredErrorSet(tags[0])
		if errSet == nil {
			return invalidType
		}
		return errSet
	}
	familySets := map[string]*ErrorSetType{}
	fullFamilies := map[string]bool{}
	selectedTags := map[string]map[string]bool{}
	seenTags := map[string]ast.ErrorTagExpr{}
	for _, tag := range tags {
		_, errSet := a.lookupDeclaredErrorSet(tag)
		if errSet == nil {
			return invalidType
		}
		familySets[tag.SetName] = errSet
		if tag.Tag == "" {
			fullFamilies[tag.SetName] = true
			for _, qualifiedTag := range errSet.Tags {
				if prev, ok := seenTags[qualifiedTag]; ok {
					_, shortName := SplitErrorTagName(qualifiedTag)
					a.errorf(tag.Position, "duplicate error tag %q in error set via %s.%s and %s", shortName, prev.SetName, prev.Tag, tag.SetName)
					return invalidType
				}
				seenTags[qualifiedTag] = ast.ErrorTagExpr{Position: tag.Position, SetName: tag.SetName, Tag: ErrorTagShortName(qualifiedTag)}
			}
			continue
		}
		qualifiedTag := QualifyErrorTag(tag.SetName, tag.Tag)
		if !errSet.HasTag(qualifiedTag) {
			a.errorf(tag.Position, "error set %q has no tag %q", tag.SetName, tag.Tag)
			return invalidType
		}
		if prev, ok := seenTags[qualifiedTag]; ok {
			a.errorf(tag.Position, "duplicate error tag %q in error set via %s.%s and %s.%s", tag.Tag, prev.SetName, prev.Tag, tag.SetName, tag.Tag)
			return invalidType
		}
		seenTags[qualifiedTag] = tag
		if selectedTags[tag.SetName] == nil {
			selectedTags[tag.SetName] = map[string]bool{}
		}
		selectedTags[tag.SetName][qualifiedTag] = true
	}
	return CanonicalizeErrorSetSelections(familySets, fullFamilies, selectedTags)
}

func (a *Analyzer) lookupDeclaredErrorSet(tag ast.ErrorTagExpr) (string, *ErrorSetType) {
	t, ok := a.namedTypes[tag.SetName]
	if !ok {
		var resolved Type
		resolved, _, ok = a.lookupVisibleType(tag.SetName)
		if ok {
			t = resolved
		}
	}
	if !ok {
		a.errorf(tag.Position, "unknown error set %q", tag.SetName)
		return "", nil
	}
	errSet, ok := t.(*ErrorSetType)
	if !ok {
		a.errorf(tag.Position, "%q is not an error set", tag.SetName)
		return "", nil
	}
	return tag.SetName, errSet
}

func containsWildcardErrorTag(tags []ast.ErrorTagExpr) bool {
	for _, tag := range tags {
		if tag.Tag == "*" {
			return true
		}
	}
	return false
}

func containsBareFamily(tags []ast.ErrorTagExpr) bool {
	for _, tag := range tags {
		if tag.Tag == "" {
			return true
		}
	}
	return false
}

// errorSetTypeQuiet looks up name as a declared error set without reporting an
// error if it is missing or not an error set.
func (a *Analyzer) errorSetTypeQuiet(name string) (*ErrorSetType, bool) {
	t, ok := a.namedTypes[name]
	if !ok {
		if resolved, _, found := a.lookupVisibleType(name); found {
			t = resolved
			ok = true
		}
	}
	if !ok {
		return nil, false
	}
	errSet, ok := t.(*ErrorSetType)
	return errSet, ok
}

// findErrorVariantOwners returns the canonical names of declared error sets that
// contain a variant with the given short name, sorted for stable diagnostics.
func (a *Analyzer) findErrorVariantOwners(shortName string) []string {
	seen := map[string]bool{}
	var owners []string
	for _, t := range a.namedTypes {
		errSet, ok := t.(*ErrorSetType)
		if !ok || errSet == nil || errSet.HasParams() {
			continue
		}
		for _, qtag := range errSet.Tags {
			if ErrorTagShortName(qtag) == shortName {
				if !seen[errSet.Name] {
					seen[errSet.Name] = true
					owners = append(owners, errSet.Name)
				}
				break
			}
		}
	}
	sort.Strings(owners)
	return owners
}

// normalizeErrorTag canonicalizes one error[...] item: qualified `Set.Tag` and
// explicit `*Set` pass through; a bare `Name` is a whole family if Name is a
// declared set, otherwise the single variant `Name` searched across all sets
// (an ambiguous bare variant must be qualified `Set.Name`).
func (a *Analyzer) normalizeErrorTag(tag ast.ErrorTagExpr) (ast.ErrorTagExpr, bool) {
	if tag.Tag != "" {
		return tag, true
	}
	if _, ok := a.errorSetTypeQuiet(tag.SetName); ok {
		return tag, true
	}
	if tag.Family {
		a.errorf(tag.Position, "unknown error set %q", tag.SetName)
		return tag, false
	}
	owners := a.findErrorVariantOwners(tag.SetName)
	switch len(owners) {
	case 0:
		a.errorf(tag.Position, "unknown error set or variant %q", tag.SetName)
		return tag, false
	case 1:
		return ast.ErrorTagExpr{Position: tag.Position, SetName: owners[0], Tag: tag.SetName}, true
	default:
		a.errorf(tag.Position, "error variant %q is ambiguous across sets %s; qualify it as %s.%s", tag.SetName, strings.Join(owners, ", "), owners[0], tag.SetName)
		return tag, false
	}
}

func onlyBareFamilies(tags []ast.ErrorTagExpr) bool {
	for _, tag := range tags {
		if tag.Tag != "" {
			return false
		}
	}
	return len(tags) > 0
}

func singleExplicitErrorFamily(tags []ast.ErrorTagExpr) (string, bool) {
	family := ""
	for _, tag := range tags {
		if tag.Tag == "" {
			return "", false
		}
		if family == "" {
			family = tag.SetName
			continue
		}
		if tag.SetName != family {
			return "", false
		}
	}
	return family, family != ""
}
