package semantic

import "testing"

func TestCatchArmResolvesLexicalModuleAlias(t *testing.T) {
	a := &Analyzer{
		currentNamespace: "API",
		moduleAliases: map[string]string{
			"API.Family":   "Outer.Provider",
			"Other.Family": "Second",
		},
	}
	set := &ErrorSetType{Tags: []string{"Outer.Provider.Fault.Bad", "Second.Fault.Bad"}}
	for _, tc := range []struct{ arm, want string }{
		{"Family.Fault.Bad", "Outer.Provider.Fault.Bad"},
		{"Second.Fault.Bad", "Second.Fault.Bad"},
	} {
		got, ok := MatchErrorTag(set, a.resolveCatchArmAlias(tc.arm))
		if !ok || got != tc.want {
			t.Errorf("arm %q resolved to %q, %v; want %q", tc.arm, got, ok, tc.want)
		}
	}
}

func TestAnalyzeCatchUsesLexicalModuleAlias(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "catch_module_alias.elisa", `
module Outer:
    module Provider:
        error Fault:
            Bad(payload: i64)
module Second:
    error Fault:
        Bad(payload: i64)
module API:
    using Outer as Root
    using Root::Provider as Family

    def failure(flag: bool) -> i64 error[Outer::Provider::Fault{Bad}, Second::Fault{Bad}]:
        raise Outer::Provider::Fault.Bad(1) if flag
        raise Second::Fault.Bad(2)

    def run(flag: bool) -> i64:
        return catch failure(flag):
            value: value
            Family::Fault.Bad(payload): payload
            Second::Fault.Bad(payload): payload
`)
}
