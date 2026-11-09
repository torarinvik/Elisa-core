package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// retainedCollectAllNames gathers every identifier-like word in a declaration's
// string fields, positions excepted. Spurious names only widen the closure.
func retainedCollectAllNames(value reflect.Value, names map[string]bool, seen map[uintptr]bool) {
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return
		}
		if value.Kind() == reflect.Pointer {
			if seen[value.Pointer()] {
				return
			}
			seen[value.Pointer()] = true
		}
		retainedCollectAllNames(value.Elem(), names, seen)
	case reflect.Struct:
		if value.Type().Name() == "Pos" {
			return
		}
		for i := 0; i < value.NumField(); i++ {
			if value.Type().Field(i).IsExported() {
				retainedCollectAllNames(value.Field(i), names, seen)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			retainedCollectAllNames(value.Index(i), names, seen)
		}
	case reflect.Map:
		for it := value.MapRange(); it.Next(); {
			retainedCollectAllNames(it.Key(), names, seen)
			retainedCollectAllNames(it.Value(), names, seen)
		}
	case reflect.String:
		for _, word := range strings.FieldsFunc(value.String(), func(c rune) bool {
			return c != '_' && (c < '0' || c > '9') && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z')
		}) {
			names[word] = true
		}
	}
}

// retainedSubsetClosure returns seeds plus every function reachable from them by
// name, hooks included, and the names seen. Non-function declarations are always
// part of a subset; those reached contribute their names too.
func retainedSubsetClosure(file *ast.File, seeds []ast.Decl) (map[*ast.FuncDecl]bool, map[ast.Decl]bool, map[string]bool, bool) {
	functions := map[string][]*ast.FuncDecl{}
	others := map[string][]ast.Decl{}
	var pending, implicit []ast.Decl
	var index func([]ast.Decl)
	index = func(decls []ast.Decl) {
		for _, d := range decls {
			switch n := d.(type) {
			case *ast.NamespaceDecl:
				index(n.Decls)
			case *ast.FuncDecl:
				functions[n.Name] = append(functions[n.Name], n)
			case *ast.StructDecl:
				others[n.Name] = append(others[n.Name], n)
			case *ast.GlobalDecl:
				others[n.Name] = append(others[n.Name], n)
			case *ast.ConstDecl:
				others[n.Name] = append(others[n.Name], n)
			case *ast.ImplDecl:
				implicit = append(implicit, n) // applies without being named
			}
		}
	}
	index(file.Decls)
	keep := map[*ast.FuncDecl]bool{}
	seen := map[ast.Decl]bool{}
	add := func(d ast.Decl) {
		if fn, isFn := d.(*ast.FuncDecl); isFn {
			if keep[fn] {
				return
			}
			keep[fn] = true
		} else if seen[d] {
			return
		} else {
			seen[d] = true
		}
		pending = append(pending, d)
	}
	for _, d := range seeds {
		add(d)
	}
	for _, d := range implicit {
		add(d)
	}
	for name, list := range functions {
		if strings.HasPrefix(name, "__") {
			for _, hook := range list {
				add(hook)
			}
		}
	}
	names := map[string]bool{}
	for len(pending) != 0 {
		d := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		found := map[string]bool{}
		retainedCollectAllNames(reflect.ValueOf(d), found, map[uintptr]bool{})
		for name := range found {
			if names[name] {
				continue
			}
			names[name] = true
			for _, callee := range functions[name] {
				add(callee)
			}
			for _, other := range others[name] {
				add(other)
			}
		}
		if len(keep) > retainedFreshClosureLimit {
			return nil, nil, nil, false
		}
	}
	return keep, seen, names, true
}

