package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"sync"
)

// A field selector contributes only its receiver's name. Keep this fast path
// bounded; deeper or cyclic graphs use the ordinary visited-set token walk.
func storeScanSimpleMentionName(expr ast.Expr) (string, bool) {
	for depth := 0; depth < 8; depth++ {
		switch node := expr.(type) {
		case *ast.Ident:
			if node != nil {
				return node.Name, true
			}
			return "", false
		case *ast.FieldExpr:
			if node == nil {
				return "", false
			}
			expr = node.Object
		case *ast.ParenExpr:
			if node == nil {
				return "", false
			}
			expr = node.Inner
		case *ast.MoveExpr:
			if node == nil {
				return "", false
			}
			expr = node.Operand
		case *ast.AddrOfExpr:
			if node == nil {
				return "", false
			}
			expr = node.Operand
		case *ast.UnaryExpr:
			if node == nil || node.LoweredCall != nil {
				return "", false
			}
			expr = node.Operand
		default:
			return "", false
		}
	}
	return "", false
}

func storeScanSimpleMentionNames(expr ast.Expr) (name, root string, known bool) {
	name, known = storeScanSimpleMentionName(expr)
	if !known {
		return "", "", false
	}
	return name, rootNameOfDotted(name), true
}

// Literal tokens are their text fields; positions and non-string fields are
// ignored by the general mention walk.
func storeScanLiteralMention(expr ast.Expr, names map[string]bool) (bool, bool) {
	switch n := expr.(type) {
	case *ast.IntLit:
		return n != nil && (names[n.Value] || names[n.Suffix]), true
	case *ast.FloatLit:
		return n != nil && (names[n.Value] || names[n.Suffix]), true
	case *ast.StringLit:
		return n != nil && names[n.Value], true
	case *ast.CharLit:
		return n != nil && names[n.Value], true
	case *ast.BoolLit, *ast.NullLit, *ast.ZeroedLit:
		return false, true
	default:
		return false, false
	}
}

// mentions: storeScanMentions(expr, names), with expr's mention tokens memoized in the index
// (fixpoints ask the same expressions again on every iteration).
func (idx *storeScanDeclIndex) mentions(expr ast.Expr, names map[string]bool) bool {
	if expr == nil || len(names) == 0 {
		return false
	}
	if hit, literal := storeScanLiteralMention(expr, names); literal {
		return hit
	}
	if name, known := storeScanSimpleMentionName(expr); known {
		if names[name] {
			return true
		}
		root := rootNameOfDotted(name)
		return root != name && names[root]
	}
	tokens, ok := idx.exprTokens[expr]
	if !ok {
		tokens = storeScanCompactMentionTokens(storeScanMentionTokens(expr))
		if idx.exprTokens == nil {
			idx.exprTokens = map[ast.Expr][]string{}
		}
		idx.exprTokens[expr] = tokens
	}
	return storeScanTokensHit(tokens, names)
}

// mentionsTwo shares the expression-token lookup when a proof asks whether one
// value reaches both a source set and a target set. Results still describe each
// set independently, including empty sets and entries explicitly mapped to false.
func (idx *storeScanDeclIndex) mentionsTwo(expr ast.Expr, left, right map[string]bool) (bool, bool) {
	if expr == nil || (len(left) == 0 && len(right) == 0) {
		return false, false
	}
	if hit, literal := storeScanLiteralMention(expr, left); literal {
		rightHit, _ := storeScanLiteralMention(expr, right)
		return hit, rightHit
	}
	if name, known := storeScanSimpleMentionName(expr); known {
		leftHit, rightHit := left[name], right[name]
		if leftHit && rightHit {
			return true, true
		}
		root := rootNameOfDotted(name)
		if root != name {
			return leftHit || left[root], rightHit || right[root]
		}
		return leftHit, rightHit
	}
	tokens, ok := idx.exprTokens[expr]
	if !ok {
		tokens = storeScanCompactMentionTokens(storeScanMentionTokens(expr))
		if idx.exprTokens == nil {
			idx.exprTokens = map[ast.Expr][]string{}
		}
		idx.exprTokens[expr] = tokens
	}
	leftHit, rightHit := false, false
	for _, token := range tokens {
		leftHit = leftHit || left[token]
		rightHit = rightHit || right[token]
		if (leftHit || len(left) == 0) && (rightHit || len(right) == 0) {
			break
		}
	}
	return leftHit, rightHit
}

