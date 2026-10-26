// Command astwalkgen generates the typed AST pointer walker used by package semantic
// (src/semantic/ast_pointer_walk_gen.go). It reproduces, without reflection, exactly what the
// reflective fillMayAdoptWalkSeen does: visit every non-nil pointer reachable through exported
// fields, structs, slices, arrays, interfaces and map values, each address once, in field order.
// Anything the generated code cannot name (unexported types, types outside src/ast and src/lexer,
// implementers it does not know) is handed to the reflective walker, so the result is identical.
//
// Usage (from compiler/): go run ./internal/astwalkgen   (writes every file in `modes`)
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"sort"
	"strings"
)

const modulePath = "elisacore"

var pkgDirs = map[string]string{
	"elisacore/src/ast":   "src/ast",
	"elisacore/src/lexer": "src/lexer",
}

type srcImporter struct {
	fset  *token.FileSet
	std   types.Importer
	cache map[string]*types.Package
}

func (im *srcImporter) Import(path string) (*types.Package, error) {
	if p, ok := im.cache[path]; ok {
		return p, nil
	}
	if !strings.HasPrefix(path, modulePath+"/") {
		return im.std.Import(path)
	}
	dir := strings.TrimPrefix(path, modulePath+"/")
	pkgs, err := parser.ParseDir(im.fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil, err
	}
	var files []*ast.File
	for name, p := range pkgs {
		if strings.HasSuffix(name, "_test") {
			continue
		}
		names := make([]string, 0, len(p.Files))
		for fn := range p.Files {
			names = append(names, fn)
		}
		sort.Strings(names)
		for _, fn := range names {
			files = append(files, p.Files[fn])
		}
	}
	conf := types.Config{Importer: im}
	pkg, err := conf.Check(path, im.fset, files, nil)
	if err != nil {
		return nil, err
	}
	im.cache[path] = pkg
	return pkg, nil
}

// walkMode is one generated walker. Both share the visit rule; the store-scan walker (the
// reflective walkStoreScanNodes) does not descend into map values.
type walkMode struct {
	File     string // output path, relative to compiler/
	Maps     bool
	Prefix   string // per-type function prefix
	Reflect  string // reflective fallback (same semantics)
	Entry    string // typed entry point
	Describe string
	// Prune: visit returns whether to descend into the node, and nodes are not
	// deduplicated -- the shape of the reflective predicate walks it replaces.
	Prune bool
	// OpaqueFields delegates maps and structs with inaccessible fields to the
	// caller's complete reflective policy instead of the pointer-only policy.
	OpaqueFields bool
	ProofBinding bool
}

var modes = []walkMode{
	{File: "src/semantic/ast_pointer_walk_gen.go", Maps: true, Prefix: "astWalk", Reflect: "fillMayAdoptWalkReflect", Entry: "fillMayAdoptWalkTyped", Describe: "fillMayAdoptWalk"},
	{File: "src/semantic/ast_scan_walk_gen.go", Maps: false, Prefix: "astScanWalk", Reflect: "storeScanWalkReflect", Entry: "storeScanWalkTyped", Describe: "walkStoreScanNodes"},
	{File: "src/semantic/ast_prune_walk_gen.go", Maps: false, Prefix: "astPruneWalk", Reflect: "pruneWalkReflect", Entry: "pruneWalkTyped", Describe: "walkPruned", Prune: true},
	{File: "src/semantic/ast_canonical_walk_gen.go", Maps: true, Prefix: "astCanonicalWalk", Reflect: "canonicalASTWalkOpaque", Entry: "canonicalASTWalkTyped", Describe: "canonical AST membership", Prune: true, OpaqueFields: true},
	{File: "src/semantic/proof_binding_walk_gen.go", Describe: "proof-binding source syntax", ProofBinding: true},
}

type gen struct {
	maps         bool
	opaqueFields bool
	out          bytes.Buffer
	universe     []*types.Named // exported named types in the walked packages
	ptrFuncs     map[string]bool
	valFuncs     map[string]bool
	ifaceFunc    map[string]bool
	queue        []func()
	funcCount    int
}

