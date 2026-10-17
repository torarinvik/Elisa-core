// Command astclonegen emits the typed replay copier for checked syntax snapshots.
// Run from compiler/: go run ./internal/astclonegen
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

type generator struct {
	out      bytes.Buffer
	astPkg   *types.Package
	universe []*types.Named
	needed   map[string]string
	structs  map[string]*types.Named
	queue    []func()
	serial   int
}

func sourceType(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string {
		if p.Path() == modulePath+"/src/ast" {
			return ""
		}
		return p.Name()
	})
}
func fieldType(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string { return p.Name() })
}
func mutable(t types.Type) bool {
	switch t := t.(type) {
	case *types.Named:
		return mutable(t.Underlying())
	case *types.Pointer, *types.Slice, *types.Map, *types.Interface:
		return true
	case *types.Array:
		return mutable(t.Elem())
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			if mutable(t.Field(i).Type()) {
				return true
			}
		}
	}
	return false
}
func (g *generator) track(t types.Type) {
	switch t := t.(type) {
	case *types.Named:
		if st, ok := t.Underlying().(*types.Struct); ok {
			key := fieldType(t)
			if _, seen := g.structs[key]; seen {
				return
			}
			g.structs[key] = t
			for i := 0; i < st.NumFields(); i++ {
				if !st.Field(i).Exported() {
					panic("private syntax field " + key)
				}
				g.track(st.Field(i).Type())
			}
		}
	case *types.Pointer:
		g.track(t.Elem())
	case *types.Slice:
		g.track(t.Elem())
	case *types.Array:
		g.track(t.Elem())
	case *types.Map:
		g.track(t.Key())
		g.track(t.Elem())
	}
}
func (g *generator) expression(t types.Type, in string) string {
	g.track(t)
	if !mutable(t) {
		return in
	}
	key := fieldType(t)
	name, ok := g.needed[key]
	if !ok {
		g.serial++
		name = fmt.Sprintf("syntaxFast%d", g.serial)
		g.needed[key] = name
		g.queue = append(g.queue, func() { g.emit(name, t) })
	}
	return "c." + name + "(" + in + ")"
}
func (g *generator) fields(st *types.Struct, from, to string) {
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if !f.Exported() {
			panic("private syntax field")
		}
		g.track(f.Type())
		if mutable(f.Type()) {
			fmt.Fprintf(&g.out, "%s.%s = %s\n", to, f.Name(), g.expression(f.Type(), from+"."+f.Name()))
		}
	}
}
func (g *generator) emit(name string, t types.Type) {
	text := sourceType(t)
	fmt.Fprintf(&g.out, "func(c *syntaxFastCopier) %s(in %s) %s {\n", name, text, text)
	underlying := t
	if n, ok := t.(*types.Named); ok {
		underlying = n.Underlying()
	}
	switch tt := underlying.(type) {
	case *types.Pointer:
		fmt.Fprintf(&g.out, "if in==nil {return nil};if prior,ok:=c.nodes[in];ok {return prior.(%s)}\nout:=new(%s);c.nodes[in]=out;*out=*in\n", text, sourceType(tt.Elem()))
		if n, ok := tt.Elem().(*types.Named); ok {
			if st, ok := n.Underlying().(*types.Struct); ok {
				g.track(n)
				g.fields(st, "in", "out")
			} else if mutable(tt.Elem()) {
				fmt.Fprintf(&g.out, "*out=%s\n", g.expression(tt.Elem(), "*in"))
			}
		} else if mutable(tt.Elem()) {
			fmt.Fprintf(&g.out, "*out=%s\n", g.expression(tt.Elem(), "*in"))
		}
		g.out.WriteString("return out\n")
	case *types.Struct:
		g.out.WriteString("out:=in\n")
		g.fields(tt, "in", "out")
		g.out.WriteString("return out\n")
	case *types.Interface:
		g.out.WriteString("if in==nil {return nil}\nswitch value:=in.(type) {\n")
		for _, candidate := range g.universe {
			if types.Implements(types.NewPointer(candidate), tt) {
				fmt.Fprintf(&g.out, "case *%s:return %s\n", sourceType(candidate), g.expression(types.NewPointer(candidate), "value"))
			}
			if types.Implements(candidate, tt) {
				fmt.Fprintf(&g.out, "case %s:return %s\n", sourceType(candidate), g.expression(candidate, "value"))
			}
		}
		g.out.WriteString("default:c.err=fmt.Errorf(\"unsupported syntax replay type %T\",in);return nil\n}\n")
	case *types.Slice:
		fmt.Fprintf(&g.out, "if in==nil {return nil};key:=syntaxFastSlice{%q,unsafe.Pointer(unsafe.SliceData(in)),len(in),cap(in)}\nif prior,ok:=c.slices[key];ok {return prior.(%s)}\nout:=make(%s,len(in),cap(in));c.slices[key]=out\nfull,target:=in[:cap(in)],out[:cap(out)];copy(target,full)\n", name, text, text)
		if mutable(tt.Elem()) {
			fmt.Fprintf(&g.out, "for i:=range full {target[i]=%s}\n", g.expression(tt.Elem(), "full[i]"))
		}
		g.out.WriteString("return out\n")
	case *types.Map:
		fmt.Fprintf(&g.out, "if in==nil {return nil};key:=syntaxFastMap{%q,reflect.ValueOf(in).UnsafePointer()}\nif prior,ok:=c.maps[key];ok {return prior.(%s)}\nout:=make(%s,len(in));c.maps[key]=out\nfor k,v:=range in {out[%s]=%s};return out\n", name, text, text, g.expression(tt.Key(), "k"), g.expression(tt.Elem(), "v"))
	case *types.Array:
		fmt.Fprintf(&g.out, "out:=in;for i:=range in {out[i]=%s};return out\n", g.expression(tt.Elem(), "in[i]"))
	default:
		panic("unsupported syntax replay " + text)
	}
	g.out.WriteString("}\n\n")
}
func generate() ([]byte, error) {
	fset := token.NewFileSet()
	im := &srcImporter{fset: fset, std: importer.ForCompiler(fset, "source", nil), cache: map[string]*types.Package{}}
	pkg, err := im.Import(modulePath + "/src/ast")
	if err != nil {
		return nil, err
	}
	g := &generator{astPkg: pkg, needed: map[string]string{}, structs: map[string]*types.Named{}}
	for _, name := range pkg.Scope().Names() {
		if obj, ok := pkg.Scope().Lookup(name).(*types.TypeName); ok && !obj.IsAlias() {
			if named, ok := obj.Type().(*types.Named); ok {
				if _, ok := named.Underlying().(*types.Struct); ok {
					g.universe = append(g.universe, named)
				}
			}
		}
	}
	root := pkg.Scope().Lookup("File").Type()
	expression := g.expression(types.NewPointer(root), "file")
	for len(g.queue) > 0 {
		queue := g.queue
		g.queue = nil
		for _, emit := range queue {
			emit()
		}
	}
	var out bytes.Buffer
	out.WriteString("// Code generated by internal/astclonegen; DO NOT EDIT.\npackage ast\nimport(\"fmt\";\"reflect\";\"unsafe\";\"elisacore/src/lexer\")\nvar _ = lexer.Pos{}\n")
	out.WriteString("type syntaxFastSlice struct {kind string; pointer unsafe.Pointer; length,capacity int}\ntype syntaxFastMap struct {kind string; pointer unsafe.Pointer}\ntype syntaxFastCopier struct {nodes map[any]any;slices map[syntaxFastSlice]any;maps map[syntaxFastMap]any;err error}\n")
	fmt.Fprintf(&out, "func copyFastSyntaxFile(file *File) (*File,error) {c:=&syntaxFastCopier{nodes:map[any]any{},slices:map[syntaxFastSlice]any{},maps:map[syntaxFastMap]any{}};out:=%s;return out,c.err}\n", expression)
	out.Write(g.out.Bytes())
	out.WriteString("var syntaxFastTypes=map[reflect.Type][]string{\n")
	keys := make([]string, 0, len(g.structs))
	for key := range g.structs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		named := g.structs[key]
		st := named.Underlying().(*types.Struct)
		fmt.Fprintf(&out, "reflect.TypeOf(%s{}):{", sourceType(named))
		for i := 0; i < st.NumFields(); i++ {
			field := st.Field(i)
			fmt.Fprintf(&out, "%q,", field.Name()+":"+fieldType(field.Type()))
		}
		out.WriteString("},\n")
	}
	out.WriteString("}\nvar syntaxFastSchemaValid=func() bool {for typ,fields:=range syntaxFastTypes {if typ.NumField()!=len(fields) {return false};for i,field:=range fields {f:=typ.Field(i);if f.Name+\":\"+f.Type.String()!=field {return false}}};return true}()\nfunc FastSyntaxCopiesAvailable() bool {return syntaxFastSchemaValid}\n")
	return format.Source(out.Bytes())
}
func main() {
	source, err := generate()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = os.WriteFile("src/ast/syntax_snapshot_gen.go", source, 0644); err != nil {
		panic(err)
	}
}
