package semantic

import "testing"

func TestAssignableToFuncTypesIsContravariantInParams(t *testing.T) {
	boxType := &StructType{Name: "Box"}
	intType := &BuiltinType{Name: "int"}
	nonNullBox := &RefType{Elem: boxType, State: RefStateNonNull}
	nullableBox := &RefType{Elem: boxType, State: RefStateNullable}

	nullableSlot := &FuncType{Params: []Type{nullableBox}, ExplicitParamCount: 1, Return: intType}
	nonNullOnly := &FuncType{Params: []Type{nonNullBox}, ExplicitParamCount: 1, Return: intType}
	if AssignableTo(nullableSlot, nonNullOnly) {
		t.Fatalf("expected func(Box&) -> int not to be assignable to func(Box&?) -> int")
	}

	nonNullSlot := &FuncType{Params: []Type{nonNullBox}, ExplicitParamCount: 1, Return: intType}
	nullableCapable := &FuncType{Params: []Type{nullableBox}, ExplicitParamCount: 1, Return: intType}
	if !AssignableTo(nonNullSlot, nullableCapable) {
		t.Fatalf("expected func(Box&?) -> int to be assignable to func(Box&) -> int")
	}
}

func TestAssignableToFuncTypesIsCovariantInReturn(t *testing.T) {
	boxType := &StructType{Name: "Box"}
	nonNullBox := &RefType{Elem: boxType, State: RefStateNonNull}
	nullableBox := &RefType{Elem: boxType, State: RefStateNullable}

	nullableReturnSlot := &FuncType{Return: nullableBox}
	nonNullReturnSource := &FuncType{Return: nonNullBox}
	if !AssignableTo(nullableReturnSlot, nonNullReturnSource) {
		t.Fatalf("expected func() -> Box& to be assignable to func() -> Box&?")
	}

	nonNullReturnSlot := &FuncType{Return: nonNullBox}
	nullableReturnSource := &FuncType{Return: nullableBox}
	if AssignableTo(nonNullReturnSlot, nullableReturnSource) {
		t.Fatalf("expected func() -> Box&? not to be assignable to func() -> Box&")
	}
}

func TestAssignableToRejectsImmutableRefToMutableRef(t *testing.T) {
	intType := &BuiltinType{Name: "i64"}
	immutableRef := &RefType{Elem: intType, State: RefStateNullable, Storage: RefStorageAny, ExplicitStorage: true}
	mutableRef := &RefType{Elem: intType, Mutable: true, State: RefStateNullable, Storage: RefStorageAny, ExplicitStorage: true}

	if AssignableTo(mutableRef, immutableRef) {
		t.Fatalf("immutable nullable ref must not be assignable to mutable nullable ref")
	}
	if !AssignableTo(immutableRef, mutableRef) {
		t.Fatalf("mutable nullable ref should be assignable to immutable nullable ref")
	}
}

func TestFuncTypeProofMetadataDoesNotAffectTypeIdentity(t *testing.T) {
	intType := &BuiltinType{Name: "int"}
	plain := &FuncType{
		Params:             []Type{intType},
		ExplicitParamCount: 1,
		Return:             intType,
	}
	refined := &FuncType{
		Params:             []Type{intType},
		ExplicitParamCount: 1,
		Return:             intType,
		RefinementEnsures: []RefinementEnsure{{
			ParamIndex: 0,
			LawName:    "Positive",
		}},
	}

	if len(refined.RefinementEnsures) == 0 {
		t.Fatal("test setup must keep proof metadata on the function signature")
	}
	if !SameType(plain, refined) || !SameType(refined, plain) {
		t.Fatalf("refinement proof metadata must survive on FuncType without entering SameType")
	}
	if !AssignableTo(plain, refined) || !AssignableTo(refined, plain) {
		t.Fatalf("refinement proof metadata must not make SMT/contract entailment part of AssignableTo")
	}
}
