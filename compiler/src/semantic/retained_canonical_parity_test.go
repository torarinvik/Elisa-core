//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Retained source nodes deliberately retain their declaration-local byte offsets
// (retainedShiftDecl), unlike incremental-session nodes. Also, unchanged non-function
// declarations currently retain old line positions; that separate remapping defect
// must not obscure semantic metadata parity. Canonicalize ONLY lexer.Pos values in
// independent graph copies. The existing strict oracle still checks AST shape and
// sharing, every live published fact, function types/summaries and alias relations.
// Callers must compare original diagnostics and layout/member metadata separately.
func assertRetainedCanonicalLiveResultParity(t testing.TB, cold, warm *Result) {
	t.Helper()
	left, right := cloneRetainedCanonicalResult(cold), cloneRetainedCanonicalResult(warm)
	functions := map[string]*Symbol{}
	for fn, symbol := range right.analyzer.funcDeclSymbols {
		functions[fn.Name] = symbol
	}
	for fn, symbol := range left.analyzer.funcDeclSymbols {
		other := functions[fn.Name]
		if other != nil && !reflect.DeepEqual(symbol.Type, other.Type) {
			t.Fatalf("canonical function type/summary differs for %s: %s", fn.Name, retainedCanonicalFirstDifference(reflect.ValueOf(symbol.Type), reflect.ValueOf(other.Type)))
		}
	}
	assertRetainedLiveResultParity(t, left, right)
}

func retainedCanonicalFirstDifference(left, right reflect.Value) string {
	type pair struct {
		typ  reflect.Type
		a, b uintptr
	}
	seen := map[pair]bool{}
	var visit func(reflect.Value, reflect.Value, string) string
	visit = func(a, b reflect.Value, path string) string {
		if !a.IsValid() || !b.IsValid() {
			if a.IsValid() != b.IsValid() {
				return path + ": validity differs"
			}
			return ""
		}
		if a.Type() != b.Type() {
			return fmt.Sprintf("%s: type %s / %s", path, a.Type(), b.Type())
		}
		switch a.Kind() {
		case reflect.Pointer, reflect.Interface:
			if a.IsNil() || b.IsNil() {
				if a.IsNil() != b.IsNil() {
					return fmt.Sprintf("%s: nil cold=%v warm=%v", path, a.IsNil(), b.IsNil())
				}
				return ""
			}
			if a.Kind() == reflect.Pointer {
				key := pair{a.Type(), a.Pointer(), b.Pointer()}
				if seen[key] {
					return ""
				}
				seen[key] = true
			}
			return visit(a.Elem(), b.Elem(), path+".*")
		case reflect.Struct:
			// Owned copies make private semantic fields readable without
			// changing either source graph.
			x, y := reflect.New(a.Type()).Elem(), reflect.New(b.Type()).Elem()
			x.Set(a)
			y.Set(b)
			for i := 0; i < a.NumField(); i++ {
				if diff := visit(settable(x.Field(i)), settable(y.Field(i)), path+"."+a.Type().Field(i).Name); diff != "" {
					return diff
				}
			}
		case reflect.Slice, reflect.Array:
			if a.Kind() == reflect.Slice && a.IsNil() != b.IsNil() {
				return fmt.Sprintf("%s: slice nil cold=%v warm=%v", path, a.IsNil(), b.IsNil())
			}
			if a.Len() != b.Len() {
				return fmt.Sprintf("%s: length cold=%d warm=%d", path, a.Len(), b.Len())
			}
			for i := 0; i < a.Len(); i++ {
				if diff := visit(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i)); diff != "" {
					return diff
				}
			}
		case reflect.Map:
			if a.IsNil() != b.IsNil() {
				return fmt.Sprintf("%s: map nil cold=%v warm=%v", path, a.IsNil(), b.IsNil())
			}
			if a.Len() != b.Len() {
				return fmt.Sprintf("%s: map size cold=%d warm=%d", path, a.Len(), b.Len())
			}
			iter := a.MapRange()
			for iter.Next() {
				key := iter.Key()
				other := b.MapIndex(key)
				if !other.IsValid() {
					for _, candidate := range b.MapKeys() {
						if reflect.DeepEqual(key.Interface(), candidate.Interface()) {
							return fmt.Sprintf("%s: map key identity differs despite structurally equal %T keys: cold=%+v warm=%+v", path, key.Interface(), key.Interface(), candidate.Interface())
						}
					}
					return fmt.Sprintf("%s: missing key %v", path, key.Interface())
				}
				if diff := visit(iter.Value(), other, fmt.Sprintf("%s[%v]", path, key.Interface())); diff != "" {
					return diff
				}
			}
		default:
			if a.CanInterface() && !reflect.DeepEqual(a.Interface(), b.Interface()) {
				return fmt.Sprintf("%s (%s): cold=%#v warm=%#v", path, a.Type(), a.Interface(), b.Interface())
			}
		}
		return ""
	}
	return visit(left, right, "Value")
}