// retainedBuildSubset copies the wanted declarations under their module blocks and
// runs the declaration phase on them.
func (r *RetainedAnalysis) retainedBuildSubset(file *ast.File, wanted func(ast.Decl) bool) (*preBodyState, *ast.File, string) {
	var leaves []ast.Decl
	var collect func([]ast.Decl)
	collect = func(decls []ast.Decl) {
		for _, d := range decls {
			if n, isNs := d.(*ast.NamespaceDecl); isNs {
				collect(n.Decls)
			} else if wanted(d) {
				leaves = append(leaves, d)
			}
		}
	}
	collect(file.Decls)
	snapshot, err := ast.NewSyntaxSnapshot(&ast.File{Decls: leaves}, 1<<30)
	if err != nil {
		return nil, nil, "subset does not snapshot"
	}
	copies := make(map[ast.Decl]ast.Decl, len(leaves))
	visibility := map[ast.Decl]string{}
	for i, d := range leaves {
		c, err := snapshot.CopyDeclaration(i)
		if err != nil {
			return nil, nil, "subset does not copy"
		}
		copies[d] = c
		if v, ok := file.DeclVisibility[d]; ok {
			visibility[c] = v
		}
	}
	var shell func([]ast.Decl) []ast.Decl
	shell = func(decls []ast.Decl) []ast.Decl {
		out := make([]ast.Decl, 0, len(decls))
		for _, d := range decls {
			if n, isNs := d.(*ast.NamespaceDecl); isNs {
				block := *n
				block.Decls = shell(n.Decls)
				out = append(out, &block)
				if v, ok := file.DeclVisibility[d]; ok {
					visibility[&block] = v
				}
			} else if c, ok := copies[d]; ok {
				out = append(out, c)
			}
		}
		return out
	}
	subset := &ast.File{Filename: file.Filename, Decls: shell(file.Decls), DeclVisibility: visibility}
	pendingRetainedMu.Lock()
	defer pendingRetainedMu.Unlock()
	state := scratchPreBody(subset, r.options)
	if state == nil {
		return nil, nil, "subset has no declaration results"
	}
	return state, subset, ""
}

// subsetMatchesBaseline checks a subset's functions and struct flags against the
// whole-program baseline, and that it knows every region-backed enum it can name.
// A function outside the subset names nothing edited, and everything it does name
// keeps its facts, so by induction over the callee relation it keeps its own.
func (r *RetainedAnalysis) subsetMatchesBaseline(state *preBodyState, names map[string]bool, skipKey string) string {
	for k, value := range state.fingerprint {
		if k == skipKey {
			continue
		}
		if r.pre.fingerprint[k] != value {
			return "disagrees with baseline fingerprint of " + k
		}
		if r.pre.classification[k] != state.classification[k] {
			return "disagrees with baseline classification of " + k
		}
	}
	whole := map[string]string{}
	var wholeBacked, backed string
	for _, line := range strings.Split(r.pre.global, "\n") {
		if strings.HasPrefix(line, "classification-region-backed=") {
			wholeBacked = line
		} else if head, _, ok := strings.Cut(line, ":"); ok && !strings.HasPrefix(line, "#") {
			whole[head] = line
		}
	}
	for _, line := range strings.Split(state.global, "\n") {
		if strings.HasPrefix(line, "classification-region-backed=") {
			backed = line
		} else if strings.HasPrefix(line, "classification-") || strings.HasPrefix(line, "#") {
			continue
		} else if head, _, ok := strings.Cut(line, ":"); ok && whole[head] != line && whole[head] != "" {
			return "disagrees with baseline flags of " + head
		}
	}
	_, list, _ := strings.Cut(wholeBacked, "=")
	for _, quoted := range strings.Fields(strings.Trim(list, "[]")) {
		enum, err := strconv.Unquote(quoted)
		if err != nil {
			return "has an unreadable region-backed set"
		}
		simple := enum
		if at := strings.LastIndexAny(simple, ".:"); at >= 0 {
			simple = simple[at+1:]
		}
		if names[simple] && !strings.Contains(backed, strconv.Quote(enum)) {
			return "loses region-backed enum " + enum
		}
	}
	_, list, _ = strings.Cut(backed, "=")
	for _, quoted := range strings.Fields(strings.Trim(list, "[]")) {
		if !strings.Contains(wholeBacked, quoted) {
			return "gains region-backed enum " + quoted
		}
	}
	return ""
}

