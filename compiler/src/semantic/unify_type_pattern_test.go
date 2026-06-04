package semantic

import "testing"

func TestUnifyTypePatternMatchesGenericInstance(t *testing.T) {
	free := map[string]bool{"T": true}
	pattern := &GenericInstanceType{Name: "BoxTag", Args: []Type{&TypeParamType{Name: "T"}}}
	actual := &GenericInstanceType{Name: "BoxTag", Args: []Type{(&BuiltinType{Name: "i64"})}}

	subst, ok := UnifyTypePattern(pattern, actual, free)
	if !ok {
		t.Fatalf("expected BoxTag[T] to unify with BoxTag[i64]")
	}
	if got := subst["T"]; !SameType(got, (&BuiltinType{Name: "i64"})) {
		t.Fatalf("expected T=i64, got %v", got)
	}
}

func TestUnifyTypePatternRejectsHeadMismatch(t *testing.T) {
	free := map[string]bool{"T": true}
	pattern := &GenericInstanceType{Name: "BoxTag", Args: []Type{&TypeParamType{Name: "T"}}}
	actual := &GenericInstanceType{Name: "OtherTag", Args: []Type{(&BuiltinType{Name: "i64"})}}

	if _, ok := UnifyTypePattern(pattern, actual, free); ok {
		t.Fatalf("expected BoxTag[T] not to unify with OtherTag[i64]")
	}
}

func TestUnifyTypePatternMatchesDArrayElem(t *testing.T) {
	free := map[string]bool{"T": true}
	pattern := &DArrayType{Elem: &TypeParamType{Name: "T"}, SurfaceName: "darray"}
	actual := &DArrayType{Elem: (&BuiltinType{Name: "i64"}), SurfaceName: "darray"}

	subst, ok := UnifyTypePattern(pattern, actual, free)
	if !ok || !SameType(subst["T"], (&BuiltinType{Name: "i64"})) {
		t.Fatalf("expected darray[T] to unify with darray[i64] binding T=i64, got ok=%v subst=%v", ok, subst)
	}
}

func TestUnifyTypePatternRepeatedVarMustAgree(t *testing.T) {
	free := map[string]bool{"T": true}
	pattern := &DictType{Key: &TypeParamType{Name: "T"}, Value: &TypeParamType{Name: "T"}, SurfaceName: "dict"}
	mismatch := &DictType{Key: (&BuiltinType{Name: "i64"}), Value: (&BuiltinType{Name: "bool"}), SurfaceName: "dict"}
	if _, ok := UnifyTypePattern(pattern, mismatch, free); ok {
		t.Fatalf("expected dict[T,T] not to unify with dict[i64,bool]")
	}
	match := &DictType{Key: (&BuiltinType{Name: "i64"}), Value: (&BuiltinType{Name: "i64"}), SurfaceName: "dict"}
	if _, ok := UnifyTypePattern(pattern, match, free); !ok {
		t.Fatalf("expected dict[T,T] to unify with dict[i64,i64]")
	}
}
