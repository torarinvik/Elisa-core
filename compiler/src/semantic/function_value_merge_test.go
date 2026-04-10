package semantic

import (
	"testing"

	"llcontext/src/ast"
)

func TestMergeFunctionValueTypesDropsDifferingExplicitParamNames(t *testing.T) {
	a := &Analyzer{}
	holderType := &StructType{Name: "Holder"}
	poolRefType := &RefType{Elem: &StructType{Name: "ThreadPool"}, State: RefStateNonNull}
	summary := borrowedOwnerRefSummary{
		Fields: map[string]borrowedOwnerRefSummary{
			"pool_ref": {
				HasDirect: true,
				Direct:    borrowedOwnerRefSummaryTarget{ParamIndex: 0},
			},
		},
	}
	left := &FuncType{
		Name:                         "get_pool_ref",
		Params:                       []Type{holderType},
		ExplicitParamCount:           1,
		ExplicitParamNames:           []string{"holder"},
		Return:                       poolRefType,
		ReturnBorrowedOwnerRefs:      cloneBorrowedOwnerRefSummary(summary),
		ReturnBorrowedOwnerRefsKnown: true,
	}
	right := &FuncType{
		Name:                         "get_pool_ref_alias",
		Params:                       []Type{holderType},
		ExplicitParamCount:           1,
		ExplicitParamNames:           []string{"box"},
		Return:                       poolRefType,
		ReturnBorrowedOwnerRefs:      cloneBorrowedOwnerRefSummary(summary),
		ReturnBorrowedOwnerRefsKnown: true,
	}
	merged, ok := a.mergeFunctionValueTypes(left, right)
	if !ok || merged == nil {
		t.Fatal("expected function value merge to succeed for matching callable surfaces")
	}
	if merged.Name != "func" {
		t.Fatalf("expected merged function name to fall back to func, got %q", merged.Name)
	}
	if len(merged.ExplicitParamNames) != 0 {
		t.Fatalf("expected differing explicit param names to be dropped, got %#v", merged.ExplicitParamNames)
	}
	if !merged.ReturnBorrowedOwnerRefsKnown {
		t.Fatal("expected merged borrowed-owner summary to remain known")
	}
	fieldSummary, ok := merged.ReturnBorrowedOwnerRefs.Fields["pool_ref"]
	if !ok || !fieldSummary.HasDirect || fieldSummary.Direct.ParamIndex != 0 {
		t.Fatalf("expected merged borrowed-owner summary to preserve pool_ref path, got %#v", merged.ReturnBorrowedOwnerRefs)
	}
}

func TestMergeFunctionValueTypesClearsMismatchedPoststates(t *testing.T) {
	a := &Analyzer{}
	boxRefType := &RefType{Elem: &StructType{Name: "Box"}, State: RefStateNullable}
	left := &FuncType{
		Name:               "clear_box",
		Params:             []Type{boxRefType},
		ExplicitParamCount: 1,
		Return:             &BuiltinType{Name: "void"},
		Poststates: []FuncPoststate{{
			ParamIndex: 0,
			Kind:       FuncPoststateKindRefState,
			RefState:   RefStateNull,
		}},
	}
	right := &FuncType{
		Name:               "keep_box",
		Params:             []Type{boxRefType},
		ExplicitParamCount: 1,
		Return:             &BuiltinType{Name: "void"},
		Poststates: []FuncPoststate{{
			ParamIndex: 0,
			Kind:       FuncPoststateKindRefState,
			RefState:   RefStateNonNull,
		}},
	}
	merged, ok := a.mergeFunctionValueTypes(left, right)
	if !ok || merged == nil {
		t.Fatal("expected function value merge to succeed for matching callable surfaces")
	}
	if len(merged.Poststates) != 0 {
		t.Fatalf("expected mismatched poststates to be cleared, got %#v", merged.Poststates)
	}
}

func TestMergeFunctionValueTypesUnionsPermissionRefs(t *testing.T) {
	a := &Analyzer{}
	left := &FuncType{
		Name:                   "submit_callback",
		ExplicitParamCount:     0,
		Return:                 &BuiltinType{Name: "void"},
		DeclaredPermissionRefs: []ast.PermissionRef{{Name: "Pool", Member: "Submit"}},
		DeclaredPermissions:    []string{"Pool"},
		PermissionRefs:         []ast.PermissionRef{{Name: "Pool", Member: "Submit"}},
		Permissions:            []string{"Pool"},
	}
	right := &FuncType{
		Name:                   "wait_callback",
		ExplicitParamCount:     0,
		Return:                 &BuiltinType{Name: "void"},
		DeclaredPermissionRefs: []ast.PermissionRef{{Name: "Pool", Member: "WaitAll"}},
		DeclaredPermissions:    []string{"Pool"},
		PermissionRefs:         []ast.PermissionRef{{Name: "Pool", Member: "WaitAll"}},
		Permissions:            []string{"Pool"},
	}
	merged, ok := a.mergeFunctionValueTypes(left, right)
	if !ok || merged == nil {
		t.Fatal("expected function value merge to succeed for same-family callbacks")
	}
	if len(merged.Permissions) != 1 || merged.Permissions[0] != "Pool" {
		t.Fatalf("expected merged permissions to contain Pool, got %#v", merged.Permissions)
	}
	if got := PermissionRefsString(merged.PermissionRefs); got != " can[Pool.Submit, Pool.WaitAll]" {
		t.Fatalf("expected merged permission refs to union both callbacks, got %q", got)
	}
	if got := PermissionRefsString(merged.DeclaredPermissionRefs); got != " can[Pool.Submit, Pool.WaitAll]" {
		t.Fatalf("expected merged declared permission refs to union both callbacks, got %q", got)
	}
}
