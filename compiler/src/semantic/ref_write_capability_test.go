package semantic

import (
	"strings"
	"testing"
)

// Writing THROUGH a reference is the reference type's capability (`mutable T&`). A binding's
// `mutable` only makes the slot rebindable. Each program below wrote through a read-only
// reference -- a string literal (read-only storage: SIGBUS), a const, or a value the caller
// lent read-only -- because a `mutable` binding or parameter was taken as writability.
func TestRebindableReadOnlyRefCannotWriteThrough(t *testing.T) {
	const note = "lets it be re-pointed, not written through"
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"literal_index_store", "def main() -> i32:\n    mutable x: u8& = \"abc\"\n    x[0] <- 65\n    x[0].i32()\n", "cannot mutate through readonly ref"},
		{"field_store", "struct S:\n    a: mutable i64\n\ndef main() -> i32:\n    s: S = S{a: 1}\n    mutable r: S& = &s\n    r.a <- 5\n    s.a.i32()\n", "cannot mutate through readonly ref"},
		{"const_field_store", "struct S:\n    a: mutable i64\n\nconst K: S = S{a: 1}\n\ndef main() -> i32:\n    mutable r: S& = &K\n    r.a <- 5\n    K.a.i32()\n", "cannot mutate through readonly ref"},
		{"argument", "def poke(p: mutable u8&) -> void:\n    p <- 66\n\ndef main() -> i32:\n    mutable x: u8& = \"abc\"\n    poke(x)\n    0\n", `argument 1 to "poke" expects mutable u8&, got u8&`},
		{"struct_argument", "struct S:\n    a: mutable i64\n\ndef bump(r: mutable S&) -> void:\n    r.a <- 7\n\ndef main() -> i32:\n    s: S = S{a: 1}\n    mutable r: S& = &s\n    bump(r)\n    s.a.i32()\n", `argument 1 to "bump" expects mutable S&, got S&`},
		{"global_argument", "global mutable g: u8& = \"abc\"\n\ndef poke(p: mutable u8&) -> void:\n    p <- 66\n\ndef main() -> i32:\n    poke(g)\n    0\n", `argument 1 to "poke" expects mutable u8&, got u8&`},
		{"parameter_store", "def poke(mutable p: u8&) -> void:\n    p[0] <- 65\n\ndef main() -> i32:\n    poke(\"abc\")\n    0\n", "cannot mutate through readonly ref"},
		{"parameter_forward", "def poke(p: mutable u8&) -> void:\n    p <- 66\n\ndef outer(mutable q: u8&) -> void:\n    poke(q)\n\ndef main() -> i32:\n    outer(\"abc\")\n    0\n", `argument 1 to "poke" expects mutable u8&, got u8&`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ref_rebindable_"+tc.name+".elisa", tc.src, AnalyzeOptions{})
			joined := strings.Join(result.Errors(), "\n")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("a rebindable read-only reference must not be written through; want %q, got:\n%s", tc.want, joined)
			}
			if !strings.Contains(joined, note) {
				t.Fatalf("want the note explaining the binding's `mutable` (%q), got:\n%s", note, joined)
			}
		})
	}
}

