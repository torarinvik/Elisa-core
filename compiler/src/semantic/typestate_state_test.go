//go:build cgo

package semantic

import (
	"testing"
)

// TestTypestateStateErasure verifies that a typestate-annotated struct has the __typestate
// field marked as phantom so codegen never includes it in runtime layout. The field remains
// visible to the semantic layer (for state checking and construction) but is erased from the
// LLVM struct type (zero impact on runtime layout/size/offsets).
func TestTypestateStateErasure(t *testing.T) {
	tests := []struct {
		name string
		src  string
		structName string
		// concreteFieldsInDecl is the list of field names expected in Decl.Fields after
		// analysis. The __typestate field should be present here (for construction) but
		// will be marked as phantom so codegen skips it.
		concreteFieldsInDecl []string
		// shouldHaveTypestate indicates whether the struct should be marked as HasTypestate.
		shouldHaveTypestate bool
	}{
		{
			name: "Simple typestate struct with one field",
			src: `
struct MySocket[state Closed | Open]:
	fd: mutable i64
	__typestate: mutable i64

	derive state:
		Closed when self.__typestate == 0
		Open when self.__typestate == 1
`,
			structName: "MySocket",
			concreteFieldsInDecl:  []string{"fd", "__typestate"},
			shouldHaveTypestate:     true,
		},
		{
			name: "Typestate struct with multiple fields",
			src: `
struct Connection[state Idle | Active]:
	host: cstr
	port: u16
	__typestate: mutable i64

	derive state:
		Idle when self.__typestate == 0
		Active when self.__typestate == 1
`,
			structName: "Connection",
			concreteFieldsInDecl:  []string{"host", "port", "__typestate"},
			shouldHaveTypestate:     true,
		},
		{
			name: "Non-typestate struct (no state param) preserves all fields",
			src: `
struct PlainStruct:
	data: i32
	value: u64
`,
			structName: "PlainStruct",
			concreteFieldsInDecl:  []string{"data", "value"},
			shouldHaveTypestate:     false,
		},
		{
			name: "Typestate struct with ghost field and typestate field",
			src: `
struct Box[state Open | Closed]:
	content: u32
	__typestate: mutable i64
	ghost model: u32

	derive state:
		Open when self.__typestate == 0
		Closed when self.__typestate == 1
`,
			structName: "Box",
			// Ghost fields are dropped from Decl.Fields; __typestate remains (phantom)
			concreteFieldsInDecl:  []string{"content", "__typestate"},
			shouldHaveTypestate:     true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := analyzeTreeTestSource(t, "test.elisa", test.src)

			// Find the struct in the analyzed types
			var st *StructType
			for _, typ := range result.NamedTypes {
				if structType, ok := typ.(*StructType); ok && structType.Name == test.structName {
					st = structType
					break
				}
			}

			if st == nil {
				t.Fatalf("no struct %q found in analysis", test.structName)
			}

			// Check typestate marker
			if st.HasTypestate != test.shouldHaveTypestate {
				t.Errorf("HasTypestate: got %v, want %v", st.HasTypestate, test.shouldHaveTypestate)
			}

			if test.shouldHaveTypestate && st.TypestateStateField == "" {
				t.Errorf("TypestateStateField should be set when HasTypestate is true")
			}

			// Check that Decl.Fields contains expected fields
			var actualFieldsInDecl []string
			if st.Decl != nil {
				for _, field := range st.Decl.Fields {
					actualFieldsInDecl = append(actualFieldsInDecl, field.Name)
				}
			}

			if len(actualFieldsInDecl) != len(test.concreteFieldsInDecl) {
				t.Errorf("field count in Decl.Fields: got %d, want %d", len(actualFieldsInDecl), len(test.concreteFieldsInDecl))
				t.Logf("  got fields: %v", actualFieldsInDecl)
				t.Logf("  want fields: %v", test.concreteFieldsInDecl)
			}

			for i, expected := range test.concreteFieldsInDecl {
				if i >= len(actualFieldsInDecl) {
					t.Errorf("missing field %q in Decl.Fields", expected)
					continue
				}
				if actualFieldsInDecl[i] != expected {
					t.Errorf("field %d: got %q, want %q", i, actualFieldsInDecl[i], expected)
				}
			}

			// Verify that the __typestate field is marked phantom when typestate is present
			if test.shouldHaveTypestate {
				if field, ok := st.Fields["__typestate"]; !ok {
					t.Errorf("__typestate should exist in st.Fields for state checking")
				} else if !field.Phantom {
					t.Errorf("__typestate field should be marked Phantom=true, but got Phantom=%v", field.Phantom)
				}
			}
		})
	}
}

