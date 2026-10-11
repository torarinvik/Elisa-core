package semantic

import (
	"strings"
	"testing"
)

func TestPermissionForwardingLocalGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "permission_forwarding.elisa", `global mutable hot: i64 = 7
def read_hot() -> i64:
    can Global.Read:
        return hot
def apply[permission P](callback: fn() -> i64 can[P]) -> i64:
    can P:
        return callback()
def forward[permission P](callback: fn() -> i64 can[P]) -> i64:
    can P:
        return apply(callback)
def main() -> i64:
    can Global.Read:
        return forward(read_hot)
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(result), "requires Global") || len(result.Errors()) != 0 {
		t.Fatalf("granted forwarding rejected: %s", allDiagnostics(result))
	}
}

func TestPermissionParameterDoesNotGrantDirectGlobalRead(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "permission_parameter_global.elisa", `global mutable hot: i64 = 7
def invalid[permission P](callback: fn() -> i64 can[P]) -> i64:
    can P:
        return hot
`, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "mutable global read") || len(result.Errors()) == 0 {
		t.Fatalf("abstract row authorized direct global read: %s", allDiagnostics(result))
	}
}

func TestPermissionParameterRequiresLocalInvocationGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "permission_parameter_missing_local.elisa", `def invalid[permission P](callback: fn() -> i64 can[P]) -> i64 can[P]:
    return callback()
`, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "requires can[P]") {
		t.Fatalf("signature authorized ungranted callback invocation: %s", allDiagnostics(result))
	}
}

func TestPermissionForwardingDoesNotAuthorizeUngrantingCaller(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "permission_forwarding_missing_global.elisa", `global mutable hot: i64 = 7
def read_hot() -> i64:
    can Global.Read:
        return hot
def forward[permission P](callback: fn() -> i64 can[P]) -> i64:
    can P:
        return callback()
def main() -> i64:
    return forward(read_hot)
`, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "requires can[Global]") || len(result.Errors()) == 0 {
		t.Fatalf("concrete caller bypassed Global.Read: %s", allDiagnostics(result))
	}
}

func TestPermissionParameterDoesNotGrantDirectGlobalWrite(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "permission_parameter_global_write.elisa", `global mutable hot: i64 = 7
def invalid[permission P](callback: fn() -> i64 can[P]) -> i64:
    can P:
        hot <- 9
        return callback()
`, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "mutable global write") || len(result.Errors()) == 0 {
		t.Fatalf("abstract row authorized direct global write: %s", allDiagnostics(result))
	}
}

func TestPermissionForwardingConcreteWriteGrant(t *testing.T) {
	for _, tc := range []struct {
		name, grant string
		reject      bool
	}{
		{"read_only", "Global.Read", true},
		{"write", "Global.Write", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "permission_write_"+tc.name+".elisa", `global mutable hot: i64 = 7
def write_hot() -> i64:
    can Global.Write:
        hot <- 9
        return 9
def forward[permission P](callback: fn() -> i64 can[P]) -> i64:
    can P:
        return callback()
def main() -> i64:
    can `+tc.grant+`:
        return forward(write_hot)
`, AnalyzeOptions{})
			if tc.reject {
				if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "Global.Write") {
					t.Fatalf("write callback bypassed write grant: %s", allDiagnostics(result))
				}
			} else if len(result.Errors()) != 0 {
				t.Fatalf("granted write forwarding rejected: %s", allDiagnostics(result))
			}
		})
	}
}

func TestPermissionForwardingDoesNotSuppressMandatoryUnsafe(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "permission_forwarding_unsafe.elisa", `global mutable hot: i64 = 7
def read_hot() -> i64:
    can Global.Read:
        return hot
def forward[permission P](callback: fn() -> i64 can[P]) -> i64:
    can P:
        return callback()
def main() -> i64:
    can Global.Read:
        return forward(read_hot)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "Unsafe.MutableGlobal") {
		t.Fatalf("forwarding suppressed mandatory unsafe grant: %s", allDiagnostics(result))
	}
}
