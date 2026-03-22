package semantic

import (
	"fmt"
	"sort"
	"strings"

	"llcontext/src/ast"
)

type Type interface {
	String() string
	isType()
}

type PermissionSet struct {
	Name      string
	Members   []string
	MemberSet map[string]bool
	Decl      *ast.PermissionDecl
	Builtin   bool
}

type Shape interface {
	String() string
	isShape()
}

type InvalidType struct{}

type NeverType struct{}

type NullType struct{}

type BuiltinType struct {
	Name string
}

type TypeParamType struct {
	Name string
}

type RefStorageParamType struct {
	Name string
}

type RefStorageValueType struct {
	Storage RefStorage
}

type RefStateParamType struct {
	Name string
}

type RefStateValueType struct {
	State RefState
}

type ErrorSetType struct {
	Name string
	Tags []string
}

type ErrorUnionType struct {
	Value  Type
	Errors *ErrorSetType
}

type OptionalType struct {
	Value Type
}

type ConstEnumMember struct {
	Name  string
	Value int64
	Decl  *ast.ConstEnumMemberDecl
}

type ShapeParam struct {
	Name string
}

type NamedShape struct {
	Name string
}

type WildcardShape struct{}

type FreshShape struct {
	ID     int
	Label  string
	Origin string
}

type RefState int

const (
	RefStateNonNull RefState = iota
	RefStateNullable
	RefStateNull
)

type RefStorage int

const (
	RefStorageAny RefStorage = iota
	RefStorageHeap
	RefStorageStack
	RefStorageStatic
)

type RefType struct {
	Elem            Type
	State           RefState
	StateParam      string
	Storage         RefStorage
	StorageParam    string
	Region          string
	ExplicitStorage bool
}

type ArrayType struct {
	Elem         Type
	Size         string
	HasConstSize bool
	ConstSize    int64
	SurfaceName  string
}

type DArrayType struct {
	Elem        Type
	Shape       Shape
	SurfaceName string
}

type ViewType struct {
	Elem  Type
	Begin string
	End   string
}

type DArrayViewType struct {
	Elem        Type
	Begin       string
	End         string
	SurfaceName string
}

type DStrType struct {
	Shape       Shape
	SurfaceName string
}

type DictType struct {
	Key         Type
	Value       Type
	SurfaceName string
}

type SViewType struct {
	Begin string
	End   string
}

type EnumVariant struct {
	Name         string
	Tag          uint32
	Payload      []Type
	PayloadNames []string
	TailIndex    int
	Decl         *ast.EnumVariantDecl
}

type PackedEnumStoreType struct {
	Name  string
	Enum  *EnumType
	State Type
}

type EnumType struct {
	Name       string
	Packed     bool
	Common     map[string]Field
	StoreType  *PackedEnumStoreType
	Variants   []*EnumVariant
	VariantMap map[string]*EnumVariant
	Decl       *ast.EnumDecl
}

type ConstEnumType struct {
	Name      string
	Storage   Type
	Members   []*ConstEnumMember
	MemberMap map[string]*ConstEnumMember
	Decl      *ast.ConstEnumDecl
}

type Field struct {
	Name    string
	Type    Type
	Mutable bool
	IsTail  bool
}

type StructType struct {
	Name             string
	TypeParams       []string
	RefStorageParams []string
	RefStateParams   []string
	GenericParams    []ast.GenericParam
	Fields           map[string]Field
	Affine           bool
	ReprC            bool
	Decl             *ast.StructDecl
	Builtin          bool
}

type OpaqueType struct {
	Name string
}

type GenericInstanceType struct {
	Name string
	Base Type
	Args []Type
}

type AggregateStateType struct {
	Base   Type
	State  RefState
	States []RefState
}

type FuncType struct {
	Name                         string
	TypeParams                   []string
	RefStorageParams             []string
	RefStateParams               []string
	RegionParams                 []string
	PermissionParams             []string
	GenericParams                []ast.GenericParam
	UsedPermissionParams         []string
	DeclaredPermissionRefs       []ast.PermissionRef
	DeclaredPermissions          []string
	PermissionRefs               []ast.PermissionRef
	Permissions                  []string
	ShapeParams                  []string
	FreshReturnShapeParams       []string
	Params                       []Type
	Return                       Type
	Variadic                     bool
	ReturnProvenance             regionRefState
	ReturnProvenanceKnown        bool
	ReturnBorrowedOwnerRefs      borrowedOwnerRefSummary
	ReturnBorrowedOwnerRefsKnown bool
}

