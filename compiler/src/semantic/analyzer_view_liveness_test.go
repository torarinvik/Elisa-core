package semantic

import (
	"strings"
	"testing"
)

// B2 early free must count every local derived from the object as a use of it: a view, an enum
// payload, a tuple or a closure built from `buf` still points into buf's arena. Freeing buf's stack
// at its own last mention (the push) left each of these reading freed memory at run time.
func TestRegionStacksDoesNotEarlyFreeObjectWithLiveDerivedLocal(t *testing.T) {
	cases := map[string]string{
		"sview": `def f() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        buf: mutable darray[u8] = [65.u8()]
        v: sview = buf.as_sview()
        buf.push(1.u8())
        return v[0].i64()
`,
		"enum_payload": `enum Tok:
    Word(text: sview)
    Nil

def f() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        buf: mutable darray[u8] = [65.u8()]
        t: Tok = Tok.Word(buf.as_sview())
        buf.push(1.u8())
        return match t:
            Tok.Word(text): text[0].i64()
            Tok.Nil: 0
`,
		"transitive": `def f() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        buf: mutable darray[u8] = [65.u8()]
        v: sview = buf.as_sview()
        w: sview = v
        buf.push(1.u8())
        return w[0].i64()
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "b2_derived_"+name+".elisa", src, AnalyzeOptions{})
			asn := onlyRegionStack(t, result)
			if _, ok := asn.StackEarlyFreeAfter[asn.StackOf["buf"]]; ok {
				t.Fatalf("buf has a live derived local and must not be early-freed, got %v", asn.StackEarlyFreeAfter)
			}
		})
	}
}

// A view carried in an enum payload depends on its source exactly like a bare view: growing the
// source over a relocating (chained) backing invalidates it.
func TestAnalyzeEnumPayloadViewInvalidatedBySourcePush(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "enum_payload_view_invalidated.elisa", `enum Tok:
    Word(text: sview)
    Nil

def build(owner: Arena) -> i64:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        items: mutable darray[u8] = [65.u8()]
        t: Tok = Tok.Word(items.as_sview())
        items.push(1.u8())
        return match t:
            Tok.Word(text): text[0].i64()
            Tok.Nil: 0
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `"t" cannot be used: storage dependency facts were invalidated by darray push of items`) {
		t.Fatalf("expected the enum-payload view to be invalidated by the push, got:\n%s", all)
	}
}

