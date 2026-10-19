package semantic

// One operation owns this set. Small graphs avoid a hash table; larger graphs
// spill once. Explicit caller maps remain authoritative and receive every write.
type mutableBorrowVisitSet struct {
	small [8]Type
	count int
	large map[Type]bool
}

func (s *mutableBorrowVisitSet) has(t Type) bool {
	if s.large != nil {
		return s.large[t]
	}
	for i := 0; i < s.count; i++ {
		if s.small[i] == t {
			return true
		}
	}
	return false
}
func (s *mutableBorrowVisitSet) add(t Type) {
	if s.large != nil {
		s.large[t] = true
		return
	}
	if s.count < len(s.small) {
		s.small[s.count] = t
		s.count++
		return
	}
	s.large = make(map[Type]bool, 16)
	for i := 0; i < s.count; i++ {
		s.large[s.small[i]] = true
	}
	clear(s.small[:])
	s.count = 0
	s.large[t] = true
}
