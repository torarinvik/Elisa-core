package semantic

import (
	"reflect"
	"testing"
)

func TestSMTSplitTopLevelPreservesBoundaryCases(t *testing.T) {
	tests := []struct {
		text string
		want []string
		ok   bool
	}{
		{"", nil, true},
		{"\n\n; comment\n", nil, true},
		{"(assert (= |a(b)| 1))\n(assert (= \"a\"\"b\" 2))\n", []string{"(assert (= |a(b)| 1))", "(assert (= \"a\"\"b\" 2))"}, true},
		{"(assert true)\n(assert false", []string{"(assert true)"}, false},
		{"(assert |unterminated", nil, false},
		{"(assert true))", nil, false},
	}
	for _, tc := range tests {
		got, ok := smtSplitTopLevel(tc.text)
		if ok != tc.ok || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%q: got %v/%v want %v/%v", tc.text, got, ok, tc.want, tc.ok)
		}
	}
}