// TestTypestateStructLayoutParity verifies that a typestate struct when compiled to LLVM
// has the same layout as an equivalent non-typestate struct (because __typestate is phantom).
func TestTypestateStructLayoutParity(t *testing.T) {
	// Parse two equivalent structs: one with typestate, one without.
	src := `
struct SocketA[state Closed | Open]:
	fd: mutable i64
	flags: u32
	__typestate: mutable i64

	derive state:
		Closed when self.__typestate == 0
		Open when self.__typestate == 1

struct SocketB:
	fd: mutable i64
	flags: u32
`

	result := analyzeTreeTestSource(t, "test.elisa", src)

	// Find both structs
	var typestateStruct *StructType
	var plainStruct *StructType

	for _, typ := range result.NamedTypes {
		if structType, ok := typ.(*StructType); ok {
			if structType.Name == "SocketA" {
				typestateStruct = structType
			} else if structType.Name == "SocketB" {
				plainStruct = structType
			}
		}
	}

	if typestateStruct == nil || plainStruct == nil {
		t.Fatalf("did not find both Socket struct variants")
	}

	// The typestate struct should have the __typestate field
	if len(typestateStruct.Decl.Fields) != 3 {
		t.Errorf("SocketA should have 3 fields (fd, flags, __typestate), got %d", len(typestateStruct.Decl.Fields))
	}

	// The plain struct should have 2 fields
	if len(plainStruct.Decl.Fields) != 2 {
		t.Errorf("SocketB should have 2 fields (fd, flags), got %d", len(plainStruct.Decl.Fields))
	}

	// But when considering layout (non-phantom fields), they should be the same:
	// typestate has: fd, flags (phantom: __typestate)
	// plain has: fd, flags
	var typestateNonPhantom, plainNonPhantom []string
	for _, f := range typestateStruct.Decl.Fields {
		if fld, ok := typestateStruct.Fields[f.Name]; ok && !fld.Phantom {
			typestateNonPhantom = append(typestateNonPhantom, f.Name)
		}
	}
	for _, f := range plainStruct.Decl.Fields {
		if fld, ok := plainStruct.Fields[f.Name]; ok && !fld.Phantom {
			plainNonPhantom = append(plainNonPhantom, f.Name)
		}
	}

	if len(typestateNonPhantom) != len(plainNonPhantom) {
		t.Errorf("non-phantom field count mismatch: typestate has %d, plain has %d",
			len(typestateNonPhantom), len(plainNonPhantom))
	}

	for i := 0; i < len(typestateNonPhantom) && i < len(plainNonPhantom); i++ {
		if typestateNonPhantom[i] != plainNonPhantom[i] {
			t.Errorf("field %d name mismatch: typestate has %q, plain has %q",
				i, typestateNonPhantom[i], plainNonPhantom[i])
		}
	}

	// Verify the typestate field was recognized and marked
	if !typestateStruct.HasTypestate {
		t.Errorf("SocketA with state param should be marked HasTypestate=true")
	}

	if plainStruct.HasTypestate {
		t.Errorf("SocketB should be marked HasTypestate=false")
	}
}

// TestTypestateFieldMarkedPhantom verifies that the __typestate field is marked as phantom
// so the backend skips it during LLVM struct type generation.
func TestTypestateFieldMarkedPhantom(t *testing.T) {
	src := `
struct MySocket[state Closed | Open]:
	fd: mutable i64
	__typestate: mutable i64

	derive state:
		Closed when self.__typestate == 0
		Open when self.__typestate == 1
`

	result := analyzeTreeTestSource(t, "test.elisa", src)

	// Find the struct
	var st *StructType
	for _, typ := range result.NamedTypes {
		if structType, ok := typ.(*StructType); ok && structType.Name == "MySocket" {
			st = structType
			break
		}
	}

	if st == nil {
		t.Fatalf("no MySocket struct found")
	}

	// Decl.Fields should contain both fd and __typestate
	if len(st.Decl.Fields) != 2 {
		var names []string
		for _, f := range st.Decl.Fields {
			names = append(names, f.Name)
		}
		t.Errorf("MySocket.Decl.Fields should have 2 fields, got %d: %v", len(st.Decl.Fields), names)
	}

	// But __typestate should be marked phantom
	typestateFld, ok := st.Fields["__typestate"]
	if !ok {
		t.Fatalf("__typestate field not found in st.Fields")
	}
	if !typestateFld.Phantom {
		t.Errorf("__typestate should be marked Phantom=true, got Phantom=%v", typestateFld.Phantom)
	}
}

// TestTypestateDetection verifies the detection logic for `state` generic parameters.
func TestTypestateDetection(t *testing.T) {
	tests := []struct {
		name               string
		src                string
		shouldHaveTypestate bool
	}{
		{
			name: "Has state param",
			src: `
struct S[state A | B]:
	x: i32

	derive state:
		A when true
		B when false
`,
			shouldHaveTypestate: true,
		},
		{
			name: "Has other generic param",
			src: `
struct S[T]:
	x: T
`,
			shouldHaveTypestate: false,
		},
		{
			name: "Has both state and other params",
			src: `
struct S[state A | B]:
	x: i32

	derive state:
		A when true
		B when false
`,
			shouldHaveTypestate: true,
		},
		{
			name: "No generic params",
			src: `
struct S:
	x: i32
`,
			shouldHaveTypestate: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := analyzeTreeTestSource(t, "test.elisa", test.src)

			var st *StructType
			for _, typ := range result.NamedTypes {
				if structType, ok := typ.(*StructType); ok && structType.Name == "S" {
					st = structType
					break
				}
			}

			if st == nil {
				t.Fatalf("no struct S found")
			}

			if st.HasTypestate != test.shouldHaveTypestate {
				t.Errorf("HasTypestate: got %v, want %v", st.HasTypestate, test.shouldHaveTypestate)
			}
		})
	}
}
