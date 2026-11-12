package statecache

import (
	"crypto/sha256"
	"sync"
)

const parallelChecksumFormat = "ELISTATE5"
const parallelChecksumMin = 32 << 20

// stateChecksum keeps sequential SHA-256 for older formats. Large framed
// checkpoints use SHA-256 over eight ordered block digests. The block count
// and boundaries depend only on the format and payload size.
func stateChecksum(data []byte) [sha256.Size]byte {
	if string(data[:len(parallelChecksumFormat)]) != parallelChecksumFormat {
		return sha256.Sum256(data)
	}
	const blocks = 8
	var hashes [blocks * sha256.Size]byte
	var group sync.WaitGroup
	for index := 0; index < blocks; index++ {
		group.Go(func() {
			from := index * len(data) / blocks
			to := (index + 1) * len(data) / blocks
			digest := sha256.Sum256(data[from:to])
			copy(hashes[index*sha256.Size:], digest[:])
		})
	}
	group.Wait()
	return sha256.Sum256(hashes[:])
}
