package semantic

import (
	"strings"
	"testing"
)

func TestNestedModuleAliasTypeSeparator(t *testing.T) {
	for _, spelling := range []string{"Outer::Right::Nested.Item", "Second::Nested.Item", "Second::Nested::Item"} {
		t.Run(spelling, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "nested_alias_separator.elisa", `
module Outer:
    module Right:
        module Nested:
            struct Item:
                value: i64
using Outer as First
using First::Right as Second
def read(item: `+spelling+`) -> i64:
    return item.value
`)
			errors := strings.Join(result.Errors(), "\n")
			if strings.Contains(spelling, ".") {
				if !strings.Contains(errors, "is a namespace; write") {
					t.Fatalf("expected module separator diagnostic, got: %s", errors)
				}
			} else if errors != "" {
				t.Fatalf("valid module alias path rejected: %s", errors)
			}
		})
	}
}

func TestModuleAliasPrefixDoesNotTurnTypesIntoModules(t *testing.T) {
	a := &Analyzer{
		moduleAliases:   map[string]string{"Second": "Outer.Right"},
		declaredModules: map[string]bool{"Outer.Right": true, "Outer.Right.Nested": true},
	}
	for key, want := range map[string]bool{"Second": true, "Second.Nested": true, "Second.Kind": false, "Second.Nested.Item": false, "Unknown.Nested": false} {
		if got := a.nameIsModulePath(key); got != want {
			t.Errorf("nameIsModulePath(%q) = %v, want %v", key, got, want)
		}
	}
}
