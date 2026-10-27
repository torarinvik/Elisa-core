//go:build cgo

package backend

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func structLiteralCacheFixture(fieldCount, depth int) (*semantic.GenericInstanceType, *semantic.GenericInstanceType) {
	decl := &ast.StructDecl{Name: "Record", Fields: make([]ast.FieldDecl, fieldCount)}
	base := &semantic.StructType{
		Name:          "Record",
		TypeParams:    []string{"T"},
		GenericParams: []ast.GenericParam{{Kind: ast.GenericParamType, Name: "T"}},
		Decl:          decl,
		Fields:        make(map[string]semantic.Field, fieldCount),
	}
	for index := 0; index < fieldCount; index++ {
		name := fmt.Sprintf("field%d", index)
		decl.Fields[index] = ast.FieldDecl{Name: name}
		var typ semantic.Type = &semantic.TypeParamType{Name: "T"}
		for nested := 0; nested < depth; nested++ {
			nestedName := fmt.Sprintf("Nested%d", nested)
			typ = &semantic.GenericInstanceType{
				Name: nestedName,
				Base: &semantic.StructType{Name: nestedName},
				Args: []semantic.Type{typ},
			}
		}
		base.Fields[name] = semantic.Field{Name: name, Type: typ}
	}
	i64 := &semantic.BuiltinType{Name: "i64"}
	u8 := &semantic.BuiltinType{Name: "u8"}
	return &semantic.GenericInstanceType{Name: "Record", Base: base, Args: []semantic.Type{i64}},
		&semantic.GenericInstanceType{Name: "Record", Base: base, Args: []semantic.Type{u8}}
}

func structLiteralFieldBuiltinName(t semantic.Type) string {
	for {
		instance, ok := t.(*semantic.GenericInstanceType)
		if !ok || len(instance.Args) != 1 {
			break
		}
		t = instance.Args[0]
	}
	builtin, _ := t.(*semantic.BuiltinType)
	if builtin == nil {
		return ""
	}
	return builtin.Name
}

func TestStructLiteralFieldsMemoUsesExactTypeAndResult(t *testing.T) {
	i64Record, u8Record := structLiteralCacheFixture(3, 2)
	result := &semantic.Result{}
	shared := &sharedCodegenLookups{result: result}
	firstGenerator := &llvmGenerator{result: result, sharedLookups: shared}
	secondGenerator := &llvmGenerator{result: result, sharedLookups: shared}

	i64Fields, err := firstGenerator.structLiteralFields(i64Record)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := secondGenerator.structLiteralFields(&semantic.AggregateStateType{Base: i64Record, State: semantic.RefStateNonNull})
	if err != nil {
		t.Fatal(err)
	}
	if len(i64Fields) != 3 || &i64Fields[0] != &reused[0] {
		t.Fatal("same stripped concrete type did not reuse its immutable field projection")
	}
	if got := structLiteralFieldBuiltinName(i64Fields[0].Type); got != "i64" {
		t.Fatalf("i64 field substitution = %q", got)
	}

	u8Fields, err := secondGenerator.structLiteralFields(u8Record)
	if err != nil {
		t.Fatal(err)
	}
	if &u8Fields[0] == &i64Fields[0] {
		t.Fatal("generic instances with different arguments shared field projections")
	}
	if got := structLiteralFieldBuiltinName(u8Fields[0].Type); got != "u8" {
		t.Fatalf("u8 field substitution = %q", got)
	}

	otherResult := &semantic.Result{}
	other := &llvmGenerator{result: otherResult, sharedLookups: shared}
	otherFields, err := other.structLiteralFields(i64Record)
	if err != nil {
		t.Fatal(err)
	}
	if &otherFields[0] == &i64Fields[0] {
		t.Fatal("field projection crossed semantic-result domains")
	}
}

func BenchmarkStructLiteralFieldsSharedAcrossPartitions(b *testing.B) {
	instance, _ := structLiteralCacheFixture(24, 16)
	result := &semantic.Result{}
	b.Run("recompute", func(b *testing.B) {
		generator := &llvmGenerator{result: result}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if _, err := generator.structLiteralFields(instance); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("shared-cache-hit", func(b *testing.B) {
		shared := &sharedCodegenLookups{result: result}
		generator := &llvmGenerator{result: result, sharedLookups: shared}
		if _, err := generator.structLiteralFields(instance); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if _, err := generator.structLiteralFields(instance); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func structLiteralPartitionSource(edited bool) string {
	var source strings.Builder
	source.WriteString("struct Pair:\n    left: i64\n    right: i64\n\n")
	for index := 0; index < 80; index++ {
		value := "value + 1"
		if edited && index == 0 {
			value = "value + 2"
		}
		fmt.Fprintf(&source, "@inline(never)\ndef work%d(value: i64) -> i64:\n    pair: Pair = Pair{left: value, right: %s}\n    return pair.left + pair.right\n\n", index, value)
	}
	source.WriteString("def main() -> i32:\n    if work0(0) == 1:\n        return 0\n    return 1\n")
	return source.String()
}

func TestStructLiteralFieldsMemoAcrossEditedPartitions(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	const parts = 8
	session := NewIncrementalObjectSession(parts, 16<<20)
	session.minWeight = 0
	dir := t.TempDir()
	object := filepath.Join(dir, "struct-literal.o")

	baseSource := structLiteralPartitionSource(false)
	base := parseAndAnalyzeBackendTest(t, "struct-literal-partitions.elisa", baseSource)
	if err := session.WriteObject(base, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
	first := session.Stats()
	if first.SerialFallbacks != 0 || first.PartitionsEmitted == 0 {
		t.Fatalf("initial O0 build did not use partitioned codegen: %+v", first)
	}
	if got := incrementalObjectNativeStatus(t, object); got != 0 {
		t.Fatalf("base executable status = %d, want 0", got)
	}

	editedSource := structLiteralPartitionSource(true)
	edited := parseAndAnalyzeBackendTest(t, "struct-literal-partitions.elisa", editedSource)
	if err := session.WriteObject(edited, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
	second := session.Stats()
	emitted := second.PartitionsEmitted - first.PartitionsEmitted
	reused := second.PartitionsReused - first.PartitionsReused
	if emitted != 1 || reused != parts-1 {
		t.Fatalf("body edit should rebuild one owner and reuse the other %d: emitted=%d reused=%d stats=%+v", parts-1, emitted, reused, second)
	}
	if got := incrementalObjectNativeStatus(t, object); got != 1 {
		t.Fatalf("edited executable status = %d, want 1", got)
	}
}
