package semantic

import (
	"fmt"
	"testing"
)

var regionCloneBenchSink map[*Symbol]regionRefState

func BenchmarkCloneRegionRefStates(b *testing.B) {
	for _, n := range []int{4, 16, 64} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			a := &Analyzer{analyzerFunctionRegionState: analyzerFunctionRegionState{
				currentRegionRefs: make(map[*Symbol]regionRefState, n),
			}}
			for i := 0; i < n; i++ {
				a.currentRegionRefs[&Symbol{Name: fmt.Sprint(i)}] = regionRefState{DirectParamDep: i, HasDirectParamDep: true}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				regionCloneBenchSink = a.cloneRegionRefStates()
			}
		})
	}
}

var regionStateCloneBenchSink map[*Symbol]regionState

func BenchmarkCloneRegionStates(b *testing.B) {
	for _, n := range []int{4, 16, 64} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			a := &Analyzer{analyzerFunctionRegionState: analyzerFunctionRegionState{
				currentRegions: make(map[*Symbol]regionState, n),
			}}
			for i := 0; i < n; i++ {
				a.currentRegions[&Symbol{Name: fmt.Sprint(i)}] = regionState{Generation: i}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				regionStateCloneBenchSink = a.cloneRegionStates()
			}
		})
	}
}

var regionDependencyCloneBenchSink map[*Symbol]regionDependencyState

func BenchmarkCloneRegionDependencyStates(b *testing.B) {
	for _, n := range []int{1, 8, 32} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			m := make(map[*Symbol]regionDependencyState, n)
			for i := 0; i < n; i++ {
				m[&Symbol{Name: fmt.Sprint(i)}] = regionDependencyState{Generation: i}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				regionDependencyCloneBenchSink = cloneRegionDependencyStates(m)
			}
		})
	}
}

var packedDependencyCloneBenchSink map[*Symbol]packedStoreDependencyState

func BenchmarkClonePackedStoreDependencies(b *testing.B) {
	for _, n := range []int{1, 8, 32, 128} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			source := make(map[*Symbol]packedStoreDependencyState, n)
			for i := 0; i < n; i++ {
				source[&Symbol{Name: fmt.Sprint(i)}] = packedStoreDependencyState{}
			}
			b.Run("entry-loop", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					cloned := make(map[*Symbol]packedStoreDependencyState, len(source))
					for symbol, state := range source {
						cloned[symbol] = state
					}
					packedDependencyCloneBenchSink = cloned
				}
			})
			b.Run("runtime-clone", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					packedDependencyCloneBenchSink = clonePackedStoreDependencyStates(source)
				}
			})
		})
	}
}
