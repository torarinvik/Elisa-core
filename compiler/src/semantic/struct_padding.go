package semantic

import (
	"fmt"
	"sort"
	"unsafe"

	"elisacore/src/ast"
)

type structPaddingWarning struct {
	CurrentSize    int
	BestSize       int
	AvoidableBytes int
	SuggestedOrder []string
}

type hostABILayout struct {
	Size  int
	Align int
}

type structLayoutField struct {
	Name  string
	Size  int
	Align int
	Index int
}

func (a *Analyzer) warnOnAvoidableStructPadding(decls []scopedDecl) {
	for _, scoped := range decls {
		stDecl, ok := scoped.Decl.(*ast.StructDecl)
		if !ok {
			continue
		}
		qualifiedName := joinQualifiedName(scoped.Namespace, stDecl.Name)
		st, _ := a.namedTypes[qualifiedName].(*StructType)
		warning, ok := a.analyzeAvoidableStructPadding(st)
		if !ok {
			continue
		}
		a.warnf(stDecl.Pos(), "struct %q has %d bytes of avoidable padding in declared field order (current estimated size %d bytes, reordered estimate %d bytes); consider ordering fields as %s", qualifiedName, warning.AvoidableBytes, warning.CurrentSize, warning.BestSize, quotedFieldOrder(warning.SuggestedOrder))
	}
}

func (a *Analyzer) analyzeAvoidableStructPadding(st *StructType) (structPaddingWarning, bool) {
	if st == nil || st.Decl == nil || st.Builtin {
		return structPaddingWarning{}, false
	}
	if len(genericParamsForStructType(st)) != 0 || len(st.Decl.Fields) < 2 {
		return structPaddingWarning{}, false
	}
	fields := make([]structLayoutField, 0, len(st.Decl.Fields))
	for i, fieldDecl := range st.Decl.Fields {
		field, ok := st.Fields[fieldDecl.Name]
		if !ok {
			return structPaddingWarning{}, false
		}
		layout, ok := a.hostABILayoutForType(field.Type, map[string]bool{})
		if !ok || layout.Align <= 0 {
			return structPaddingWarning{}, false
		}
		fields = append(fields, structLayoutField{Name: fieldDecl.Name, Size: layout.Size, Align: layout.Align, Index: i})
	}
	requestedAlign, _ := RequestedAlignment(st)
	currentSize, _ := hostABIStructSizeForFieldOrder(fields, requestedAlign)
	reordered := append([]structLayoutField(nil), fields...)
	sort.SliceStable(reordered, func(i, j int) bool {
		if reordered[i].Align != reordered[j].Align {
			return reordered[i].Align > reordered[j].Align
		}
		if reordered[i].Size != reordered[j].Size {
			return reordered[i].Size > reordered[j].Size
		}
		return reordered[i].Index < reordered[j].Index
	})
	bestSize, _ := hostABIStructSizeForFieldOrder(reordered, requestedAlign)
	if bestSize >= currentSize || sameFieldOrder(fields, reordered) {
		return structPaddingWarning{}, false
	}
	order := make([]string, 0, len(reordered))
	for _, field := range reordered {
		order = append(order, field.Name)
	}
	return structPaddingWarning{CurrentSize: currentSize, BestSize: bestSize, AvoidableBytes: currentSize - bestSize, SuggestedOrder: order}, true
}

func (a *Analyzer) hostABILayoutForType(t Type, seen map[string]bool) (hostABILayout, bool) {
	t = StripAggregateStateType(t)
	switch tt := t.(type) {
	case nil, *InvalidType, *NeverType, *NullType:
		return hostABILayout{}, false
	case *BuiltinType:
		return hostABIBuiltinLayout(tt.Name)
	case *ConstEnumType:
		if tt == nil {
			return hostABILayout{}, false
		}
		return a.hostABILayoutForType(tt.Storage, seen)
	case *RefType:
		return hostPointerLayout(), true
	case *FuncType:
		return hostPointerLayout(), true
	case *OpaqueType:
		return hostPointerLayout(), true
	case *ArrayType:
		if tt == nil || !tt.HasConstSize || tt.ConstSize < 0 {
			return hostABILayout{}, false
		}
		elem, ok := a.hostABILayoutForType(tt.Elem, seen)
		if !ok {
			return hostABILayout{}, false
		}
		return hostABILayout{Size: elem.Size * int(tt.ConstSize), Align: elem.Align}, true
	case *DStrType:
		return hostPointerLayout(), true
	case *SViewType:
		st, ok := a.namedTypes["StringView"].(*StructType)
		if !ok || st == nil {
			return hostABILayout{}, false
		}
		return a.hostABILayoutForStructType(st, nil, seen)
	case *ViewType, *DArrayViewType:
		st, ok := a.namedTypes["DynArrayView"].(*StructType)
		if !ok || st == nil {
			return hostABILayout{}, false
		}
		return a.hostABILayoutForStructType(st, nil, seen)
	case *DArrayType:
		base, ok := a.namedTypes["DynArray"].(*StructType)
		if !ok || base == nil {
			return hostABILayout{}, false
		}
		instance := &GenericInstanceType{Name: "DynArray", Base: base, Args: []Type{tt.Elem}}
		return a.hostABILayoutForType(instance, seen)
	case *DictType:
		base, ok := a.namedTypes["DynDict"].(*StructType)
		if !ok || base == nil {
			return hostABILayout{}, false
		}
		instance := &GenericInstanceType{Name: "DynDict", Base: base, Args: []Type{tt.Key, tt.Value}}
		return a.hostABILayoutForType(instance, seen)
	case *PackedEnumStoreType, *PackedVariantViewType, *EnumType, *OptionalType, *ErrorUnionType:
		return hostABILayout{}, false
	case *StructType:
		return a.hostABILayoutForStructType(tt, nil, seen)
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		if !ok || base == nil {
			return hostABILayout{}, false
		}
		bindings := genericBindingsForStructInstance(base, tt.Args)
		return a.hostABILayoutForStructType(base, bindings, seen)
	default:
		return hostABILayout{}, false
	}
}

