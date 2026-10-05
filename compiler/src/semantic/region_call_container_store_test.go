package semantic

import (
	"strings"
	"testing"
)

const callContainerStorePrelude = `struct Source:
    text: sview

struct Target:
    text: sview

def copy_text(target: mutable darray[Target]&, source: darray[Source]&) -> void can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        target.push(Target{text: source[0].text})
`

func TestCallContainerStoreUsesTrackedElementLifetime(t *testing.T) {
	src := callContainerStorePrelude + `def run():
    can Memory.Allocate, Abort.Panic:
        region outer(4096):
            target: mutable darray[Target] @outer = []
            region inner(2048):
                in inner:
                    source: mutable darray[Source] = [Source{text: "static"}]
                    copy_text(target, source)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "call_container_store_static_element.elisa", src)
	if errs := strings.Join(result.Errors(), "\n"); errs != "" {
		t.Fatalf("a source container's local backing must not taint static element views, got:\n%s", errs)
	}
}

func TestCallContainerStoreStillRejectsTrackedShortElementLifetime(t *testing.T) {
	src := callContainerStorePrelude + `def run():
    can Memory.Allocate, Abort.Panic:
        region outer(4096):
            target: mutable darray[Target] @outer = []
            region inner(2048):
                in inner:
                    bytes: mutable darray[u8] = [65.u8()]
                    source: mutable darray[Source] = [Source{text: bytes.as_sview()}]
                    copy_text(target, source)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "call_container_store_local_element.elisa", src)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "longer-lived region") {
		t.Fatalf("a short-lived view inside the source element must not escape into the outer target, got:\n%s", joined)
	}
}

func TestCallContainerStoreStillRejectsRetainedSourceHeader(t *testing.T) {
	src := `struct Source:
    text: sview

struct Holder:
    values: darray[Source]

def retain_source(target: mutable darray[Holder]&, source: darray[Source]&) -> void can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        target.push(Holder{values: source})

def run():
    can Memory.Allocate, Abort.Panic:
        region outer(4096):
            target: mutable darray[Holder] @outer = []
            region inner(2048):
                in inner:
                    source: mutable darray[Source] = [Source{text: "static"}]
                    retain_source(target, source)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "call_container_store_retained_header.elisa", src)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "longer-lived region") {
		t.Fatalf("retaining the source darray header must still be rejected, got:\n%s", joined)
	}
}
