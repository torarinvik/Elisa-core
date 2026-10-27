//go:build cgo

package backend

import (
	"fmt"
	"testing"

	"elisacore/src/easm"
	"elisacore/src/semantic"
)

func TestPartitionHelperNamespaceConflictSources(t *testing.T) {
	var unit [32]byte
	unit[0] = 42
	name := fmt.Sprintf("elisa.partition.%x.0.lambda_0", unit)
	cases := []struct {
		name     string
		result   *semantic.Result
		conflict bool
	}{
		{"clean", &semantic.Result{GlobalScope: &semantic.Scope{Symbols: map[string]*semantic.Symbol{"other": {Name: "other"}}}}, false},
		{"source symbol", &semantic.Result{GlobalScope: &semantic.Scope{Symbols: map[string]*semantic.Symbol{name: {Name: name}}}}, true},
		{"link name", &semantic.Result{GlobalScope: &semantic.Scope{Symbols: map[string]*semantic.Symbol{"other": {Name: "other", LinkName: name}}}}, true},
		{"export name", &semantic.Result{GlobalScope: &semantic.Scope{}, ExportedFuncs: []*semantic.ExportedFunc{{PublicName: name}}}, true},
		{"export link name", &semantic.Result{GlobalScope: &semantic.Scope{}, ExportedFuncs: []*semantic.ExportedFunc{{LinkName: name}}}, true},
		{"exported global", &semantic.Result{GlobalScope: &semantic.Scope{}, ExportedGlobals: []*semantic.ExportedGlobal{{PublicName: name}}}, true},
		{"easm", &semantic.Result{GlobalScope: &semantic.Scope{}, EASMModules: []*easm.Module{{}}}, true},
		{"missing scope", &semantic.Result{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := partitionHelperNamespaceConflicts(tc.result, unit, nil); got != tc.conflict {
				t.Fatalf("got %v want %v", got, tc.conflict)
			}
		})
	}
}
