package semantic

import "testing"

func TestFrameBorrowRespectsProgramLivedViewTypes(t *testing.T) {
	a := &Analyzer{programRegions: map[string]bool{"source": true}}
	for _, typ := range []Type{
		&ViewType{Region: "source"},
		&SViewType{Region: "source"},
		&CStrType{Region: "source"},
		&DArrayType{Elem: &SViewType{Region: "source"}},
	} {
		if a.typeMayHoldFrameBorrow(typ, map[Type]bool{}) {
			t.Errorf("%T with program-lived views may not hold a frame borrow", typ)
		}
	}
	for _, typ := range []Type{&ViewType{}, &SViewType{Region: "local"}, &CStrType{}} {
		if !a.typeMayHoldFrameBorrow(typ, map[Type]bool{}) {
			t.Errorf("%T without a program-lived region may hold a frame borrow", typ)
		}
	}
}
