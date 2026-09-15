package main

import "testing"

func TestNamedStringViewConstantsKeepTheirABI(t *testing.T) {
	exit, stdout, stderr := runStressProgram(t, "const_sview_abi", `module Names:
    public:
        const MAIN: sview = "main"
        const EMPTY: sview = ""

def same(value: sview) -> bool:
    return value == Names::MAIN

def returned() -> sview:
    return Names::MAIN

@test
def named_string_view_constants() -> void:
    can Abort.Panic:
        assert same(Names::MAIN)
        assert returned() == "main"
        assert Names::EMPTY == ""
`)
	if exit != 0 {
		t.Fatalf("named sview constant ABI failed: exit=%d\nstdout=%s\nstderr=%s", exit, stdout, stderr)
	}
}
