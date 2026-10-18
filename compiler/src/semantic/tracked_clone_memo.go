package semantic

// A memo belongs to one clone operation (or one root-map clone). Small graphs
// use stack storage; larger graphs spill once to the ordinary identity map.
// No type graph or clone is retained across analysis operations.
type trackedCloneMemo struct {
	small     [8]struct{ key, value Type }
	count     int
	large     map[Type]Type
	spillHint int
}

func (m *trackedCloneMemo) get(key Type) (Type, bool) {
	if m.large != nil {
		value, ok := m.large[key]
		return value, ok
	}
	for i := 0; i < m.count; i++ {
		if m.small[i].key == key {
			return m.small[i].value, true
		}
	}
	return nil, false
}
func (m *trackedCloneMemo) set(key, value Type) {
	if m.large != nil {
		m.large[key] = value
		return
	}
	for i := 0; i < m.count; i++ {
		if m.small[i].key == key {
			m.small[i].value = value
			return
		}
	}
	if m.count < len(m.small) {
		m.small[m.count].key = key
		m.small[m.count].value = value
		m.count++
		return
	}
	hint := m.spillHint
	if hint < 16 {
		hint = 16
	}
	m.large = make(map[Type]Type, hint)
	for i := 0; i < m.count; i++ {
		m.large[m.small[i].key] = m.small[i].value
	}
	clear(m.small[:])
	m.count = 0
	m.large[key] = value
}