// cloneRetainedCanonicalResult preserves graph identity, including AST map keys,
// while replacing all source positions with one canonical value. No source AST,
// type, map, slice or analyzer is written during comparison.
func cloneRetainedCanonicalResult(source *Result) *Result {
	type key struct {
		typ reflect.Type
		ptr uintptr
		len int
	}
	seen := map[key]reflect.Value{}
	position := reflect.TypeOf(lexer.Pos{})
	var clone func(reflect.Value) reflect.Value
	clone = func(value reflect.Value) reflect.Value {
		if !value.IsValid() {
			return value
		}
		if value.Type() == position {
			return reflect.Zero(position)
		}
		switch value.Kind() {
		case reflect.Pointer:
			if value.IsNil() {
				return reflect.Zero(value.Type())
			}
			k := key{typ: value.Type(), ptr: value.Pointer()}
			if prior, ok := seen[k]; ok {
				return prior
			}
			out := reflect.New(value.Type().Elem())
			seen[k] = out
			out.Elem().Set(clone(value.Elem()))
			return out
		case reflect.Interface:
			out := reflect.New(value.Type()).Elem()
			if !value.IsNil() {
				out.Set(clone(value.Elem()))
			}
			return out
		case reflect.Struct:
			out := reflect.New(value.Type()).Elem()
			out.Set(value)
			for i := 0; i < out.NumField(); i++ {
				// Read private fields from the owned copy. settable is the
				// established semantic graph-copy helper; the source stays readonly.
				field := settable(out.Field(i))
				field.Set(clone(field))
			}
			return out
		case reflect.Slice:
			if value.IsNil() {
				return reflect.Zero(value.Type())
			}
			k := key{typ: value.Type(), ptr: value.Pointer(), len: value.Len()}
			if prior, ok := seen[k]; ok {
				return prior
			}
			out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
			seen[k] = out
			// Member.Decl pointers can address value elements of AST slices.
			for i := 0; i < value.Len(); i++ {
				if value.Index(i).CanAddr() {
					old := value.Index(i).Addr()
					seen[key{typ: old.Type(), ptr: old.Pointer()}] = out.Index(i).Addr()
				}
			}
			for i := 0; i < value.Len(); i++ {
				out.Index(i).Set(clone(value.Index(i)))
			}
			return out
		case reflect.Array:
			out := reflect.New(value.Type()).Elem()
			for i := 0; i < value.Len(); i++ {
				out.Index(i).Set(clone(value.Index(i)))
			}
			return out
		case reflect.Map:
			if value.IsNil() {
				return reflect.Zero(value.Type())
			}
			k := key{typ: value.Type(), ptr: uintptr(value.UnsafePointer())}
			if prior, ok := seen[k]; ok {
				return prior
			}
			out := reflect.MakeMapWithSize(value.Type(), value.Len())
			seen[k] = out
			iter := value.MapRange()
			for iter.Next() {
				out.SetMapIndex(clone(iter.Key()), clone(iter.Value()))
			}
			return out
		default:
			return value
		}
	}
	result := *source
	fields, originals := reflect.ValueOf(&result).Elem(), reflect.ValueOf(source).Elem()
	for i := 0; i < fields.NumField(); i++ {
		if !fields.Field(i).CanSet() || fields.Type().Field(i).Name == "GlobalScope" {
			continue
		}
		fields.Field(i).Set(clone(originals.Field(i)))
	}
	// The strict oracle only reads this analyzer's function-symbol map.
	result.analyzer = &Analyzer{funcDeclSymbols: clone(reflect.ValueOf(source.analyzer.funcDeclSymbols)).Interface().(map[*ast.FuncDecl]*Symbol)}
	result.shared = nil
	return &result
}

func TestRetainedCanonicalParityCloneIsIndependent(t *testing.T) {
	result := AnalyzeWithOptions(retainedParse(t, retainedBitGroupFixture), AnalyzeOptions{})
	packet := result.NamedTypes["Packet"].(*StructType)
	position := packet.Decl.Position
	copy := cloneRetainedCanonicalResult(result)
	cloned := copy.NamedTypes["Packet"].(*StructType)
	if cloned == packet || cloned.Decl == packet.Decl || cloned.Decl.Position != (lexer.Pos{}) || packet.Decl.Position != position {
		t.Fatal("canonicalization shared or modified source graph")
	}
	group := cloned.Fields["flags"].Type.(*BitGroupType)
	if group.Members[0].Decl != &group.Decl.Members[0] {
		t.Fatal("canonical clone lost member declaration identity")
	}
	cloned.Fields["extra"] = Field{Name: "extra", Type: &BuiltinType{Name: "u64"}}
	if _, ok := packet.Fields["extra"]; ok {
		t.Fatal("canonical clone shared source field map")
	}
	// The source can still be compared with another independent clone.
	assertRetainedCanonicalLiveResultParity(t, result, result)
}

// Diagnostic isolation: no source or record edit is involved. Report the
// invocation-local callee identities before and after a disk/no-op round trip,
// then check the same complete semantic oracle in serial and parallel modes.
func TestRetainedGenericCalleeNoEditCanonicalParity(t *testing.T) {
	for _, mode := range []string{"0", "1"} {
		t.Run("parallel="+mode, func(t *testing.T) {
			t.Setenv("ELISA_PARALLEL_BODIES", mode)
			const source = retainedBitGroupFixture
			initial, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
			logCallees := func(label string, result *Result) {
				for _, decl := range retainedWalkDecls(result.File) {
					if fn, ok := decl.(*ast.FuncDecl); ok {
						walkStoreScanNodes(funcScanStmts(fn), func(node any) {
							if call, ok := node.(*ast.CallExpr); ok {
								if ident, ok := call.Func.(*ast.Ident); ok && ident.Name == "identity" {
									t.Logf("%s %s callee type=%T facts=%#v", label, fn.Name, result.ExprTypes[ident], result.ExprFacts[ident])
								}
							}
						})
					}
				}
			}
			logCallees("initial", initial)
			data, err := state.MarshalState()
			if err != nil {
				t.Fatal(err)
			}
			state, err = RestoreAnalysisState(data)
			if err != nil {
				t.Fatal(err)
			}
			warm, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
			if err != nil {
				t.Fatal(err)
			}
			cold := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
			logCallees("replay", warm)
			logCallees("cold", cold)
			assertRetainedCanonicalLiveResultParity(t, cold, warm)
		})
	}
}