// Mention queries only test membership. Long expressions often repeat the
// same identifiers, so keep one copy of each token in their cached index.
// Avoid an extra map for the overwhelmingly common short expressions.
func storeScanCompactMentionTokens(tokens []string) []string {
	if len(tokens) < 32 {
		return tokens
	}
	seen := make(map[string]bool, len(tokens))
	out := tokens[:0]
	for _, token := range tokens {
		if !seen[token] {
			seen[token] = true
			out = append(out, token)
		}
	}
	clear(tokens[len(out):])
	return out
}

// headerMentions: stmtHeaderMentionsAny(stmt, names), memoized the same way.
func (idx *storeScanDeclIndex) headerMentions(stmt ast.Stmt, names map[string]bool) bool {
	if stmt == nil || len(names) == 0 {
		return false
	}
	tokens, ok := idx.headerTokens[stmt]
	if !ok {
		tokens = storeScanHeaderTokens(stmt)
		if idx.headerTokens == nil {
			idx.headerTokens = map[ast.Stmt][]string{}
		}
		idx.headerTokens[stmt] = tokens
	}
	return storeScanTokensHit(tokens, names)
}

// headerMentionsTwo resolves the cached header once when both taint sets are
// queried for the same binder. Each answer still uses ordinary map semantics.
func (idx *storeScanDeclIndex) headerMentionsTwo(stmt ast.Stmt, left, right map[string]bool) (bool, bool) {
	if stmt == nil || (len(left) == 0 && len(right) == 0) {
		return false, false
	}
	tokens, ok := idx.headerTokens[stmt]
	if !ok {
		tokens = storeScanHeaderTokens(stmt)
		if idx.headerTokens == nil {
			idx.headerTokens = map[ast.Stmt][]string{}
		}
		idx.headerTokens[stmt] = tokens
	}
	leftHit, rightHit := false, false
	for _, token := range tokens {
		leftHit = leftHit || left[token]
		rightHit = rightHit || right[token]
		if (leftHit || len(left) == 0) && (rightHit || len(right) == 0) {
			break
		}
	}
	return leftHit, rightHit
}

func storeScanTokensHit(tokens []string, names map[string]bool) bool {
	for _, t := range tokens {
		if names[t] {
			return true
		}
	}
	return false
}

// storeScanMentionTokens: every string storeScanMentions would test against its name set.
func storeScanMentionTokens(node any) []string {
	return storeScanMentionTokensKeyed(node, nil)
}

// storeScanMentionTokensKeyed is storeScanMentionTokens with each identifier spelled by its
// scoped key (when sc is non-nil) instead of its name, and without the bare strings of scoped
// names (a pattern's binders), which name no single binding.
func storeScanMentionTokensKeyed(node any, sc *storeFlowKindScopes) []string {
	var tokens []string
	seen := map[uintptr]bool{}
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if id, ok := v.Interface().(*ast.Ident); ok && id != nil {
				if sc != nil {
					tokens = append(tokens, sc.key(id))
				}
				if root := rootNameOfDotted(id.Name); root != id.Name {
					tokens = append(tokens, id.Name, root)
				} else if sc != nil {
					return
				} else {
					tokens = append(tokens, id.Name)
				}
				return
			}
			if fe, ok := v.Interface().(*ast.FieldExpr); ok && fe != nil {
				rec(reflect.ValueOf(fe.Object))
				return
			}
			rec(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				rec(v.Elem())
			}
		case reflect.Struct:
			if v.Type() == storeScanPosType {
				return
			}
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		case reflect.String:
			if sc == nil || !sc.scoped[v.String()] {
				tokens = append(tokens, v.String())
			}
		}
	}
	rec(reflect.ValueOf(node))
	return tokens
}

