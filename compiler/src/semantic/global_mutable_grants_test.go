package semantic

import (
	"strings"
	"testing"
)

func TestMutableGlobalGrantsDefault(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{"read", "return hot", "mutable global read"},
		{"write", "hot <- 1\nreturn 0", "mutable global write"},
		{"wrong_read", "can Global.Write:\n    return hot", "mutable global read"},
		{"wrong_write", "can Global.Read:\n    hot <- 1\nreturn 0", "mutable global write"},
		{"readonly_alias", "can Global.Read:\n    r: i32& = &hot\n    return r", ""},
		{"inferred_alias", "can Global.Read:\n    r = &hot\n    r <- 1\nreturn 0", "mutable global write"},
		{"mutable_alias", "can Global.Read:\n    r: mutable i32& = &hot\n    r <- 1\nreturn 0", "mutable global write"},
		{"block_expr", "value =\n    hot\nreturn value", "mutable global read"},
		{"read_granted", "can Global.Read:\n    return hot", ""},
		{"write_granted", "can Global.Write:\n    hot <- 1\nreturn 0", ""},
		{"compound_wrong", "can Global.Write:\n    hot += 1\nreturn 0", "mutable global read"},
		{"grant_scope_ends", "can Global{Read, Write}:\n    hot += 1\nreturn hot", "mutable global read"},
		{"trusted_read", "trusted Global.Read:\n    return hot", ""},
		{"trusted_member", "trusted Global.Read:\n    hot += 1\nreturn 0", "mutable global write"},
		{"shadow", "hot: mutable i32 = 2\nhot <- 3\nreturn hot", ""},
		{"read_before_shadow", "saved = hot\nhot: mutable i32 = 2\nreturn saved + hot", "mutable global read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "global mutable hot: i32 = 0\ndef test() -> i32:\n" + "    " + strings.ReplaceAll(tt.body, "\n", "\n    ") + "\n"
			result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, tt.name+".elisa", src, AnalyzeOptions{})
			errors := strings.Join(result.Errors(), "\n")
			if tt.want == "" {
				if errors != "" {
					t.Fatalf("unexpected errors:\n%s", errors)
				}
			} else if !strings.Contains(errors, tt.want) {
				t.Fatalf("missing %q error:\n%s", tt.want, errors)
			}
		})
	}
}

func TestMutableGlobalGrantsTransitiveAndTrusted(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		grant := "can Global.Read"
		relayBody := "    can Global.Read:\n        return reader()"
		if trusted {
			grant = "trusted Global.Read"
			// No effect remains to authorize here. An explicit can would itself
			// contribute a tracked caller contract even around this pure call.
			relayBody = "    return reader()"
		}
		src := "global mutable hot: i32 = 0\ndef reader() -> i32:\n    " + grant + ":\n        return hot\ndef relay() -> i32:\n" + relayBody + "\ndef main() -> i32:\n    return relay()\n"
		result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "transitive.elisa", src, AnalyzeOptions{})
		errors := strings.Join(result.Errors(), "\n")
		if trusted {
			if errors != "" {
				t.Fatalf("trusted reader must discharge mutable global propagation:\n%s", errors)
			}
		} else if !strings.Contains(errors, `call to "relay"`) {
			t.Fatalf("missing transitive grant error:\n%s", errors)
		}
	}
}

func TestImmutableGlobalReadRemainsAdvisoryByDefault(t *testing.T) {
	result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, "immutable_global.elisa", `global hot: i32 = 1
const LIMIT: i32 = 2
def reader() -> i32:
    return hot + LIMIT
def main() -> i32:
    return reader()
`, AnalyzeOptions{})
	if errors := strings.Join(result.Errors(), "\n"); errors != "" {
		t.Fatalf("immutable global/const gained errors:\n%s", errors)
	}
}

