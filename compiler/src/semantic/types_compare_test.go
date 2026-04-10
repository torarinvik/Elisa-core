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
