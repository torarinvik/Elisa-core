package semantic

import (
	"context"
	"crypto/sha256"
	"elisacore/src/ast"
	"elisacore/src/incremental"
	"elisacore/src/lexer"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
)

type primitiveLocalBinding struct {
	name      string
	parameter int
	local     *ast.VarDeclStmt
}
type primitiveLocalInitializer struct {
	owner                      *ast.FuncDecl
	declaration                *ast.VarDeclStmt
	syntax                     *ast.FuncDecl
	nodes                      []ast.Expr
	bindings                   []primitiveLocalBinding
	queryResult                incremental.EvaluationResult
	hasQueryResult             bool
	source, expression, key    string
	bindingOwn                 string
	dependencies               []primitiveBindingDependency
	localDependencies          []*ast.VarDeclStmt
	bindingBytes, bindingEdges int
}

// Plans own only invocation-local syntax. Persistent queries contain strings.
// Consumed bindings include transitive value syntax, not merely scalar types:
// constant/range diagnostics can depend on those values.
func primitiveLocalInitializers(fn *ast.FuncDecl) ([]*primitiveLocalInitializer, bool) {
	if !primitiveLocalSchemaValid || fn == nil || len(fn.Body) < 2 || len(fn.Body) > 257 {
		return nil, false
	}
	// Local products still require the i64 declaration/conversion protocol.
	// Broadening return-body admission must not silently broaden local replay.
	if predicateBuiltinSyntaxName(fn.ReturnType) != "i64" {
		return nil, false
	}
	for _, param := range fn.Params {
		if predicateBuiltinSyntaxName(param.Type) != "i64" {
			return nil, false
		}
	}
	header := *fn
	header.Body = fn.Body[len(fn.Body)-1:]
	if _, ok := primitiveReturnHeader(&header); !ok {
		return nil, false
	}
	available := append([]ast.ParamDecl(nil), fn.Params...)
	names := map[string]bool{}
	bindings := make([]primitiveLocalBinding, 0, len(fn.Params)+len(fn.Body))
	bindingIndices := make(map[string]int, len(fn.Params)+len(fn.Body))
	sourceBytes := len(fn.Name)
	for i, p := range fn.Params {
		names[p.Name] = true
		bindingIndices[p.Name] = i
		sourceBytes += len(p.Name)
		bindings = append(bindings, primitiveLocalBinding{name: p.Name, parameter: i})
	}
	if sourceBytes > primitiveSourceByteBudget {
		return nil, false
	}
	values := map[string]string{}
	var inputs []*primitiveLocalInitializer
	for _, stmt := range fn.Body[:len(fn.Body)-1] {
		decl, ok := stmt.(*ast.VarDeclStmt)
		if !ok || decl == nil || decl.Value == nil || names[decl.Name] || len(available) >= 256 {
			return nil, false
		}
		if len(decl.Name) > primitiveSourceByteBudget-sourceBytes {
			return nil, false
		}
		sourceBytes += len(decl.Name)
		copy := *decl
		copy.Position = lexer.Pos{}
		copy.Name = ""
		copy.Type = nil
		copy.Value = nil
		if !reflect.ValueOf(copy).IsZero() || predicateBuiltinSyntaxName(decl.Type) != "i64" || !primitiveParameterSyntax([]ast.ParamDecl{{Name: decl.Name, Type: decl.Type}}) {
			return nil, false
		}
		var nodes []ast.Expr
		nodes, sourceBytes, ok = primitiveI64ExpressionNodes(decl.Value, names, sourceBytes)
		if !ok {
			return nil, false
		}
		used := map[int]bool{}
		usedIndices := make([]int, 0, len(nodes))
		for _, node := range nodes {
			if id, ok := node.(*ast.Ident); ok {
				index, found := bindingIndices[id.Name]
				if !found {
					return nil, false
				}
				if !used[index] {
					used[index] = true
					usedIndices = append(usedIndices, index)
				}
			}
		}
		sort.Ints(usedIndices)
		var consumed []primitiveLocalBinding
		var params []ast.ParamDecl
		var parameters, dependencies []primitiveBindingDependency
		var localDependencies []*ast.VarDeclStmt
		for _, i := range usedIndices {
			binding := bindings[i]
			consumed = append(consumed, binding)
			params = append(params, available[i])
			value := values[binding.name]
			if binding.local == nil {
				value = "parameter:" + strconv.Itoa(binding.parameter)
			}
			id := sha256.Sum256([]byte(binding.name))
			dependency := primitiveBindingDependency{Identity: hex.EncodeToString(id[:]), Value: value}
			if binding.local == nil {
				parameters = append(parameters, dependency)
			} else {
				dependencies = append(dependencies, dependency)
				localDependencies = append(localDependencies, binding.local)
			}
		}
		syntax := &ast.FuncDecl{Name: fn.Name, Params: params, ReturnType: fn.ReturnType, Body: []ast.Stmt{&ast.ReturnStmt{Value: decl.Value}}}
		expression := primitiveExpressionDigest(syntax, nodes)
		own := primitiveBindingOwn(decl.Name, expression, parameters)
		source := primitiveBindingSource(own, dependencies)
		values[decl.Name] = source
		inputs = append(inputs, &primitiveLocalInitializer{owner: fn, declaration: decl, syntax: syntax, nodes: nodes, bindings: consumed, source: source, expression: expression, bindingOwn: own, dependencies: dependencies, localDependencies: localDependencies})
		names[decl.Name] = true
		available = append(available, ast.ParamDecl{Name: decl.Name, Type: decl.Type})
		bindings = append(bindings, primitiveLocalBinding{name: decl.Name, parameter: -1, local: decl})
		bindingIndices[decl.Name] = len(bindings) - 1
	}
	// The final return is analyzed on the ordinary path. Its expression grammar
	// does not affect the earlier local initializer products or their bindings.
	return inputs, true
}

