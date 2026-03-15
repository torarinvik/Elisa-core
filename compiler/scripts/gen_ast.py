#!/usr/bin/env python3
"""Generate ast.go for the llcontext compiler."""
import os

outpath = os.path.join(os.path.dirname(__file__), '..', 'ast', 'ast.go')

decl_types = ['ConstDecl', 'GlobalDecl', 'StructDecl', 'FuncDecl', 'ExternFuncDecl', 'ExternVarDecl', 'ExternTypeDecl', 'StaticIfDecl']
type_types = ['NamedType', 'RefType', 'GenericType', 'MutableType', 'TailType']
expr_types = ['Ident', 'IntLit', 'StringLit', 'BoolLit', 'NullLit', 'ZeroedLit', 'BinaryExpr', 'UnaryExpr', 'CallExpr', 'FieldExpr', 'IndexExpr', 'CastExpr', 'SizeofExpr', 'TernaryExpr', 'AddrOfExpr', 'StructLitExpr', 'ParenExpr']
stmt_types = ['AssignStmt', 'AugAssignStmt', 'AsRefAssignStmt', 'VarDeclStmt', 'ReturnStmt', 'IfStmt', 'WhileStmt', 'PassStmt', 'PanicStmt', 'ExprStmt', 'StaticIfStmt', 'StaticErrorStmt', 'DiscardStmt']
all_types = decl_types + type_types + expr_types + stmt_types

lines = []
def w(s=''):
    lines.append(s)

w('package ast')
w()
w('import "llcontext/lexer"')
w()
w('type Node interface {')
w('\tPos() lexer.Pos')
w('\tnodeTag()')
w('}')
w()
w('type File struct {')
w('\tFilename string')
w('\tDecls    []Decl')
w('}')
w()
w('type Decl interface {')
w('\tNode')
w('\tdeclTag()')
w('}')
w()

# ConstDecl
w('type ConstDecl struct {')
w('\tPosition lexer.Pos')
w('\tName     string')
w('\tType     TypeExpr')
w('\tValue    Expr')
w('}')
w()

# GlobalDecl
w('type GlobalDecl struct {')
w('\tPosition lexer.Pos')
w('\tMutable  bool')
w('\tName     string')
w('\tType     TypeExpr')
w('\tValue    Expr')
w('}')
w()

# StructDecl
w('type StructDecl struct {')
w('\tPosition   lexer.Pos')
w('\tName       string')
w('\tTypeParams []string')
w('\tReprC      bool')
w('\tFields     []FieldDecl')
w('}')
w()
w('type FieldDecl struct {')
w('\tPosition lexer.Pos')
w('\tName     string')
w('\tMutable  bool')
w('\tIsTail   bool')
w('\tType     TypeExpr')
w('}')
w()

# FuncDecl
w('type FuncDecl struct {')
w('\tPosition   lexer.Pos')
w('\tName       string')
w('\tTypeParams []string')
w('\tParams     []ParamDecl')
w('\tReturnType TypeExpr')
w('\tBody       []Stmt')
w('}')
w()
w('type ParamDecl struct {')
w('\tPosition lexer.Pos')
w('\tName     string')
w('\tMutable  bool')
w('\tType     TypeExpr')
w('}')
w()

# ExternFuncDecl
w('type ExternFuncDecl struct {')
w('\tPosition   lexer.Pos')
w('\tName       string')
w('\tParams     []ParamDecl')
w('\tReturnType TypeExpr')
w('\tVariadic   bool')
w('}')
w()

# ExternVarDecl
w('type ExternVarDecl struct {')
w('\tPosition lexer.Pos')
w('\tName     string')
w('\tType     TypeExpr')
w('}')
w()

# ExternTypeDecl
w('type ExternTypeDecl struct {')
w('\tPosition lexer.Pos')
w('\tName     string')
w('}')
w()

# StaticIfDecl
w('type StaticIfDecl struct {')
w('\tPosition lexer.Pos')
w('\tCond     Expr')
w('\tThen     []Decl')
w('\tElifs    []StaticElifDecl')
w('\tElse     []Decl')
w('}')
w()
w('type StaticElifDecl struct {')
w('\tPosition lexer.Pos')
w('\tCond     Expr')
w('\tBody     []Decl')
w('}')
w()

# TypeExpr
w('type TypeExpr interface {')
w('\tNode')
w('\ttypeExprTag()')
w('}')
w()
w('type NamedType struct {')
w('\tPosition lexer.Pos')
w('\tName     string')
w('}')
w()
w('type RefType struct {')
w('\tPosition lexer.Pos')
w('\tElem     TypeExpr')
w('\tNullable bool')
w('}')
w()
w('type GenericType struct {')
w('\tPosition lexer.Pos')
w('\tName     string')
w('\tArgs     []TypeExpr')
w('}')
w()
w('type MutableType struct {')
w('\tPosition lexer.Pos')
w('\tElem     TypeExpr')
w('}')
w()
w('type TailType struct {')
w('\tPosition lexer.Pos')
w('\tElem     TypeExpr')
w('}')
w()

