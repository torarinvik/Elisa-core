package semantic

import (
	"math/rand"
	"reflect"
	"strconv"
	"testing"
)

func TestSinkDataflowMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(80423))
	for trial := 0; trial < 1200; trial++ {
		n, p := 1+rng.Intn(14), 1+rng.Intn(7)
		cfg := &CFG{Entry: rng.Intn(n), Blocks: make([]CFGBlock, n), ParamLocations: make([]string, p)}
		for i := range cfg.ParamLocations {
			cfg.ParamLocations[i] = "p" + strconv.Itoa(i)
		}
		for i := range cfg.Blocks {
			for j := 0; j < rng.Intn(4); j++ {
				cfg.Blocks[i].Edges = append(cfg.Blocks[i].Edges, FlowEdge{To: rng.Intn(n+2) - 1})
			}
			for j := 0; j < p; j++ {
				if rng.Intn(5) == 0 {
					cfg.Blocks[i].Instrs = append(cfg.Blocks[i].Instrs, FlowInstr{Kind: FlowInstrConsume, Location: cfg.ParamLocations[j]})
				}
			}
			if rng.Intn(4) == 0 {
				cfg.ExitBlocks = append(cfg.ExitBlocks, i)
			}
		}
		want, got := referenceInferSinkParamsFromCFG(cfg), inferSinkParamsFromCFG(cfg)
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("trial %d: want %v got %v", trial, want, got)
		}
	}
}

func BenchmarkSinkDataflow(b *testing.B) {
	cfg := &CFG{Blocks: make([]CFGBlock, 128), ParamLocations: []string{"a", "b", "c", "d", "e", "f", "g", "h"}, ExitBlocks: []int{127}}
	for i := range cfg.Blocks {
		if i+1 < len(cfg.Blocks) {
			cfg.Blocks[i].Edges = []FlowEdge{{To: i + 1}}
		}
		if i > 0 && i%5 == 0 {
			cfg.Blocks[i].Edges = append(cfg.Blocks[i].Edges, FlowEdge{To: i - 1})
		}
		if i%7 == 0 {
			cfg.Blocks[i].Instrs = []FlowInstr{{Kind: FlowInstrConsume, Location: cfg.ParamLocations[i%8]}}
		}
	}
	for _, bench := range []struct {
		name string
		fn   func(*CFG) []bool
	}{{"reference", referenceInferSinkParamsFromCFG}, {"current", inferSinkParamsFromCFG}} {
		b.Run(bench.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				bench.fn(cfg)
			}
		})
	}
}

// Frozen pre-optimization implementation is an independent dataflow oracle.
func referenceInferSinkParamsFromCFG(cfg *CFG) []bool {
	if cfg == nil || len(cfg.ParamLocations) == 0 {
		return nil
	}
	indexByParam := map[string]int{}
	for i, name := range cfg.ParamLocations {
		indexByParam[name] = i
	}
	blockCount := len(cfg.Blocks)
	if blockCount == 0 {
		return make([]bool, len(cfg.ParamLocations))
	}
	preds := make([][]int, blockCount)
	gen := make([][]bool, blockCount)
	for i, block := range cfg.Blocks {
		gen[i] = make([]bool, len(cfg.ParamLocations))
		for _, instr := range block.Instrs {
			if instr.Kind != FlowInstrConsume {
				continue
			}
			if index, ok := indexByParam[instr.Location]; ok {
				gen[i][index] = true
			}
		}
		for _, edge := range block.Edges {
			if edge.To >= 0 && edge.To < blockCount {
				preds[edge.To] = append(preds[edge.To], i)
			}
		}
	}
	in := make([][]bool, blockCount)
	out := make([][]bool, blockCount)
	for i := 0; i < blockCount; i++ {
		in[i] = make([]bool, len(cfg.ParamLocations))
		out[i] = make([]bool, len(cfg.ParamLocations))
		if i != cfg.Entry {
			for j := range in[i] {
				in[i][j] = true
			}
		}
	}
	changed := true
	for changed {
		changed = false
		for i := 0; i < blockCount; i++ {
			if i != cfg.Entry {
				nextIn := intersectPredOut(preds[i], out, len(cfg.ParamLocations))
				if !boolSliceEqual(in[i], nextIn) {
					copy(in[i], nextIn)
					changed = true
				}
			}
			nextOut := mergeMustConsume(in[i], gen[i])
			if !boolSliceEqual(out[i], nextOut) {
				copy(out[i], nextOut)
				changed = true
			}
		}
	}
	if len(cfg.ExitBlocks) == 0 {
		return make([]bool, len(cfg.ParamLocations))
	}
	mustConsume := make([]bool, len(cfg.ParamLocations))
	for i := range mustConsume {
		mustConsume[i] = true
	}
	for _, exit := range cfg.ExitBlocks {
		if exit < 0 || exit >= len(out) {
			continue
		}
		for i := range mustConsume {
			mustConsume[i] = mustConsume[i] && out[exit][i]
		}
	}
	return mustConsume
}

func intersectPredOut(preds []int, out [][]bool, size int) []bool {
	result := make([]bool, size)
	if len(preds) == 0 {
		return result
	}
	for i := 0; i < size; i++ {
		result[i] = true
	}
	for _, pred := range preds {
		if pred < 0 || pred >= len(out) {
			continue
		}
		for i := 0; i < size; i++ {
			result[i] = result[i] && out[pred][i]
		}
	}
	return result
}

func mergeMustConsume(base []bool, gen []bool) []bool {
	out := append([]bool(nil), base...)
	for i, value := range gen {
		out[i] = out[i] || value
	}
	return out
}

func boolSliceEqual(left []bool, right []bool) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
