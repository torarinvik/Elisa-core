package semantic

import "testing"

// A builtin darray extend(xs) copies xs's elements. Program-lived elements (sview @source)
// carry nothing of a local holding xs into the call target; region-less views still do.
func TestStoreScanProgramLivedExtend(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"program_lived_rows_merged_from_local", `global region source

struct Path:
    names: mutable darray[sview @source] = []

struct State:
    names: mutable darray[sview @source] = []
    exit_paths: mutable darray[Path] = []
    seen: mutable bool = false

def capture(exits: mutable State&, names: darray[sview @source]&) -> void:
    can Memory.Allocate, Abort.Panic:
        path: mutable Path = Path{}
        path.names.extend(names)
        exits.exit_paths.push(path)

def merge(target: mutable State&, alt: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        capture(target, alt.names) if alt.seen

def fill[@r](f: mutable State&, n: sview @source, t: mutable dstr& @r) -> void:
    can Memory.Allocate, Abort.Panic:
        tmp: mutable dstr = []
        tmp.push(66)
        t.push(65)
        f.seen <- tmp.count > 0
        f.names.push(n)

def caller(target: mutable State&, n: sview @source, k: u32) -> void:
    can Memory.Allocate, Abort.Panic:
        match k:
            0:
                local: mutable State = State{}
                d: mutable dstr = []
                fill(local, n, d)
                merge(target, local)
            _:
                pass
`, nil},
		{"region_less_rows_merged_from_local", `global region source

struct Path:
    names: mutable darray[sview] = []

struct State:
    names: mutable darray[sview] = []
    exit_paths: mutable darray[Path] = []
    seen: mutable bool = false

def capture(exits: mutable State&, names: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        path: mutable Path = Path{}
        path.names.extend(names)
        exits.exit_paths.push(path)

def merge(target: mutable State&, alt: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        capture(target, alt.names) if alt.seen

def fill[@r](f: mutable State&, n: sview @source, t: mutable dstr& @r) -> void:
    can Memory.Allocate, Abort.Panic:
        tmp: mutable dstr = []
        tmp.push(66)
        t.push(65)
        f.seen <- tmp.count > 0
        f.names.push(n)

def caller(target: mutable State&, n: sview @source, k: u32) -> void:
    can Memory.Allocate, Abort.Panic:
        match k:
            0:
                local: mutable State = State{}
                d: mutable dstr = []
                fill(local, n, d)
                merge(target, local)
            _:
                pass
`, []string{":36:31"}},
	}
	for _, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, c.name+".elisa", c.src)
		expectErrorLines(t, result.Errors(), c.want)
	}
}