// scopedDeclarationPreBody handles declarations other than functions: added consts
// and structs with fresh names, and const value edits. Every non-function
// declaration is analyzed, with the functions naming the edit and all they reach.
func (r *RetainedAnalysis) scopedDeclarationPreBody(a *Analyzer, file *ast.File, src []byte, newDecls []ast.Decl, newSpans []string, added, valueEdits []ast.Decl, diags []Diagnostic, growth retainedGrowth) (*preBodyState, *ast.File, string) {
	if r.pre == nil || r.pre.resolvedTypeNames.failure != "" || !r.resolvedTypeNameSourceOwnersValid {
		return nil, nil, "resolved type-name baseline is unavailable"
	}
	if r.pre.plainInputs != "" {
		return nil, nil, "plain default certificate needs the independent scratch"
	}
	if prefix, why := a.captureProofPrefix(); why != "" || len(prefix) != 0 {
		return nil, nil, "proof report declaration prefix needs the independent scratch"
	}
	declName := func(d ast.Decl) string {
		if st, ok := d.(*ast.StructDecl); ok {
			return st.Name
		}
		return retainedValueDeclName(d)
	}
	excluded := map[ast.Decl]bool{}
	for _, d := range added {
		name := declName(d)
		if retainedIdentifierCount(src, name) != 1 || retainedIdentifierCount(r.src, name) != 0 {
			return nil, nil, name + " is mentioned elsewhere in the source"
		}
		if _, shadowed := a.globalScope.Lookup(name); shadowed || a.namedTypes[name] != nil || a.moduleAliases[name] != "" {
			return nil, nil, name + " resolves to an existing name"
		}
		excluded[d] = true
	}
	seeds := append(append([]ast.Decl(nil), added...), valueEdits...)
	for _, edit := range valueEdits {
		name := declName(edit)
		for j, d := range newDecls {
			if fn, isFn := d.(*ast.FuncDecl); isFn && j < len(newSpans) && retainedIdentifierCount([]byte(newSpans[j]), name) != 0 {
				// A contract's callers are re-analyzed too, and they name nothing edited.
				if retainedIdentifierCount([]byte(retainedHeader(fn)), name) != 0 {
					return nil, nil, fn.Name + " names the const in its header or contract"
				}
				seeds = append(seeds, d)
			}
		}
	}
	keep, _, names, ok := retainedSubsetClosure(file, seeds)
	if !ok {
		return nil, nil, "the edit reaches too many functions"
	}
	wanted := func(d ast.Decl) bool {
		fn, isFn := d.(*ast.FuncDecl)
		return !isFn || keep[fn]
	}
	closed := len(valueEdits) == 0
	for _, d := range added {
		closed = closed && retainedClosedValueOrStruct(a, d)
	}
	if closed {
		// Nothing the declaration can depend on lies outside it, and nothing names it.
		wanted = func(d ast.Decl) bool { return excluded[d] }
		names = map[string]bool{}
	}
	with, subset, why := r.retainedBuildSubset(file, wanted)
	if why != "" {
		return nil, nil, why
	}
	if why := r.subsetMatchesBaseline(with, names, ""); why != "" && !closed {
		return nil, nil, "subset " + why
	}
	if with.resolvedTypeNames.failure != "" || with.proofPrefixError != "" || len(with.proofPrefix) != 0 {
		return nil, nil, "subset has uncertified declaration results"
	}
	rendered := map[string]bool{}
	for _, d := range diags {
		rendered[d.String()] = true
	}
	for _, d := range with.diags {
		if !rendered[d.String()] {
			return nil, nil, "subset reports a new declaration diagnostic"
		}
	}
	global := r.pre.global
	if len(added) != 0 {
		base, _, why := r.retainedBuildSubset(file, func(d ast.Decl) bool { return wanted(d) && !excluded[d] })
		if why != "" {
			return nil, nil, why
		}
		var deltaOK bool
		if global, deltaOK = retainedGlobalWithDelta(global, retainedWithoutStructLines(base.global, added), retainedWithoutStructLines(with.global, added)); !deltaOK {
			return nil, nil, "the edit changes program-wide declaration results"
		}
		for _, line := range strings.Split(with.global, "\n") {
			if head, _, found := strings.Cut(line, ":"); found && !strings.HasPrefix(line, "#") {
				for _, d := range added {
					if st, isStruct := d.(*ast.StructDecl); isStruct && (head == st.Name || strings.HasSuffix(head, "."+st.Name) || strings.HasSuffix(head, "::"+st.Name)) {
						global += "\n" + line
					}
				}
			}
		}
		parts := strings.Split(global, "\n")
		sort.Strings(parts)
		global = strings.Join(parts, "\n")
		if diff := globalGrowthDifference(r.pre.global, global, growth); diff != "" {
			return nil, nil, "program-wide declaration tables moved" + diff
		}
	}
	names2 := cloneResolvedTypeNameContributions(r.pre.resolvedTypeNames)
	for owner, contribution := range with.resolvedTypeNames.declarations {
		names2.declarations[owner] = contribution
	}
	for key, contribution := range with.resolvedTypeNames.functions {
		names2.functions[key] = contribution
	}
	return &preBodyState{
		classification: r.pre.classification, fingerprint: r.pre.fingerprint, global: global,
		resolvedTypeNames: names2, resolvedTypeNameSourceOwners: with.resolvedTypeNameSourceOwners, resolvedTypeNameRows: with.resolvedTypeNameRows,
		diags: diags, hasProofReport: r.pre.hasProofReport, keyOf: with.keyOf, prepared: with.prepared,
		targets: with.targets, regionIndex: with.regionIndex,
	}, subset, ""
}

