package semantic

import (
	"os"
	"strings"
	"testing"

	"elisacore/src/smt"
)

func TestSMTDeclineCacheRoundTripAndOptIn(t *testing.T) {
	cache := &smtProofCache{directory: t.TempDir(), identity: "test-identity"}
	query := "(declare-const x Int)\n(assert (not (> x 0)))\n"
	prior := smtDeclineCacheEnabled
	defer func() { smtDeclineCacheEnabled = prior }()

	smtDeclineCacheEnabled = false
	cache.publishDeclined(query, smt.Sat, "x = 0")
	if _, ok := cache.declined(query); ok {
		t.Fatal("declined verdict served without opt-in")
	}

	smtDeclineCacheEnabled = true
	cache.publishDeclined(query, smt.Sat, "x = 0")
	if ce, ok := cache.declined(query); !ok || ce != "x = 0" {
		t.Fatalf("sat record: got %q %v", ce, ok)
	}
	cache.publishDeclined(query, smt.Unsat, "")
	if ce, ok := cache.declined(query); !ok || ce != "x = 0" {
		t.Fatal("an unsat result must not be written as a decline")
	}
	other := query + "(assert true)\n"
	if _, ok := cache.declined(other); ok {
		t.Fatal("different query text must miss")
	}
	cache.publishDeclined(other, smt.Unknown, "")
	if _, ok := cache.declined(other); ok {
		t.Fatal("uncertified unknown persisted")
	}
	cache.publishResourceDeclined(other)
	if ce, ok := cache.declined(other); !ok || ce != "" {
		t.Fatalf("unknown record: got %q %v", ce, ok)
	}

	path := cache.declinePath(cache.key(query))
	raw, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(raw), "x = 0", "x = 1", 1)), 0o600)
	if _, ok := cache.declined(query); ok {
		t.Fatal("tampered counterexample accepted")
	}
	if cache.contains(query) {
		t.Fatal("a declined record must never read as a proof")
	}
}

func TestSMTDeclinePersistentHitWarmsMemoryCache(t *testing.T) {
	prior := smtDeclineCacheEnabled
	smtDeclineCacheEnabled = true
	defer func() { smtDeclineCacheEnabled = prior }()
	cache := &smtProofCache{directory: t.TempDir(), identity: "test-identity"}
	query := "(declare-const x Int)(assert (= x 1))"
	cache.publishDeclined(query, smt.Sat, "x = 1")
	analyzer := &Analyzer{smtProofCacheInitialized: true, smtProofCache: cache}
	first, ok := analyzer.smtCachedQuery(query)
	if !ok || first.proven || first.ce != "x = 1" {
		t.Fatalf("first hit: %+v %v", first, ok)
	}
	if err := os.Remove(cache.declinePath(cache.key(query))); err != nil {
		t.Fatal(err)
	}
	second, ok := analyzer.smtCachedQuery(query)
	if !ok || second != first {
		t.Fatalf("memory hit lost exact decline: %+v %v", second, ok)
	}
	if analyzer.smtStats.PersistentCacheHits != 1 || analyzer.smtStats.CacheHits != 2 || analyzer.smtStats.Proven != 0 {
		t.Fatalf("incorrect cache accounting: %+v", analyzer.smtStats)
	}
}
