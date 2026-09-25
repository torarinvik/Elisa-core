package semantic

import (
	"strings"
	"testing"
)

func TestZeroedCannotMaterializeInvalidReferenceRepresentations(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "local reference",
			source: `def bad() -> i64:
	value: i64& = zeroed
	return value
`,
			want: "use of uninitialized variable",
		},
		{
			name: "returned reference",
			source: `def bad() -> i64&:
	return zeroed
`,
			want: "zero representation may contain a non-null reference",
		},
		{
			name: "unconstrained generic local",
			source: `def bad[T]() -> T:
	value: T = zeroed
	return value
`,
			want: "use of uninitialized variable",
		},
		{
			name: "generic aggregate field",
			source: `struct Box[T]:
	value: T

def bad[T]() -> Box[T]:
	return zeroed
`,
			want: "zero representation may contain a non-null reference",
		},
		{
			name: "aggregate reference field",
			source: `struct RefBox:
	value: i64&

def bad() -> RefBox:
	return zeroed
`,
			want: "zero representation may contain a non-null reference",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result := analyzeTreeTestSourceWithSemanticErrors(t, "zeroed_invalid_representation.elisa", test.source)
			diagnostics := strings.Join(result.Errors(), "\n")
			if !strings.Contains(diagnostics, test.want) {
				t.Fatalf("expected zeroed invalid-representation diagnostic, got:\n%s", diagnostics)
			}
		})
	}
}

func TestZeroedStillSupportsNullableReferencesAndEmptyContainers(t *testing.T) {
	analyzeTreeTestSource(t, "zeroed_valid_representation.elisa", `def maybe() -> i64&?:
	return zeroed

def empty[T]() -> darray[T]:
	values: darray[T] = zeroed
	return values

def identity[T](value: T) -> T:
	return value
`)
}

func TestZeroedUnknownGenericInstanceFailsClosed(t *testing.T) {
	analyzer := &Analyzer{}
	unknownLayouts := []Type{
		&GenericInstanceType{Name: "Unresolved", Args: []Type{&BuiltinType{Name: "i64"}}},
		&GenericInstanceType{Name: "OpaqueGeneric", Base: &OpaqueType{Name: "OpaqueGeneric"}, Args: []Type{&BuiltinType{Name: "i64"}}},
	}
	for _, typ := range unknownLayouts {
		if !analyzer.zeroedTypeHasInvalidRepresentation(typ) {
			t.Errorf("zeroed validity analysis treated unknown generic layout %s as valid", typ)
		}
		if !analyzer.zeroedTypeContainsSView(typ) {
			t.Errorf("sview validity analysis treated unknown generic layout %s as sview-free", typ)
		}
	}
}

func TestZeroedGenericPlaceholderMustBeWrittenBeforeRead(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "zeroed_generic_uninitialized.elisa", `def bad[T]() -> T:
	value: T = zeroed
	return value
`)
	diagnostics := strings.Join(result.Errors(), "\n")
	if !strings.Contains(diagnostics, "use of uninitialized variable") {
		t.Fatalf("expected the generic zeroed placeholder read to be rejected, got:\n%s", diagnostics)
	}
	analyzeTreeTestSource(t, "zeroed_generic_initialized.elisa", `def good[T](seed: T) -> T:
	mutable value: T = zeroed
	value <- seed
	return value
`)
}

func TestZeroedGenericAggregateWithReferenceFieldIsRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "zeroed_generic_reference_aggregate.elisa", `struct Box[T]:
	value: T

def bad() -> Box[i64&]:
	return zeroed
`)
	diagnostics := strings.Join(result.Errors(), "\n")
	if !strings.Contains(diagnostics, "zero representation may contain a non-null reference") {
		t.Fatalf("expected concrete generic aggregate reference to be rejected, got:\n%s", diagnostics)
	}
}

func TestZeroedNestedGenericAggregateWithValidConcreteFields(t *testing.T) {
	analyzeTreeTestSource(t, "zeroed_nested_generic_valid.elisa", `struct Atomic[T]:
	value: T

struct Cell[T]:
	slot: Atomic[T]

global mutable cell: Cell[i32] = zeroed
`)
}

func TestZeroedCannotCreateNestedSView(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "zeroed_nested_sview.elisa", `struct ViewBox:
	view: sview

def bad() -> ViewBox:
	return zeroed
`)
	diagnostics := strings.Join(result.Errors(), "\n")
	if !strings.Contains(diagnostics, "`zeroed` cannot construct an `sview`") {
		t.Fatalf("expected nested sview zeroing to be rejected, got:\n%s", diagnostics)
	}
}

func TestZeroedInactiveReferenceStorageRequiresExplicitUnsafeBoundary(t *testing.T) {
	analyzeTreeTestSource(t, "zeroed_trusted_inactive_storage.elisa", `struct Slot[T]:
	value: T

def inactive[T]() -> Slot[T]:
	trusted Unsafe.PointerCast:
		return zeroed
`)

	result := analyzeTreeTestSourceWithSemanticErrors(t, "zeroed_trusted_sview.elisa", `struct Slot:
	value: sview

def invalid() -> Slot:
	trusted Unsafe.PointerCast:
		return zeroed
`)
	diagnostics := strings.Join(result.Errors(), "\n")
	if !strings.Contains(diagnostics, "`zeroed` cannot construct an `sview`") {
		t.Fatalf("trusted raw-storage boundary must not waive the sview invariant, got:\n%s", diagnostics)
	}
}