func TestMutableGlobalTargetIndexAndReferenceWrites(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"index_read", `global mutable slots: i32[2] = [0, 0]
global mutable cursor: usize = 0
def store():
    trusted Unsafe.UncheckedIndex:
        can Global.Write:
            slots[cursor] <- 1
`, "mutable global read"},
		{"parenthesized_index_read", `global mutable slots: i32[2] = [0, 0]
global mutable cursor: usize = 0
def store():
    trusted Unsafe.UncheckedIndex:
        can Global.Write:
            (slots[cursor]) <- 1
`, "mutable global read"},
		{"return_mutable_ref", `global mutable hot: i32 = 0
def borrow() -> mutable i32&:
    can Global.Read:
        return &hot
`, "mutable global write"},
		{"return_readonly_ref", `global mutable hot: i32 = 0
def borrow() -> i32&:
    trusted Global.Read:
        return &hot
`, ""},
		{"generic_transitive", `global mutable hot: i32 = 0
def reader[T](value: T) -> i32:
    can Global.Read:
        return hot
def caller() -> i32:
    return reader[i32](0)
`, "call to"},
		{"lambda_read", `global mutable hot: i32 = 0
def caller() -> i32:
    f = fn() => hot
    return f()
`, "mutable global read"},
		{"lambda_granted", `global mutable hot: i32 = 0
def caller() -> i32:
    can Global.Read:
        f = fn() => hot
        return f()
`, ""},
		{"callback_argument", `global mutable hot: i32 = 0
def read_hot() -> i32:
    can Global.Read:
        return hot
def apply(callback: fn() -> i32 can[Global.Read]) -> i32:
    can Global.Read:
        return callback()
def caller() -> i32:
    return apply(read_hot)
`, "callback argument"},
		{"callback_granted", `global mutable hot: i32 = 0
def read_hot() -> i32:
    can Global.Read:
        return hot
def apply(callback: fn() -> i32 can[Global.Read]) -> i32:
    can Global.Read:
        return callback()
def caller() -> i32:
    can Global.Read:
        return apply(read_hot)
`, ""},
		{"global_arena_write", `global mutable arena: Arena = zeroed
def append_value(out: mutable darray[i32]&):
    can Global.Read:
        in arena:
            out.push(1)
`, "mutable global write"},
		{"reference_write", `global mutable hot: i32 = 0
def set(value: mutable i32&):
    value <- 1
def store():
    can Global.Read:
        set(&hot)
`, "mutable global write"},
		{"reference_granted", `global mutable hot: i32 = 0
def set(value: mutable i32&):
    value <- 1
def store():
    trusted Global{Read, Write}:
        set(&hot)
`, ""},
		{"nested_shadow", `global mutable hot: i32 = 0
def reader() -> i32:
    if true:
        hot: i32 = 1
    return hot
`, "mutable global read"},
		{"qualified", `module State:
    public:
        global mutable hot: i32 = 0
def reader() -> i32:
    return State::hot
`, "mutable global read"},
		{"container", `global mutable values: darray[i32] = []
def store():
    can Global.Read:
        values.clear()
`, "mutable global write"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, tt.name+".elisa", tt.src, AnalyzeOptions{})
			errors := strings.Join(result.Errors(), "\n")
			if tt.want == "" {
				if errors != "" {
					t.Fatalf("unexpected errors:\n%s", errors)
				}
			} else if !strings.Contains(errors, tt.want) {
				t.Fatalf("missing %q error:\n%s", tt.want, errors)
			}
		})
	}
}

func TestMutableGlobalLoweredDefaultsAndMethods(t *testing.T) {
	tests := []struct{ name, source, want string }{
		{"branch_callback", `global mutable hot: i32 = 0
def reader() -> i32:
    can Global.Read:
        return hot
def pure() -> i32:
    return 0
def test(condition: bool) -> i32:
    f: mutable fn() -> i32 can[Global.Read] = pure
    if condition:
        f <- reader
    else:
        f <- pure
    return f()
`, "requires can[Global]"},
		{"default_call", `global mutable hot: i32 = 0
def reader() -> i32:
    can Global.Read:
        return hot
def consume(x: i32 = reader()) -> i32:
    return x
def test() -> i32:
    return consume()
`, "requires can[Global]"},
		{"default_callback", `global mutable hot: i32 = 0
def reader() -> i32:
    can Global.Read:
        return hot
def consume(f: fn() -> i32 can[Global.Read] = reader) -> i32:
    can Global.Read:
        return f()
def test() -> i32:
    return consume()
`, "requires can[Global]"},
		{"default_granted", `global mutable hot: i32 = 0
def reader() -> i32:
    can Global.Read:
        return hot
def consume(x: i32 = reader()) -> i32:
    return x
def test() -> i32:
    can Global.Read:
        return consume()
`, ""},
		{"default_nested", `global mutable hot: i32 = 0
def reader() -> i32:
    can Global.Read:
        return hot
def consume(x: i32 = reader()) -> i32:
    return x
def nested(x: i32 = consume()) -> i32:
    return x
def test() -> i32:
    return nested()
`, "requires can[Global]"},
		{"default_read", `global mutable hot: i32 = 0
def reader() -> i32:
    can Global.Read:
        return hot
def consume(x: i32 = hot) -> i32:
    return x
def test() -> i32:
    return consume()
`, "mutable global read"},
		{"default_unused_callback", `global mutable hot: i32 = 0
def reader() -> i32:
    can Global.Read:
        return hot
def consume(f: fn() -> i32 can[Global.Read] = reader) -> i32:
    return 0
def test() -> i32:
    return consume()
`, "requires can[Global]"},
		{"generic", `global mutable hot: i32 = 0
def reader() -> i32:
    can Global.Read:
        return hot
def read_generic[T](value: T) -> i32:
    can Global.Read:
        return hot
def test(value: i32) -> i32:
    return read_generic[i32](value)
`, "requires can[Global]"},
		{"method", `global mutable hot: i32 = 0
def reader() -> i32:
    can Global.Read:
        return hot
struct Box:
    value: i32
impl Box:
    def read(self: Box) -> i32:
        can Global.Read:
            return hot
def test(box: Box) -> i32:
    return box.read()
`, "requires can[Global]"},
		{"paren", `global mutable hot: i32 = 0
def reader() -> i32:
    can Global.Read:
        return hot
def test() -> i32:
    return (reader)()
`, "requires can[Global]"},
		{"factory", `global mutable hot: i32 = 0
def reader() -> i32:
    can Global.Read:
        return hot
def factory() -> fn() -> i32 can[Global.Read]:
    return reader
def test() -> i32:
    f = factory()
    return f()
`, "requires can[Global]"},
		{"factory_granted", `global mutable hot: i32 = 0
def reader() -> i32:
    can Global.Read:
        return hot
def factory() -> fn() -> i32 can[Global.Read]:
    return reader
def test() -> i32:
    f = factory()
    can Global.Read:
        return f()
`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, tt.name+".elisa", tt.source, AnalyzeOptions{})
			errors := strings.Join(result.Errors(), "\n")
			if tt.want == "" {
				if errors != "" {
					t.Fatalf("unexpected errors: %s", errors)
				}
			} else if !strings.Contains(errors, tt.want) {
				t.Fatalf("missing %q: %s", tt.want, errors)
			}
		})
	}
}

