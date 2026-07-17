package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIConstDictLookupUsesStaticBuckets(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("rel include %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	preamble := fmt.Sprintf("# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"))
	src := preamble + `
const NUMBERS: dict[cstr, u8] = {"one": 1, "two": 2, "three": 3}
const FLAGS: dict[u8, u16] = {1: 10, 200: 20}

@test
def const_dict_lookup_static_buckets() -> void:
    can Abort.Panic:
        direct: u8 = NUMBERS["one"]
        if direct != 1:
            panic("wrong direct const dict index value")
        two: u8&? = NUMBERS.get("two")
        if two == null:
            panic("missing two")
        else:
            if two != 2:
                panic("wrong string-key value")
        if NUMBERS.get("missing") != null:
            panic("found absent string key")
        high: u16&? = FLAGS.get(200)
        if high == null:
            panic("missing scalar key")
        else:
            if high != 20:
                panic("wrong scalar-key value")
        key_text: sview = sview("three", 0, 5)
        three: u8&? = NUMBERS.get(key_text)
        if three == null:
            panic("missing sview get key")
        elif three[0] != 3:
            panic("wrong sview get value")
        indexed: u8&? = NUMBERS[key_text]
        if indexed == null:
            panic("missing sview index key")
        elif indexed[0] != 3:
            panic("wrong sview index value")
        absent_key: sview = sview("absent", 0, 6)
        if NUMBERS[absent_key] != null:
            panic("found absent sview index key")
`
	fixturePath := filepath.Join(fixtureDir, "const_dict_lookup_static_buckets.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected const dict lookup test to pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] const_dict_lookup_static_buckets") {
		t.Fatalf("expected const_dict_lookup_static_buckets to pass, got:\n%s", stdout.String())
	}
}

func TestRunCLIDictGetConstCorrectness(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("rel include %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	preamble := fmt.Sprintf("# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"))
	src := preamble + `
const NUMBERS: dict[cstr, u8] = {"one": 1}

@test
def mutable_dict_get_returns_mutable_ref() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region r:
            owner: mutable Arena& = &r
            numbers: mutable dict[cstr, u8] = arena_dict_new[cstr, u8](owner, 4)
            _ = numbers.put("one", 1)
            slot: mutable u8&? = numbers.get("one")
            if slot == null:
                panic("missing mutable slot")
            else:
                slot[0] <- 7
            again: mutable u8&? = numbers.get("one")
            if again == null:
                panic("missing mutated slot")
            elif again[0] != 7:
                panic("mutable dict get did not expose stored value")
            key_text: sview = sview("one", 0, 3)
            via_view: mutable u8&? = numbers.get(key_text)
            if via_view == null:
                panic("missing mutable sview slot")
            else:
                via_view[0] <- 9
            via_index: mutable u8&? = numbers[key_text]
            if via_index == null:
                panic("missing mutable sview index slot")
            elif via_index[0] != 9:
                panic("mutable sview dict index did not expose stored value")

@test
def const_dict_get_returns_immutable_ref() -> void:
    can Abort.Panic:
        slot: u8&? = NUMBERS.get("one")
        if slot == null:
            panic("missing const slot")
        elif slot[0] != 1:
            panic("wrong const slot")
`
	fixturePath := filepath.Join(fixtureDir, "dict_get_const_correctness.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected dict get const-correctness tests to pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] mutable_dict_get_returns_mutable_ref") {
		t.Fatalf("expected mutable_dict_get_returns_mutable_ref to pass, got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] const_dict_get_returns_immutable_ref") {
		t.Fatalf("expected const_dict_get_returns_immutable_ref to pass, got:\n%s", stdout.String())
	}
}

func TestRunCLIDictIterationYieldsKeyValuePairs(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("rel include %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	preamble := fmt.Sprintf("# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"))
	src := preamble + `
@test
def dict_iteration_yields_key_value_pairs() -> void:
    can Memory.Allocate, Abort.Panic:
        expected: dict[cstr, u8] = {
            "one": 1,
            "two": 2,
            "skip": 4
        }
        sum: mutable u8 = 0
        saw_one: mutable bool = false
        saw_two: mutable bool = false
        saw_skip: mutable bool = false

        for key, want in expected:
            got: u8 = get expected[key[0:key.len]] else 255
            if got != want:
                panic("dict iteration key/value mismatch")
            sum <- sum + want
            key_view: sview = key[0:key.len]
            if key_view == "one":
                saw_one <- true
            elif key_view == "two":
                saw_two <- true
            elif key_view == "skip":
                saw_skip <- true

        if sum != 7:
            panic("wrong dict iteration sum")
        if not saw_one or not saw_two or not saw_skip:
            panic("dict iteration missed an occupied bucket")
`
	fixturePath := filepath.Join(fixtureDir, "dict_iteration_yields_key_value_pairs.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected dict iteration test to pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] dict_iteration_yields_key_value_pairs") {
		t.Fatalf("expected dict_iteration_yields_key_value_pairs to pass, got:\n%s", stdout.String())
	}
}

// TestRunCLIDictIterationVisitsExactlyCountEntries pins the ITERATION COUNT of a dict walk,
// which is a different property from the key/value correctness asserted above.
//
// This is the regression guard for the dict-iteration overcount bug (task_dcf0ced9): `for k,
// v in d:` used to run MORE times than `d.count`. It survived because every existing dict
// test checked a value sum or per-entry flags -- both of which happily tolerate extra
// iterations over empty/tombstone slots, since a spurious visit contributes nothing to a sum
// and re-sets an already-set flag. Only a bare counter observes it.
//
// The grown case is the sharp one: 17 entries forces the open-addressing backing to a
// capacity well above the count, so an iterator that walked the whole slot array instead of
// skipping empties would overcount badly. The literal cases cover both binding forms
// (two-name destructure and single-name entry) and both a scalar and a cstr key type.
func TestRunCLIDictIterationVisitsExactlyCountEntries(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("rel include %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	preamble := fmt.Sprintf("# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"))
	src := preamble + `
@test
def dict_iteration_visits_exactly_count_entries() -> void:
    can Memory.Allocate, Abort.Panic:
        d: dict[i64, i64] = {1: 10, 2: 20, 3: 30}
        n: mutable i64 = 0
        for k, v in d:
            n <- n + 1
        if n != d.count.i64():
            panic("two-name dict iteration count != d.count")

@test
def dict_single_name_iteration_visits_exactly_count_entries() -> void:
    can Memory.Allocate, Abort.Panic:
        d: dict[i64, i64] = {1: 10, 2: 20, 3: 30}
        n: mutable i64 = 0
        for e in d:
            n <- n + 1
        if n != d.count.i64():
            panic("single-name dict iteration count != d.count")

@test
def cstr_dict_iteration_visits_exactly_count_entries() -> void:
    can Memory.Allocate, Abort.Panic:
        d: dict[cstr, u8] = {"one": 1, "two": 2, "skip": 4}
        n: mutable i64 = 0
        for k, v in d:
            n <- n + 1
        if n != d.count.i64():
            panic("cstr dict iteration count != d.count")

@test
def grown_dict_iteration_visits_exactly_count_entries() -> void:
    can Memory.Allocate, Abort.Panic:
        d: mutable dict[i64, i64] = {}
        for i in 0..<17:
            d.entry(i).insert(i * 2)
        n: mutable i64 = 0
        for k, v in d:
            n <- n + 1
        if n != d.count.i64():
            panic("grown dict iteration count != d.count")
`
	fixturePath := filepath.Join(fixtureDir, "dict_iteration_visits_exactly_count_entries.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected dict iteration-count tests to pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, name := range []string{
		"dict_iteration_visits_exactly_count_entries",
		"dict_single_name_iteration_visits_exactly_count_entries",
		"cstr_dict_iteration_visits_exactly_count_entries",
		"grown_dict_iteration_visits_exactly_count_entries",
	} {
		if !strings.Contains(stdout.String(), "[       OK ] "+name) {
			t.Fatalf("expected %s to pass, got:\n%s", name, stdout.String())
		}
	}
}
