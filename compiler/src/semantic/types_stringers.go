package semantic

import (
	"fmt"
	"sort"
	"strings"

	"elisacore/src/ast"
)

func (*InvalidType) isType()           {}
func (*NeverType) isType()             {}
func (*NullType) isType()              {}
func (*BuiltinType) isType()           {}
func (*IDType) isType()                {}
func (*TypeParamType) isType()         {}
func (*StructStateCaseType) isType()   {}
func (*StructStateSetType) isType()    {}
func (*RefStorageParamType) isType()   {}
func (*RefStorageValueType) isType()   {}
func (*RefStateParamType) isType()     {}
func (*RefStateValueType) isType()     {}
func (*ErrorSetType) isType()          {}
func (*ErrorUnionType) isType()        {}
func (*OptionalType) isType()          {}
func (*TupleType) isType()             {}
func (*ConstEnumType) isType()         {}
func (*RefType) isType()               {}
func (*ArrayType) isType()             {}
func (*DArrayType) isType()            {}
func (*ViewType) isType()              {}
func (*DArrayViewType) isType()        {}
func (*StoreRowsViewType) isType()     {}
func (*StoreRowViewType) isType()      {}
func (*DStrType) isType()              {}
func (*DictType) isType()              {}
func (*DictEntryType) isType()         {}
func (*SViewType) isType()             {}
func (*PackedEnumStoreType) isType()   {}
func (*TreeStoreType) isType()         {}
func (*PackedVariantViewType) isType() {}
func (*TreeVariantViewType) isType()   {}
func (*TreeNodeType) isType()          {}
func (*TreeType) isType()              {}
func (*TreeCategoryType) isType()      {}
func (*TreeBlockType) isType()         {}
func (*TreeStructType) isType()        {}
func (*EnumType) isType()              {}
func (*StructType) isType()            {}
func (*OpaqueType) isType()            {}
func (*GenericInstanceType) isType()   {}
func (*AggregateStateType) isType()    {}
func (*FuncType) isType()              {}

func (*ShapeParam) isShape()    {}
func (*NamedShape) isShape()    {}
func (*WildcardShape) isShape() {}
func (*FreshShape) isShape()    {}

