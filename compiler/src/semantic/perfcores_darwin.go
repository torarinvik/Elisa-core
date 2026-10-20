//go:build darwin

package semantic

import (
	"encoding/binary"
	"syscall"
)

// performanceCores is the number of performance cores on Apple silicon (0 when
// the machine does not report core levels).
func performanceCores() int {
	raw, err := syscall.Sysctl("hw.perflevel0.physicalcpu")
	if err != nil {
		return 0
	}
	buf := []byte(raw)
	// Sysctl strips one trailing NUL; restore the 4-byte little-endian value.
	for len(buf) < 4 {
		buf = append(buf, 0)
	}
	return int(binary.LittleEndian.Uint32(buf[:4]))
}
