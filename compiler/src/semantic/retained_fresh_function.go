package semantic

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"elisacore/src/ast"
)

// A fresh closed function can skip the whole-program scratch declaration phase.
//
// The scratch phase establishes two things for an added function: its own
// declaration facts, and that the rest of the program's facts did not change.
//
// Its own facts come from the same capture code, run by an independent analyzer
// on a copy of the function alone. That is exact only when the facts cannot
// depend on the rest of the program, which is what the closed shape requires:
// builtin scalar header types no declaration shadows, nothing but scalar locals,
// literals and operators in the body, so no calls, allocation, type names,
// annotations or contracts.
//
// The rest of the program is unchanged because a declaration pass can only see
// the function through its name or through what its body contributes to a
// program-wide table. The name occurs exactly once in the new source and never
// in the old, and it resolves to nothing live. The closed body contributes
// nothing to region, store, append-only, export or lifetime tables, and the
// computed table deltas are checked below.
//
// Body analysis still runs on the live analyzer exactly as for a scratch-backed
// add, and the live declaration is still compared with the isolated fingerprint.
// Anything outside this shape, including a program under the plain-default
// certificate (whose check needs the independent whole-program scratch), declines
// to the scratch path.

// retainedClosedFunctionShape reports why fn is not a closed scalar function.
func retainedClosedFunctionShape(a *Analyzer, fn *ast.FuncDecl, sd scopedDecl) string {
	if !retainedPlainFunction(fn) || fn.Name == "main" {
		return fn.Name + " is not a plain helper"
	}
	if plainDefaultClosedFields(reflect.ValueOf(fn).Elem(), map[string]bool{"Position": true, "Name": true, "Params": true, "ReturnType": true, "Body": true}) != nil {
		return fn.Name + " has more than a scalar header and body"
	}
	scalar := func(expr ast.TypeExpr) bool {
		named, ok := expr.(*ast.NamedType)
		return ok && plainDefaultBuiltinName(named.Name) && a.typeNameIsUnshadowedBuiltin(fn, sd, expr)
	}
	if !scalar(fn.ReturnType) {
		return fn.Name + " does not return a builtin scalar"
	}
	names := map[string]bool{}
	for _, param := range fn.Params {
		if plainDefaultClosedFields(reflect.ValueOf(param), map[string]bool{"Position": true, "Name": true, "Type": true}) != nil || !scalar(param.Type) {
			return fn.Name + " has a parameter that is not a plain builtin scalar"
		}
		names[param.Name] = true
	}
	if len(fn.Body) == 0 || classifierFoldShape(fn) {
		return fn.Name + " has no closed body"
	}
	if !retainedClosedStmts(fn.Body, names) {
		return fn.Name + " has a body outside the closed scalar subset"
	}
	return ""
}

func retainedClosedStmts(stmts []ast.Stmt, names map[string]bool) bool {
	closed := func(node any, fields ...string) bool {
		allowed := map[string]bool{"Position": true}
		for _, field := range fields {
			allowed[field] = true
		}
		return plainDefaultClosedFields(reflect.ValueOf(node).Elem(), allowed) == nil
	}
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ReturnStmt:
			if s.Value == nil || !closed(s, "Value") || !retainedClosedExpr(s.Value, names) {
				return false
			}
		case *ast.VarDeclStmt:
			if s.Value == nil || !closed(s, "Name", "Mutable", "BindingExplicit", "Value") || !retainedClosedExpr(s.Value, names) {
				return false
			}
			names[s.Name] = true
		case *ast.AssignStmt:
			target, ok := s.Target.(*ast.Ident)
			if !ok || !names[target.Name] || !closed(s, "Target", "Value") || !retainedClosedExpr(s.Value, names) {
				return false
			}
		case *ast.IfStmt:
			if !closed(s, "Hint", "Cond", "Then", "Else", "FromSource") || !retainedClosedExpr(s.Cond, names) || !retainedClosedStmts(s.Then, names) || !retainedClosedStmts(s.Else, names) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func retainedClosedExpr(expr ast.Expr, names map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
		return true
	case *ast.Ident:
		return names[e.Name]
	case *ast.ParenExpr:
		return retainedClosedExpr(e.Inner, names)
	case *ast.UnaryExpr:
		return e.LoweredCall == nil && retainedClosedExpr(e.Operand, names)
	case *ast.BinaryExpr:
		return e.LoweredCall == nil && retainedClosedExpr(e.Left, names) && retainedClosedExpr(e.Right, names)
	}
	return false
}

// retainedIdentifierCount counts name as a whole identifier token. Comments and
// strings count too, which can only cause a decline.
func retainedIdentifierCount(src []byte, name string) int {
	word := func(c byte) bool {
		return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
	}
	count := 0
	for from := 0; ; {
		at := bytes.Index(src[from:], []byte(name))
		if at < 0 {
			return count
		}
		at += from
		end := at + len(name)
		if (at == 0 || !word(src[at-1])) && (end == len(src) || !word(src[end])) {
			count++
		}
		from = end
	}
}