func (*InvalidType) String() string { return "<invalid>" }
func (*NeverType) String() string   { return "<never>" }
func (*NullType) String() string    { return "null" }
func (t *BuiltinType) String() string {
	return t.Name
}
func (t *IDType) String() string {
	if t == nil || t.Tag == nil {
		return "id[<invalid>]"
	}
	return fmt.Sprintf("id[%s]", t.Tag.String())
}
func (t *TypeParamType) String() string { return t.Name }
func (t *StructStateCaseType) String() string {
	if t == nil {
		return "<invalid-struct-state>"
	}
	return t.Case
}
func (t *StructStateSetType) String() string {
	if t == nil {
		return "<invalid-struct-state-set>"
	}
	return strings.Join(t.Cases, " | ")
}
func (t *RefStorageParamType) String() string {
	return t.Name
}
func (t *RefStorageValueType) String() string {
	if t == nil {
		return "<invalid-refstorage>"
	}
	return RefStorageName(t.Storage)
}
func (t *RefStateParamType) String() string {
	return t.Name
}
func (t *RefStateValueType) String() string {
	if t == nil {
		return "<invalid-refstate>"
	}
	return ast.RefStateMarker(ast.RefState(t.State))
}
func (t *ErrorSetType) String() string { return t.Name }
func (t *ErrorUnionType) String() string {
	if t == nil || t.Value == nil || t.Errors == nil {
		return "<invalid-error-union>"
	}
	return fmt.Sprintf("%s | %s", t.Value.String(), t.Errors.String())
}
func (t *OptionalType) String() string {
	if t == nil || t.Value == nil {
		return "<invalid-optional>"
	}
	return t.Value.String() + "?"
}
func (t *TupleType) String() string {
	if t == nil {
		return "<invalid-tuple>"
	}
	parts := make([]string, 0, len(t.Fields))
	for _, field := range t.Fields {
		if field.Type == nil {
			if field.Name != "" {
				parts = append(parts, field.Name+": <invalid>")
			} else {
				parts = append(parts, "<invalid>")
			}
			continue
		}
		if field.Name != "" {
			parts = append(parts, field.Name+": "+field.Type.String())
			continue
		}
		parts = append(parts, field.Type.String())
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
func (t *ConstEnumType) String() string {
	if t == nil {
		return "<invalid-const-enum>"
	}
	return t.Name
}

func QualifyErrorTag(setName string, tagName string) string {
	if setName == "" {
		return tagName
	}
	if tagName == "" {
		return setName
	}
	return setName + "." + tagName
}

func SplitErrorTagName(name string) (string, string) {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) != 2 {
		return "", name
	}
	return parts[0], parts[1]
}

func ErrorTagShortName(name string) string {
	_, tag := SplitErrorTagName(name)
	return tag
}

func ErrorTagDiagnosticName(tag string) string {
	if tag == "" {
		return "<invalid-error-tag>"
	}
	return tag
}

func ErrorSetDiagnosticName(errSet *ErrorSetType) string {
	if errSet == nil {
		return "<invalid-error-set>"
	}
	return errSet.String()
}

func ErrorTypeDiagnosticName(t Type) string {
	if t == nil {
		return "<invalid>"
	}
	switch tt := t.(type) {
	case *ErrorSetType:
		return ErrorSetDiagnosticName(tt)
	default:
		return diagnosticTypeString(t)
	}
}

func ErrorSetTagsEqual(a *ErrorSetType, b *ErrorSetType) bool {
	if a == nil || b == nil {
		return a == b
	}
	if len(a.Tags) != len(b.Tags) {
		return false
	}
	for i := range a.Tags {
		if a.Tags[i] != b.Tags[i] {
			return false
		}
	}
	return true
}

func CanonicalizeErrorSetSelections(familySets map[string]*ErrorSetType, fullFamilies map[string]bool, selectedTags map[string]map[string]bool) *ErrorSetType {
	if len(familySets) == 0 {
		return &ErrorSetType{Name: "error[]"}
	}
	familyNames := make([]string, 0, len(familySets))
	for familyName := range familySets {
		familyNames = append(familyNames, familyName)
	}
	sort.Strings(familyNames)

	if len(familyNames) == 1 {
		familyName := familyNames[0]
		errSet := familySets[familyName]
		if fullFamilies[familyName] || errorSetSelectionIsFull(errSet, selectedTags[familyName]) {
			return errSet
		}
	}

	nameParts := make([]string, 0)
	canonicalTags := make([]string, 0)
	for _, familyName := range familyNames {
		errSet := familySets[familyName]
		if errSet == nil {
			continue
		}
		selected := selectedTags[familyName]
		if fullFamilies[familyName] || errorSetSelectionIsFull(errSet, selected) {
			nameParts = append(nameParts, familyName)
			canonicalTags = append(canonicalTags, errSet.Tags...)
			continue
		}
		for _, qualifiedTag := range errSet.Tags {
			if selected == nil || !selected[qualifiedTag] {
				continue
			}
			nameParts = append(nameParts, qualifiedTag)
			canonicalTags = append(canonicalTags, qualifiedTag)
		}
	}
	return &ErrorSetType{Name: "error[" + strings.Join(nameParts, ", ") + "]", Tags: canonicalTags}
}

func errorSetSelectionIsFull(errSet *ErrorSetType, selected map[string]bool) bool {
	if errSet == nil {
		return false
	}
	if len(errSet.Tags) == 0 {
		return true
	}
	if len(selected) < len(errSet.Tags) {
		return false
	}
	for _, tag := range errSet.Tags {
		if !selected[tag] {
			return false
		}
	}
	return true
}
func (s *ShapeParam) String() string    { return s.Name }
func (s *NamedShape) String() string    { return s.Name }
func (s *WildcardShape) String() string { return "_" }
func (s *FreshShape) String() string {
	if s.Label != "" {
		return fmt.Sprintf("%s#%d", s.Label, s.ID)
	}
	return fmt.Sprintf("shape#%d", s.ID)
}

func RefStorageName(storage RefStorage) string {
	switch storage {
	case RefStorageHeap:
		return "heap"
	case RefStorageStack:
		return "stack"
	case RefStorageStatic:
		return "static"
	default:
		return ""
	}
}

func (t *RefType) String() string {
	s := t.Elem.String()
	if t.Mutable {
		s = "mutable " + s
	}
	if t.StorageParam != "" {
		s = t.StorageParam + " " + s
	} else if t.Region != "" {
		s = t.Region + " " + s
	} else if t.Storage != RefStorageAny {
		s = RefStorageName(t.Storage) + " " + s
	}
	if t.StateParam != "" {
		return s + "&[" + t.StateParam + "]"
	}
	switch t.State {
	case RefStateNullable:
		s += "&?"
	case RefStateNull:
		s += "!"
	default:
		s += "&"
	}
	return s
}
func (t *ArrayType) String() string {
	if t.SurfaceName == "str" || t.SurfaceName == "string" {
		return fmt.Sprintf("str[%s]", t.Size)
	}
	if t.SurfaceName == "array" {
		return fmt.Sprintf("array[%s, %s]", t.Elem.String(), t.Size)
	}
	return fmt.Sprintf("%s[%s]", t.Elem.String(), t.Size)
}
func (t *DArrayType) String() string {
	if t == nil || t.Elem == nil {
		return "<invalid-darray>"
	}
	if isWildcardShape(t.Shape) {
		return fmt.Sprintf("darray[%s]", t.Elem.String())
	}
	return fmt.Sprintf("darray[%s, %s]", t.Elem.String(), t.Shape.String())
}
func (t *ViewType) String() string {
	if t.Begin != "" || t.End != "" {
		return fmt.Sprintf("view[%s, %s, %s]", t.Elem.String(), t.Begin, t.End)
	}
	return fmt.Sprintf("view[%s]", t.Elem.String())
}
func (t *DArrayViewType) String() string {
	return fmt.Sprintf("dview[%s]", t.Elem.String())
}
func (t *StoreRowsViewType) String() string {
	if t == nil || t.Store == nil {
		return "<invalid-store-rows>"
	}
	return t.Store.String() + ".rows()"
}
func (t *StoreRowViewType) String() string {
	if t == nil || t.Store == nil {
		return "<invalid-store-row>"
	}
	return t.Store.String() + ".row"
}
func (t *DStrType) String() string {
	if isWildcardShape(t.Shape) {
		return "cstr"
	}
	return fmt.Sprintf("cstr[%s]", t.Shape.String())
}
func (t *DictType) String() string {
	if t == nil || t.Key == nil || t.Value == nil {
		return "<invalid-dict>"
	}
	return fmt.Sprintf("dict[%s, %s]", t.Key.String(), t.Value.String())
}
func (t *DictEntryType) String() string {
	if t == nil || t.Dict == nil {
		return "<invalid-dict-entry>"
	}
	if t.Mutable {
		return fmt.Sprintf("dict.entry[mutable %s, %s]", t.Dict.Key.String(), t.Dict.Value.String())
	}
	return fmt.Sprintf("dict.entry[%s, %s]", t.Dict.Key.String(), t.Dict.Value.String())
}
func (t *SViewType) String() string {
	if t == nil {
		return "<invalid-sview>"
	}
	if t.Begin == "" && t.End == "" {
		return "sview"
	}
	return fmt.Sprintf("sview[%s, %s]", t.Begin, t.End)
}
func (t *PackedEnumStoreType) String() string {
	if t == nil {
		return "<invalid-packed-store>"
	}
	if t.State != nil {
		return fmt.Sprintf("%s[%s]", t.Name, t.State.String())
	}
	return t.Name
}
func (t *TreeStoreType) String() string {
	if t == nil {
		return "<invalid-tree-store>"
	}
	if t.State != nil {
		return fmt.Sprintf("%s[%s]", t.Name, t.State.String())
	}
	return t.Name
}
func (t *PackedVariantViewType) String() string {
	if t == nil || t.Enum == nil || t.Variant == nil {
		return "<invalid-packed-view>"
	}
	return fmt.Sprintf("packedview[%s.%s]", t.Enum.Name, t.Variant.Name)
}

func (t *TreeVariantViewType) SurfaceName() string {
	if t == nil || t.Category == nil || t.Variant == nil {
		return "<invalid-tree-view>"
	}
	return fmt.Sprintf("%s.%s", t.Category.Name, t.Variant.Name)
}

func (t *TreeVariantViewType) String() string {
	return t.SurfaceName()
}
func (t *TreeNodeType) String() string {
	if t == nil {
		return "<invalid-tree-node>"
	}
	return t.Name
}
func (t *TreeType) String() string {
	if t == nil {
		return "<invalid-tree>"
	}
	return t.Name
}
func (t *TreeCategoryType) String() string {
	if t == nil {
		return "<invalid-tree-category>"
	}
	return t.Name
}
func (t *TreeBlockType) String() string {
	if t == nil {
		return "<invalid-tree-block>"
	}
	return t.Name
}
func (t *TreeStructType) String() string {
	if t == nil {
		return "<invalid-tree-struct>"
	}
	return t.Name
}