// A closure nested in a returned tuple captures a local container's header; the hidden region
// parameter is never threaded through a function value, so the capture is freed at return.
func TestAnalyzeRejectsClosureInReturnedTupleCapturingLocal(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "closure_tuple_return.elisa", `def make() -> (a: i64, f: fn() -> i64):
    buf: mutable darray[i64] = [7]
    r: darray[i64]& = &buf
    return (1, fn() => r[0])
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "cannot return value: region dependency facts include local region") {
		t.Fatalf("expected the tuple-wrapped closure return to be rejected, got:\n%s", all)
	}
}

func TestAnalyzeAcceptsScalarClosureInReturnedTuple(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "closure_tuple_scalar.elisa", `def make(k: i64) -> (a: i64, f: fn() -> i64):
    return (1, fn() => k + 6)
`, AnalyzeOptions{})
	if all := strings.Join(result.Errors(), "\n"); all != "" {
		t.Fatalf("a closure capturing only a scalar must be accepted, got:\n%s", all)
	}
}

// A view stored INTO a container (`vs.push(s)`, `vs.extend([s])`, `keep(&vs, s)`, or a closure
// capturing it pushed into a darray of structs) keeps the source alive as long as the container:
// early-freeing buf after its own last mention left `vs[0]` reading freed memory (segfault).
func TestRegionStacksDoesNotEarlyFreeObjectStoredIntoContainer(t *testing.T) {
	prefix := `struct H:
    f: fn() -> i64

def keep(out: mutable darray[sview]&, s: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        out.push(s)

def f() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        vs: mutable darray[sview] = []
        hs: mutable darray[H] = []
        buf: mutable darray[u8] = [65.u8()]
        s: sview = buf.as_sview()
`
	cases := map[string]string{
		"push":    "        vs.push(s)\n        buf.push(1.u8())\n        return vs[0][0].i64()\n",
		"extend":  "        vs.extend([s])\n        buf.push(1.u8())\n        return vs[0][0].i64()\n",
		"addr_of": "        keep(&vs, s)\n        buf.push(1.u8())\n        return vs[0][0].i64()\n",
		"closure": "        hs.push(H{f: fn() => s[0].i64()})\n        buf.push(1.u8())\n        return hs[0].f()\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "b2_store_"+name+".elisa", prefix+body, AnalyzeOptions{})
			for _, asn := range result.RegionStacks {
				if stack, ok := asn.StackOf["buf"]; ok {
					if _, freed := asn.StackEarlyFreeAfter[stack]; freed {
						t.Fatalf("buf is reachable through a container and must not be early-freed, got %v", asn.StackEarlyFreeAfter)
					}
				}
			}
		})
	}
}

// A view stored into a field or element of a local, pushed into a darray, or produced by a
// `.cast[...] can ...` stays tied to its backing: growing the backing over a relocating
// (chained) arena invalidates reads through the holder and through copies of the holder.
func TestAnalyzeViewStoredIntoHolderInvalidatedBySourcePush(t *testing.T) {
	prefix := `struct Holder:
    s: mutable sview
    n: i64

def build(owner: Arena) -> i64 can[Unsafe.PointerCast]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        buf: mutable darray[u8] = []
        buf.push(65.u8())
        h: mutable Holder = Holder{s: "", n: 0}
        vs: mutable darray[sview] = [""]
        ws: mutable darray[sview] = []
        c: mutable u8& = &buf[0]
`
	cases := map[string][2]string{
		"field_store": {"        h.s <- buf.as_sview()\n        buf.push(1.u8())\n        return h.s[0].i64()\n", `"h" cannot be used`},
		"index_store": {"        vs[0] <- buf.as_sview()\n        buf.push(1.u8())\n        return vs[0][0].i64()\n", `"vs" cannot be used`},
		"cast_rebind": {"        c <- (&buf[0]).cast[u8&] can Unsafe.PointerCast\n        buf.push(1.u8())\n        return c.i64()\n", `"c" cannot be used`},
		"holder_copy": {"        ws.push(buf.as_sview())\n        xs: darray[sview] = ws\n        buf.push(1.u8())\n        return xs[0][0].i64()\n", `"xs" cannot be used`},
		"struct_copy": {"        h.s <- buf.as_sview()\n        g: Holder = h\n        buf.push(1.u8())\n        return g.s[0].i64()\n", `"g" cannot be used`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "holder_view_"+name+".elisa", prefix+c[0])
			all := strings.Join(result.Errors(), "\n")
			if !strings.Contains(all, c[1]+": storage dependency facts were invalidated by darray push of buf") {
				t.Fatalf("expected stale-view error %s, got:\n%s", c[1], all)
			}
		})
	}
}

// Field-path keys: growing one darray field of a root cannot move a sibling field's buffer when
// the two element types differ (no shallow copy can make a darray[u8] and a darray[u32] share a
// backing). Same-typed siblings may share one (`P{storage: buf, lines: buf}`), so they still
// overlap, as do growth of the viewed field itself and replacement of the whole root.
func TestAnalyzeFieldPathViewSiblingGrowth(t *testing.T) {
	prefix := `struct P:
    storage: mutable darray[u8]
    lines: mutable darray[u32]
    bytes: mutable darray[u8]
    names: mutable darray[sview]

def bytes_view_range(buf: darray[u8], start: usize, count: usize) -> sview:
    return buf.as_sview()

def grow(p: lmut P) -> u8:
    view: sview = bytes_view_range(p.storage, 0, p.storage.count)
`
	cases := map[string][2]string{
		"other_elem_type":  {"    p.lines <- p.lines.push(3.u32())\n    p.lines.push(4.u32())\n    return view[0]\n", ""},
		"sview_elem_type":  {"    p.names <- p.names.push(view)\n    return view[0]\n", ""},
		"same_elem_type":   {"    p.bytes <- p.bytes.push(3.u8())\n    return view[0]\n", `"view" cannot be used: storage dependency facts were invalidated by darray push of p.bytes`},
		"viewed_field":     {"    p.storage <- p.storage.push(3.u8())\n    return view[0]\n", `"view" cannot be used: storage dependency facts were invalidated by darray push of p.storage`},
		"root_replacement": {"    p <- P{storage: [], lines: [], bytes: [], names: []}\n    return view[0]\n", `"view" cannot be used: storage dependency facts were invalidated by reassignment of p`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "field_path_view_"+name+".elisa", prefix+c[0])
			all := strings.Join(result.Errors(), "\n")
			if c[1] == "" {
				if strings.Contains(all, "cannot be used") {
					t.Fatalf("unexpected stale-view error:\n%s", all)
				}
				return
			}
			if !strings.Contains(all, c[1]) {
				t.Fatalf("expected %s, got:\n%s", c[1], all)
			}
		})
	}
}

// A view returned by a user function depends on the argument storage the callee's returns
// borrow from, traced syntactically through returns, locals, field paths and nested calls
// (impl methods union over every impl). Growth of exactly that storage invalidates the view;
// growth of an unrelated sibling, or of a darray that only HOLDS copies of views, does not.
func TestAnalyzeUserCallReturnedViewGrowth(t *testing.T) {
	prefix := `struct H:
    buf: mutable darray[u8]
    other: mutable darray[u32]
    views: mutable darray[sview]

protocol Named:
    def name(self: Self&) -> sview

impl Named for H:
    def name(self: Self&) -> sview:
        return self.buf.as_sview()

def bytes_view_range(buf: darray[u8], start: usize, count: usize) -> sview:
    return buf.as_sview()

def head(h: H&) -> sview:
    v: sview = bytes_view_range(h.buf, 0, h.buf.count)
    return v

def first(h: H&) -> sview:
    return h.views[0]

def via[T: Named](x: T&) -> sview:
    return x.name()

`
	cases := map[string][2]string{
		"free_fn_grow":       {"def f(h: lmut H) -> u8:\n    v: sview = head(&h)\n    h.buf <- h.buf.push(1.u8())\n    return v[0]\n", `"v" cannot be used: storage dependency facts were invalidated by darray push of h.buf`},
		"ufcs_grow":          {"def f(h: lmut H) -> u8:\n    v: sview = h.head()\n    h.buf <- h.buf.push(1.u8())\n    return v[0]\n", `"v" cannot be used`},
		"impl_method_grow":   {"def f(h: lmut H) -> u8:\n    v: sview = h.name()\n    h.buf <- h.buf.push(1.u8())\n    return v[0]\n", `"v" cannot be used`},
		"generic_dispatch":   {"def f(h: lmut H) -> u8:\n    v: sview = via(&h)\n    h.buf <- h.buf.push(1.u8())\n    return v[0]\n", `"v" cannot be used`},
		"sibling_grow":       {"def f(h: lmut H) -> u8:\n    v: sview = head(&h)\n    h.other <- h.other.push(1.u32())\n    return v[0]\n", ""},
		"sibling_elem_store": {"def f(h: lmut H) -> u8:\n    v: sview = head(&h)\n    h.other[0] <- 2.u32()\n    return v[0]\n", ""},
		"view_holder_grow":   {"def f(h: lmut H) -> u8:\n    v: sview = first(&h)\n    h.views <- h.views.push(\"x\")\n    return v[0]\n", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "user_call_view_"+name+".elisa", prefix+c[0])
			all := strings.Join(result.Errors(), "\n")
			if c[1] == "" {
				if strings.Contains(all, "cannot be used") {
					t.Fatalf("unexpected stale-view error:\n%s", all)
				}
				return
			}
			if !strings.Contains(all, c[1]) {
				t.Fatalf("missing %q in:\n%s", c[1], all)
			}
		})
	}
}

// A generic callee's template summary says nothing about a `T` result, so `id(view_of_local)`
// used to return a view of freed storage; the syntactic return origins carry the argument's
// region through, exactly as for the non-generic `id(x: sview)`.
func TestAnalyzeGenericIdentityReturnsLocalView(t *testing.T) {
	src := `def id[T](x: T) -> T:
    return x

def leak() -> sview:
    buf: darray[u8] = [65.u8(), 66.u8()]
    return id(buf.as_sview())
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "generic_identity_local_view.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "cannot return value: region dependency facts include local region") {
		t.Fatalf("missing local-region escape in:\n%s", all)
	}
}
