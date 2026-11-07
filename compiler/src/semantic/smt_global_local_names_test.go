package semantic

import (
	"reflect"
	"testing"
)

func TestSMTGlobalImmutableLocalNamesTracksScopeChanges(t *testing.T) {
	a := &Analyzer{}
	scope := &Scope{Symbols: map[string]*Symbol{}}
	for _, sym := range []*Symbol{
		{Name: "z", Kind: SymbolLocal},
		{Name: "a", Kind: SymbolLocal},
		{Name: "mutable", Kind: SymbolLocal, Mutable: true},
		{Name: "global", Kind: SymbolGlobal},
	} {
		scope.Define(sym)
	}
	if got := a.smtGlobalImmutableLocalNames(scope); !reflect.DeepEqual(got, []string{"a", "z"}) {
		t.Fatalf("names = %v", got)
	}
	scope.Define(&Symbol{Name: "b", Kind: SymbolLocal})
	if got := a.smtGlobalImmutableLocalNames(scope); !reflect.DeepEqual(got, []string{"a", "b", "z"}) {
		t.Fatalf("names after define = %v", got)
	}
	other := &Scope{Symbols: map[string]*Symbol{}}
	other.Define(&Symbol{Name: "other", Kind: SymbolLocal})
	if got := a.smtGlobalImmutableLocalNames(other); !reflect.DeepEqual(got, []string{"other"}) {
		t.Fatalf("names for other scope = %v", got)
	}
}