func TestMutableGlobalCallbackContainersAndLoopJumps(t *testing.T) {
	tests := []struct{ name, source, want string }{
		{"optional_factory", `global mutable hot: i32 = 0
def writer() -> i32:
    can Global.Write:
        hot <- 1
    return 0
def pure() -> i32:
    return 0
type Action = fn() -> i32 can[Global.Write]
def optional_factory(condition: bool) -> Action?:
    if condition:
        return writer
    return null
def test(condition: bool) -> i32:
    action = optional_factory(condition)
    if action is callback:
        can Global.Read:
            return callback()
    return 0
`, "add can Global.Write"},
		{"alias_factory", `global mutable hot: i32 = 0
def writer() -> i32:
    can Global.Write:
        hot <- 1
    return 0
def pure() -> i32:
    return 0
type Action = fn() -> i32 can[Global.Write]
def factory() -> Action:
    callback = writer
    return callback
def test() -> i32:
    callback = factory()
    can Global.Read:
        return callback()
`, "add can Global.Write"},
		{"branch", `global mutable hot: i32 = 0
def writer() -> i32:
    can Global.Write:
        hot <- 1
    return 0
def pure() -> i32:
    return 0
def test(condition: bool) -> i32:
    callback: mutable fn() -> i32 can[Global.Write] = pure
    if condition:
        callback <- writer
    else:
        callback <- pure
    can Global.Read:
        return callback()
`, "add can Global.Write"},
		{"field", `global mutable hot: i32 = 0
def writer() -> i32:
    can Global.Write:
        hot <- 1
    return 0
def pure() -> i32:
    return 0
struct Holder:
    action: fn() -> i32 can[Global.Write]
def test() -> i32:
    holder = Holder{action: writer}
    can Global.Read:
        return holder.action()
`, "add can Global.Write"},
		{"for_break", `global mutable hot: i32 = 0
def writer() -> i32:
    can Global.Write:
        hot <- 1
    return 0
def pure() -> i32:
    return 0
def test(condition: bool) -> i32:
    callback: mutable fn() -> i32 can[Global.Write] = pure
    for index in 0..<1:
        callback <- writer
        break
    can Global.Read:
        return callback()
`, "add can Global.Write"},
		{"global", `global mutable hot: i32 = 0
def writer() -> i32:
    can Global.Write:
        hot <- 1
    return 0
def pure() -> i32:
    return 0
global mutable action: fn() -> i32 can[Global.Write] = writer
def test() -> i32:
    can Global.Read:
        return action()
`, "add can Global.Write"},
		{"loop", `global mutable hot: i32 = 0
def writer() -> i32:
    can Global.Write:
        hot <- 1
    return 0
def pure() -> i32:
    return 0
def test(condition: bool) -> i32:
    callback: mutable fn() -> i32 can[Global.Write] = pure
    while condition:
        callback <- writer
        break
    can Global.Read:
        return callback()
`, "add can Global.Write"},
		{"loop_continue", `global mutable hot: i32 = 0
def writer() -> i32:
    can Global.Write:
        hot <- 1
    return 0
def pure() -> i32:
    return 0
def test(condition: bool) -> i32:
    callback: mutable fn() -> i32 can[Global.Write] = pure
    while condition:
        callback <- writer
        continue
    can Global.Read:
        return callback()
`, "add can Global.Write"},
		{"loop_granted", `global mutable hot: i32 = 0
def writer() -> i32:
    can Global.Write:
        hot <- 1
    return 0
def pure() -> i32:
    return 0
def test(condition: bool) -> i32:
    callback: mutable fn() -> i32 can[Global.Write] = pure
    while condition:
        callback <- writer
        break
    can Global.Read, Global.Write:
        return callback()
`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := analyzePermissionGrantTestSourceAllowingErrorsWithOptions(t, tt.name+".elisa", tt.source, AnalyzeOptions{})
			errors := strings.Join(result.Errors(), "\n")
			if tt.want == "" {
				if errors != "" {
					t.Fatalf("unexpected errors: %s", errors)
				}
			} else if !strings.Contains(errors, tt.want) {
				t.Fatalf("missing %q: %s", tt.want, errors)
			}
		})
	}
}
