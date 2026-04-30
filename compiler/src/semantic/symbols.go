package semantic

import (
	"fmt"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

type Diagnostic struct {
	Pos      lexer.Pos
	Severity DiagnosticSeverity
	Message  string
}

type DiagnosticSeverity string

const (
	DiagnosticSeverityError      DiagnosticSeverity = "error"
	DiagnosticSeverityWarning    DiagnosticSeverity = "warning"
	DiagnosticSeverityDeprecated DiagnosticSeverity = "deprecated"
)

func (d Diagnostic) String() string {
	if d.Severity == "" || d.Severity == DiagnosticSeverityError {
		return fmt.Sprintf("%s: %s", d.Pos, d.Message)
	}
	return fmt.Sprintf("%s: %s: %s", d.Pos, d.Severity, d.Message)
}

type Result struct {
	File                    *ast.File
	LoweredFile             *ast.File
	GlobalScope             *Scope
	NamedTypes              map[string]Type
	TreeAttributes          map[string]map[string]*TreeAttribute
	StaticInterfaces        map[string]*StaticInterface
	StaticImpls             map[string]*StaticImpl
	ContextBundles          map[string]*ContextBundle
	ParamPacks              map[string]*ParamPack
	ConstValues             map[string]ConstValue
	ExprTypes               map[ast.Expr]Type
	AttributeFieldRefs      map[*ast.FieldExpr]*AttributeFieldRef
	RewriteDefaults         map[*ast.Ident]bool
	OptionalBindSourceTypes map[*ast.OptionalBindExpr]Type
	InterfaceMethodRefs     map[*ast.FieldExpr]*InterfaceMethodRef
	SafeCalls               map[*ast.CallExpr]*SafeCallInfo
	ExprFacts               map[ast.Expr]OptimizationFacts
	CastHooks               map[ast.Expr]*Symbol
	InitCalls               map[*ast.StructLitExpr]*ast.CallExpr
	DenseNodeKeys           map[ast.Expr]DenseNodeKeyInfo
	NodeTables              map[ast.Expr]NodeTableInfo
	PackedLowering          PackedLoweringMetadata
	ParallelFor             map[*ast.ParallelForStmt]*ParallelForInfo
	Defer                   map[*ast.DeferStmt]*DeferInfo
	Fold                    map[*ast.FoldExpr]*FoldInfo
	Lambdas                 map[*ast.LambdaExpr]*LambdaInfo
	FunctionAnalyses        map[*ast.FuncDecl]*FunctionAnalysis
	AnnotatedFuncs          []*AnnotatedFunc
	ExportedTypes           []*ExportedType
	ExportedFuncs           []*ExportedFunc
	ExportedGlobals         []*ExportedGlobal
	Diagnostics             []Diagnostic
}

func (r *Result) ActiveFile() *ast.File {
	if r == nil {
		return nil
	}
	if r.LoweredFile != nil {
		return r.LoweredFile
	}
	return r.File
}

type TreeAttribute struct {
	Name       string
	Receiver   Type
	ReturnType Type
	Decl       *ast.AttributeDecl
}

type AttributeFieldRef struct {
	Attribute *TreeAttribute
}

type SafeCallInfo struct {
	ResolvedFuncName string
	ResolvedFuncType *FuncType
	ReceiverArgType  Type
	TailArgs         []ast.Expr
	ImplicitArgs     []ast.Expr
}

type ParallelForInfo struct {
	SourceType Type
	ItemType   Type
	Captures   []string
}

type DeferInfo struct {
	Mode     ast.DeferMode
	Captures []string
}

type FoldInfo struct {
	Captures []string
}

type LambdaInfo struct {
	Captures []string
}

type DenseNodeKeyInfo struct {
	Enum      *EnumType
	StoreRoot *Symbol
	StorePath string
}

type NodeTableInfo struct {
	Enum      *EnumType
	Elem      Type
	StoreRoot *Symbol
	StorePath string
	CountExpr string
}

type PackedLoweringMetadata struct {
	Contract                          string
	CanonicalPackedLowering           string
	OnePackedEnumOneHandleInvariant   bool
	PublicationReadonlyGateStoreState string
}

type AnnotatedFunc struct {
	Name        string
	Annotations []ast.Annotation
	Signature   *FuncType
	Decl        *ast.FuncDecl
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
		if d.Severity != "" && d.Severity != DiagnosticSeverityError {
			continue
		}
		out = append(out, d.String())
	}
	return out
}

func (r *Result) FunctionAnalysis(decl *ast.FuncDecl) (*FunctionAnalysis, bool) {
	if r == nil || decl == nil || r.FunctionAnalyses == nil {
		return nil, false
	}
	analysis, ok := r.FunctionAnalyses[decl]
	return analysis, ok
}

func (r *Result) FunctionAnalysisByName(name string) (*FunctionAnalysis, bool) {
	if r == nil || name == "" || r.GlobalScope == nil {
		return nil, false
	}
	sym, ok := r.GlobalScope.Lookup(name)
	if !ok {
		return nil, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok {
		return nil, false
	}
	return r.FunctionAnalysis(decl)
}

func (r *Result) Warnings() []string {
	out := make([]string, 0, len(r.Diagnostics))
	for _, d := range r.Diagnostics {
		if d.Severity != DiagnosticSeverityWarning {
			continue
		}
		out = append(out, d.String())
	}
	return out
}

func (r *Result) Deprecations() []string {
	out := make([]string, 0, len(r.Diagnostics))
	for _, d := range r.Diagnostics {
		if d.Severity != DiagnosticSeverityDeprecated {
			continue
		}
		out = append(out, d.String())
	}
	return out
}

func (r *Result) Notices() []string {
	out := r.Warnings()
	out = append(out, r.Deprecations()...)
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
	SymbolRegionMark SymbolKind = "region-mark"
	SymbolCheckpoint SymbolKind = "checkpoint"
)

type Symbol struct {
	Name       string
	Kind       SymbolKind
	Type       Type
	Node       ast.Node
	AliasOf    *Symbol
	ParamIndex int
	Mutable    bool
}

func symbolAliasRoot(sym *Symbol) *Symbol {
	for sym != nil && sym.AliasOf != nil {
		sym = sym.AliasOf
	}
	return sym
}

type Scope struct {
	Parent                  *Scope
	Symbols                 map[string]*Symbol
	Refinements             map[string]Type
	ConditionalBindingHints map[string]string
}

func NewScope(parent *Scope) *Scope {
	return &Scope{Parent: parent, Symbols: map[string]*Symbol{}, Refinements: map[string]Type{}, ConditionalBindingHints: map[string]string{}}
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

func (s *Scope) LookupConditionalBindingHint(name string) (string, bool) {
	for cur := s; cur != nil; cur = cur.Parent {
		if hint, ok := cur.ConditionalBindingHints[name]; ok {
			return hint, true
		}
	}
	return "", false
}