// A legacy `x: mutable T& = init` binding is writable only when its initializer is: before, the
// binding's `mutable` promoted any initializer, so an alias of a string literal, of a read-only
// parameter, of a read-only field or of a read-only call result could be written through.
func TestLegacyMutableRefBindingNeedsWritableInitializer(t *testing.T) {
	const note = "was initialized from a read-only reference"
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"literal", "def main() -> i32:\n    x: mutable u8& = \"abc\"\n    x <- 5\n    0\n", "cannot assign int to u8&"},
		{"readonly_local", "def main() -> i32:\n    s: u8& = \"abc\"\n    x: mutable u8& = s\n    x <- 5\n    0\n", "cannot assign int to u8&"},
		{"legacy_chain", "def main() -> i32:\n    s: mutable static u8& = \"abc\"\n    x: mutable static u8& = s\n    x <- 5\n    0\n", "cannot assign int to static u8&"},
		{"storage_qualified", "def main() -> i32:\n    s: static u8& = \"abc\"\n    x: mutable static u8& = s\n    x <- 5\n    0\n", "cannot assign int to static u8&"},
		{"storage_dropped", "def main() -> i32:\n    s: mutable static u8& = \"abc\"\n    x: mutable u8& = s\n    x <- 5\n    0\n", "cannot assign int to u8&"},
		{"coercion_cast", "def main() -> i32:\n    x: mutable u8& = \"abc\".cast[u8&]\n    x[0] <- 65\n    x[0].i32()\n", "cannot mutate through readonly ref"},
		{"readonly_field", "struct S:\n    name: u8&\n\ndef main() -> i32:\n    s: S = S{name: \"abc\"}\n    x: mutable u8& = s.name\n    x[0] <- 65\n    x[0].i32()\n", "cannot mutate through readonly ref"},
		{"readonly_call", "def id(p: u8&) -> u8&:\n    p\n\ndef main() -> i32:\n    x: mutable u8& = id(\"abc\")\n    x[0] <- 65\n    x[0].i32()\n", "cannot mutate through readonly ref"},
		{"region_readonly_alias", "def fill(a: mutable Arena&) -> i64:\n    1\n\ndef main() -> i64:\n    region r(4096):\n        ro: Arena& = (&r).cast[Arena&]\n        x: mutable Arena& = ro\n        return fill(x)\n    0\n", `argument 1 to "fill" expects mutable Arena&, got Arena&`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ref_legacy_"+tc.name+".elisa", tc.src, AnalyzeOptions{})
			joined := strings.Join(result.Errors(), "\n")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("a legacy mutable binding of a read-only reference must stay read-only; want %q, got:\n%s", tc.want, joined)
			}
			if !strings.Contains(joined, note) {
				t.Fatalf("want the note explaining the initializer (%q), got:\n%s", note, joined)
			}
		})
	}
}

// The note's suggested type must be one source can write: the printer's `static mutable u8&`
// does not parse, `mutable static u8&` does.
func TestReadOnlyRefNoteSuggestsParseableType(t *testing.T) {
	src := "def main() -> i32:\n    s: static u8& = \"abc\"\n    x: mutable static u8& = s\n    x <- 5\n    0\n"
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ref_note_spelling.elisa", src, AnalyzeOptions{})
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "(mutable static u8&)") || strings.Contains(joined, "static mutable u8&") {
		t.Fatalf("want the suggestion spelled `mutable static u8&`, got:\n%s", joined)
	}
}

// `<-` through a `u8&` stores ONE byte. Storing a pointer there truncated the address to its low
// byte (and through a `static u8&` literal, wrote into read-only storage).
func TestBytePointerStoreThroughByteRefRejected(t *testing.T) {
	const want = "cannot store a byte pointer"
	cases := []struct {
		name string
		src  string
	}{
		{"literal", "def f(r: mutable u8&) -> i64:\n    r <- \"abc\"\n    0\n"},
		{"pointer", "def f(r: mutable u8&, s: u8&) -> i64:\n    r <- s\n    0\n"},
		{"static_out_parameter", "def set_name(out: mutable static u8&, v: static u8&) -> void:\n    out <- v\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ref_byte_store_"+tc.name+".elisa", tc.src, AnalyzeOptions{})
			joined := strings.Join(result.Errors(), "\n")
			if !strings.Contains(joined, want) {
				t.Fatalf("want %q, got:\n%s", want, joined)
			}
		})
	}
}

// A string literal is read-only storage, so it is never a `mutable u8&`.
func TestStringLiteralIsNotAWritableRef(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"store", "def main() -> i32:\n    b: mutable u8 = 1\n    x: mutable u8& = &b\n    x <- \"abc\"\n    b.i32()\n", "cannot assign static u8& to mutable u8&"},
		{"argument", "def f(r: mutable u8&) -> i32:\n    r <- 7\n    0\n\ndef main() -> i32:\n    f(\"abc\")\n", `argument 1 to "f" expects mutable u8&, got static u8&`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ref_literal_"+tc.name+".elisa", tc.src, AnalyzeOptions{})
			joined := strings.Join(result.Errors(), "\n")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("want %q, got:\n%s", tc.want, joined)
			}
		})
	}
}

