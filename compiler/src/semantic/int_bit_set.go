package semantic

import "math/bits"

// IntBitSet is a tiny integer-indexed bit set with an inline first word and
// lazily allocated overflow words for indices >= 64.
type IntBitSet struct {
	inline uint64
	extra  []uint64
}

func intBitSetOf(indices ...int) IntBitSet {
	var out IntBitSet
	for _, index := range indices {
		out.Add(index)
	}
	return out
}

func (s IntBitSet) Clone() IntBitSet {
	out := IntBitSet{inline: s.inline}
	if len(s.extra) != 0 {
		out.extra = append([]uint64(nil), s.extra...)
	}
	return out
}

func (s IntBitSet) IsEmpty() bool {
	if s.inline != 0 {
		return false
	}
	for _, word := range s.extra {
		if word != 0 {
			return false
		}
	}
	return true
}

func (s IntBitSet) Contains(index int) bool {
	if index < 0 {
		return false
	}
	word := index >> 6
	mask := uint64(1) << uint(index&63)
	if word == 0 {
		return s.inline&mask != 0
	}
	extraIndex := word - 1
	if extraIndex >= len(s.extra) {
		return false
	}
	return s.extra[extraIndex]&mask != 0
}

func (s *IntBitSet) Add(index int) {
	if s == nil || index < 0 {
		return
	}
	word := index >> 6
	mask := uint64(1) << uint(index&63)
	if word == 0 {
		s.inline |= mask
		return
	}
	extraIndex := word - 1
	if extraIndex >= len(s.extra) {
		s.extra = append(s.extra, make([]uint64, extraIndex+1-len(s.extra))...)
	}
	s.extra[extraIndex] |= mask
}

func (s IntBitSet) Count() int {
	count := bits.OnesCount64(s.inline)
	for _, word := range s.extra {
		count += bits.OnesCount64(word)
	}
	return count
}

func (s IntBitSet) ForEach(fn func(int)) {
	if fn == nil {
		return
	}
	intBitSetForEachWord(s.inline, 0, fn)
	for i, word := range s.extra {
		intBitSetForEachWord(word, (i+1)*64, fn)
	}
}

func intBitSetForEachWord(word uint64, base int, fn func(int)) {
	for word != 0 {
		bit := bits.TrailingZeros64(word)
		fn(base + bit)
		word &= word - 1
	}
}