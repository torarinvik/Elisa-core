package semantic

import "testing"

func TestAnalyzeStaticInterfaceZeroArgMethodCall(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "static_interface_zero_arg.llcontext", `
struct BuilderTag:
    tag: int

interface Builder:
    type State
    def state() -> State

impl Builder for BuilderTag:
    type State = int

    def state() -> int:
        return 1

def build[B: Builder]() -> B.State:
    return B.state()
`)

	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
}

func TestAnalyzeStaticInterfaceZeroArgMethodCallWithAssociatedLocal(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "static_interface_zero_arg_local.llcontext", `
struct BuilderTag:
    tag: int

interface Builder:
    type State
    def state() -> State

impl Builder for BuilderTag:
    type State = int

    def state() -> int:
        return 1

def build[B: Builder]() -> B.State:
    value: B.State = B.state()
    return value
`)

	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
}

func TestAnalyzeStaticInterfaceExplicitSpecializationWithBoundTypeParam(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "static_interface_bound_forward.llcontext", `
struct BuilderTag:
    tag: int

interface Builder:
    type State
    def state() -> State

impl Builder for BuilderTag:
    type State = int

    def state() -> int:
        return 1

def inner[B: Builder]() -> B.State:
    return B.state()

def outer[B: Builder]() -> B.State:
    return inner.specialize[B]()()
`)

	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
}