func walked(n *types.Named) bool {
	if n.Obj().Pkg() == nil {
		return false
	}
	_, ok := pkgDirs[n.Obj().Pkg().Path()]
	return ok && n.Obj().Exported() && n.TypeParams().Len() == 0
}

func qual(n *types.Named) string {
	return n.Obj().Pkg().Name() + "." + n.Obj().Name()
}

func ident(n *types.Named) string {
	return n.Obj().Pkg().Name() + "_" + n.Obj().Name()
}

// typeExpr renders t as Go source usable from package semantic, or "" if it cannot be named.
func typeExpr(t types.Type) string {
	switch t := t.(type) {
	case *types.Named:
		if t.Obj().Pkg() == nil {
			return t.Obj().Name()
		}
		if !t.Obj().Exported() || t.TypeParams().Len() > 0 {
			return ""
		}
		return t.Obj().Pkg().Name() + "." + t.Obj().Name()
	case *types.Basic:
		return t.Name()
	case *types.Pointer:
		if e := typeExpr(t.Elem()); e != "" {
			return "*" + e
		}
	case *types.Slice:
		if e := typeExpr(t.Elem()); e != "" {
			return "[]" + e
		}
	}
	return ""
}

// emitWalk writes code walking the value at expression `x` (addressable) of type t.
func (g *gen) emitWalk(b *bytes.Buffer, x string, t types.Type, depth int) {
	switch tt := t.(type) {
	case *types.Basic:
		return
	case *types.Signature, *types.Chan:
		return
	case *types.Named:
		switch u := tt.Underlying().(type) {
		case *types.Struct:
			if walked(tt) {
				g.needVal(tt)
				fmt.Fprintf(b, "astWalkVal_%s(&%s, visit, seen)\n", ident(tt), x)
				return
			}
			g.fallback(b, x)
			return
		case *types.Interface:
			if walked(tt) {
				g.needIface(tt)
				fmt.Fprintf(b, "if %s != nil { astWalkIface_%s(%s, visit, seen) }\n", x, ident(tt), x)
				return
			}
			if u.NumMethods() == 0 || typeExpr(tt) != "" {
				fmt.Fprintf(b, "if %s != nil { fillMayAdoptWalkReflect(reflect.ValueOf(%s), visit, seen) }\n", x, x)
				return
			}
			g.fallback(b, x)
			return
		case *types.Basic:
			return
		default:
			if typeExpr(tt) == "" {
				g.fallback(b, x)
				return
			}
			g.emitWalk(b, x, u, depth)
			return
		}
	case *types.Pointer:
		if n, ok := tt.Elem().(*types.Named); ok && walked(n) {
			if _, isStruct := n.Underlying().(*types.Struct); isStruct {
				g.needPtr(n)
				fmt.Fprintf(b, "astWalkPtr_%s(%s, visit, seen)\n", ident(n), x)
				return
			}
		}
		if typeExpr(tt) != "" {
			fmt.Fprintf(b, "if %s != nil { fillMayAdoptWalkReflect(reflect.ValueOf(%s), visit, seen) }\n", x, x)
			return
		}
		g.fallback(b, x)
	case *types.Slice, *types.Array:
		var elem types.Type
		if s, ok := tt.(*types.Slice); ok {
			elem = s.Elem()
		} else {
			elem = tt.(*types.Array).Elem()
		}
		v := fmt.Sprintf("i%d", depth)
		var inner bytes.Buffer
		g.emitWalk(&inner, fmt.Sprintf("%s[%s]", x, v), elem, depth+1)
		if inner.Len() == 0 {
			return // elements hold no pointers
		}
		fmt.Fprintf(b, "for %s := range %s {\n", v, x)
		b.Write(inner.Bytes())
		b.WriteString("}\n")
	case *types.Map:
		if g.opaqueFields {
			// Canonical identity includes keys as well as values and must retain
			// the outer map's identity guard for aliases and cycles.
			g.fallback(b, x)
			return
		}
		if !g.maps {
			return
		}
		// Map values are not addressable: walk a copy (only pointers inside it are visited).
		c := fmt.Sprintf("c%d", depth)
		var inner bytes.Buffer
		g.emitWalk(&inner, c, tt.Elem(), depth+1)
		if inner.Len() == 0 {
			return
		}
		fmt.Fprintf(b, "for _, %s := range %s {\n", c, x)
		b.Write(inner.Bytes())
		b.WriteString("}\n")
	case *types.Struct:
		g.fallback(b, x) // anonymous struct: rare, keep reflective
	default:
		g.fallback(b, x)
	}
}

