package semantic

import (
	"elisacore/src/lexer"
)

type ConstValueKind int

const (
	ConstUnknown ConstValueKind = iota
	ConstInt
	ConstFloat
	ConstBool
	ConstString
	ConstTuple
	ConstList
	ConstRecord
	ConstDict
	ConstOptional
)

const (
	semanticTraversalDepthLimit    = 256
	semanticSubstitutionDepthLimit = 512
	semanticCloneDepthLimit        = 512
)

type ConstValue struct {
	Kind   ConstValueKind
	Int    int64
	Float  float64
	Bool   bool
	String string
	Elems  []ConstValue
	Fields map[string]ConstValue
	Dict   []ConstDictEntry
	Some   bool
	Value  *ConstValue
}

type ConstDictEntry struct {
	Key   ConstValue
	Value ConstValue
}

type ShapeTransformSpec struct {
	FreshReturnShapeParams []string
}

type externLinkNameSignature struct {
	SymbolName string
	Signature  string
	Pos        lexer.Pos
}

var shapeTransformTable = map[string]ShapeTransformSpec{
	"resize":                    {FreshReturnShapeParams: []string{"shape_out"}},
	"push":                      {FreshReturnShapeParams: []string{"shape_out"}},
	"append_many":               {FreshReturnShapeParams: []string{"shape_out"}},
	"truncate":                  {FreshReturnShapeParams: []string{"shape_out"}},
	"clear":                     {FreshReturnShapeParams: []string{"shape_out"}},
	"concat":                    {FreshReturnShapeParams: []string{"shape_result"}},
	"strcat":                    {FreshReturnShapeParams: []string{"shape_result"}},
	"arena_da_append":           {FreshReturnShapeParams: []string{"shape_out"}},
	"arena_da_append_many":      {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_concat2":                {FreshReturnShapeParams: []string{"shape_result"}},
	"rt_concat2_scratch":        {FreshReturnShapeParams: []string{"shape_result"}},
	"rt_int_to_string":          {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_int_to_string_scratch":  {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_bool_to_string":         {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_bool_to_string_scratch": {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_char_to_string":         {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_char_to_string_scratch": {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_string_slice":          {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_string_from_view":      {FreshReturnShapeParams: []string{"shape_out"}},
	"arena_da_from_view":        {FreshReturnShapeParams: []string{"shape_out"}},
}

type freshReturnStatus int

const (
	freshReturnUnknown freshReturnStatus = iota
	freshReturnAlways
	freshReturnNotFresh
)
