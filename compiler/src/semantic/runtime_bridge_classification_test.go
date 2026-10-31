package semantic

import "testing"

func TestClassifyRuntimeBridgePairs(t *testing.T) {
	i64 := &BuiltinType{Name: "i64"}
	u8 := &BuiltinType{Name: "u8"}
	cases := []struct {
		name        string
		left, right Type
		want        runtimeBridgeKind
	}{
		{"array", &DArrayType{Elem: i64}, &GenericInstanceType{Name: "DynArray", Args: []Type{i64}}, runtimeBridgeDArrayDynArray},
		{"array view", &ViewType{Elem: i64}, &StructType{Name: "DynArrayView"}, runtimeBridgeDArrayViewDynArrayView},
		{"cstring", &CStrType{}, &RefType{Elem: u8}, runtimeBridgeCStrU8Ref},
		{"dict", &DictType{Key: i64, Value: u8}, &GenericInstanceType{Name: "DynDict", Args: []Type{i64, u8}}, runtimeBridgeDictDynDict},
		{"string view", &SViewType{}, &StructType{Name: "StringView"}, runtimeBridgeSViewStringView},
		{"wrong array arity", &DArrayType{Elem: i64}, &GenericInstanceType{Name: "DynArray"}, runtimeBridgeNone},
		{"wrong reference", &CStrType{}, &RefType{Elem: i64}, runtimeBridgeNone},
		{"unsupported dict key", &DictType{Key: &DArrayType{Elem: i64}, Value: u8}, &GenericInstanceType{Name: "DynDict", Args: []Type{i64, u8}}, runtimeBridgeNone},
		{"unrelated types", i64, &StructType{Name: "StringView"}, runtimeBridgeNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, pair := range [][2]Type{{tc.left, tc.right}, {tc.right, tc.left}} {
				got, ok := classifyRuntimeBridge(pair[0], pair[1])
				if got.Kind != tc.want || ok != (tc.want != runtimeBridgeNone) {
					t.Fatalf("%T and %T: kind=%v present=%v, want %v", pair[0], pair[1], got.Kind, ok, tc.want)
				}
			}
		})
	}
}