func (input *primitiveLocalInitializer) currentBindings(a *Analyzer) bool {
	if a.currentScope == nil || a.currentFuncDecl != input.owner || a.currentReturn != a.namedTypes["i64"] {
		return false
	}
	for _, binding := range input.bindings {
		symbol, found := a.currentScope.Lookup(binding.name)
		if !found || symbol == nil || symbol.Type != a.namedTypes["i64"] || symbol.Mutable || symbol.Ghost || symbol.AliasOf != nil {
			return false
		}
		if binding.local != nil {
			if symbol.Kind != SymbolLocal || symbol.Node != binding.local {
				return false
			}
		} else if symbol.Kind != SymbolParam || symbol.Node != input.owner || symbol.ParamIndex != binding.parameter {
			return false
		}
	}
	return true
}

func primitiveTypeTag(a *Analyzer, typ Type) (byte, bool) {
	builtin, ok := typ.(*BuiltinType)
	if !ok || builtin != a.namedTypes[builtin.Name] {
		return 0, false
	}
	switch builtin.Name {
	case "int":
		return 'n', true
	case "i64":
		return 'i', true
	}
	return 0, false
}

func (a *Analyzer) analyzeLocalInitializer(n *ast.VarDeclStmt, expected Type) Type {
	invocation := a.incrementalBodies
	if invocation == nil || !invocation.tracking || a.suppressDiagnostics || a.convergeDepth != 0 {
		return a.analyzeValueExpr(n.Value, expected)
	}
	input := invocation.localInitializers[n]
	if input == nil || expected != a.namedTypes["i64"] {
		return a.analyzeValueExpr(n.Value, expected)
	}
	if !input.currentSyntax() {
		return a.analyzeValueExpr(n.Value, expected)
	}
	invocation.prepareAnalyzer(a)
	if invocation.safety != primitiveOperatorAbsence {
		return a.analyzeValueExpr(n.Value, expected)
	}
	if !input.currentBindings(a) {
		return a.analyzeValueExpr(n.Value, expected)
	}
	session := invocation.session
	query := incremental.Key{Kind: primitiveLocalTypingKind, Name: input.key}
	var encoded string
	var err error
	if input.hasQueryResult {
		encoded, err = input.queryResult.Value, input.queryResult.Err
	} else {
		encoded, err = session.db.Evaluate(context.Background(), query)
	}
	if err == nil && len(encoded) == len(input.nodes)+1 && validPrimitiveTyping(encoded) {
		restored := replayPrimitiveParameterNodes(a, input.nodes, encoded[:len(input.nodes)])
		session.stats.LocalInitializersReused++
		session.stats.ReusedExpressionNodes += uint64(restored)
		if encoded[len(input.nodes)] == 'i' {
			return a.namedTypes["i64"]
		}
		return a.namedTypes["int"]
	}
	session.stats.LocalInitializersChecked++
	before := len(a.diagnostics)
	result := a.analyzeValueExpr(n.Value, expected)
	rootTag, ok := primitiveTypeTag(a, result)
	if !ok || len(a.diagnostics) != before || n.Value != input.syntax.Body[0].(*ast.ReturnStmt).Value {
		return result
	}
	if !input.currentSyntax() {
		return result
	}
	nodes := input.nodes
	tags := make([]byte, len(nodes)+1)
	for i, node := range nodes {
		tag, ok := primitiveTypeTag(a, a.exprTypes[node])
		if !ok {
			return result
		}
		tags[i] = tag
	}
	tags[len(nodes)] = rootTag // initializer type before declaration conversions/checks
	product, _ := json.Marshal(primitiveBodyProduct{Source: input.source, Config: invocation.config, Safety: invocation.safety, Types: string(tags)})
	// Publish the product input now, but leave its derived query lazy. The
	// next analysis validates this query only if it actually tries to reuse it.
	session.storePrimitiveProduct(input.key, string(product))
	return result
}

