package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// docs/127 D9 — `extern enum Name of T:` mirrors a C enum.
//
// A C enum's value set is open at the ABI: any integer of the underlying type can arrive,
// whatever the header names. Elisa's exhaustiveness check is therefore sound for enums the
// language builds and UNSOUND for one reinterpreted from a foreign integer — the arms cover
// every value the checker knows about, and the optimizer is entitled to assume that, so an
// unnamed value walks off the end of the switch.
//
// Two spellings close it, and the choice is the C API's to make:
//
//	extern enum DeviceState of i32:   # closed: validate on the way in, match stays exhaustive
//	@open extern enum SdlEvent of u32: # open:   nothing to validate, `match` must carry `_:`
//
// For the closed form the parser synthesizes an ordinary Elisa validator beside the enum:
//
//	def DeviceState__from_c(raw: i32) -> DeviceState error[ForeignEnum]:
//	    k: DeviceState = raw.DeviceState()
//	    if k != DeviceState.Off and k != DeviceState.Idle and k != DeviceState.Running:
//	        raise ForeignEnum.OutOfRange
//	    return k
//
// Synthesizing SOURCE-level AST rather than a backend intrinsic is deliberate: error unions,
// `try`/`else` and const-enum compares are already byte-identical between the two compilers,
// so D9 needs no backend work in either and cannot drift. `Name.from_c(raw)` is the surface
// spelling; the analyzer resolves it to this function (Elisa has no static methods).
const (
	foreignEnumErrorSetName  = "ForeignEnum"
	foreignEnumOutOfRangeTag = "OutOfRange"
	foreignEnumValidatorParm = "raw"
)

// parseExternEnumDecl parses `extern enum Name [of T]:` and its member block. The caller has
// consumed `extern` and left the cursor on `enum`.
func (p *Parser) parseExternEnumDecl(pos lexer.Pos, annotations []ast.Annotation) ast.Decl {
	p.expect(lexer.TOKEN_ENUM)
	name := p.expect(lexer.TOKEN_IDENT).Text
	// The storage type is REQUIRED. C's enum type is only "compatible with int", and the width
	// a given header settles on is the whole reason this declaration exists; defaulting it would
	// put a silent guess on an ABI boundary, and would be one more default to keep identical
	// between the two compilers. Say it.
	var storage ast.TypeExpr
	if p.matchIdentText("of") {
		storage = p.parseTypeExpr()
	} else {
		p.errorAt(pos, "extern enum %q must declare its C storage type; add `of i32` (or the width the header uses) so the ABI is explicit", name)
		storage = &ast.NamedType{Position: pos, Name: "i32"}
	}
	members := p.parseConstEnumMemberBlock()
	open := false
	for _, annotation := range annotations {
		if annotation.Name == "open" {
			open = true
		}
	}
	decl := &ast.ConstEnumDecl{Position: pos, Name: name, Storage: storage, Members: members, Foreign: true, Open: open}
	if !open && len(members) > 0 {
		p.pendingDecls = append(p.pendingDecls, p.synthesizeForeignEnumValidator(decl, storage)...)
	}
	return decl
}

// synthesizeForeignEnumValidator builds the validator (and, once per file, the ForeignEnum
// error set it raises). Every node is freshly built: sharing a member's Value expression with
// the enum declaration would give two meanings to one node in the analyzer's side tables.
func (p *Parser) synthesizeForeignEnumValidator(decl *ast.ConstEnumDecl, storage ast.TypeExpr) []ast.Decl {
	pos := decl.Position
	var decls []ast.Decl
	if !p.foreignEnumErrorSetEmitted {
		p.foreignEnumErrorSetEmitted = true
		decls = append(decls, &ast.ErrorDecl{
			Position: pos,
			Name:     foreignEnumErrorSetName,
			Tags:     []ast.ErrorVariantDecl{{Position: pos, Name: foreignEnumOutOfRangeTag}},
		})
	}
	enumType := func() ast.TypeExpr { return &ast.NamedType{Position: pos, Name: decl.Name} }
	// k: Name = raw.Name()
	bind := &ast.VarDeclStmt{
		Position: pos,
		Name:     "k",
		Type:     enumType(),
		Value: &ast.CastExpr{
			Position: pos,
			Operand:  &ast.Ident{Position: pos, Name: foreignEnumValidatorParm},
			Target:   enumType(),
			Origin:   ast.CastExprOriginPostfixShorthand,
		},
	}
	// k != Name.M1 and k != Name.M2 and ...
	var cond ast.Expr
	for _, member := range decl.Members {
		test := &ast.BinaryExpr{
			Position: pos,
			Op:       lexer.TOKEN_BANGEQ,
			Left:     &ast.Ident{Position: pos, Name: "k"},
			Right:    &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: decl.Name}, Field: member.Name},
		}
		if cond == nil {
			cond = test
			continue
		}
		cond = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_AND, Left: cond, Right: test}
	}
	guard := &ast.IfStmt{
		Position: pos,
		Cond:     cond,
		Then: []ast.Stmt{&ast.ExprStmt{Position: pos, Expr: &ast.RaiseExpr{
			Position: pos,
			Error:    &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: foreignEnumErrorSetName}, Field: foreignEnumOutOfRangeTag},
		}}},
	}
	return append(decls, &ast.FuncDecl{
		Position: pos,
		Name:     ast.ForeignEnumValidatorName(decl.Name),
		Params:   []ast.ParamDecl{{Position: pos, Name: foreignEnumValidatorParm, Type: storage}},
		ReturnType: &ast.ErrorUnionTypeExpr{
			Position: pos,
			Value:    enumType(),
			Errors: &ast.ErrorSetExpr{Position: pos, Tags: []ast.ErrorTagExpr{
				{Position: pos, SetName: foreignEnumErrorSetName, Family: true},
			}},
		},
		Body: []ast.Stmt{bind, guard, &ast.ReturnStmt{Position: pos, Value: &ast.Ident{Position: pos, Name: "k"}}},
	})
}