func (g *gen) fallback(b *bytes.Buffer, x string) {
	fmt.Fprintf(b, "fillMayAdoptWalkReflect(reflect.ValueOf(&%s).Elem(), visit, seen)\n", x)
}

func (g *gen) needPtr(n *types.Named) {
	key := ident(n)
	if g.ptrFuncs[key] {
		return
	}
	g.ptrFuncs[key] = true
	g.needVal(n)
	g.queue = append(g.queue, func() {
		fmt.Fprintf(&g.out, "func astWalkPtr_%s(p *%s, visit func(any), seen map[uintptr]bool) {\n", key, qual(n))
		fmt.Fprintf(&g.out, "if p == nil { return }\nk := uintptr(unsafe.Pointer(p))\nif seen[k] { return }\nseen[k] = true\nvisit(p)\nastWalkVal_%s(p, visit, seen)\n}\n\n", key)
	})
}

func (g *gen) needVal(n *types.Named) {
	key := ident(n)
	if g.valFuncs[key] {
		return
	}
	g.valFuncs[key] = true
	st := n.Underlying().(*types.Struct)
	g.queue = append(g.queue, func() {
		var b bytes.Buffer
		if g.opaqueFields {
			for i := 0; i < st.NumFields(); i++ {
				if !st.Field(i).Exported() {
					g.fallback(&b, "*p")
					fmt.Fprintf(&g.out, "func astWalkVal_%s(p *%s, visit func(any), seen map[uintptr]bool) {\n", key, qual(n))
					g.out.Write(b.Bytes())
					g.out.WriteString("}\n\n")
					return
				}
			}
		}
		for i := 0; i < st.NumFields(); i++ {
			f := st.Field(i)
			if !f.Exported() {
				continue
			}
			g.emitWalk(&b, "p."+f.Name(), f.Type(), 0)
		}
		fmt.Fprintf(&g.out, "func astWalkVal_%s(p *%s, visit func(any), seen map[uintptr]bool) {\n", key, qual(n))
		if b.Len() == 0 {
			b.WriteString("_, _, _ = p, visit, seen\n")
		}
		g.out.Write(b.Bytes())
		g.out.WriteString("}\n\n")
	})
}

func (g *gen) needIface(n *types.Named) {
	key := ident(n)
	if g.ifaceFunc[key] {
		return
	}
	g.ifaceFunc[key] = true
	iface := n.Underlying().(*types.Interface)
	g.queue = append(g.queue, func() {
		var b bytes.Buffer
		fmt.Fprintf(&b, "func astWalkIface_%s(v %s, visit func(any), seen map[uintptr]bool) {\nswitch x := v.(type) {\n", key, qual(n))
		for _, c := range g.universe {
			st, isStruct := c.Underlying().(*types.Struct)
			_ = st
			if !isStruct {
				continue
			}
			if types.Implements(types.NewPointer(c), iface) {
				g.needPtr(c)
				fmt.Fprintf(&b, "case *%s:\nastWalkPtr_%s(x, visit, seen)\n", qual(c), ident(c))
			}
			if types.Implements(c, iface) {
				g.needVal(c)
				fmt.Fprintf(&b, "case %s:\nastWalkVal_%s(&x, visit, seen)\n", qual(c), ident(c))
			}
		}
		b.WriteString("default:\nfillMayAdoptWalkReflect(reflect.ValueOf(v), visit, seen)\n}\n}\n\n")
		g.out.Write(b.Bytes())
	})
}