# Expr
w('type Expr interface {')
w('\tNode')
w('\texprTag()')
w('}')
w()
for name, fields in [
    ('Ident', [('Name', 'string')]),
    ('IntLit', [('Value', 'string'), ('Suffix', 'string'), ('IsHex', 'bool')]),
    ('StringLit', [('Value', 'string')]),
    ('BoolLit', [('Value', 'bool')]),
    ('NullLit', []),
    ('ZeroedLit', []),
    ('BinaryExpr', [('Op', 'lexer.TokenKind'), ('Left', 'Expr'), ('Right', 'Expr')]),
    ('UnaryExpr', [('Op', 'lexer.TokenKind'), ('Operand', 'Expr')]),
    ('CallExpr', [('Func', 'Expr'), ('Args', '[]Expr')]),
    ('FieldExpr', [('Object', 'Expr'), ('Field', 'string')]),
    ('IndexExpr', [('Object', 'Expr'), ('Index', 'Expr')]),
    ('CastExpr', [('Operand', 'Expr'), ('Target', 'TypeExpr')]),
    ('SizeofExpr', [('Type', 'TypeExpr')]),
    ('TernaryExpr', [('Value', 'Expr'), ('Cond', 'Expr'), ('Alt', 'Expr')]),
    ('AddrOfExpr', [('Operand', 'Expr')]),
    ('StructLitExpr', [('Name', 'string'), ('Args', '[]Expr')]),
    ('ParenExpr', [('Inner', 'Expr')]),
]:
    w(f'type {name} struct {{')
    w('\tPosition lexer.Pos')
    for fname, ftype in fields:
        w(f'\t{fname}     {ftype}')
    w('}')
    w()

# Stmt
w('type Stmt interface {')
w('\tNode')
w('\tstmtTag()')
w('}')
w()
for name, fields in [
    ('AssignStmt', [('Target', 'Expr'), ('Value', 'Expr')]),
    ('AugAssignStmt', [('Op', 'lexer.TokenKind'), ('Target', 'Expr'), ('Value', 'Expr')]),
    ('AsRefAssignStmt', [('Target', 'Expr'), ('AsKind', 'string'), ('Value', 'Expr')]),
    ('VarDeclStmt', [('Name', 'string'), ('Mutable', 'bool'), ('Type', 'TypeExpr'), ('Value', 'Expr')]),
    ('ReturnStmt', [('Value', 'Expr')]),
    ('IfStmt', [('Cond', 'Expr'), ('Then', '[]Stmt'), ('Elifs', '[]ElifClause'), ('Else', '[]Stmt')]),
    ('WhileStmt', [('Cond', 'Expr'), ('Body', '[]Stmt')]),
    ('PassStmt', []),
    ('PanicStmt', [('Message', 'Expr')]),
    ('ExprStmt', [('Expr', 'Expr')]),
    ('StaticIfStmt', [('Cond', 'Expr'), ('Then', '[]Stmt'), ('Elifs', '[]StaticElifClause'), ('Else', '[]Stmt')]),
    ('StaticErrorStmt', [('Message', 'Expr')]),
    ('DiscardStmt', [('Value', 'Expr')]),
]:
    w(f'type {name} struct {{')
    w('\tPosition lexer.Pos')
    for fname, ftype in fields:
        w(f'\t{fname}     {ftype}')
    w('}')
    w()

w('type ElifClause struct {')
w('\tPosition lexer.Pos')
w('\tCond     Expr')
w('\tBody     []Stmt')
w('}')
w()
w('type StaticElifClause struct {')
w('\tPosition lexer.Pos')
w('\tCond     Expr')
w('\tBody     []Stmt')
w('}')
w()

# Tag implementations
w('// ---------- Tag implementations ----------')
w()
for t in all_types:
    w(f'func (n *{t}) Pos() lexer.Pos {{ return n.Position }}')
w()
for t in all_types:
    w(f'func (*{t}) nodeTag() {{}}')
w()
for t in decl_types:
    w(f'func (*{t}) declTag() {{}}')
w()
for t in type_types:
    w(f'func (*{t}) typeExprTag() {{}}')
w()
for t in expr_types:
    w(f'func (*{t}) exprTag() {{}}')
w()
for t in stmt_types:
    w(f'func (*{t}) stmtTag() {{}}')
w()

with open(outpath, 'w') as f:
    f.write('\n'.join(lines))

print(f'Wrote {len(lines)} lines to {outpath}')
