package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// A field that flow typing narrowed to null (`leaf.plain` after
// `return null if ... or leaf.plain != null`) has no payload to address. Reading it
// through a ref parameter used to fail the object build with "field plain requires an
// addressable object"; it reads as null, as a known-null identifier does. The helpers
// sit in a module so the object build lowers them although `main` never calls them.
func TestObjectLoweringReadsKnownNullFieldAsNull(t *testing.T) {
	src := `extern Handle
extern handle_count(value: Handle) -> u32

struct Leaf:
    step: Handle?
    plain: Handle?
    checked: Handle?

module Tools:
    def unused() -> u32:
        0

extend Tools:
    private:
        def with_step(leaf: Leaf&, step: Handle?) -> Handle?:
            found: Handle = get step else return null
            if leaf.step is earlier:
                return null if earlier != found
            found

        def checked_leaf(value: Handle, leaf: Leaf&) -> Leaf?:
            return null if handle_count(value) != 2 or leaf.plain != null
            if leaf.checked is earlier:
                return null if earlier != value
            step: Handle = get with_step(leaf, value) else return null
            Leaf{step: step, plain: leaf.plain, checked: value}

        def plain_leaf(value: Handle, leaf: Leaf&) -> Leaf?:
            return null if leaf.checked != null
            if leaf.plain is earlier:
                return null if earlier != value
            step: Handle = get with_step(leaf, value) else return null
            Leaf{step: step, plain: value, checked: leaf.checked}

    public:
        def either(value: Handle, leaf: Leaf&) -> Leaf?:
            return checked_leaf(value, leaf) if handle_count(value) == 1
            plain_leaf(value, leaf)

def main() -> i32:
    0
`
	dir := t.TempDir()
	path := filepath.Join(dir, "known_null_field.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	objectPath := filepath.Join(dir, "known_null_field.o")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "obj", "-o", objectPath, path}, &stdout, &stderr); code != 0 {
		t.Fatalf("object lowering failed (exit %d)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if info, err := os.Stat(objectPath); err != nil || info.Size() == 0 {
		t.Fatalf("expected a non-empty object, stat=%v", err)
	}
}
