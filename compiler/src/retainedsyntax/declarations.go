// Package retainedsyntax handles syntax-only declaration matching and position rebinding.
package retainedsyntax

import (
	"bytes"
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

func LineKey(p lexer.Pos) string {
	var digits [41]byte
	suffix := strconv.AppendInt(digits[:0], int64(p.Line), 10)
	suffix = append(suffix, ':')
	suffix = strconv.AppendInt(suffix, int64(p.Col), 10)
	var key strings.Builder
	key.Grow(len(p.File) + 1 + len(suffix))
	key.WriteString(p.File)
	key.WriteByte(':')
	key.Write(suffix)
	return key.String()
}

var retainedDeclType = reflect.TypeOf((*ast.Decl)(nil)).Elem()

// WalkDecls lists every declaration in source order, descending into
// declaration containers (modules, impls, conditional blocks) but not bodies.
func WalkDecls(file *ast.File) []ast.Decl {
	var out []ast.Decl
	var visit func(v reflect.Value)
	visit = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return
			}
			if v.Kind() == reflect.Pointer && v.CanInterface() {
				switch node := v.Interface().(type) {
				case *ast.FuncDecl:
					out = append(out, node)
					return
				case *ast.NamespaceDecl:
					out = append(out, node)
					for _, member := range node.Decls {
						visit(reflect.ValueOf(member))
					}
					return
				case ast.Decl:
					out = append(out, node)
				case *ast.Ident, *ast.IntLit, *ast.FloatLit, *ast.StringLit,
					*ast.CharLit, *ast.BoolLit, *ast.NullLit, *ast.ZeroedLit:
					return
				}
			}
			visit(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				switch f := v.Field(i); f.Kind() {
				case reflect.Slice, reflect.Interface, reflect.Pointer:
					visit(f)
				}
			}
		case reflect.Slice:
			if v.Type().Elem().Kind() == reflect.Uint8 {
				return
			}
			if v.Type() == reflect.TypeFor[[]ast.Decl]() {
				for i := 0; i < v.Len(); i++ {
					visit(v.Index(i).Elem())
				}
				return
			}
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		}
	}
	if file == nil {
		return nil
	}
	for _, decl := range file.Decls {
		visit(reflect.ValueOf(decl))
	}
	return out
}

var retainedPosType = reflect.TypeOf(lexer.Pos{})

func ShiftPos(p lexer.Pos, marks map[string][][2]int) lexer.Pos {
	ms := marks[p.File]
	if len(ms) == 0 || p.Line == 0 {
		return p
	}
	d := 0
	for _, m := range ms {
		if p.Line >= m[0] {
			d = m[1]
		}
	}
	if d != 0 {
		p.Line += d
		if p.EndLine != 0 {
			p.EndLine += d
		}
	}
	return p
}

// ShiftDecl moves every position inside an unchanged declaration by
// the line delta of the edit above it. Byte offsets are left as they were: they
// index the expanded buffer, are never printed, and stay consistent within a
// declaration.
func ShiftDecl(decl ast.Decl, marks map[string][][2]int) {
	retainedShiftDeclPositions(decl, marks, false)
}

// Nonfunction containers and their members are paired independently. Shift only
// this declaration's own positions so nested members are never shifted twice.
func ShiftDeclOwn(decl ast.Decl, marks map[string][][2]int) {
	retainedShiftDeclPositions(decl, marks, true)
}

func retainedShiftDeclPositions(decl ast.Decl, marks map[string][][2]int, ownOnly bool) {
	root := reflect.ValueOf(decl).Pointer()
	seen := map[uintptr]bool{}
	var visit func(v reflect.Value)
	visit = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if ownOnly && !v.IsNil() && v.Pointer() != root && v.Type().Implements(retainedDeclType) {
				return
			}
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			visit(v.Elem())
		case reflect.Interface:
			if !v.IsNil() && v.Elem().Kind() == reflect.Pointer {
				visit(v.Elem())
			}
		case reflect.Struct:
			if v.Type() == retainedPosType {
				if v.CanSet() {
					v.Set(reflect.ValueOf(ShiftPos(v.Interface().(lexer.Pos), marks)))
				}
				return
			}
			for i := 0; i < v.NumField(); i++ {
				visit(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		}
	}
	visit(reflect.ValueOf(decl))
}

