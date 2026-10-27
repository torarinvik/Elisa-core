//go:build cgo

package backend

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func TestPartitionSubmitSupportedSignature(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "submit_signature.elisa", partitionSubmitRuntimeSource())
	sym, ok := result.GlobalScope.Lookup("pool_submit1")
	if !ok {
		t.Fatal("missing pool_submit1")
	}
	valid, ok := sym.Type.(*semantic.FuncType)
	if !ok || !partitionSubmitSupportedSignature(valid) {
		t.Fatalf("real submit signature rejected: %#v", sym.Type)
	}
	bad := *valid
	bad.GenericParams = []ast.GenericParam{{Kind: ast.GenericParamType, Name: "R"}}
	bad.TypeParams = []string{"R"}
	if partitionSubmitSupportedSignature(&bad) {
		t.Fatal("accepted missing capture binder")
	}
	bad = *valid
	bad.GenericParams = []ast.GenericParam{{Kind: ast.GenericParamValue, Name: "A"}, {Kind: ast.GenericParamType, Name: "R"}}
	if partitionSubmitSupportedSignature(&bad) {
		t.Fatal("accepted non-type capture binder")
	}
	bad = *valid
	bad.Params = append([]semantic.Type(nil), valid.Params...)
	bad.Params[2] = &semantic.TypeParamType{Name: "R"}
	if partitionSubmitSupportedSignature(&bad) {
		t.Fatal("accepted unrelated capture argument")
	}
	bad = *valid
	bad.Params = append([]semantic.Type(nil), valid.Params...)
	bad.Params[0] = &semantic.TypeParamType{Name: "A"}
	if partitionSubmitSupportedSignature(&bad) {
		t.Fatal("accepted a pool parameter that depends on the capture type")
	}
	bad = *valid
	bad.Params = append([]semantic.Type(nil), valid.Params...)
	poolRef := *(valid.Params[0].(*semantic.RefType))
	poolRef.Elem = &semantic.GenericInstanceType{
		Name: "ThreadPool",
		Base: &semantic.StructType{Name: "ThreadPool", Builtin: true},
		Args: []semantic.Type{&semantic.TypeParamType{Name: "A"}},
	}
	bad.Params[0] = &poolRef
	if partitionSubmitSupportedSignature(&bad) {
		t.Fatal("accepted a generic pool parameter that depends on the capture type")
	}
	bad = *valid
	bad.Return = &semantic.TypeParamType{Name: "A"}
	if partitionSubmitSupportedSignature(&bad) {
		t.Fatal("accepted a return type that depends on the capture type")
	}
	bad = *valid
	taskResult := *(valid.Return.(*semantic.GenericInstanceType))
	taskResult.Args = append([]semantic.Type(nil), taskResult.Args...)
	taskResult.Args[1] = &semantic.TypeParamType{Name: "A"}
	bad.Return = &taskResult
	if partitionSubmitSupportedSignature(&bad) {
		t.Fatal("accepted a task result state that depends on the capture type")
	}
	bad = *valid
	bad.Params = append([]semantic.Type(nil), valid.Params...)
	bad.Params[1] = &semantic.FuncType{Params: []semantic.Type{&semantic.TypeParamType{Name: "R"}}, Return: &semantic.TypeParamType{Name: "R"}}
	if partitionSubmitSupportedSignature(&bad) {
		t.Fatal("accepted unrelated callback argument")
	}
}

func TestPartitionSubmitRejectsUnfamiliarCallABI(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "submit_abi_signature.elisa", partitionSubmitRuntimeSource())
	sym, ok := result.GlobalScope.Lookup("pool_submit1")
	if !ok {
		t.Fatal("missing pool_submit1")
	}
	base, ok := sym.Type.(*semantic.FuncType)
	if !ok || !partitionSubmitSupportedSignature(base) {
		t.Fatal("fixture submit signature was rejected")
	}
	callback, ok := base.Params[1].(*semantic.FuncType)
	if !ok {
		t.Fatal("fixture callback is not a function")
	}
	cases := []struct {
		name   string
		mutate func(*semantic.FuncType)
	}{
		{"fast calling convention", func(fn *semantic.FuncType) { fn.CallConv = "fast" }},
		{"explicit C ABI", func(fn *semantic.FuncType) { fn.CallConv = "c" }},
		{"native extern ABI", func(fn *semantic.FuncType) { fn.IsNativeExtern = true }},
		{"async entry", func(fn *semantic.FuncType) { fn.HasAsyncEntry = true }},
		{"segment agnostic", func(fn *semantic.FuncType) { fn.HasSegmentAgnostic = true }},
		{"segment establishing", func(fn *semantic.FuncType) { fn.HasSegmentEstablishing = true }},
		{"host segment transition", func(fn *semantic.FuncType) { fn.SegmentTransition = semantic.FuncSegmentTransitionHost }},
		{"guest segment transition", func(fn *semantic.FuncType) { fn.SegmentTransition = semantic.FuncSegmentTransitionGuest }},
		{"reentrant safe", func(fn *semantic.FuncType) { fn.HasReentrantSafe = true }},
		{"empty explicit C plan", func(fn *semantic.FuncType) { fn.CParamPlan = []semantic.CParamPart{} }},
		{"C parameter plan", func(fn *semantic.FuncType) { fn.CParamPlan = []semantic.CParamPart{{}} }},
		{"implicit parameter", func(fn *semantic.FuncType) { fn.ImplicitParamNames = []string{"hidden"} }},
		{"region polymorphism", func(fn *semantic.FuncType) { fn.RegionPolymorphic = true }},
		{"inferred region", func(fn *semantic.FuncType) { fn.InferredRegionParams = []string{"r"} }},
		{"owned return", func(fn *semantic.FuncType) { fn.ReturnsOwnedRegion = true }},
		{"owned parameter", func(fn *semantic.FuncType) { fn.OwnedParams = []bool{true} }},
		{"intrinsic", func(fn *semantic.FuncType) { fn.IntrinsicName = "llvm.test" }},
		{"wrong explicit parameter count", func(fn *semantic.FuncType) { fn.ExplicitParamCount = 5 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outer := *base
			outer.Params = append([]semantic.Type(nil), base.Params...)
			inner := *callback
			tc.mutate(&inner)
			outer.Params[1] = &inner
			if partitionSubmitSupportedSignature(&outer) {
				t.Fatal("accepted unsupported callback ABI")
			}
			outer = *base
			tc.mutate(&outer)
			if partitionSubmitSupportedSignature(&outer) {
				t.Fatal("accepted unsupported submit function ABI")
			}
		})
	}
}

func TestPartitionSubmitRejectsGenericCallback(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "submit_generic_callback.elisa", partitionSubmitRuntimeSource())
	sym, ok := result.GlobalScope.Lookup("pool_submit1")
	if !ok {
		t.Fatal("missing pool_submit1")
	}
	base := sym.Type.(*semantic.FuncType)
	for _, mutate := range []func(*semantic.FuncType){
		func(fn *semantic.FuncType) { fn.TypeParams = []string{"T"} },
		func(fn *semantic.FuncType) {
			fn.GenericParams = []ast.GenericParam{{Kind: ast.GenericParamType, Name: "T"}}
		},
		func(fn *semantic.FuncType) { fn.RegionParams = []string{"r"} },
	} {
		outer := *base
		outer.Params = append([]semantic.Type(nil), base.Params...)
		callback := *(base.Params[1].(*semantic.FuncType))
		mutate(&callback)
		outer.Params[1] = &callback
		if partitionSubmitSupportedSignature(&outer) {
			t.Fatal("accepted a generic or region-bound callback")
		}
	}
}