// retainedWithoutStructLines drops the added structs' own flag lines, which are
// new rather than a change in table size.
func retainedWithoutStructLines(global string, added []ast.Decl) string {
	lines := strings.Split(global, "\n")
	kept := lines[:0]
next:
	for _, line := range lines {
		if head, _, found := strings.Cut(line, ":"); found && !strings.HasPrefix(line, "#") {
			for _, d := range added {
				if st, isStruct := d.(*ast.StructDecl); isStruct && (head == st.Name || strings.HasSuffix(head, "."+st.Name) || strings.HasSuffix(head, "::"+st.Name)) {
					continue next
				}
			}
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// retainedClosedValueOrStruct: a literal const of builtin type, or a struct of
// plain builtin scalar fields, with nothing else declared on it.
func retainedClosedValueOrStruct(a *Analyzer, d ast.Decl) bool {
	stub := &ast.FuncDecl{}
	scalar := func(expr ast.TypeExpr) bool {
		named, ok := expr.(*ast.NamedType)
		return ok && plainDefaultBuiltinName(named.Name) && a.typeNameIsUnshadowedBuiltin(stub, scopedDecl{Decl: stub}, expr)
	}
	switch n := d.(type) {
	case *ast.ConstDecl:
		if plainDefaultClosedFields(reflect.ValueOf(n).Elem(), map[string]bool{"Position": true, "Name": true, "Type": true, "Value": true}) != nil || !scalar(n.Type) {
			return false
		}
		switch n.Value.(type) {
		case *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
			return true
		}
	case *ast.StructDecl:
		if plainDefaultClosedFields(reflect.ValueOf(n).Elem(), map[string]bool{"Position": true, "Name": true, "Fields": true}) != nil || len(n.Fields) == 0 {
			return false
		}
		for _, field := range n.Fields {
			if plainDefaultClosedFields(reflect.ValueOf(field), map[string]bool{"Position": true, "Name": true, "Type": true}) != nil || !scalar(field.Type) {
				return false
			}
		}
		return true
	}
	return false
}
