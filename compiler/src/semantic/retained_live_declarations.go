package semantic

import (
	"elisacore/src/ast"
	"reflect"
)

type fnKey struct{ pos, name string }
type declKey struct {
	pos  string
	kind reflect.Type
}

// retainedIndexLiveDeclarations includes synthesized declarations and preserves collisions
// so update matching can reject ambiguous source identities.
func retainedIndexLiveDeclarations(file *ast.File, decls []scopedDecl) (map[declKey][]ast.Decl, map[fnKey][]*ast.FuncDecl, map[*ast.FuncDecl]scopedDecl) {
	liveOther := map[declKey][]ast.Decl{}
	liveFns := map[fnKey][]*ast.FuncDecl{}
	for _, d := range retainedWalkDecls(file) {
		if f, ok := d.(*ast.FuncDecl); ok {
			k := fnKey{retainedLineKey(f.Pos()), f.Name}
			liveFns[k] = append(liveFns[k], f)
		} else {
			k := declKey{retainedLineKey(d.Pos()), reflect.TypeOf(d)}
			liveOther[k] = append(liveOther[k], d)
		}
	}
	scoped := make(map[*ast.FuncDecl]scopedDecl, len(decls))
	for _, sd := range decls {
		if f, ok := sd.Decl.(*ast.FuncDecl); ok {
			scoped[f] = sd
		}
	}
	return liveOther, liveFns, scoped
}