func main() {
	for _, m := range modes {
		src, err := generate(m)
		if err != nil {
			fmt.Fprintln(os.Stderr, "astwalkgen:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(m.File, src, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "astwalkgen:", err)
			os.Exit(1)
		}
	}
}

// generate returns the formatted walker source. It must run with compiler/ as the working
// directory (package paths are resolved relative to it).
func generate(m walkMode) ([]byte, error) {
	if m.ProofBinding {
		return generateProofBinding()
	}
	fset := token.NewFileSet()
	im := &srcImporter{fset: fset, std: importer.ForCompiler(fset, "source", nil), cache: map[string]*types.Package{}}
	g := &gen{maps: m.Maps, opaqueFields: m.OpaqueFields, ptrFuncs: map[string]bool{}, valFuncs: map[string]bool{}, ifaceFunc: map[string]bool{}}
	paths := make([]string, 0, len(pkgDirs))
	for p := range pkgDirs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		pkg, err := im.Import(path)
		if err != nil {
			return nil, err
		}
		scope := pkg.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || tn.IsAlias() {
				continue
			}
			if n, ok := tn.Type().(*types.Named); ok && walked(n) {
				g.universe = append(g.universe, n)
			}
		}
	}
	// Entry points: every walked struct pointer, plus the slices of statements/declarations that
	// call sites pass directly.
	var entry bytes.Buffer
	entry.WriteString("func fillMayAdoptWalkTyped(v any, visit func(any), seen map[uintptr]bool) bool {\nswitch x := v.(type) {\n")
	for _, n := range g.universe {
		switch n.Underlying().(type) {
		case *types.Struct:
			g.needPtr(n)
			fmt.Fprintf(&entry, "case *%s:\nastWalkPtr_%s(x, visit, seen)\n", qual(n), ident(n))
		case *types.Interface:
			// A bare interface value reaches here only through its dynamic type.
		}
	}
	for _, n := range g.universe {
		if _, ok := n.Underlying().(*types.Interface); ok && walked(n) {
			fmt.Fprintf(&entry, "case []%s:\n", qual(n))
			var b bytes.Buffer
			g.emitWalk(&b, "x", types.NewSlice(n), 0)
			entry.Write(b.Bytes())
		}
	}
	entry.WriteString("default:\nreturn false\n}\nreturn true\n}\n\n")
	for len(g.queue) > 0 {
		q := g.queue
		g.queue = nil
		for _, f := range q {
			f()
		}
	}
	var file bytes.Buffer
	file.WriteString("// Code generated by internal/astwalkgen; DO NOT EDIT.\n// Typed equivalent of the reflective " + m.Describe + " walk.\n\npackage semantic\n\nimport (\n\t\"reflect\"\n\t\"unsafe\"\n\n\t\"elisacore/src/ast\"\n\t\"elisacore/src/lexer\"\n)\n\n")
	file.WriteString("var _ = lexer.Pos{}\n\n")
	file.Write(entry.Bytes())
	file.Write(g.out.Bytes())
	text := file.String()
	text = strings.ReplaceAll(text, "astWalk", m.Prefix)
	text = strings.ReplaceAll(text, "fillMayAdoptWalkReflect", m.Reflect)
	text = strings.ReplaceAll(text, "fillMayAdoptWalkTyped", m.Entry)
	if m.Prune {
		text = strings.ReplaceAll(text, "if p == nil { return }\nk := uintptr(unsafe.Pointer(p))\nif seen[k] { return }\nseen[k] = true\nvisit(p)\n", "if p == nil || !visit(p) { return }\n")
		text = strings.ReplaceAll(text, "visit func(any), seen map[uintptr]bool", "visit func(any) bool, seen map[uintptr]bool")
		text = strings.Replace(text, "\t\"unsafe\"\n", "", 1)
	}
	src, err := format.Source([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("format: %w", err)
	}
	return src, nil
}

