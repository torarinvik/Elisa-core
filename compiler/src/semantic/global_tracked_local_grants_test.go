package semantic

import (
	"strings"
	"testing"
)

func TestTrackedLocalGlobalGrantRequiresCallerAuthority(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"block", "can Global.Read:\n    return 0", "add can Global.Read"},
		{"inline", "return 0 can Global.Read", "add can Global.Read"},
		{"grouped", "can Global{Read,Write}:\n    return 0", "add can[Global{Read,Write}]"},
		{"whole_family", "can Global:\n    return 0", "add can[Global]"},
		{"trusted_same_member", "trusted Global.Read:\n    can Global.Read:\n        return 0", ""},
		{"trusted_other_member", "trusted Global.Write:\n    can Global.Read:\n        return 0", "add can Global.Read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "def tracked() -> i32:\n    " + strings.ReplaceAll(tc.body, "\n", "\n    ") + "\ndef caller() -> i32:\n    return tracked()\n"
			result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, tc.name+".elisa", src, AnalyzeOptions{})
			errors := strings.Join(result.Errors(), "\n")
			if tc.want == "" {
				if errors != "" {
					t.Fatalf("trusted same-member suppression must remain selective: %s", errors)
				}
			} else if !strings.Contains(errors, tc.want) {
				t.Fatalf("tracked local grant must propagate mandatory authority %q: %s", tc.want, errors)
			}
		})
	}
}

func TestTrackedLocalGlobalGrantSurvivesCallAndFunctionValue(t *testing.T) {
	for _, body := range []string{
		"return relay()",
		"callback = tracked\n    return callback()",
	} {
		result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "tracked_global_transitive.elisa", `
def tracked() -> i32:
    return 0 can Global.Read
def relay() -> i32:
    can Global.Read:
        return tracked()
def caller() -> i32:
    `+body+"\n", AnalyzeOptions{})
		if errors := strings.Join(result.Errors(), "\n"); !strings.Contains(errors, "add can Global.Read") {
			t.Fatalf("tracked Global authority must survive transitive/function-value calls: %s", errors)
		}
	}
}

func TestImplicitImmutableGlobalRowRemainsAdvisory(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "immutable_global_advisory.elisa", `
global value: i32 = 1
def legacy() -> i32:
    return value
def caller() -> i32:
    return legacy()
`, AnalyzeOptions{})
	if errors := strings.Join(result.Errors(), "\n"); errors != "" {
		t.Fatalf("implicit immutable-only Global rows must remain advisory: %s", errors)
	}
}

func TestTrackedLocalGlobalGrantAcceptsMatchingCallerAuthority(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "tracked_global_granted.elisa", `
def tracked() -> i32:
    return 0 can Global.Read
def caller() -> i32:
    can Global.Read:
        return tracked()
`, AnalyzeOptions{})
	if errors := strings.Join(result.Errors(), "\n"); errors != "" {
		t.Fatalf("matching local caller authority must be accepted: %s", errors)
	}
}
