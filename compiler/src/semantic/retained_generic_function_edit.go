package semantic

import (
	"slices"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
)

// A type-binder spelling change uses the existing remove/redeclare path, so its
// function type, specialization identity, and dependent bodies are rebuilt.
// Restrict this entry point to alpha-equivalent headers at fixed protocol.
func retainedGenericFunctionBinderRename(old, current *ast.FuncDecl) bool {
	if old.Name != current.Name || old.Static || current.Static || old.IsLaw || current.IsLaw || old.IsContract || current.IsContract || old.IsLemma || current.IsLemma || old.IsGhost || current.IsGhost || strings.HasPrefix(old.Name, "__") || len(old.TypeParams) == 0 || len(old.TypeParams) != len(current.TypeParams) || len(old.GenericParams) != len(current.GenericParams) || len(old.Params) != len(current.Params) {
		return false
	}
	if !slices.Equal(old.RegionParams, current.RegionParams) || !slices.Equal(old.PermissionParams, current.PermissionParams) {
		return false
	}
	for i, p := range old.Params {
		if p.Name != current.Params[i].Name {
			return false
		}
	}
	// Only type syntax is alpha-renamed. Keep annotations, defaults, contracts,
	// effect declarations and every other header property exactly unchanged.
	nonTypeHeader := func(fn *ast.FuncDecl) string {
		header := *fn
		header.TypeParams, header.GenericParams = nil, nil
		header.ReturnType = nil
		header.Params = slices.Clone(fn.Params)
		for i := range header.Params {
			header.Params[i].Type = nil
		}
		return retainedHeader(&header)
	}
	if nonTypeHeader(old) != nonTypeHeader(current) {
		return false
	}

	renamed := false
	for i, p := range old.GenericParams {
		q := current.GenericParams[i]
		if p.Kind != q.Kind || (p.Kind != ast.GenericParamType && p.Kind != ast.GenericParamRegion) || p.InterfaceBound != q.InterfaceBound || p.StateOwner != q.StateOwner || !slices.Equal(p.StateCases, q.StateCases) || (p.Kind != ast.GenericParamType && p.Name != q.Name) {
			return false
		}
	}
	for i, name := range old.TypeParams {
		renamed = renamed || name != current.TypeParams[i]
	}
	if !renamed {
		return false
	}
	type headerToken struct {
		kind lexer.TokenKind
		text string
	}
	normalize := func(fn *ast.FuncDecl) ([]headerToken, bool) {
		names := map[string]string{}
		for i, name := range fn.TypeParams {
			names[name] = "\x00type-binder:" + strconv.Itoa(i)
		}
		scanner := lexer.New("<retained-header>", []byte(retainedHeader(fn)))
		var tokens []headerToken
		scanned := scanner.Tokenize()
		for i, token := range scanned {
			text := token.Text
			if token.Kind == lexer.TOKEN_IDENT {
				if canonical, ok := names[text]; ok {
					// Qualified members/namespaces are not references to this binder.
					// Associated-type headers need a semantic normalization of their own.
					adjacent := func(j int) bool {
						return j >= 0 && j < len(scanned) && (scanned[j].Text == "." || scanned[j].Text == "::")
					}
					if adjacent(i-1) || adjacent(i+1) {
						return nil, false
					}
					text = canonical
				}
			}
			tokens = append(tokens, headerToken{token.Kind, text})
		}
		return tokens, len(scanner.Errors()) == 0
	}
	before, oldOK := normalize(old)
	after, newOK := normalize(current)
	return oldOK && newOK && slices.Equal(before, after)
}

// Non-body consumers (for example global specialized function pointers and
// type/default expressions) are not rebuilt by the function replacement path.
// Keep their existing conservative fallback instead of leaving old base types.
func retainedGenericFunctionHasNonBodyUsers(decls []ast.Decl, target *ast.FuncDecl) bool {
	name := target.Name
	names := map[string]bool{name: true}
	pattern := retainedNamesPattern(names)
	for _, decl := range decls {
		if decl == target {
			continue
		}
		if fn, ok := decl.(*ast.FuncDecl); ok {
			if retainedNameTextMatch(retainedHeader(fn), names, pattern) {
				return true
			}
			continue
		}
		if _, namespace := decl.(*ast.NamespaceDecl); namespace {
			continue
		}
		if retainedNameTextMatch(unparse.FormatDecl(decl), names, pattern) {
			return true
		}
	}
	return false
}