// generateProofBinding emits a scalar-aware AST traversal matching the original
// reflective proof-binding collector. It deliberately has a separate mode:
// the other walkers have different map, array, pruning, and pointer policies.
func generateProofBinding() ([]byte, error) {
	fset := token.NewFileSet()
	im := &srcImporter{fset: fset, std: importer.ForCompiler(fset, "source", nil), cache: map[string]*types.Package{}}
	pkg, err := im.Import("elisacore/src/ast")
	if err != nil {
		return nil, err
	}
	declName, ok := pkg.Scope().Lookup("Decl").(*types.TypeName)
	if !ok {
		return nil, fmt.Errorf("ast.Decl interface not found")
	}
	declIface, ok := types.Unalias(declName.Type()).Underlying().(*types.Interface)
	if !ok {
		return nil, fmt.Errorf("ast.Decl is not an interface")
	}
	declIface.Complete()

	var structs []*types.Named
	for _, name := range pkg.Scope().Names() {
		tn, ok := pkg.Scope().Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		named, ok := tn.Type().(*types.Named)
		if !ok || !tn.Exported() || named.TypeParams().Len() != 0 {
			continue
		}
		if _, ok := named.Underlying().(*types.Struct); ok {
			structs = append(structs, named)
		}
	}
	sort.Slice(structs, func(i, j int) bool { return structs[i].Obj().Name() < structs[j].Obj().Name() })
	byName := make(map[string]*types.Named, len(structs))
	for _, named := range structs {
		byName[named.Obj().Name()] = named
	}
	fileType := byName["File"]
	if fileType == nil {
		return nil, fmt.Errorf("ast.File struct not found")
	}

	var out bytes.Buffer
	out.WriteString("// Code generated by internal/astwalkgen; DO NOT EDIT.\n// Typed scalar-aware equivalent of the proof-binding source walk.\n\npackage semantic\n\nimport (\n\t\"reflect\"\n\t\"unsafe\"\n\n\t\"elisacore/src/ast\"\n\t\"elisacore/src/lexer\"\n)\n\n")
	visitType := "func(any, reflect.Type, ast.Decl, uint8, string, string, bool)"
	fmt.Fprintf(&out, "func proofBindingASTWalkTyped(root *ast.File, visit %s, seen map[uintptr]bool) {\nproofBindingWalkPtr_%s(root, visit, seen, nil)\n}\n\n", visitType, ident(fileType))

	// The dispatcher only handles AST pointer structs. Value implementers and
	// unknown dynamic types use the exact reflective fallback in the caller.
	fmt.Fprintf(&out, "func proofBindingWalkAny(value any, visit %s, seen map[uintptr]bool, owner ast.Decl) bool {\nswitch node := value.(type) {\n", visitType)
	for _, named := range structs {
		fmt.Fprintf(&out, "case *%s:\nproofBindingWalkPtr_%s(node, visit, seen, owner)\nreturn true\n", qual(named), ident(named))
	}
	out.WriteString("default:\nreturn false\n}\n}\n\n")

	for _, named := range structs {
		key := ident(named)
		fmt.Fprintf(&out, "func proofBindingWalkPtr_%s(p *%s, visit %s, seen map[uintptr]bool, owner ast.Decl) {\n", key, qual(named), visitType)
		out.WriteString("if p == nil { return }\nk := uintptr(unsafe.Pointer(p))\nif seen[k] { return }\nseen[k] = true\n")
		ptrType := types.NewPointer(named)
		isDecl := types.Implements(ptrType, declIface)
		isBindingNode := named.Obj().Name() == "ExprBlock" || named.Obj().Name() == "RegionStmt" || named.Obj().Name() == "VarDeclStmt"
		if isDecl {
			out.WriteString("owner = p\n")
		}
		if isDecl || isBindingNode {
			fmt.Fprintf(&out, "visit(p, reflect.TypeFor[*%s](), owner, proofBindingVisitNode, \"\", \"\", true)\n", qual(named))
		}
		fmt.Fprintf(&out, "proofBindingWalkVal_%s(p, visit, seen, owner)\n}\n\n", key)

		fmt.Fprintf(&out, "func proofBindingWalkVal_%s(p *%s, visit %s, seen map[uintptr]bool, owner ast.Decl) {\n", key, qual(named), visitType)
		st := named.Underlying().(*types.Struct)
		// Reflection can read unexported fields but generated code cannot. Keep
		// the complete subtree on the original fallback policy in that case.
		for i := 0; i < st.NumFields(); i++ {
			if !st.Field(i).Exported() {
				fmt.Fprintf(&out, "proofBindingWalkReflect(reflect.ValueOf(p).Elem(), visit, seen, owner)\n}\n\n")
				goto nextStruct
			}
		}
		for i := 0; i < st.NumFields(); i++ {
			field := st.Field(i)
			if err := emitProofBindingField(&out, named, field); err != nil {
				return nil, fmt.Errorf("%s.%s: %w", named.Obj().Name(), field.Name(), err)
			}
		}
		out.WriteString("}\n\n")
	nextStruct:
	}

	src, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format proof-binding walk: %w", err)
	}
	return src, nil
}

