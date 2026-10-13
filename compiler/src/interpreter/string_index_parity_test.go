package interpreter

import "testing"

func TestStringIndexKeepsRuntimeZeroFallback(t *testing.T) {
	for _, tc := range []struct {
		text        string
		index, want int64
	}{
		{"hello", 0, 104}, {"hello", 4, 111}, {"hello", 5, 0},
		{"hello", -1, 0}, {"hello", 9223372036854775807, 0},
		{"", 0, 0}, {"\xff", 0, 255},
	} {
		got, err := indexValueAt(StringValue(tc.text), tc.index)
		if err != nil || got.kind != valueInt || got.int64Val != tc.want {
			t.Fatalf("text=%q index=%d: got %v, err=%v; want %d", tc.text, tc.index, got, err, tc.want)
		}
	}
	for _, index := range []int64{-1, 1} {
		if _, err := indexValueAt(ListValue([]Value{IntValue(42)}), index); err == nil {
			t.Fatalf("list index %d lost its bounds error", index)
		}
	}
}
