package main

import "testing"

// docs/120 §8 field-path arg-manifest: an lmut sub-struct of a mutable root is threaded by
// `report.cache <- bump(report.cache)`; codegen passes the field's address so the callee's
// writes (including container growth) land in the caller's field.
const fieldManifestBody = `
struct Cache:
    hits: mutable i64 = 0
    slots: mutable darray[i64] = []

struct Report:
    cache: mutable Cache = Cache{}
    total: mutable i64 = 0

def bump(cache: lmut Cache) -> void:
    cache.hits <- cache.hits + 10
    cache.slots <- cache.slots.push(7)

@test
def field_manifest_writes_through() -> void:
    report: mutable Report = Report{}
    report.cache.hits <- report.cache.hits + 1
    report.cache.slots <- report.cache.slots.push(5)
    report.cache <- bump(report.cache)
    report.cache <- bump(report.cache)
    copy: Report = Report{..report, total: 100}
    if report.cache.hits != 21 or report.cache.slots.count.i64() != 3 or copy.total != 100:
        panic("field-path manifest did not write through")
    if report.cache.slots[1] != 7 or copy.cache.hits != 21:
        panic("field-path manifest values wrong")
`

func TestLmutFieldPathArgManifest(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "field_manifest", fieldManifestBody)
	assertAllPassed(t, exit, stdout, stderr, "field_manifest_writes_through")
}
