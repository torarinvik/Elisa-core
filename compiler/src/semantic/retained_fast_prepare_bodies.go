package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"os"
	"strings"
)

// shadowPredict reports, beside the scratch comparison's verdict, whether every edited
// function's local facts are unchanged (the condition fast preparation will rely on).
func (r *RetainedAnalysis) shadowPredict(a *Analyzer, edits [][3]*ast.FuncDecl, scoped map[*ast.FuncDecl]scopedDecl, diff string) {
	same := true
	for _, e := range edits {
		live, old, edit := e[0], e[1], e[2]
		before, ok := r.digestOf[old]
		if !ok {
			before = r.localFactDigest(a, live, old, scoped[live])
		}
		after := r.localFactDigest(a, live, edit, scoped[live])
		if r.digestOf == nil {
			r.digestOf = map[*ast.FuncDecl]string{}
		}
		r.digestOf[edit] = after
		if before == "" || before != after {
			same = false
			fmt.Fprintf(os.Stderr, "[fast-predict] %s facts differ\n--- before\n%s\n--- after\n%s\n", edit.Name, before, after)
		}
	}
	verdict := "UNSAFE"
	switch {
	case same && diff == "":
		verdict = "agree-skip"
	case !same && diff != "":
		verdict = "agree-scratch"
	case !same && diff == "":
		verdict = "conservative"
	}
	fmt.Fprintf(os.Stderr, "[fast-predict] %d edits facts-same=%v scratch-diff=%q -> %s\n", len(edits), same, diff, verdict)
}

// sourceBodySyntaxWitnessKeys is what a body edit may use: every TypeExpr
// syntax the function's source header and body already contain. A  projected
// comparison body carries the body half in its comparison text.
func (r *RetainedAnalysis) sourceBodySyntaxWitnessKeys(old *ast.FuncDecl) (map[string]bool, error) {
	keys := map[string]bool{}
	if old == nil {
		return nil, fmt.Errorf("missing comparison function")
	}
	if len(old.Body) != 0 {
		bodyKeys, err := resolvedTypeNameSyntaxKeysOf(old.Body)
		if err != nil {
			return nil, err
		}
		for key := range bodyKeys {
			keys[key] = true
		}
	} else {
		text := r.comparisonText(old)
		bodyKeys, err := decodeResolvedTypeNameSyntaxKeys(text.bodyTypeNames)
		if err != nil {
			return nil, fmt.Errorf("projected source body has no type-syntax inventory: %w", err)
		}
		for key := range bodyKeys {
			keys[key] = true
		}
	}
	headerKeys, err := resolvedTypeNameHeaderSyntaxKeys(old)
	if err != nil {
		return nil, err
	}
	for key := range headerKeys {
		keys[key] = true
	}
	return keys, nil
}

