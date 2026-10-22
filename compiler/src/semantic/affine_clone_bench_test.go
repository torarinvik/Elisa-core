package semantic

import (
	"maps"
	"strconv"
	"testing"
)

var affineCloneSink map[affineValueKey]affineValueState

func BenchmarkAffineCloneStrategy(b *testing.B) {
	for _, sparse := range []bool{false, true} {
		source := make(map[affineValueKey]affineValueState, 1024)
		for i := 0; i < 1024; i++ {
			source[affineValueKey{Path: strconv.Itoa(i)}] = affineValueState{ConsumedBy: "consume"}
		}
		if sparse {
			for key := range source {
				if key.Path != "0" {
					delete(source, key)
				}
			}
		}
		for _, native := range []bool{false, true} {
			b.Run(strconv.FormatBool(sparse)+"/native"+strconv.FormatBool(native), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if native {
						affineCloneSink = maps.Clone(source)
					} else {
						out := make(map[affineValueKey]affineValueState, len(source))
						for key, state := range source {
							out[key] = state
						}
						affineCloneSink = out
					}
				}
			})
		}
	}
}
