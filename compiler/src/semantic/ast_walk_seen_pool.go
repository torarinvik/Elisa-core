package semantic

import "sync"

var fillMayAdoptSeenPool = sync.Pool{
	New: func() any { return &fillWalkSeen{values: make(map[uintptr]bool)} },
}

type fillWalkSeen struct {
	values map[uintptr]bool
	peak   int
	sparse int
}

const fillWalkSeenMaxEntries = 1 << 16

func (s *fillWalkSeen) reset() bool {
	return s.resetWithLimit(fillWalkSeenMaxEntries)
}

func (s *fillWalkSeen) resetWithLimit(limit int) bool {
	used := len(s.values)
	// Avoid retaining an exceptional whole-program walk in every worker's pool.
	if used > limit {
		return false
	}
	s.peak = max(s.peak, used)
	if used < s.peak/8 {
		s.sparse++
	} else {
		s.sparse = 0
	}
	if s.sparse >= 32 {
		// Both clear and range scan retained map storage. Replace an oversized
		// table after sustained sparse use, not when alternating between large
		// and small bodies: repeated shrinking would force repeated map growth.
		s.values = make(map[uintptr]bool, used)
		s.peak = used
		s.sparse = 0
	} else {
		clear(s.values)
	}
	return true
}