// storeScanDeclIndex: per-function facts the store scan used to recompute with a whole-body walk
// per query (one walk per call site made the scan quadratic in function size).
type storeScanDeclIndex struct {
	decls          map[string][]*ast.VarDeclStmt // every declaration of a name
	otherBound     map[string]bool               // names bound by a non-VarDecl construct
	parameterNames map[string]bool               // immutable names, shared by this declaration's taint queries
	// nameNodes lists offsets into flat for nodes whose own expressions
	// (a statement's header only) mention the name.
	nameNodesOnce sync.Once
	nameNodes     map[string][]int // lazily built only for filled-local proofs
	// flat: every node walkStoreScanNodes visits, in walk order (fixpoints iterate this instead
	// of re-walking the AST reflectively per iteration).
	flat    []any
	returns []*ast.ReturnStmt
	// Payload proofs handle only assignments and calls. Build their ordered
	// subset on first use, rather than dispatching every AST node per query.
	payloadNodes      []any
	payloadNodesReady bool
	// Binding fixpoints only process declarations, assignments, calls, and
	// statements with binders. They reuse this ordered subset.
	bindingNodes        []any
	bindingNodesReady   bool
	paramFactNodes      []any
	paramFactNodesReady bool
	viewNodes           []any
	viewNodesReady      bool
	// bindingWrites is populated on demand once per function. Pattern binder typing used to
	// reflectively rescan the entire body for every binder name.
	bindingWrites      map[string]int
	bindingWritesReady bool
	// locals: names declared or bound anywhere in the body.
	locals        map[string]bool
	iterBinders   map[string][]*ast.IterForStmt
	nonIterBinder map[string]bool
	// memoized mention tokens (see mentions / headerMentions)
	exprTokens      map[ast.Expr][]string
	headerTokens    map[ast.Stmt][]string
	bound           map[ast.Stmt][]string
	bundleCache     map[ast.Expr]bool
	identCounts     map[ast.Expr]map[string]int
	bodyIdentCounts map[string]int
	kindScopes      *storeFlowKindScopes
	paramAliases    map[storeScanAliasKey]storeScanAliasResult
}

// View-local scans inspect statements and calls, but never other expression
// nodes. Preserve walk order while skipping those expressions on later scans.
func (idx *storeScanDeclIndex) viewLocalNodes() []any {
	if !idx.viewNodesReady {
		for _, node := range idx.flat {
			switch node.(type) {
			case ast.Stmt, *ast.CallExpr:
				idx.viewNodes = append(idx.viewNodes, node)
			}
		}
		idx.viewNodesReady = true
	}
	return idx.viewNodes
}

// Parameter write proofs only inspect assignments, calls, and nodes with a
// Target or Store expression. Preserve their original order across queries.
func (idx *storeScanDeclIndex) paramWriteNodes() []any {
	if !idx.paramFactNodesReady {
		for _, node := range idx.flat {
			switch node.(type) {
			case *ast.AssignStmt, *ast.CallExpr:
				idx.paramFactNodes = append(idx.paramFactNodes, node)
				continue
			}
			v := reflect.ValueOf(node)
			if v.Kind() == reflect.Pointer && !v.IsNil() && v.Elem().Kind() == reflect.Struct && len(reflectMetaOf(v.Elem().Type()).storeScanPlaceFields) != 0 {
				idx.paramFactNodes = append(idx.paramFactNodes, node)
			}
		}
		idx.paramFactNodesReady = true
	}
	return idx.paramFactNodes
}

func (a *Analyzer) invalidateStoreScanParamAliases() {
	for _, idx := range a.storeScanIndexCache {
		if idx != nil {
			idx.paramAliases = nil
		}
	}
}

func (idx *storeScanDeclIndex) bindingEffectNodes() []any {
	if !idx.bindingNodesReady {
		if idx.bound == nil {
			idx.bound = map[ast.Stmt][]string{}
		}
		for _, node := range idx.flat {
			switch n := node.(type) {
			case *ast.ForStmt:
				// The closure's ForStmt case has no effect.
			case *ast.VarDeclStmt, *ast.AssignStmt, *ast.CallExpr:
				idx.bindingNodes = append(idx.bindingNodes, node)
			case ast.Stmt:
				if n != nil && len(idx.boundNames(n)) != 0 {
					idx.bindingNodes = append(idx.bindingNodes, node)
				}
			}
		}
		idx.bindingNodesReady = true
	}
	return idx.bindingNodes
}

