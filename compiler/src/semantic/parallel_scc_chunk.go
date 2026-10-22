package semantic

import (
	"os"
	"strconv"
)

// Keep the normal batching policy while permitting bounded seed experiments
// that trade more unit exports for shorter recursive-group scheduling tails.
func parallelSCCChunkSize() int {
	if value, err := strconv.Atoi(os.Getenv("ELISA_PARALLEL_SCC_CHUNK")); err == nil && value >= 1 && value <= 64 {
		return value
	}
	return 8
}