// POSITIVE: writable references keep working -- through a writable initializer, a rebind, an
// explicit `mutable T&` type, a C-string out-parameter, a `zeroed` placeholder, a ternary of
// writable arms, a narrowed optional field, and an Unsafe-granted cast.
func TestWritableRefBindingsStillCompile(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"legacy_from_address", "def f() -> i64:\n    v: mutable i64 = 1\n    x: mutable i64& = &v\n    x <- 5\n    v\n"},
		{"legacy_rebind_then_write", "def f() -> i64:\n    v: mutable i64 = 1\n    w: mutable i64 = 7\n    x: mutable i64& = &v\n    x <- &w\n    x <- 9\n    v * 10 + w\n"},
		{"explicit_writable_type", "struct S:\n    a: mutable i64\n\ndef f() -> i64:\n    s: mutable S = S{a: 1}\n    mutable r: mutable S& = &s\n    r.a <- 5\n    s.a\n"},
		{"explicit_rebind_readonly", "def f() -> i64:\n    mutable x: u8& = \"a\"\n    x <- \"b\"\n    x[0].i64()\n"},
		{"byte_copy", "def f(r: mutable u8&, s: u8&) -> i64:\n    r <- s[0]\n    0\n"},
		{"cstr_out_parameter", "def set_name(out: mutable cstr&) -> void:\n    out <- \"hello\"\n\ndef f() -> i64:\n    name: mutable cstr = \"zz\"\n    set_name(&name)\n    name[1].i64()\n"},
		{"zeroed_placeholder", "struct S:\n    v: mutable i64\n\ndef f() -> i64:\n    s: mutable S = S{v: 1}\n    x: mutable S& = zeroed\n    x as & <- &s\n    x.v <- 7\n    s.v\n"},
		{"ternary_writable_arms", "struct S:\n    v: mutable i64\n\ndef f(a: mutable S&, b: mutable S&, c: bool) -> i64:\n    mutable x: mutable S& = a if c else b\n    x.v <- 7\n    x.v\n"},
		{"legacy_ternary_writable_arms", "struct S:\n    v: mutable i64\n\ndef f(a: mutable S&, b: mutable S&, c: bool) -> i64:\n    x: mutable S& = a if c else b\n    x.v <- 7\n    x.v\n"},
		{"narrowed_field", "struct S:\n    v: mutable i64\n\nstruct H:\n    o: mutable S&?\n\ndef f(h: mutable H&) -> i64:\n    if h.o != null:\n        mutable x: mutable S& = h.o\n        x.v <- 7\n        return x.v\n    0\n"},
		{"unsafe_cast_alias", "def f(v: void&) -> i64:\n    trusted Unsafe.PointerCast:\n        x: mutable u8& = v.cast[mutable u8&]\n        x[0] <- 65\n        return x[0].i64()\n"},
		// The scope owns a region's arena; `&r` types read-only only because r cannot be reassigned.
		{"region_arena_address", "def fill(a: mutable Arena&) -> i64:\n    1\n\ndef f() -> i64:\n    region r(4096):\n        a: mutable Arena& = &r\n        return fill(a)\n    0\n"},
		{"cstr_out_parameter_static_slot", "def set_name(out: mutable cstr&) -> void:\n    out <- \"hello\"\n\ndef f() -> i64:\n    name: mutable static u8& = \"zz\"\n    set_name(&name)\n    name[1].i64()\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ref_ok_"+tc.name+".elisa", tc.src, AnalyzeOptions{})
			if errs := result.Errors(); len(errs) != 0 {
				t.Fatalf("a writable reference must stay writable, got: %v", errs)
			}
		})
	}
}