func (*InvalidType) isType()         {}
func (*NeverType) isType()           {}
func (*NullType) isType()            {}
func (*BuiltinType) isType()         {}
func (*TypeParamType) isType()       {}
func (*RefStorageParamType) isType() {}
func (*RefStorageValueType) isType() {}
func (*RefStateParamType) isType()   {}
func (*RefStateValueType) isType()   {}
func (*ErrorSetType) isType()        {}
func (*ErrorUnionType) isType()      {}
func (*OptionalType) isType()        {}
func (*ConstEnumType) isType()       {}
func (*RefType) isType()             {}
func (*ArrayType) isType()           {}
func (*DArrayType) isType()          {}
func (*ViewType) isType()            {}
func (*DArrayViewType) isType()      {}
func (*DStrType) isType()            {}
func (*DictType) isType()            {}
func (*SViewType) isType()           {}
func (*PackedEnumStoreType) isType() {}
func (*EnumType) isType()            {}
func (*StructType) isType()          {}
func (*OpaqueType) isType()          {}
func (*GenericInstanceType) isType() {}
func (*AggregateStateType) isType()  {}
func (*FuncType) isType()            {}

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
func (t *TypeParamType) String() string { return t.Name }
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
		return t.String()
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
		return "any"
	}
}

func (t *RefType) String() string {
	s := t.Elem.String()
	if t.StorageParam != "" {
		s = t.StorageParam + " " + s
	} else if t.Region != "" {
		s = t.Region + " " + s
	} else if t.ExplicitStorage || t.Storage != RefStorageAny {
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
func (t *DStrType) String() string {
	if isWildcardShape(t.Shape) {
		return "dstr"
	}
	return fmt.Sprintf("dstr[%s]", t.Shape.String())
}
func (t *DictType) String() string {
	if t == nil || t.Key == nil || t.Value == nil {
		return "<invalid-dict>"
	}
	return fmt.Sprintf("dict[%s, %s]", t.Key.String(), t.Value.String())
}
func (t *SViewType) String() string {
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
func (t *ConstEnumType) Member(name string) (*ConstEnumMember, bool) {
	if t == nil || t.MemberMap == nil {
		return nil, false
	}
	member, ok := t.MemberMap[name]
	return member, ok
}
func (t *EnumType) String() string   { return t.Name }
func (t *StructType) String() string { return t.Name }
func (t *OpaqueType) String() string { return t.Name }
func (t *GenericInstanceType) String() string {
	parts := make([]string, 0, len(t.Args))
	for _, arg := range t.Args {
		parts = append(parts, arg.String())
	}
	return fmt.Sprintf("%s[%s]", t.Name, strings.Join(parts, ", "))
}
func (t *AggregateStateType) String() string {
	if t == nil || t.Base == nil {
		return "<invalid-aggregate-state>"
	}
	states := aggregateStateStates(t)
	parts := make([]string, 0, len(states))
	for _, state := range states {
		parts = append(parts, ast.RefStateMarker(ast.RefState(state)))
	}
	return fmt.Sprintf("%s[%s]", t.Base.String(), strings.Join(parts, ", "))
}
func (t *FuncType) String() string {
	parts := make([]string, 0, len(t.Params))
	for _, p := range t.Params {
		parts = append(parts, p.String())
	}
	generics := make([]string, 0, len(t.TypeParams)+len(t.RegionParams)+len(t.PermissionParams))
	for _, param := range t.TypeParams {
		generics = append(generics, param)
	}
	for _, param := range t.RefStorageParams {
		generics = append(generics, "refstorage "+param)
	}
	for _, param := range t.RefStateParams {
		generics = append(generics, "refstate "+param)
	}
	for _, param := range t.RegionParams {
		generics = append(generics, "region "+param)
	}
	for _, param := range t.PermissionParams {
		generics = append(generics, "permission "+param)
	}
	prefix := ""
	if len(generics) > 0 {
		prefix = "[" + strings.Join(generics, ", ") + "]"
	}
	if t.Variadic {
		parts = append(parts, "...")
	}
	if t.Return == nil {
		return fmt.Sprintf("func%s(%s)%s", prefix, strings.Join(parts, ", "), permissionFamiliesString(t.Permissions))
	}
	return fmt.Sprintf("func%s(%s) -> %s%s", prefix, strings.Join(parts, ", "), t.Return.String(), permissionFamiliesString(t.Permissions))
}

func permissionFamiliesString(families []string) string {
	if len(families) == 0 {
		return ""
	}
	return " can[" + strings.Join(families, ", ") + "]"
}

func BasePackedEnumStoreType(t Type) (*PackedEnumStoreType, bool) {
	storeType, ok := t.(*PackedEnumStoreType)
	if !ok || storeType == nil {
		return nil, false
	}
	if storeType.Enum != nil && storeType.Enum.StoreType != nil {
		return storeType.Enum.StoreType, true
	}
	base := *storeType
	base.State = nil
	return &base, true
}

func PackedEnumStoreWithState(storeType *PackedEnumStoreType, state Type) *PackedEnumStoreType {
	if base, ok := BasePackedEnumStoreType(storeType); ok && base != nil {
		next := *base
		next.State = state
		return &next
	}
	if storeType == nil {
		return nil
	}
	next := *storeType
	next.State = state
	return &next
}

func PackedEnumStoreState(t Type) Type {
	storeType, ok := t.(*PackedEnumStoreType)
	if !ok || storeType == nil {
		return nil
	}
	return storeType.State
}

func PackedEnumStoreStateName(t Type) string {
	state := PackedEnumStoreState(t)
	if state == nil {
		return ""
	}
	if builtin, ok := state.(*BuiltinType); ok {
		return builtin.Name
	}
	return state.String()
}

func IsLocalPackedEnumStoreType(t Type) bool {
	return PackedEnumStoreStateName(t) == "Local"
}

func IsFrozenPackedEnumStoreType(t Type) bool {
	return PackedEnumStoreStateName(t) == "Frozen"
}

func PermissionRefString(ref ast.PermissionRef) string {
	if ref.Member != "" {
		return ref.Name + "." + ref.Member
	}
	return ref.Name
}

func PermissionRefsString(refs []ast.PermissionRef) string {
	if len(refs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		parts = append(parts, PermissionRefString(ref))
	}
	return " can[" + strings.Join(parts, ", ") + "]"
}

var (
	invalidType = &InvalidType{}
	neverType   = &NeverType{}
	nullType    = &NullType{}
)

func IsInvalidType(t Type) bool {
	_, ok := t.(*InvalidType)
	return ok
}

func IsNeverType(t Type) bool {
	_, ok := t.(*NeverType)
	return ok
}

func IsNullType(t Type) bool {
	_, ok := t.(*NullType)
	return ok
}

func IsOptionalType(t Type) (*OptionalType, bool) {
	opt, ok := t.(*OptionalType)
	return opt, ok
}

func (t *ErrorSetType) HasTag(name string) bool {
	if t == nil {
		return false
	}
	for _, tag := range t.Tags {
		if tag == name {
			return true
		}
	}
	return false
}

func (t *ErrorSetType) HasQualifiedTag(setName string, tagName string) bool {
	return t.HasTag(QualifyErrorTag(setName, tagName))
}

func (t *ErrorSetType) TagCode(name string) (uint32, bool) {
	if t == nil {
		return 0, false
	}
	for i, tag := range t.Tags {
		if tag == name {
			return uint32(i + 1), true
		}
	}
	return 0, false
}

func (t *ErrorSetType) TagCodeFor(setName string, tagName string) (uint32, bool) {
	return t.TagCode(QualifyErrorTag(setName, tagName))
}

func MatchErrorTag(dst *ErrorSetType, srcTag string) (string, bool) {
	if dst == nil {
		return "", false
	}
	if dst.HasTag(srcTag) {
		return srcTag, true
	}
	shortName := ErrorTagShortName(srcTag)
	match := ""
	for _, candidate := range dst.Tags {
		if ErrorTagShortName(candidate) != shortName {
			continue
		}
		if match != "" {
			return "", false
		}
		match = candidate
	}
	if match == "" {
		return "", false
	}
	return match, true
}

func ErrorSetAssignable(dst, src *ErrorSetType) bool {
	if dst == nil || src == nil {
		return dst == src
	}
	for _, tag := range src.Tags {
		if _, ok := MatchErrorTag(dst, tag); !ok {
			return false
		}
	}
	return true
}

func IsBoolType(t Type) bool {
	b, ok := t.(*BuiltinType)
	return ok && b.Name == "bool"
}

func IsConstEnumType(t Type) (*ConstEnumType, bool) {
	ce, ok := t.(*ConstEnumType)
	return ce, ok
}

func ConstEnumStorageType(t Type) (Type, bool) {
	ce, ok := t.(*ConstEnumType)
	if !ok || ce == nil || ce.Storage == nil {
		return nil, false
	}
	return ce.Storage, true
}

func IsNumericType(t Type) bool {
	b, ok := t.(*BuiltinType)
	if !ok {
		return false
	}
	switch b.Name {
	case "char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr", "f32", "f64":
		return true
	default:
		return false
	}
}

func IsFloatType(t Type) bool {
	b, ok := t.(*BuiltinType)
	if !ok {
		return false
	}
	switch b.Name {
	case "f32", "f64":
		return true
	default:
		return false
	}
}

func IsIntegralType(t Type) bool {
	b, ok := t.(*BuiltinType)
	if !ok {
		return false
	}
	switch b.Name {
	case "char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr":
		return true
	default:
		return false
	}
}

func IsIntegralStorageType(t Type) bool {
	return IsIntegralType(t)
}

func IsTypeParamType(t Type) (*TypeParamType, bool) {
	tp, ok := t.(*TypeParamType)
	return tp, ok
}

func IsRefType(t Type) (*RefType, bool) {
	r, ok := t.(*RefType)
	return r, ok
}

func aggregateStateStates(t *AggregateStateType) []RefState {
	if t == nil {
		return nil
	}
	if len(t.States) != 0 {
		states := make([]RefState, len(t.States))
		copy(states, t.States)
		return states
	}
	return []RefState{t.State}
}

func cloneAggregateStateWithBase(base Type, states []RefState) *AggregateStateType {
	cloned := &AggregateStateType{Base: base}
	if len(states) > 0 {
		cloned.State = states[0]
		cloned.States = append([]RefState(nil), states...)
	}
	return cloned
}

func StripAggregateStateType(t Type) Type {
	agg, ok := t.(*AggregateStateType)
	if !ok || agg == nil {
		return t
	}
	return agg.Base
}

func AggregateStateParamCount(t Type) int {
	switch tt := StripAggregateStateType(t).(type) {
	case *StructType:
		if tt == nil || tt.Decl == nil {
			return 0
		}
		if tt.Decl.StateParamCount > 0 {
			return tt.Decl.StateParamCount
		}
		if tt.Decl.HasStateParam {
			return 1
		}
		return 0
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		if !ok || base == nil || base.Decl == nil {
			return 0
		}
		if base.Decl.StateParamCount > 0 {
			return base.Decl.StateParamCount
		}
		if base.Decl.HasStateParam {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func SupportsAggregateStateType(t Type) bool {
	return AggregateStateParamCount(t) > 0
}

func DefaultAggregateStateType(t Type) Type {
	if t == nil {
		return nil
	}
	if agg, ok := t.(*AggregateStateType); ok {
		if len(aggregateStateStates(agg)) == AggregateStateParamCount(agg.Base) {
			return t
		}
		return t
	}
	count := AggregateStateParamCount(t)
	if count == 0 {
		return t
	}
	states := make([]RefState, count)
	for i := range states {
		states[i] = RefStateNullable
	}
	return cloneAggregateStateWithBase(StripAggregateStateType(t), states)
}

func (t *EnumType) Variant(name string) (*EnumVariant, bool) {
	if t == nil || t.VariantMap == nil {
		return nil, false
	}
	variant, ok := t.VariantMap[name]
	return variant, ok
}

func (v *EnumVariant) HasNamedPayloads() bool {
	if v == nil || len(v.PayloadNames) == 0 {
		return false
	}
	return v.PayloadNames[0] != ""
}

func (v *EnumVariant) PayloadIndex(name string) (int, bool) {
	if v == nil {
		return 0, false
	}
	for i, payloadName := range v.PayloadNames {
		if payloadName == name {
			return i, true
		}
	}
	return 0, false
}

func (v *EnumVariant) PayloadLabel(index int) string {
	if v == nil || index < 0 || index >= len(v.PayloadNames) {
		return ""
	}
	return v.PayloadNames[index]
}

func (v *EnumVariant) HasTailPayload() bool {
	if v == nil {
		return false
	}
	return v.TailIndex >= 0 && v.TailIndex < len(v.Payload)
}

func (v *EnumVariant) TailPayloadIndex() (int, bool) {
	if !v.HasTailPayload() {
		return 0, false
	}
	return v.TailIndex, true
}

func (v *EnumVariant) TailPayloadViewType() (*DArrayViewType, bool) {
	index, ok := v.TailPayloadIndex()
	if !ok {
		return nil, false
	}
	viewType, ok := v.Payload[index].(*DArrayViewType)
	if !ok {
		return nil, false
	}
	return viewType, true
}

func (v *EnumVariant) TailPayloadElemType() (Type, bool) {
	viewType, ok := v.TailPayloadViewType()
	if !ok || viewType == nil {
		return nil, false
	}
	return viewType.Elem, true
}

func cloneRefTypeWithState(ref *RefType, state RefState) *RefType {
	if ref == nil {
		return nil
	}
	return &RefType{Elem: ref.Elem, State: state, Storage: ref.Storage, Region: ref.Region, ExplicitStorage: ref.ExplicitStorage}
}

func cloneRefType(ref *RefType) *RefType {
	if ref == nil {
		return nil
	}
	return cloneRefTypeWithState(ref, ref.State)
}

func refStateAssignable(dst, src RefState) bool {
	switch dst {
	case RefStateNullable:
		return true
	case RefStateNonNull:
		return src == RefStateNonNull
	case RefStateNull:
		return src == RefStateNull
	default:
		return false
	}
}

func refStorageAssignable(dstStorage, srcStorage RefStorage, dstExplicit, srcExplicit bool) bool {
	if !dstExplicit || !srcExplicit {
		return true
	}
	return dstStorage == srcStorage
}

func refRegionAssignable(dstRegion, srcRegion string) bool {
	if dstRegion == "" {
		return true
	}
	return dstRegion == srcRegion
}

func mergeRefRegions(a, b string) (string, bool) {
	if a == b {
		return a, true
	}
	if a == "" || b == "" {
		return "", true
	}
	return "", false
}

func mergeRefStorages(aStorage, bStorage RefStorage, aExplicit, bExplicit bool) (RefStorage, bool, bool) {
	if !aExplicit || !bExplicit {
		return RefStorageAny, false, true
	}
	if aStorage == bStorage {
		return aStorage, true, true
	}
	return RefStorageAny, false, false
}

func mergeRefStates(a, b RefState) (RefState, bool) {
	if a == b {
		return a, true
	}
	if a == RefStateNullable || b == RefStateNullable {
		return RefStateNullable, true
	}
	if (a == RefStateNonNull && b == RefStateNull) || (a == RefStateNull && b == RefStateNonNull) {
		return RefStateNullable, true
	}
	return RefStateNullable, false
}

func CommonNumericType(a, b Type) Type {
	if !IsNumericType(a) || !IsNumericType(b) {
		return invalidType
	}
	if SameType(a, b) {
		return a
	}
	if IsFloatType(a) || IsFloatType(b) {
		if ta, ok := a.(*BuiltinType); ok && ta.Name == "f64" {
			return a
		}
		if tb, ok := b.(*BuiltinType); ok && tb.Name == "f64" {
			return b
		}
		if ta, ok := a.(*BuiltinType); ok && ta.Name == "f32" {
			return a
		}
		if tb, ok := b.(*BuiltinType); ok && tb.Name == "f32" {
			return b
		}
	}
	if ta, ok := a.(*BuiltinType); ok && ta.Name == "char" {
		return b
	}
	if tb, ok := b.(*BuiltinType); ok && tb.Name == "char" {
		return a
	}
	if ta, ok := a.(*BuiltinType); ok && ta.Name == "int" {
		return b
	}
	if tb, ok := b.(*BuiltinType); ok && tb.Name == "int" {
		return a
	}
	return a
}

func arraySizesEqual(a, b *ArrayType) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.HasConstSize && b.HasConstSize {
		return a.ConstSize == b.ConstSize
	}
	return a.Size == b.Size
}

func viewBoundsEqual(a, b *ViewType) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Begin == b.Begin && a.End == b.End
}

func viewBoundsMatch(pattern, actual *ViewType) bool {
	if pattern == nil || actual == nil {
		return pattern == actual
	}
	if pattern.Begin == "" && pattern.End == "" {
		return true
	}
	return viewBoundsEqual(pattern, actual)
}

func SameShape(a, b Shape) bool {
	if a == nil || b == nil {
		return a == b
	}
	switch sa := a.(type) {
	case *ShapeParam:
		sb, ok := b.(*ShapeParam)
		return ok && sa.Name == sb.Name
	case *NamedShape:
		sb, ok := b.(*NamedShape)
		return ok && sa.Name == sb.Name
	case *WildcardShape:
		_, ok := b.(*WildcardShape)
		return ok
	case *FreshShape:
		sb, ok := b.(*FreshShape)
		return ok && sa.ID == sb.ID
	default:
		return false
	}
}

func shapeMatchesPattern(pattern, actual Shape) bool {
	if pattern == nil || actual == nil {
		return pattern == actual
	}
	if isWildcardShape(pattern) {
		return true
	}
	return SameShape(pattern, actual)
}

func isWildcardShape(shape Shape) bool {
	_, ok := shape.(*WildcardShape)
	return ok
}

func isDictRuntimeKeyType(t Type) bool {
	_, ok := t.(*DStrType)
	return ok
}
