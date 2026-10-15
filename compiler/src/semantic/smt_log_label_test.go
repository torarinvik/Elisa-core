package semantic

import "testing"

// Attribution tags for the rlimit calibration log: which theory bridges a query uses.
func TestSMTQueryFeatureTags(t *testing.T) {
	for query, want := range map[string]string{
		"(assert (< x 1))\n(assert (= y (mod x 4)))":                          "lia",
		"(assert (= s (bv2nat (bvand ((_ int2bv 64) h) ((_ int2bv 64) m)))))": "bv",
		"(assert (forall ((i Int)) (> (select a i) 0)))":                      "q,arr",
		"(assert (= z (* x y)))":                                              "nl",
	} {
		if got := smtQueryFeatures(query); got != want {
			t.Errorf("features(%q) = %q, want %q", query, got, want)
		}
	}
}
