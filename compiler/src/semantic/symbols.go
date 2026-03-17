package semantic

import (
	"fmt"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

type Diagnostic struct {
	Pos     lexer.Pos
	Message string
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("%s:%d:%d: %s", d.Pos.File, d.Pos.Line, d.Pos.Col, d.Message)
}

type Result struct {
	File            *ast.File
	GlobalScope     *Scope
	NamedTypes      map[string]Type
	ConstValues     map[string]ConstValue
	ExprTypes       map[ast.Expr]Type
	ExportedTypes   []*ExportedType
	ExportedFuncs   []*ExportedFunc
	ExportedGlobals []*ExportedGlobal
	Diagnostics     []Diagnostic
}

type ExportedType struct {
	PublicName string
	Type       Type
	Decl       *ast.ExportTypeDecl
}

type ExportedFunc struct {
	PublicName        string
	Signature         *FuncType
	TargetName        string
	TargetBase        *FuncType
	TargetSpecialized *FuncType
	TargetGenericDecl *ast.FuncDecl
	TargetBindings    map[string]Type
	Decl              *ast.ExportFuncDecl
}

type ExportedGlobal struct {
	PublicName string
	Type       Type
	TargetName string
	TargetKind SymbolKind
	Mutable    bool
	Decl       *ast.ExportGlobalDecl
}

func (r *Result) Errors() []string {
	out := make([]string, 0, len(r.Diagnostics))
	for _, d := range r.Diagnostics {
		out = append(out, d.String())
	}
	return out
}

type SymbolKind string

const (
	SymbolConst      SymbolKind = "const"
	SymbolGlobal     SymbolKind = "global"
	SymbolFunc       SymbolKind = "func"
	SymbolExternFunc SymbolKind = "extern-func"
	SymbolExternVar  SymbolKind = "extern-var"
	SymbolStruct     SymbolKind = "struct"
	SymbolExternType SymbolKind = "extern-type"
	SymbolParam      SymbolKind = "param"
	SymbolLocal      SymbolKind = "local"
	SymbolRegion     SymbolKind = "region"
)

type Symbol struct {
	Name    string
	Kind    SymbolKind
	Type    Type
	Node    ast.Node
	Mutable bool
}

type Scope struct {
	Parent      *Scope
	Symbols     map[string]*Symbol
	Refinements map[string]Type
}

func NewScope(parent *Scope) *Scope {
	return &Scope{Parent: parent, Symbols: map[string]*Symbol{}, Refinements: map[string]Type{}}
}

func (s *Scope) Define(sym *Symbol) (*Symbol, bool) {
	if existing, ok := s.Symbols[sym.Name]; ok {
		return existing, false
	}
	s.Symbols[sym.Name] = sym
	return sym, true
}

func (s *Scope) Lookup(name string) (*Symbol, bool) {
	for cur := s; cur != nil; cur = cur.Parent {
		if sym, ok := cur.Symbols[name]; ok {
			return sym, true
		}
	}
	return nil, false
}

func (s *Scope) LookupRefinement(key string) (Type, bool) {
	for cur := s; cur != nil; cur = cur.Parent {
		if t, ok := cur.Refinements[key]; ok {
			return t, true
		}
	}
	return nil, false
}
