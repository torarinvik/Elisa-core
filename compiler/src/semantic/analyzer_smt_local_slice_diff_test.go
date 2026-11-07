package semantic

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func referenceSliceImmutableLocalHypotheses(localHyps, context string) string {
	if localHyps == "" {
		return ""
	}
	type block struct {
		sym   string
		lines []string
	}
	var blocks []block
	index := map[string]int{}
	for _, line := range strings.SplitAfter(localHyps, "\n") {
		if line == "" {
			continue
		}
		sym, ok := localHypothesisSymbol(line)
		if !ok {
			return localHyps // unexpected shape: keep everything
		}
		if i, seen := index[sym]; seen {
			blocks[i].lines = append(blocks[i].lines, line)
			continue
		}
		index[sym] = len(blocks)
		blocks = append(blocks, block{sym: sym, lines: []string{line}})
	}
	reached := map[string]bool{}
	scan := smtTokenScanner{text: context}
	for tok, ok := scan.next(); ok; tok, ok = scan.next() {
		reached[tok] = true
	}
	keep := make([]bool, len(blocks))
	for changed := true; changed; {
		changed = false
		for i, blk := range blocks {
			if keep[i] || !reached[blk.sym] {
				continue
			}
			keep[i] = true
			changed = true
			for _, line := range blk.lines {
				scan := smtTokenScanner{text: line}
				for tok, ok := scan.next(); ok; tok, ok = scan.next() {
					reached[tok] = true
				}
			}
		}
	}
	var b strings.Builder
	for i, blk := range blocks {
		if keep[i] {
			for _, line := range blk.lines {
				b.WriteString(line)
			}
		}
	}
	return b.String()
}

func TestImmutableLocalSliceMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(90210))
	for trial := 0; trial < 1500; trial++ {
		var hyps, context strings.Builder
		count := rng.Intn(50)
		for i := 0; i < count; i++ {
			fmt.Fprintf(&hyps, "(assert (= v_%d (+ v_%d %d)))\n", i, rng.Intn(count+1), rng.Intn(10))
			if rng.Intn(3) == 0 {
				fmt.Fprintf(&hyps, "(assert (< v_%d %d))\n", i, rng.Intn(100)+1)
			}
		}
		for i := 0; i < rng.Intn(8); i++ {
			fmt.Fprintf(&context, "(assert (> v_%d 0))\n", rng.Intn(count+3))
		}
		if trial%17 == 0 {
			hyps.WriteString("unexpected\n")
		}
		got := sliceImmutableLocalHypotheses(hyps.String(), context.String())
		want := referenceSliceImmutableLocalHypotheses(hyps.String(), context.String())
		if got != want {
			t.Fatalf("trial %d: got %q, want %q", trial, got, want)
		}
	}
}

func TestImmutableLocalSliceInterleavedAndMalformed(t *testing.T) {
	cases := []struct{ hyps, context string }{
		{"(assert (= v_a v_b))\n(assert (= v_b 1))\n(assert (< v_a 8))\n", "(assert (> v_a 0))"},
		{"(assert (= v_a v_b))\n(assert (= v_b v_a))", "v_a"},
		{"(assert (= b_x v_a))\n(assert (= v_a 1))\n", "b_x"},
		{"(assert (= v_a 1))\n\n", "v_a"},
		{"(assert (= v_a 1))", ""},
	}
	for _, tc := range cases {
		got := sliceImmutableLocalHypotheses(tc.hyps, tc.context)
		want := referenceSliceImmutableLocalHypotheses(tc.hyps, tc.context)
		if got != want {
			t.Fatalf("%q: got %q want %q", tc.hyps, got, want)
		}
	}
}
