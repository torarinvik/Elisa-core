package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// The pristine parse supplies declaration headers, source positions and gensym
// comparisons, not analyzed bodies. Project its bodies out of the disk graph
// after recording those comparisons. Live declarations always keep their bodies.
// Projection leaves the caller's separate pristine parse completely unchanged.
func (r *RetainedAnalysis) marshalComparisonProjection() ([]byte, error) {
	live := make(map[*ast.FuncDecl]bool, len(r.a.funcDeclSymbols))
	for fn := range r.a.funcDeclSymbols {
		live[fn] = true
	}
	for _, decl := range retainedWalkDecls(r.a.file) {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			live[fn] = true
		}
	}
	for _, sd := range r.decls {
		if fn, ok := sd.Decl.(*ast.FuncDecl); ok {
			live[fn] = true
		}
	}
	omitted := map[*ast.FuncDecl]bool{}
	texts := make(map[*ast.FuncDecl]funcComparisonText, len(r.texts))
	for fn, text := range r.texts {
		texts[fn] = text
	}
	for _, decl := range retainedWalkDecls(r.pristine) {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || live[fn] || len(fn.Body) == 0 {
			continue
		}
		texts[fn] = r.comparisonText(fn)
		omitted[fn] = true
	}
	registry := semanticStateRegistry()
	if len(omitted) == 0 {
		return registry.EncodeFramed(r)
	}
	fnType := reflect.TypeFor[ast.FuncDecl]()
	retainedType := reflect.TypeFor[RetainedAnalysis]()
	body, _ := fnType.FieldByName("Body")
	textField, _ := retainedType.FieldByName("texts")
	root := reflect.ValueOf(r).Pointer()
	return registry.EncodeFramedWithFieldAccess(r, func(v reflect.Value, i int) reflect.Value {
		if v.Type() == retainedType && i == textField.Index[0] && v.CanAddr() && v.Addr().Pointer() == root {
			return reflect.ValueOf(texts)
		}
		if v.Type() == fnType && i == body.Index[0] && v.CanAddr() {
			if fn, ok := settable(v).Addr().Interface().(*ast.FuncDecl); ok && omitted[fn] {
				return reflect.Zero(v.Field(i).Type())
			}
		}
		return settable(v.Field(i))
	})
}