// fastPathBodies returns the edited bodies prepared without the scratch, or a reason the
// update must wait for it. Only line-preserving edits to function bodies qualify; each
// edited function's declaration-level facts must equal its previous version's, and no
// declaration-phase diagnostic may lie inside it.
func (r *RetainedAnalysis) fastPathBodies(a *Analyzer, edits [][3]*ast.FuncDecl, scoped map[*ast.FuncDecl]scopedDecl, oldDecls []ast.Decl) ([][]ast.Stmt, map[string]map[ast.TypeExpr]string, map[string]string, string) {
	if r.fastPrepareInvalidated != "" {
		return nil, nil, nil, r.fastPrepareInvalidated
	}
	if r.pre == nil || r.classify == nil {
		return nil, nil, nil, "no retained baseline"
	}
	if r.pre.resolvedTypeNames.failure != "" {
		return nil, nil, nil, "resolved type-name ownership baseline is unavailable"
	}
	if !r.resolvedTypeNameSourceOwnersValid {
		return nil, nil, nil, "source TypeExpr ownership inventory is unavailable"
	}
	functionKeys := retainedPreBodyFunctionKeys(r.decls)
	bodies := make([][]ast.Stmt, len(edits))
	resolvedTypeNameRows := map[string]map[ast.TypeExpr]string{}
	resolvedTypeNameContributions := map[string]string{}
	for i, e := range edits {
		live, old, edit := e[0], e[1], e[2]
		oldTypeSyntax, oldTypeSyntaxErr := "", error(nil)
		if len(old.Body) == 0 {
			oldTypeSyntax = r.comparisonText(old).bodyTypeSyntax
			if oldTypeSyntax == "" {
				oldTypeSyntaxErr = fmt.Errorf("projected source body has no type-syntax fingerprint")
			}
		} else {
			oldTypeSyntax, oldTypeSyntaxErr = resolvedTypeNameBodySyntaxFingerprint(old)
		}
		newTypeSyntax, newTypeSyntaxErr := resolvedTypeNameBodySyntaxFingerprint(edit)
		if newTypeSyntaxErr != nil {
			return nil, nil, nil, edit.Name + " changes or cannot certify body type-name syntax"
		}
		syntaxUnchanged := oldTypeSyntaxErr == nil && oldTypeSyntax == newTypeSyntax
		if !syntaxUnchanged {
			// Witness extension: an edit may shift TypeExpr paths or repeat syntax the
			// function already declares in its header or body. Everything is matched by
			// pointer-free syntax, never by path identity or position.
			oldKeys, err := r.sourceBodySyntaxWitnessKeys(old)
			if err != nil {
				return nil, nil, nil, edit.Name + " changes or cannot certify body type-name syntax"
			}
			uncertified := false
			err = visitResolvedTypeNameSyntax(edit.Body, func(key string, expr ast.TypeExpr) {
				if !oldKeys[key] && !a.typeNameIsUnshadowedBuiltin(edit, scoped[live], expr) && !a.typeNameIsUniqueScalarStruct(edit, scoped[live], expr) {
					uncertified = true
				}
			})
			if err != nil {
				return nil, nil, nil, edit.Name + " cannot inventory prepared body type-name syntax: " + err.Error()
			}
			if uncertified {
				return nil, nil, nil, edit.Name + " introduces body type-name syntax outside the function's certified inventory"
			}
		}
		op := old.Pos()
		end := int(^uint(0) >> 1)
		for _, d := range oldDecls {
			if p := d.Pos(); p.File == op.File && p.Line > op.Line && p.Line < end {
				end = p.Line
			}
		}
		for _, d := range r.pre.diags {
			if d.Pos.File == op.File && d.Pos.Line >= op.Line && d.Pos.Line < end {
				return nil, nil, nil, edit.Name + " holds a declaration-phase diagnostic"
			}
		}
		if live.IsLaw {
			return nil, nil, nil, edit.Name + " is a law"
		}
		if classifierFoldShape(old) || classifierFoldShape(edit) {
			return nil, nil, nil, edit.Name + " may fold into a classifier table"
		}
		if r.appendOnlyReports(a, edit, scoped[live]) {
			return nil, nil, nil, edit.Name + " breaks an append-only store rule"
		}
		before, ok := r.digestOf[old]
		if !ok {
			before = r.localFactDigest(a, live, old, scoped[live])
		}
		after := r.localFactDigest(a, live, edit, scoped[live])
		if before == "" || before != after {
			if os.Getenv("ELISACORE_ANALYSIS_CACHE_DEBUG") == "1" || os.Getenv("ELISACORE_RETAINED_FAST_DEBUG") == "1" {
				fmt.Fprintf(os.Stderr, "[fast-path] %s precomputed=%v\n--- before\n%s\n--- after\n%s\n", edit.Name, ok, before, after)
			}
			return nil, nil, nil, edit.Name + " changes declaration-level facts"
		}
		body, why := r.fastPrepareBody(a, live, edit, scoped[live])
		if why != "" {
			return nil, nil, nil, edit.Name + ": " + why
		}
		key := functionKeys[live]
		if syntaxUnchanged {
			rows, err := rebindResolvedTypeNameBodyRows(a, r.resolvedTypeNameSourceOwners, key, body)
			if err != nil {
				return nil, nil, nil, edit.Name + ": " + err.Error()
			}
			if r.pre.resolvedTypeNames.functions[key].body != "" && len(rows) == 0 {
				return nil, nil, nil, edit.Name + " lost previously certified body type-name rows"
			}
			if len(rows) != 0 {
				resolvedTypeNameRows[key] = rows
			}
		} else {
			// Fast preparation records the declaration-phase rows of the prepared body
			// on the live analyzer itself; the body replay records the rest exactly as a
			// cold analysis would. Re-encode that declaration-phase set for the next
			// update's comparison baseline.
			contribution, err := resolvedTypeNamePreparedBodyContribution(a, key, r.resolvedTypeNameSourceOwners, body)
			if err != nil {
				return nil, nil, nil, edit.Name + ": " + err.Error()
			}
			resolvedTypeNameRows[key] = map[ast.TypeExpr]string{}
			resolvedTypeNameContributions[key] = contribution
		}
		if r.digestOf == nil {
			r.digestOf = map[*ast.FuncDecl]string{}
		}
		r.digestOf[edit] = after
		bodies[i] = body
	}
	return bodies, resolvedTypeNameRows, resolvedTypeNameContributions, ""
}

