package semantic

import (
	"fmt"
	"sort"
	"strings"
)

func (*InvalidType) isType()            {}
func (*NeverType) isType()              {}
func (*NullType) isType()               {}
func (*BuiltinType) isType()            {}
func (*BitIntType) isType()             {}
func (*IDType) isType()                 {}
func (*AddressSpaceType) isType()       {}
func (*TypeParamType) isType()          {}
func (*ConstParamType) isType()         {}
func (*ConstValueType) isType()         {}
func (*StructStateCaseType) isType()    {}
func (*StructStateSetType) isType()     {}
func (*RefStorageValueType) isType()    {}
func (*RegionParamType) isType()        {}
func (*RegionValueType) isType()        {}
func (*ErrorSetType) isType()           {}
func (*ErrorUnionType) isType()         {}
func (*OptionalType) isType()           {}
func (*TupleType) isType()              {}
func (*BitGroupType) isType()           {}
func (*ConstEnumType) isType()          {}
func (*RefType) isType()                {}
func (*ArrayType) isType()              {}
func (*DArrayType) isType()             {}
func (*ViewType) isType()               {}
func (*StoreRowsViewType) isType()      {}
func (*StoreRowViewType) isType()       {}
func (*DStrType) isType()               {}
func (*DictType) isType()               {}
func (*SetType) isType()                {}
func (*DictEntryType) isType()          {}
func (*SViewType) isType()              {}
func (*PackedEnumStoreType) isType()    {}
func (*TreeStoreType) isType()          {}
func (*FrozenTreeRowsViewType) isType() {}
func (*PackedVariantViewType) isType()  {}
func (*TreeVariantViewType) isType()    {}
func (*TreeNodeType) isType()           {}
func (*TreeType) isType()               {}
func (*TreeCategoryType) isType()       {}
func (*TreeBlockType) isType()          {}
func (*TreeStructType) isType()         {}
func (*EnumType) isType()               {}
func (*StructType) isType()             {}
func (*OpaqueType) isType()             {}
func (*GenericInstanceType) isType()    {}
func (*AggregateStateType) isType()     {}
func (*FuncType) isType()               {}

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
func (t *BitIntType) String() string {
	if t == nil {
		return "<invalid-bitint>"
	}
	return BitIntName(t.Signed, t.Width)
}
func (t *IDType) String() string {
	if t == nil || t.Tag == nil {
		return "id[<invalid>]"
	}
	return fmt.Sprintf("id[%s]", t.Tag.String())
}
func (t *AddressSpaceType) String() string {
	if t == nil || t.Elem == nil {
		return "Address[<invalid>]"
	}
	switch t.Space {
	case "guest":
		return fmt.Sprintf("GuestVAddr[%s]", t.Elem.String())
	case "host":
		return fmt.Sprintf("HostPtr[%s]", t.Elem.String())
	case "native_mapped_guest":
		return fmt.Sprintf("NativeMappedGuestPtr[%s]", t.Elem.String())
	default:
		return fmt.Sprintf("Address[%s,%s]", t.Space, t.Elem.String())
	}
}
func (t *RegionParamType) String() string {
	if t == nil {
		return "<invalid-region-param>"
	}
	return t.Name
}
func (t *RegionValueType) String() string {
	if t == nil {
		return "<invalid-region>"
	}
	return t.Name
}
func (t *TypeParamType) String() string { return t.Name }
func (t *ConstParamType) String() string {
	return t.Name
}
func (t *ConstValueType) String() string {
	if t == nil {
		return "<invalid-const>"
	}
	switch t.Value.Kind {
	case ConstInt:
		return fmt.Sprintf("%d", t.Value.Int)
	case ConstBool:
		if t.Value.Bool {
			return "true"
		}
		return "false"
	case ConstString:
		return fmt.Sprintf("%q", t.Value.String)
	case ConstTuple:
		return "<const tuple>"
	case ConstList:
		return "<const list>"
	case ConstRecord:
		return "<const record>"
	case ConstOptional:
		if !t.Value.Some {
			return "<const none>"
		}
		if t.Value.Value == nil {
			return "<const optional>"
		}
		return "<const some>"
	default:
		return "<const>"
	}
}
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
func (t *RefStorageValueType) String() string {
	if t == nil {
		return "<invalid-refstorage>"
	}
	return RefStorageName(t.Storage)
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
func (t *BitGroupType) String() string {
	if t == nil {
		return "<invalid-bitgroup>"
	}
	if t.Name != "" {
		return t.Name
	}
	if t.Kind == BitGroupBitset {
		return "bitset"
	}
	return "bitfield"
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
	if len(a.Params) != len(b.Params) {
		return false
	}
	for i := range a.Params {
		if a.Params[i] != b.Params[i] {
			return false
		}
	}
	if len(a.Tags) != len(b.Tags) {
		return false
	}
	for i := range a.Tags {
		if a.Tags[i] != b.Tags[i] {
			return false
		}
		aPayload := a.PayloadForTag(a.Tags[i])
		bPayload := b.PayloadForTag(b.Tags[i])
		if len(aPayload) != len(bPayload) {
			return false
		}
		for j := range aPayload {
			if !SameType(aPayload[j], bPayload[j]) {
				return false
			}
		}
	}
	return true
}

// errorSetNameWithParams composes a diagnostic name for a set carrying
// unresolved error-set params: the concrete component's name parts followed by
// the param names. A param-only singleton keeps its bare name (`R`).
func errorSetNameWithParams(concreteName string, tagCount int, params []string) string {
	if tagCount == 0 && len(params) == 1 {
		return params[0]
	}
	inner := ""
	if tagCount > 0 {
		inner = concreteName
		if strings.HasPrefix(inner, "error[") && strings.HasSuffix(inner, "]") {
			inner = inner[len("error[") : len(inner)-1]
		}
	}
	parts := make([]string, 0, 1+len(params))
	if inner != "" {
		parts = append(parts, inner)
	}
	parts = append(parts, params...)
	return "error[" + strings.Join(parts, ", ") + "]"
}

// UnionErrorSets merges two error sets: concrete tags (deduped, payloads
// carried over) and unresolved params (deduped). Used when substituting a
// bound error-set param into a mixed set like `error[R, Timeout]`.
func UnionErrorSets(a *ErrorSetType, b *ErrorSetType) *ErrorSetType {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	out := &ErrorSetType{
		Tags:   append([]string(nil), a.Tags...),
		Params: append([]string(nil), a.Params...),
	}
	seenTags := make(map[string]bool, len(a.Tags)+len(b.Tags))
	for _, tag := range a.Tags {
		seenTags[tag] = true
	}
	for _, tag := range b.Tags {
		if !seenTags[tag] {
			seenTags[tag] = true
			out.Tags = append(out.Tags, tag)
		}
	}
	for _, set := range []*ErrorSetType{a, b} {
		for _, tag := range set.Tags {
			if payload := set.Payloads[tag]; len(payload) != 0 {
				if out.Payloads == nil {
					out.Payloads = map[string][]Type{}
				}
				out.Payloads[tag] = payload
			}
		}
	}
	seenParams := make(map[string]bool, len(a.Params))
	for _, param := range a.Params {
		seenParams[param] = true
	}
	for _, param := range b.Params {
		if !seenParams[param] {
			seenParams[param] = true
			out.Params = append(out.Params, param)
		}
	}
	concreteName := a.Name
	if len(a.Tags) == 0 {
		concreteName = b.Name
	} else if len(b.Tags) != 0 && a.Name != b.Name {
		concreteName = "error[" + strings.Join(out.Tags, ", ") + "]"
	}
	out.Name = errorSetNameWithParams(concreteName, len(out.Tags), out.Params)
	return out
}

// SubtractErrorTags returns src without the tags present in the concrete
// component of pattern. Used to bind `R` from `error[R, Timeout]` matched
// against a concrete set: R gets everything the pattern's own tags don't claim.
func SubtractErrorTags(src *ErrorSetType, pattern *ErrorSetType) *ErrorSetType {
	if src == nil || pattern == nil || len(pattern.Tags) == 0 {
		return src
	}
	out := &ErrorSetType{Params: append([]string(nil), src.Params...)}
	for _, tag := range src.Tags {
		if _, claimed := MatchErrorTag(pattern, tag); claimed {
			continue
		}
		out.Tags = append(out.Tags, tag)
		if payload := src.Payloads[tag]; len(payload) != 0 {
			if out.Payloads == nil {
				out.Payloads = map[string][]Type{}
			}
			out.Payloads[tag] = payload
		}
	}
	out.Name = errorSetNameWithParams("error["+strings.Join(out.Tags, ", ")+"]", len(out.Tags), out.Params)
	return out
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
	canonicalPayloads := map[string][]Type{}
	for _, familyName := range familyNames {
		errSet := familySets[familyName]
		if errSet == nil {
			continue
		}
		selected := selectedTags[familyName]
		if fullFamilies[familyName] || errorSetSelectionIsFull(errSet, selected) {
			nameParts = append(nameParts, familyName)
			canonicalTags = append(canonicalTags, errSet.Tags...)
			for _, qualifiedTag := range errSet.Tags {
				if payload := errSet.Payloads[qualifiedTag]; len(payload) != 0 {
					canonicalPayloads[qualifiedTag] = payload
				}
			}
			continue
		}
		for _, qualifiedTag := range errSet.Tags {
			if selected == nil || !selected[qualifiedTag] {
				continue
			}
			nameParts = append(nameParts, qualifiedTag)
			canonicalTags = append(canonicalTags, qualifiedTag)
			if payload := errSet.Payloads[qualifiedTag]; len(payload) != 0 {
				canonicalPayloads[qualifiedTag] = payload
			}
		}
	}
	return &ErrorSetType{Name: "error[" + strings.Join(nameParts, ", ") + "]", Tags: canonicalTags, Payloads: canonicalPayloads}
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
	// Storage class stays a prefix (heap/static/stack); region provenance renders as
	// the canonical `@r` suffix (docs/68 §5), matching the surface notation.
	if t.Storage != RefStorageAny {
		s = RefStorageName(t.Storage) + " " + s
	}
	switch t.State {
	case RefStateNullable:
		s += "&?"
	case RefStateNull:
		s += "!"
	default:
		s += "&"
	}
	if t.Region != "" {
		s += " @" + t.Region
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
	if t.SurfaceName == "dstr" && isWildcardShape(t.Shape) {
		return "dstr"
	}
	if isWildcardShape(t.Shape) {
		return fmt.Sprintf("darray[%s]", t.Elem.String())
	}
	return fmt.Sprintf("darray[%s, %s]", t.Elem.String(), t.Shape.String())
}
func (t *ViewType) String() string {
	name := t.SurfaceName
	if name == "" {
		name = "view"
	}
	if t.Begin != "" || t.End != "" {
		return fmt.Sprintf("%s[%s, %s, %s]", name, t.Elem.String(), t.Begin, t.End)
	}
	return fmt.Sprintf("%s[%s]", name, t.Elem.String())
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
func (t *SetType) String() string {
	if t == nil || t.Elem == nil {
		return "<invalid-set>"
	}
	return fmt.Sprintf("set[%s]", t.Elem.String())
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
func (t *FrozenTreeRowsViewType) String() string {
	if t == nil || t.Category == nil {
		return "<invalid-frozen-tree-rows>"
	}
	return fmt.Sprintf("%s.rows", t.Category.Name)
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
