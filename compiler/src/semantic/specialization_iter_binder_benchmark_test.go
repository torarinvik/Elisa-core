//go:build cgo

package semantic

import (
	"fmt"
	"testing"
)

func BenchmarkSpecializationIterBinderConstruction(b *testing.B) {
	symbol := &Symbol{Name: "origin"}
	value := regionRefState{Deps: map[*Symbol]regionDependencyState{symbol: {Valid: true}}}
	for depth := 0; depth < 5; depth++ {
		value = regionRefState{Fields: map[string]regionRefState{"left": value, "right": value}}
	}
	source := &Analyzer{iterBinderElementStates: map[*Symbol]regionRefState{}}
	for i := 0; i < 512; i++ {
		source.iterBinderElementStates[&Symbol{Name: fmt.Sprint(i)}] = value
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		specializationAnalysisCloneSink = cloneAnalyzerValueForSpecialization(source).Addr().Interface().(*Analyzer)
	}
}
