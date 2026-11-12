package checkpointhistory

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"slices"
	"strings"
)

// A build checkpoint may end with a separately checksummed SMT history.
// Keeping it outside the graph preserves the registry schema and allows the
// decoder to read older checkpoints. Ordinary semantic snapshots use neither
// the footer nor the history encoding.
const checkpointHistoryMagic = "ELISASMT1"
const checkpointHistoryFooter = 8 + sha256.Size + len(checkpointHistoryMagic)
const checkpointHistoryMaxBytes = 256 << 20
const checkpointHistoryMinSavings = 64 << 10

func checkpointHistoryPrefix(previous, query string) int {
	const block = 4096
	limit := min(len(previous), len(query))
	start := 0
	for limit-start >= block && previous[start:start+block] == query[start:start+block] {
		start += block
	}
	for start < limit && previous[start] == query[start] {
		start++
	}
	return start
}

func Plan(history []string, limit int) (prefixes []int, capacity int) {
	if len(history) < 2 || len(history) > limit {
		return nil, 0
	}
	var total int
	for _, query := range history {
		if len(query) > checkpointHistoryMaxBytes-total {
			return nil, 0
		}
		total += len(query)
	}
	if total < checkpointHistoryMinSavings {
		return nil, 0
	}
	prefixes = make([]int, len(history))
	saved := 0
	for i := 1; i < len(history); i++ {
		prefixes[i] = checkpointHistoryPrefix(history[i-1], history[i])
		saved += prefixes[i]
	}
	// Reserve the upper bound on varint overhead so the output grows once.
	const perQueryOverhead = 2 * binary.MaxVarintLen64
	overhead := binary.MaxVarintLen64 + len(history)*perQueryOverhead + checkpointHistoryFooter
	if saved-overhead < checkpointHistoryMinSavings {
		return nil, 0
	}
	return prefixes, total - saved + overhead
}

func Append(graph []byte, history []string, prefixes []int, capacity int) []byte {
	start := len(graph)
	data := slices.Grow(graph, capacity)
	data = binary.AppendUvarint(data, uint64(len(history)))
	for i, query := range history {
		prefix := prefixes[i]
		data = binary.AppendUvarint(data, uint64(prefix))
		data = binary.AppendUvarint(data, uint64(len(query)-prefix))
		data = append(data, query[prefix:]...)
	}
	digest := checkpointHistoryDigest(data[:start], data[start:])
	data = binary.LittleEndian.AppendUint64(data, uint64(len(data)-start))
	data = append(data, digest[:]...)
	return append(data, checkpointHistoryMagic...)
}

func Split(data []byte) (graph, history []byte, err error) {
	if len(data) < checkpointHistoryFooter || !bytes.HasSuffix(data, []byte(checkpointHistoryMagic)) {
		return data, nil, nil
	}
	footer := data[len(data)-checkpointHistoryFooter:]
	size := binary.LittleEndian.Uint64(footer)
	if size == 0 || len(data)-len(footer) < sha256.Size || size > uint64(len(data)-len(footer)-sha256.Size) || size > checkpointHistoryMaxBytes {
		return nil, nil, fmt.Errorf("invalid checkpoint SMT history size")
	}
	end := len(data) - len(footer)
	start := end - int(size)
	history = data[start:end]
	digest := checkpointHistoryDigest(data[:start], history)
	if !bytes.Equal(digest[:], footer[8:8+sha256.Size]) {
		return nil, nil, fmt.Errorf("checkpoint SMT history checksum mismatch")
	}
	return data[:start], history, nil
}

// The graph's trailing checksum binds the history to this exact snapshot
// without reading the entire graph again. Decode also validates that checksum.
func checkpointHistoryDigest(graph, history []byte) [sha256.Size]byte {
	hash := sha256.New()
	hash.Write(graph[len(graph)-sha256.Size:])
	hash.Write(history)
	var digest [sha256.Size]byte
	hash.Sum(digest[:0])
	return digest
}

func Restore(data []byte, limit uint64) ([]string, error) {
	count, width := binary.Uvarint(data)
	if width <= 0 || count == 0 || count > limit {
		return nil, fmt.Errorf("invalid checkpoint SMT history count")
	}
	data = data[width:]
	history := make([]string, 0, int(count))
	previous := ""
	remaining := uint64(checkpointHistoryMaxBytes)
	for range count {
		prefix, width := binary.Uvarint(data)
		if width <= 0 || prefix > uint64(len(previous)) || prefix > remaining {
			return nil, fmt.Errorf("invalid checkpoint SMT history prefix")
		}
		data = data[width:]
		suffix, width := binary.Uvarint(data)
		if width <= 0 || suffix > uint64(len(data)-width) || suffix > remaining-prefix {
			return nil, fmt.Errorf("invalid checkpoint SMT history suffix")
		}
		data = data[width:]
		var query strings.Builder
		query.Grow(int(prefix + suffix))
		query.WriteString(previous[:int(prefix)])
		query.Write(data[:int(suffix)])
		previous = query.String()
		history = append(history, previous)
		remaining -= prefix + suffix
		data = data[int(suffix):]
	}
	if len(data) != 0 {
		return nil, fmt.Errorf("trailing checkpoint SMT history data")
	}
	return history, nil
}