// DeclSourceStart includes leading annotations in the declaration's
// comparison span. Pos() names the declaration keyword, which otherwise leaves
// a first declaration's annotations outside every span and assigns later ones
// to the preceding declaration.
func DeclSourceStart(d ast.Decl) int {
	start := d.Pos().Offset
	var annotations []ast.Annotation
	switch d := d.(type) {
	case *ast.StructDecl:
		annotations = d.Annotations
	case *ast.EnumDecl:
		annotations = d.Annotations
	case *ast.FuncDecl:
		annotations = d.Annotations
	case *ast.StoreDecl:
		annotations = d.Annotations
	case *ast.ExternFuncDecl:
		annotations = d.Annotations
	case *ast.ExternVarDecl:
		annotations = d.Annotations
	case *ast.ExternTypeDecl:
		annotations = d.Annotations
	case *ast.ExportFuncDecl:
		annotations = d.Annotations
	case *ast.ImplDecl:
		annotations = d.Annotations
	}
	for _, annotation := range annotations {
		if offset := annotation.Position.Offset; offset >= 0 && offset < start {
			start = offset
		}
	}
	return start
}

// Spans returns each declaration's source text: from its start to the
// next declaration's start in the expanded buffer (the last runs to the end).
// A container's span is therefore its own header, and comments and blank lines
// count as changes, so a declaration whose positions moved is re-analyzed.
func Spans(decls []ast.Decl, src []byte) []string {
	// A span runs to the next declaration start in the source, not in walk
	// order: synthesized declarations (machine expansions) are walked out of
	// source order, and a walk-order span would cover unrelated edits.
	starts := make([]int, 0, len(decls))
	for _, d := range decls {
		starts = append(starts, DeclSourceStart(d))
	}
	sort.Ints(starts)
	out := make([]string, len(decls))
	for i, d := range decls {
		start := DeclSourceStart(d)
		end := len(src)
		if k := sort.SearchInts(starts, start+1); k < len(starts) {
			end = starts[k]
		}
		if start < 0 || start > end || end > len(src) {
			out[i] = fmt.Sprintf("\x00unordered %d", i) // never equal: forces a fallback or splice
			continue
		}
		span := src[start:end]
		// The span stays in its own file region: text past a directive belongs to the
		// region the directive opens.
		if k := bytes.Index(span, []byte("\n#line ")); k >= 0 {
			span = span[:k+1]
		}
		// The first directive after a newline was already trimmed above, so
		// only a directive at the beginning of this span can remain. Avoid a
		// second scan of ordinary declaration text.
		if bytes.HasPrefix(span, []byte("#line ")) {
			out[i] = WithoutLineDirectives(span)
		} else {
			out[i] = string(span)
		}
	}
	return out
}

// WithoutLineDirectives drops include-splice `#line` directives from a
// span. They restate positions, which the update tracks per file from the
// declarations themselves; an edit above an include renumbers the directive
// that resumes the parent file without changing any declaration.
func WithoutLineDirectives(span []byte) string {
	if !bytes.Contains(span, []byte("#line ")) {
		return string(span)
	}
	var b strings.Builder
	b.Grow(len(span))
	for len(span) > 0 {
		line := span
		if k := bytes.IndexByte(span, '\n'); k >= 0 {
			line, span = span[:k+1], span[k+1:]
		} else {
			span = nil
		}
		if bytes.HasPrefix(line, []byte("#line ")) {
			continue
		}
		b.Write(line)
	}
	return b.String()
}

// SameDeclKind reports whether two declarations can be the same one
// across an edit: the same node type, and for functions the same name.
func SameDeclKind(a, b ast.Decl) bool {
	if reflect.TypeOf(a) != reflect.TypeOf(b) {
		return false
	}
	switch fa := a.(type) {
	case *ast.FuncDecl:
		return fa.Name == b.(*ast.FuncDecl).Name
	case *ast.ConstDecl:
		// By name, so an added const is not paired with the next one; the same for types.
		return fa.Name == b.(*ast.ConstDecl).Name
	case *ast.StructDecl:
		return fa.Name == b.(*ast.StructDecl).Name
	case *ast.ConstEnumDecl:
		return fa.Name == b.(*ast.ConstEnumDecl).Name
	case *ast.EnumDecl:
		return fa.Name == b.(*ast.EnumDecl).Name
	case *ast.GlobalDecl:
		return fa.Name == b.(*ast.GlobalDecl).Name
	case *ast.UsingDecl:
		fb := b.(*ast.UsingDecl)
		return fa.Name == fb.Name && fa.Member == fb.Member && fa.Alias == fb.Alias
	}
	return true
}
