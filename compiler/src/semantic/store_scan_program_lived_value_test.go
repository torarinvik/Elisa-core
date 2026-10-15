package semantic

import "testing"

// A callee that stores a value whose declared type is program-lived (a `darray[T] @source` field
// copied by header) stores only program-region storage: passing a local struct holding it is
// safe. The same field without the region still shares the local's storage.
func TestStoreScanProgramLivedValueCarriesNothing(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"program_lived_field_header", `global region source

struct F:
    rows: darray[sview @source] @source = []
    scratch: mutable darray[u8] = []

struct T:
    rows: mutable darray[sview @source] = []

def resolve(f: F&, t: mutable T&) -> void:
    can Memory.Allocate, Abort.Panic:
        t.rows <- f.rows

def caller(t: mutable T&, n: sview @source, k: u32) -> void:
    can Memory.Allocate, Abort.Panic:
        match k:
            0:
                local: mutable F = F{}
                local.scratch.push(1)
                resolve(local, t)
            _:
                pass
`, nil},
		{"region_less_field_header", `global region source

struct F:
    rows: darray[sview @source] = []
    scratch: mutable darray[u8] = []

struct T:
    rows: mutable darray[sview @source] = []

def resolve(f: F&, t: mutable T&) -> void:
    can Memory.Allocate, Abort.Panic:
        t.rows <- f.rows

def caller(t: mutable T&, n: sview @source, k: u32) -> void:
    can Memory.Allocate, Abort.Panic:
        match k:
            0:
                local: mutable F = F{}
                local.scratch.push(1)
                resolve(local, t)
            _:
                pass
`, []string{":20:25"}},
	}
	for _, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, c.name+".elisa", c.src)
		expectErrorLines(t, result.Errors(), c.want)
	}
}