func (a *Analyzer) hostABILayoutForStructType(st *StructType, bindings map[string]Type, seen map[string]bool) (hostABILayout, bool) {
	if st == nil || st.Decl == nil {
		return hostABILayout{}, false
	}
	if st.HasPackedGroups || st.PackedLayout {
		return hostABILayout{}, false
	}
	key := st.Name
	if key == "" {
		key = fmt.Sprintf("%p", st)
	}
	if seen[key] {
		return hostABILayout{}, false
	}
	seen[key] = true
	defer delete(seen, key)
	fields := make([]structLayoutField, 0, len(st.Decl.Fields))
	for i, fieldDecl := range st.Decl.Fields {
		field, ok := st.Fields[fieldDecl.Name]
		if !ok {
			return hostABILayout{}, false
		}
		fieldType := field.Type
		if len(bindings) != 0 {
			fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
		}
		layout, ok := a.hostABILayoutForType(fieldType, seen)
		if !ok || layout.Align <= 0 {
			return hostABILayout{}, false
		}
		fields = append(fields, structLayoutField{Name: fieldDecl.Name, Size: layout.Size, Align: layout.Align, Index: i})
	}
	requestedAlign, _ := RequestedAlignment(st)
	size, align := hostABIStructSizeForFieldOrder(fields, requestedAlign)
	return hostABILayout{Size: size, Align: align}, true
}

func hostABIBuiltinLayout(name string) (hostABILayout, bool) {
	word := int(unsafe.Sizeof(uintptr(0)))
	switch name {
	case "bool":
		return hostABILayout{Size: 1, Align: 1}, true
	case "i8", "u8":
		return hostABILayout{Size: 1, Align: 1}, true
	case "i16", "u16":
		return hostABILayout{Size: 2, Align: 2}, true
	case "i32", "u32", "f32":
		return hostABILayout{Size: 4, Align: 4}, true
	case "char", "i64", "u64", "f64":
		return hostABILayout{Size: 8, Align: 8}, true
	case "int", "isize", "usize", "uintptr":
		return hostABILayout{Size: word, Align: word}, true
	default:
		return hostABILayout{}, false
	}
}

func hostPointerLayout() hostABILayout {
	word := int(unsafe.Sizeof(uintptr(0)))
	return hostABILayout{Size: word, Align: word}
}

func hostABIStructSizeForFieldOrder(fields []structLayoutField, requestedAlign int) (int, int) {
	offset := 0
	maxAlign := 1
	if requestedAlign > maxAlign {
		maxAlign = requestedAlign
	}
	for _, field := range fields {
		if field.Align > maxAlign {
			maxAlign = field.Align
		}
		offset = alignUp(offset, field.Align)
		offset += field.Size
	}
	return alignUp(offset, maxAlign), maxAlign
}

func alignUp(value int, align int) int {
	if align <= 1 {
		return value
	}
	rem := value % align
	if rem == 0 {
		return value
	}
	return value + (align - rem)
}

func sameFieldOrder(a []structLayoutField, b []structLayoutField) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			return false
		}
	}
	return true
}

func quotedFieldOrder(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(fields))
	for _, field := range fields {
		quoted = append(quoted, fmt.Sprintf("%q", field))
	}
	return joinCommaSpace(quoted)
}

func joinCommaSpace(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += ", " + parts[i]
	}
	return out
}
