package semantic

import "elisacore/src/ast"

// retainedBodyUses records exactly the names retainedMentionsAny sees and the
// expression types retainedUsesTypes reads. It is captured from the finished,
// idle AST when publishing a semantic snapshot, not from earlier scan memos.
// Checked snapshot admission binds these derived facts to the source/options
// and compiler schema. An update consumes this immutable baseline and drops
// the index before mutating bodies or types; missing indexes use the full walk.
type retainedBodyUses struct {
	names []string
	types []Type
}

func (r *RetainedAnalysis) captureBodyUses() {
	uses := make(map[*ast.FuncDecl]retainedBodyUses, len(r.decls))
	for _, sd := range r.decls {
		fn, ok := sd.Decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		var use retainedBodyUses
		names, types := map[string]bool{}, map[Type]bool{}
		name := func(n string) {
			if !names[n] {
				names[n] = true
				use.names = append(use.names, n)
			}
		}
		visit := func(node any) {
			switch n := node.(type) {
			case *ast.Ident:
				name(n.Name)
				name(fillMayAdoptLastSegment(n.Name))
			case *ast.FieldExpr:
				name(n.Field)
			case *ast.StructLitExpr:
				name(n.Name)
				name(fillMayAdoptLastSegment(n.Name))
			}
			if expr, ok := node.(ast.Expr); ok {
				if typ, ok := r.a.lookupExprType(expr); ok && !types[typ] {
					types[typ] = true
					use.types = append(use.types, typ)
				}
			}
		}
		walkStoreScanNodes(funcScanStmts(fn), visit)
		walkStoreScanNodes(funcDefaultArgStmts(fn), visit)
		uses[fn] = use
	}
	r.bodyUses = uses
}