// A generic `p.cast[T&]` is a reinterpret in every instantiation but one: `reinterpret[Box](&v)`
// forged a `Box&` over an i64 with no Unsafe anywhere (a segfault), because a type parameter's
// assignability leniency counted as a coercion. Only a cast that keeps the pointee is one.
func TestGenericReinterpretCastRequiresUnsafe(t *testing.T) {
	const want = "pointer cast requires can[Unsafe]"
	cases := []struct {
		name string
		src  string
	}{
		{"to_type_param", "struct Box:\n    p: i64&\n\ndef reinterpret[T](p: i64&) -> T&:\n    p.cast[T&]\n"},
		{"from_type_param", "struct Node:\n    next: i64\n\ndef erase[T](p: T&) -> Node&:\n    p.cast[Node&]\n"},
		{"between_type_params", "def swap_view[T, U](p: T&) -> U&:\n    p.cast[U&]\n"},
		{"gain_mutability", "def writable[T](p: T&) -> mutable T&:\n    p.cast[mutable T&]\n"},
		// A bare type parameter may itself be a pointer: with T = Node&, each of these forges one.
		{"number_to_bare_param", "def forge[T](addr: u64) -> T:\n    return addr.cast[T]\n"},
		{"number_conversion_to_bare_param", "def forge[T](addr: u64) -> T:\n    return addr.T()\n"},
		{"null_to_bare_param", "def nothing[T]() -> T:\n    return null.cast[T]\n"},
		{"bare_param_to_pointer", "struct Node:\n    next: i64\n\ndef launder[T](p: T) -> Node&:\n    return p.cast[Node&]\n"},
		{"bare_param_to_bare_param", "def swap[T, U](value: T) -> U:\n    return value.cast[U]\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enforced := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "generic_cast_"+tc.name+".elisa", tc.src, AnalyzeOptions{EnforceUnsafePermissions: true})
			if joined := strings.Join(enforced.Errors(), "\n"); !strings.Contains(joined, want) {
				t.Fatalf("under enforcement want %q, got:\n%s", want, joined)
			}
			permissive := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "generic_cast_"+tc.name+".elisa", tc.src, AnalyzeOptions{})
			if joined := strings.Join(permissive.Warnings(), "\n"); !strings.Contains(joined, want) {
				t.Fatalf("an ungranted reinterpret must not be silent; want a %q warning, got:\n%s", want, joined)
			}
		})
	}
}

// POSITIVE: a generic cast that keeps the pointee (dropping mutability) is a coercion, and a
// granted reinterpret compiles under enforcement.
func TestGenericPointerCastCoercionAndGrantCompile(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"drop_mutability", "def readonly[T](p: mutable T&) -> T&:\n    p.cast[T&]\n"},
		{"same_type", "def same[T](p: T&) -> T&:\n    p.cast[T&]\n"},
		{"granted_reinterpret", "def reinterpret[T](p: i64&) -> T&:\n    trusted Unsafe.PointerCast:\n        return p.cast[T&]\n"},
		// A bare type parameter to a number cannot forge a pointer (the std's `flags_mask[T]`).
		{"bare_param_to_number", "def ordinal[T](value: T) -> u64:\n    return value.u64()\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "generic_cast_ok_"+tc.name+".elisa", tc.src, AnalyzeOptions{EnforceUnsafePermissions: true})
			if errs := result.Errors(); len(errs) != 0 {
				t.Fatalf("want no errors, got: %v", errs)
			}
		})
	}
}

// A pointer cast over a recursive enum walked the enum's payloads forever looking for a type
// parameter (a Go stack overflow that killed the compiler).
func TestPointerCastOverRecursiveEnumTerminates(t *testing.T) {
	src := "enum Expr:\n    Leaf(i64)\n    Add(Expr&, Expr&)\n\ndef erase(e: Expr&) -> void&:\n    trusted Unsafe.PointerCast:\n        return e.cast[void&]\n"
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "recursive_enum_cast.elisa", src, AnalyzeOptions{EnforceUnsafePermissions: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("want no errors, got: %v", errs)
	}
}

// `-> mutable T& error[E]` returns a WRITABLE reference. The parser attached `mutable` to the
// whole error union, where it was silently dropped: the function could return a read-only
// reference as writable, and the caller received a read-only one.
func TestFallibleReturnKeepsMutableRef(t *testing.T) {
	const decls = "struct Node:\n    value: mutable i64\n\nerror MemoryError:\n    OutOfMemory\n\n"
	t.Run("readonly_return_rejected", func(t *testing.T) {
		src := decls + "def get(n: Node&) -> mutable Node& error[MemoryError]:\n    return n\n"
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "fallible_mutable_return_bad.elisa", src, AnalyzeOptions{})
		want := "return type expects mutable Node& | MemoryError, got Node&"
		if joined := strings.Join(result.Errors(), "\n"); !strings.Contains(joined, want) {
			t.Fatalf("want %q, got:\n%s", want, joined)
		}
	})
	t.Run("try_result_writable", func(t *testing.T) {
		src := decls + "def get(n: mutable Node&) -> mutable Node& error[MemoryError]:\n    return n\n\ndef bump(n: mutable Node&) -> i64 error[MemoryError]:\n    w: mutable Node& = try get(n)\n    w.value <- w.value + 10\n    r: Node& = try get(n)\n    return r.value\n"
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "fallible_mutable_return_ok.elisa", src, AnalyzeOptions{})
		if errs := result.Errors(); len(errs) != 0 {
			t.Fatalf("want no errors, got: %v", errs)
		}
	})
}
