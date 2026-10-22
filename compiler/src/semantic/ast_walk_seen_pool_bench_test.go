package semantic

import "testing"

func BenchmarkFillSeenResetSparse(b *testing.B) {
	for _, adaptive := range []bool{false, true} {
		name := "clear"
		if adaptive {
			name = "adaptive"
		}
		b.Run(name, func(b *testing.B) {
			state := &fillWalkSeen{values: make(map[uintptr]bool)}
			for key := uintptr(0); key < 10000; key++ {
				state.values[key] = true
			}
			state.reset()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				state.values[1] = true
				if adaptive {
					state.reset()
				} else {
					clear(state.values)
				}
			}
		})
	}
}

func BenchmarkFillSeenResetAlternating(b *testing.B) {
	for _, adaptive := range []bool{false, true} {
		name := "clear"
		if adaptive {
			name = "adaptive"
		}
		b.Run(name, func(b *testing.B) {
			state := &fillWalkSeen{values: make(map[uintptr]bool, 10000)}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for key := uintptr(0); key < 10000; key++ {
					state.values[key] = true
				}
				if adaptive {
					state.reset()
				} else {
					clear(state.values)
				}
				state.values[1] = true
				if adaptive {
					state.reset()
				} else {
					clear(state.values)
				}
			}
		})
	}
}