// retainedIsolatedPreBody runs the declaration phase on one namespace block
// holding decls, on an independent analyzer.
func retainedIsolatedPreBody(filename, namespace string, decls []ast.Decl, visibility map[ast.Decl]string, options AnalyzeOptions) *preBodyState {
	top := decls
	if namespace != "" {
		top = []ast.Decl{&ast.NamespaceDecl{Name: namespace, Decls: decls, Module: true}}
	}
	pendingRetainedMu.Lock()
	defer pendingRetainedMu.Unlock()
	return scratchPreBody(&ast.File{Filename: filename, Decls: top, DeclVisibility: visibility}, options)
}

// retainedGlobalWithDelta applies the table-size change from base to with onto
// global. Any other difference between base and with declines.
func retainedGlobalWithDelta(global, base, with string) (string, bool) {
	baseLines, withLines := strings.Split(base, "\n"), strings.Split(with, "\n")
	if len(baseLines) != len(withLines) {
		return "", false
	}
	delta := map[string]int{}
	for i, line := range withLines {
		if line == baseLines[i] {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		baseName, baseValue, baseOK := strings.Cut(baseLines[i], "=")
		if !ok || !baseOK || name != baseName || !strings.HasPrefix(name, "#") {
			return "", false
		}
		after, afterErr := strconv.Atoi(value)
		before, beforeErr := strconv.Atoi(baseValue)
		if afterErr != nil || beforeErr != nil {
			return "", false
		}
		delta[name] = after - before
	}
	lines := strings.Split(global, "\n")
	for i, line := range lines {
		name, value, ok := strings.Cut(line, "=")
		change, moved := delta[name]
		if !ok || !moved {
			continue
		}
		count, err := strconv.Atoi(value)
		if err != nil {
			return "", false
		}
		lines[i] = name + "=" + strconv.Itoa(count+change)
		delete(delta, name)
	}
	if len(delta) != 0 {
		return "", false
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n"), true
}

// freshFunctionPreBody extends the baseline declaration results with added, or
// says why the scratch phase is needed. Nothing live is modified; fingerprints of
// unchanged functions and the shifted diagnostics are the baseline's, as on the
// body-edit fast path.
func (r *RetainedAnalysis) freshFunctionPreBody(a *Analyzer, added []*ast.FuncDecl, file *ast.File, src []byte, namespaceOf func(ast.Decl) (string, []string, string), diags []Diagnostic, growth retainedGrowth) (*preBodyState, string) {
	if r.pre == nil || r.pre.resolvedTypeNames.failure != "" || !r.resolvedTypeNameSourceOwnersValid {
		return nil, "resolved type-name baseline is unavailable"
	}
	if r.pre.plainInputs != "" {
		return nil, "plain default certificate needs the independent scratch"
	}
	if prefix, why := a.captureProofPrefix(); why != "" || len(prefix) != 0 {
		return nil, "proof report declaration prefix needs the independent scratch"
	}
	pre := &preBodyState{
		classification: make(map[string]string, len(r.pre.classification)+len(added)),
		fingerprint:    make(map[string]string, len(r.pre.fingerprint)+len(added)),
		global:         r.pre.global, resolvedTypeNames: cloneResolvedTypeNameContributions(r.pre.resolvedTypeNames),
		resolvedTypeNameSourceOwners: map[ast.TypeExpr]resolvedTypeNameSourceOwner{},
		resolvedTypeNameRows:         map[string]map[ast.TypeExpr]string{},
		diags:                        diags, hasProofReport: r.pre.hasProofReport,
		keyOf: map[*ast.FuncDecl]string{}, addedPrepared: map[string]*ast.FuncDecl{}, addedVisibility: map[ast.Decl]string{},
		targets: map[*ast.FuncDecl][]map[int]bool{}, regionIndex: map[*ast.FuncDecl]map[int]bool{},
	}
	for key, value := range r.pre.classification {
		pre.classification[key] = value
	}
	for key, value := range r.pre.fingerprint {
		pre.fingerprint[key] = value
	}
	for _, fn := range added {
		namespace, usings, why := namespaceOf(fn)
		if why != "" {
			return nil, why
		}
		if retainedIdentifierCount(src, fn.Name) != 1 || retainedIdentifierCount(r.src, fn.Name) != 0 {
			return nil, fn.Name + " is mentioned elsewhere in the source"
		}
		if _, shadowed := a.globalScope.Lookup(fn.Name); shadowed || a.namedTypes[fn.Name] != nil || a.moduleAliases[fn.Name] != "" {
			return nil, fn.Name + " resolves to an existing name"
		}
		closedWhy := retainedClosedFunctionShape(a, fn, scopedDecl{Decl: fn, Namespace: namespace, Usings: usings})
		visibility := file.DeclVisibility[fn]
		if visibility != "" && visibility != "public" && visibility != "private" {
			return nil, fn.Name + " has an unknown visibility"
		}
		var base, with *preBodyState
		var copiedFn *ast.FuncDecl
		if closedWhy == "" {
			snapshot, err := ast.NewSyntaxSnapshot(&ast.File{Decls: []ast.Decl{fn}}, 1<<30)
			if err != nil {
				return nil, fn.Name + " does not snapshot"
			}
			copied, err := snapshot.CopyDeclaration(0)
			var isFn bool
			copiedFn, isFn = copied.(*ast.FuncDecl)
			if err != nil || !isFn {
				return nil, fn.Name + " does not copy"
			}
			isolatedVisibility := map[ast.Decl]string{}
			if visibility != "" {
				isolatedVisibility[copiedFn] = visibility
			}
			base = retainedIsolatedPreBody(file.Filename, namespace, nil, nil, r.options)
			with = retainedIsolatedPreBody(file.Filename, namespace, []ast.Decl{copiedFn}, isolatedVisibility, r.options)
			if base == nil || with == nil || len(with.fingerprint) != 1 {
				return nil, fn.Name + " has isolated declaration results"
			}
		} else {
			var why string
			base, with, copiedFn, why = r.closurePreBody(fn, file)
			if why != "" {
				return nil, why
			}
		}
		if visibility != "" {
			pre.addedVisibility[copiedFn] = visibility
		}
		if len(with.diags) != len(base.diags) || len(with.proofPrefix) != 0 || with.proofPrefixError != "" || with.resolvedTypeNames.failure != "" {
			return nil, fn.Name + " has isolated declaration results"
		}
		key := with.keyOf[copiedFn]
		if key == "" || with.classification[key] == "" || with.classification[key] == "unavailable" {
			return nil, fn.Name + " has no isolated classification"
		}
		if _, taken := pre.fingerprint[key]; taken {
			return nil, fn.Name + " has an existing function key"
		}
		global, ok := retainedGlobalWithDelta(pre.global, base.global, with.global)
		if !ok {
			return nil, fn.Name + " changes program-wide declaration results"
		}
		pre.global = global
		pre.fingerprint[key] = with.fingerprint[key]
		pre.classification[key] = with.classification[key]
		pre.keyOf[copiedFn] = key
		pre.addedPrepared[retainedLineKey(fn.Pos())+"|"+fn.Name] = copiedFn
		if t, ok := with.targets[copiedFn]; ok {
			pre.targets[copiedFn] = t
		}
		if idx, ok := with.regionIndex[copiedFn]; ok {
			pre.regionIndex[copiedFn] = idx
		}
		pre.resolvedTypeNames.functions[key] = with.resolvedTypeNames.functions[key]
		for expr, owner := range with.resolvedTypeNameSourceOwners {
			pre.resolvedTypeNameSourceOwners[expr] = owner
		}
		if rows := with.resolvedTypeNameRows[key]; rows != nil {
			pre.resolvedTypeNameRows[key] = rows
		}
	}
	if diff := globalGrowthDifference(r.pre.global, pre.global, growth); diff != "" {
		return nil, fmt.Sprintf("program-wide declaration tables moved%s", diff)
	}
	return pre, ""
}

// retainedFreshClosureLimit bounds the functions a closure analysis may carry.
const retainedFreshClosureLimit = 400

// A function with calls or user types cannot be analyzed alone, but its
// declaration facts flow only along what it names. closurePreBody analyzes every
// non-function declaration plus the functions reachable by name from fn, a
// deliberate over-approximation. That partial program is trusted only through a
// check against the whole-program baseline: every function it holds, with and
// without fn, must reproduce its baseline fingerprint and classification, every
// struct must keep its induced flags, and each region-backed enum the closure can
// name must still be region-backed. So fn sees exactly the inputs a cold run gives
// it, and adding fn changes none of those functions.
func (r *RetainedAnalysis) closurePreBody(fn *ast.FuncDecl, file *ast.File) (base, with *preBodyState, copiedFn *ast.FuncDecl, why string) {
	if !retainedPlainFunction(fn) || fn.Name == "main" || len(fn.Body) == 0 || classifierFoldShape(fn) {
		return nil, nil, nil, fn.Name + " is not a plain helper"
	}
	if plainDefaultClosedFields(reflect.ValueOf(fn).Elem(), map[string]bool{"Position": true, "Name": true, "Params": true, "ReturnType": true, "Body": true}) != nil {
		return nil, nil, nil, fn.Name + " has annotations or contracts"
	}
	for _, param := range fn.Params {
		if param.DefaultValue != nil {
			return nil, nil, nil, fn.Name + " has a default argument"
		}
	}
	functions := map[string][]*ast.FuncDecl{}
	others := map[string][]ast.Decl{}
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
			}
		}
	}
	index(file.Decls)
	keep := map[*ast.FuncDecl]bool{fn: true}
	names := map[string]bool{}
	pending := []ast.Decl{fn}
	seen := map[ast.Decl]bool{fn: true}
	// Hooks and operators apply without being named.
	for name, list := range functions {
		if strings.HasPrefix(name, "__") {
			for _, hook := range list {
				keep[hook] = true
				pending = append(pending, hook)
			}
		}
	}
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
				if !keep[callee] {
					keep[callee] = true
					pending = append(pending, callee)
				}
			}
			for _, other := range others[name] {
				if !seen[other] {
					seen[other] = true
					pending = append(pending, other)
				}
			}
		}
		if len(keep) > retainedFreshClosureLimit {
			return nil, nil, nil, fn.Name + " reaches too many functions"
		}
	}
	build := func(includeFn bool) (*preBodyState, *ast.FuncDecl, string) {
		var leaves []ast.Decl
		var collect func([]ast.Decl)
		wanted := func(d ast.Decl) bool {
			f, isFn := d.(*ast.FuncDecl)
			return !isFn || keep[f] && (includeFn || f != fn)
		}
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
			return nil, nil, "closure does not snapshot"
		}
		copies := make(map[ast.Decl]ast.Decl, len(leaves))
		visibility := map[ast.Decl]string{}
		var copied *ast.FuncDecl
		for i, d := range leaves {
			c, err := snapshot.CopyDeclaration(i)
			if err != nil {
				return nil, nil, "closure does not copy"
			}
			copies[d] = c
			if v, ok := file.DeclVisibility[d]; ok {
				visibility[c] = v
			}
			if d == ast.Decl(fn) {
				copied, _ = c.(*ast.FuncDecl)
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
		top := shell(file.Decls)
		pendingRetainedMu.Lock()
		defer pendingRetainedMu.Unlock()
		state := scratchPreBody(&ast.File{Filename: file.Filename, Decls: top, DeclVisibility: visibility}, r.options)
		if state == nil {
			return nil, nil, "closure has no declaration results"
		}
		return state, copied, ""
	}
	if base, _, why = build(false); why != "" {
		return nil, nil, nil, fn.Name + ": " + why
	}
	if with, copiedFn, why = build(true); why != "" || copiedFn == nil {
		return nil, nil, nil, fn.Name + ": closure lost the function"
	}
	key := with.keyOf[copiedFn]
	if key == "" || len(with.fingerprint) != len(base.fingerprint)+1 {
		return nil, nil, nil, fn.Name + " changes the closure's function set"
	}
	for _, state := range []*preBodyState{base, with} {
		for k, value := range state.fingerprint {
			if k == key {
				continue
			}
			if r.pre.fingerprint[k] != value {
				return nil, nil, nil, fn.Name + ": closure disagrees with baseline fingerprint of " + k
			}
			if r.pre.classification[k] != state.classification[k] {
				return nil, nil, nil, fn.Name + ": closure disagrees with baseline classification of " + k
			}
		}
	}
	baseline := strings.Split(r.pre.global, "\n")
	whole := map[string]string{}
	var wholeBacked string
	for _, line := range baseline {
		if strings.HasPrefix(line, "classification-region-backed=") {
			wholeBacked = line
		} else if head, _, ok := strings.Cut(line, ":"); ok && !strings.HasPrefix(line, "#") {
			whole[head] = line
		}
	}
	var withBacked string
	for _, line := range strings.Split(with.global, "\n") {
		if strings.HasPrefix(line, "classification-region-backed=") {
			withBacked = line
		} else if strings.HasPrefix(line, "classification-") || strings.HasPrefix(line, "#") {
			continue
		} else if head, _, ok := strings.Cut(line, ":"); ok && whole[head] != line {
			return nil, nil, nil, fn.Name + ": closure disagrees with baseline flags of " + head
		}
	}
	if withBacked != wholeBacked {
		var enums []string
		if _, list, ok := strings.Cut(wholeBacked, "="); ok {
			enums = strings.Fields(strings.Trim(list, "[]"))
		}
		for _, quoted := range enums {
			enum, err := strconv.Unquote(quoted)
			if err != nil {
				return nil, nil, nil, fn.Name + ": unreadable region-backed set"
			}
			simple := enum
			if at := strings.LastIndexAny(simple, ".:"); at >= 0 {
				simple = simple[at+1:]
			}
			if names[simple] && !strings.Contains(withBacked, strconv.Quote(enum)) {
				return nil, nil, nil, fn.Name + ": closure loses region-backed enum " + enum
			}
		}
	}
	return base, with, copiedFn, ""
}

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