// Most store scans never ask for filled-local proofs. Avoid walking every
// expression to build their reverse name index until the first such query.
func (idx *storeScanDeclIndex) nodesForName(name string) []int {
	idx.nameNodesOnce.Do(func() {
		idx.nameNodes = map[string][]int{}
		exprNames := make(map[string]bool)
		for offset, node := range idx.flat {
			switch n := node.(type) {
			case *ast.ReturnStmt:
			case *ast.VarDeclStmt:
				if n != nil {
					storeScanAddExprNames(n.Value, exprNames)
					exprNames[n.Name] = true
				}
			case *ast.AssignStmt:
				if n != nil {
					storeScanAddExprNames(n.Value, exprNames)
					storeScanAddExprNames(n.Target, exprNames)
				}
			case *ast.CallExpr:
				if n != nil {
					if field, ok := n.Func.(*ast.FieldExpr); ok && field != nil {
						storeScanAddExprNames(field.Object, exprNames)
					}
					for _, arg := range n.Args {
						storeScanAddExprNames(arg, exprNames)
					}
				}
			case ast.Stmt:
				if n != nil && len(idx.boundNames(n)) > 0 {
					storeScanAddHeaderNames(n, exprNames)
				}
			}
			for token := range exprNames {
				idx.nameNodes[token] = append(idx.nameNodes[token], offset)
			}
			clear(exprNames)
		}
	})
	return idx.nameNodes[name]
}

func (a *Analyzer) storeScanIndex(decl *ast.FuncDecl) *storeScanDeclIndex {
	decl = a.readDecl(decl)
	if idx, ok := a.storeScanIndexCache[decl]; ok {
		return idx
	}
	// Specialization analyzers repeatedly inspect canonical callees while
	// analyzing each generic body. Rebuild their mutable memo shell from the
	// canonical syntax index instead of walking the same immutable body again.
	if p := a.parallel; p != nil && p.specialization != nil && p.specialization.base != nil {
		if base := p.specialization.base.analyzer; base != nil && base != a {
			if cloned := p.specialization.base.cloneSpecializationStoreScanIndex(decl, nil); cloned != nil {
				if a.storeScanIndexCache == nil {
					a.storeScanIndexCache = make(map[*ast.FuncDecl]*storeScanDeclIndex)
				}
				a.storeScanIndexCache[decl] = cloned
				return cloned
			}
		}
	}
	idx := &storeScanDeclIndex{otherBound: map[string]bool{}}
	if len(decl.Body) > 0 {
		// Top-level statements and their expressions are the minimum common
		// case; nested syntax still grows normally beyond this bounded hint.
		idx.flat = make([]any, 0, min(2*len(decl.Body), 2048))
	}

	walkStoreScanNodes(decl.Body, func(node any) {
		idx.flat = append(idx.flat, node)
		switch n := node.(type) {
		case *ast.ReturnStmt:
			if n != nil {
				idx.returns = append(idx.returns, n)
			}
		case *ast.VarDeclStmt:
			if n == nil {
				return
			}
			if idx.decls == nil {
				idx.decls = map[string][]*ast.VarDeclStmt{}
			}
			if idx.locals == nil {
				idx.locals = map[string]bool{}
			}
			idx.locals[n.Name] = true
			idx.decls[n.Name] = append(idx.decls[n.Name], n)
		case *ast.AssignStmt, *ast.CallExpr:
		case ast.Stmt:
			if n == nil {
				return
			}
			bound := stmtBoundNames(n)
			if idx.bound == nil {
				idx.bound = map[ast.Stmt][]string{}
			}
			idx.bound[n] = bound
			if len(bound) == 0 {
				return
			}
			if idx.locals == nil {
				idx.locals = map[string]bool{}
			}
			iter, isIter := n.(*ast.IterForStmt)
			for _, name := range bound {
				idx.otherBound[name] = true
				idx.locals[name] = true
				if isIter && iter != nil {
					if idx.iterBinders == nil {
						idx.iterBinders = map[string][]*ast.IterForStmt{}
					}
					idx.iterBinders[name] = append(idx.iterBinders[name], iter)
				} else {
					if idx.nonIterBinder == nil {
						idx.nonIterBinder = map[string]bool{}
					}
					idx.nonIterBinder[name] = true
				}
			}
		}
	})
	if a.storeScanIndexCache == nil {
		a.storeScanIndexCache = map[*ast.FuncDecl]*storeScanDeclIndex{}
	}
	a.storeScanIndexCache[decl] = idx
	return idx
}

func (idx *storeScanDeclIndex) bindingWriteCount(body []ast.Stmt, name string) int {
	if idx == nil {
		return 0
	}
	if !idx.bindingWritesReady {
		idx.bindingWrites = storeScanBindingWriteCounts(body)
		idx.bindingWritesReady = true
	}
	return idx.bindingWrites[name]
}