// precomputeBaselineDigests records each baseline function's fact digest while its body is
// still in memory. A saved state omits the bodies of its comparison parse, so a restored
// update could not compute an edited function's "before" digest itself.
func (r *RetainedAnalysis) precomputeBaselineDigests() {
	if retainedFastPrepareMode != "on" || r.a == nil || r.pristine == nil || r.classify == nil {
		return
	}
	type fnKey struct {
		line string
		name string
	}
	liveFns := map[fnKey][]*ast.FuncDecl{}
	for _, d := range retainedWalkDecls(r.a.file) {
		if f, ok := d.(*ast.FuncDecl); ok {
			k := fnKey{retainedLineKey(f.Pos()), f.Name}
			liveFns[k] = append(liveFns[k], f)
		}
	}
	scoped := make(map[*ast.FuncDecl]scopedDecl, len(r.decls))
	for _, sd := range r.decls {
		if f, ok := sd.Decl.(*ast.FuncDecl); ok {
			scoped[f] = sd
		}
	}
	if r.digestOf == nil {
		r.digestOf = map[*ast.FuncDecl]string{}
	}
	enums := digestStoreEnums{
		plain:        r.a.storeBackedPlainEnums(),
		bareVariants: r.a.packedStoreBareVariantNames(),
	}
	for _, d := range retainedWalkDecls(r.pristine) {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || len(fn.Body) == 0 {
			continue
		}
		if _, done := r.digestOf[fn]; done {
			continue
		}
		cands := liveFns[fnKey{retainedLineKey(fn.Pos()), fn.Name}]
		if len(cands) != 1 {
			continue
		}
		sd, known := scoped[cands[0]]
		if !known {
			continue
		}
		r.digestOf[fn] = r.localFactDigestWithStoreEnums(r.a, cands[0], fn, sd, enums)
	}
}

// typeNameIsUnshadowedBuiltin certifies a bare builtin scalar in a plain,
// non-generic function when no declaration anywhere in the program uses that
// name. Such a spelling resolves the same way in every scope.
func (a *Analyzer) typeNameIsUnshadowedBuiltin(fn *ast.FuncDecl, scoped scopedDecl, expr ast.TypeExpr) bool {
	named, ok := expr.(*ast.NamedType)
	if !ok || named == nil || named.Region != "" || named.Spelling != "" {
		return false
	}
	if _, plain := scoped.Decl.(*ast.FuncDecl); !plain || len(fn.TypeParams) != 0 {
		return false
	}
	switch a.namedTypes[named.Name].(type) {
	case *BuiltinType, *BitIntType:
	default:
		return false
	}
	for name := range a.namedTypes {
		if strings.HasSuffix(name, "."+named.Name) || strings.HasSuffix(name, "::"+named.Name) {
			return false
		}
	}
	return true
}

// typeNameIsUniqueScalarStruct admits a bare name that exactly one declared type
// in the program can mean, whatever the function's namespace and usings: a plain
// struct of builtin scalars. Such a type has no regions, storage, ownership or
// generic instantiation, so naming it gives no declaration pass a new input, and
// the local-fact digest still compares what those passes derive. Body analysis
// resolves the name itself, including any visibility error.
func (a *Analyzer) typeNameIsUniqueScalarStruct(fn *ast.FuncDecl, scoped scopedDecl, expr ast.TypeExpr) bool {
	named, ok := expr.(*ast.NamedType)
	if !ok || named == nil || named.Region != "" || named.Spelling != "" || strings.ContainsAny(named.Name, ".:") {
		return false
	}
	if _, plain := scoped.Decl.(*ast.FuncDecl); !plain || len(fn.TypeParams)+len(fn.GenericParams) != 0 {
		return false
	}
	var found Type
	for name, typ := range a.namedTypes {
		if name == named.Name || strings.HasSuffix(name, "."+named.Name) || strings.HasSuffix(name, "::"+named.Name) {
			if found != nil {
				return false
			}
			found = typ
		}
	}
	st, ok := found.(*StructType)
	if !ok || len(st.TypeParams)+len(st.RegionParams)+len(st.GenericParams)+len(st.NamedStateCases)+len(st.DerivedStates) != 0 || st.RegionOwner != "" || st.Affine || st.AppendOnly || st.Resource || st.DropHook != "" || st.ProtocolStates {
		return false
	}
	for _, field := range st.Fields {
		builtin, isBuiltin := field.Type.(*BuiltinType)
		if !isBuiltin || !plainDefaultBuiltinName(builtin.Name) {
			return false
		}
	}
	return len(st.Fields) != 0
}