func emitProofBindingField(out *bytes.Buffer, parent *types.Named, field *types.Var) error {
	if basic, ok := types.Unalias(field.Type()).Underlying().(*types.Basic); ok && basic.Kind() == types.String {
		fmt.Fprintf(out, "visit(p, reflect.TypeFor[%s](), nil, proofBindingVisitStringField, %q, string(p.%s), true)\n", qual(parent), field.Name(), field.Name())
		return nil
	}
	return emitProofBindingValue(out, "p."+field.Name(), field.Type(), 0)
}

func emitProofBindingValue(out *bytes.Buffer, expr string, t types.Type, depth int) error {
	t = types.Unalias(t)
	switch tt := t.(type) {
	case *types.Basic:
		if tt.Kind() == types.String {
			fmt.Fprintf(out, "visit(nil, nil, nil, proofBindingVisitString, \"\", string(%s), false)\n", expr)
		}
	case *types.Named:
		if tt.Obj().Pkg() != nil && tt.Obj().Pkg().Path() == "elisacore/src/lexer" && tt.Obj().Name() == "Pos" {
			fmt.Fprintf(out, "if proofBindingPositionSchemaValid { visit(nil, reflect.TypeFor[lexer.Pos](), nil, proofBindingVisitPositionFile, \"File\", %s.File, false) } else { proofBindingWalkReflect(reflect.ValueOf(&%s).Elem(), visit, seen, owner) }\n", expr, expr)
			return nil
		}
		if tt.Obj().Pkg() != nil && tt.Obj().Pkg().Path() == "elisacore/src/ast" {
			if _, ok := tt.Underlying().(*types.Struct); ok {
				fmt.Fprintf(out, "proofBindingWalkVal_%s(&%s, visit, seen, owner)\n", ident(tt), expr)
				return nil
			}
		}
		return emitProofBindingValue(out, expr, tt.Underlying(), depth)
	case *types.Pointer:
		if named, ok := types.Unalias(tt.Elem()).(*types.Named); ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "elisacore/src/ast" {
			if _, ok := named.Underlying().(*types.Struct); ok {
				fmt.Fprintf(out, "proofBindingWalkPtr_%s(%s, visit, seen, owner)\n", ident(named), expr)
				return nil
			}
		}
		fmt.Fprintf(out, "proofBindingWalkReflect(reflect.ValueOf(%s), visit, seen, owner)\n", expr)
	case *types.Interface:
		fmt.Fprintf(out, "if %s != nil { if !proofBindingWalkAny(%s, visit, seen, owner) { proofBindingWalkReflect(reflect.ValueOf(%s), visit, seen, owner) } }\n", expr, expr, expr)
	case *types.Slice:
		if basic, ok := types.Unalias(tt.Elem()).Underlying().(*types.Basic); ok && basic.Kind() == types.Uint8 {
			return nil
		}
		index := fmt.Sprintf("i%d", depth)
		var body bytes.Buffer
		if err := emitProofBindingValue(&body, expr+"["+index+"]", tt.Elem(), depth+1); err != nil {
			return err
		}
		if body.Len() != 0 {
			fmt.Fprintf(out, "for %s := range %s {\n", index, expr)
			out.Write(body.Bytes())
			out.WriteString("}\n")
		}
	case *types.Array, *types.Map, *types.Signature, *types.Chan:
		// The original proof-binding collector ignores arrays and maps.
	case *types.Struct:
		fmt.Fprintf(out, "proofBindingWalkReflect(reflect.ValueOf(&%s).Elem(), visit, seen, owner)\n", expr)
	default:
		return fmt.Errorf("unsupported type %T", t)
	}
	return nil
}
