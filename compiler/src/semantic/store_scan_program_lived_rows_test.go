package semantic

import "testing"

// In elements mode only element values of an argument are stored. A darray of program-lived
// rows (every field @source) yields rows that reach no local region; region-less rows still do.
func TestStoreScanProgramLivedElementRows(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"program_lived_rows_joined_from_local", `global region source

struct Row:
    id: u32
    names: darray[sview @source] @source

struct Sites:
    rows: mutable darray[Row] = []

def join(left: darray[Row]&, right: darray[Row]&) -> darray[Row]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[Row] = []
        for row in left |out|:
            out.push(row)
        for row in right |out|:
            out.push(row)
        return out

def merge(sites: mutable Sites&, left: darray[Row]&, right: darray[Row]&) -> void:
    can Memory.Allocate, Abort.Panic:
        sites.rows <- join(left, right)

def fill[@r](rows: mutable darray[Row]&, names: darray[sview @source] @source, t: mutable dstr& @r) -> void:
    can Memory.Allocate, Abort.Panic:
        t.push(65)
        rows.push(Row{id: 1, names: names})

def caller(sites: mutable Sites&, names: darray[sview @source] @source, k: u32) -> void:
    can Memory.Allocate, Abort.Panic:
        match k:
            0:
                a: mutable darray[Row] = []
                b: mutable darray[Row] = []
                d: mutable dstr = []
                fill(a, names, d)
                merge(sites, a, b)
            _:
                pass
`, nil},
		{"region_less_rows_joined_from_local", `struct Row:
    id: u32
    names: darray[sview]

struct Sites:
    rows: mutable darray[Row] = []

def join(left: darray[Row]&, right: darray[Row]&) -> darray[Row]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[Row] = []
        for row in left |out|:
            out.push(row)
        for row in right |out|:
            out.push(row)
        return out

def merge(sites: mutable Sites&, left: darray[Row]&, right: darray[Row]&) -> void:
    can Memory.Allocate, Abort.Panic:
        sites.rows <- join(left, right)

def caller(sites: mutable Sites&, k: u32) -> void:
    can Memory.Allocate, Abort.Panic:
        match k:
            0:
                d: mutable dstr = []
                d.push(65)
                ns: mutable darray[sview] = []
                ns.push(d.as_sview())
                a: mutable darray[Row] = []
                a.push(Row{id: 1, names: ns})
                b: mutable darray[Row] = []
                merge(sites, a, b)
            _:
                pass
`, []string{":32:30"}},
	}
	for _, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, c.name+".elisa", c.src)
		expectErrorLines(t, result.Errors(), c.want)
	}
}