var primitiveLocalSchemaValid = func() bool {
	typ := reflect.TypeOf(ast.VarDeclStmt{})
	fields := []string{"Position:lexer.Pos", "Name:string", "Mutable:bool", "BindingExplicit:bool", "Type:ast.TypeExpr", "Value:ast.Expr", "Owner:ast.Expr", "Ghost:bool"}
	if typ.NumField() != len(fields) {
		return false
	}
	for i, want := range fields {
		field := typ.Field(i)
		if field.Name+":"+field.Type.String() != want {
			return false
		}
	}
	return true
}()

// Validate the current tree against the invocation's already-checked node order.
// No fresh node slice or seen/name maps are needed: exact preorder pointer
// equality also rejects replaced children, sharing and cycles. The digest then
// checks admitted scalar fields; lowering/suffix fields must remain absent.
func (input *primitiveLocalInitializer) currentSyntax() bool {
	root := input.syntax.Body[0].(*ast.ReturnStmt).Value
	if input.declaration.Value != root {
		return false
	}
	index, bytes := 0, 0
	var visit func(ast.Expr) bool
	visit = func(expr ast.Expr) bool {
		if index >= len(input.nodes) || expr != input.nodes[index] {
			return false
		}
		index++
		switch node := expr.(type) {
		case *ast.IntLit:
			bytes += len(node.Value)
			return bytes <= primitiveSourceByteBudget && node.Suffix == ""
		case *ast.Ident:
			bytes += len(node.Name)
			return bytes <= primitiveSourceByteBudget
		case *ast.ParenExpr:
			return visit(node.Inner)
		case *ast.UnaryExpr:
			return node.LoweredCall == nil && visit(node.Operand)
		case *ast.BinaryExpr:
			return node.LoweredCall == nil && visit(node.Left) && visit(node.Right)
		}
		return false
	}
	return visit(root) && index == len(input.nodes) && primitiveExpressionDigest(input.syntax, input.nodes) == input.expression
}
