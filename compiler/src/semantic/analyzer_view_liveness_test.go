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
