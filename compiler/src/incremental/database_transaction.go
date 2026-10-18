package incremental

// applyTransaction is the session delta commit primitive. The session has
// already validated admission, cloned owned values, and made these sets
// disjoint: retired inputs cannot also occur in changes. Holding one DB gate
// gives input updates, authority retirement and memo eviction one revision.
// Caller maps/slices are borrowed only for this call.
func (db *Database) applyTransaction(changes map[Key]Change, retired, evicted []Key) bool {
	db.gate <- struct{}{}
	defer func() { <-db.gate }()
	modified := len(retired) != 0
	for _, key := range evicted {
		if _, ok := db.results[key]; ok {
			modified = true
			break
		}
	}
	if !modified {
		for key, change := range changes {
			old := db.inputs[key]
			if old.present != !change.Remove || (!change.Remove && old.value != change.Value) {
				modified = true
				break
			}
		}
	}
	if !modified {
		return false
	}
	db.revision++
	for _, key := range evicted {
		delete(db.results, key)
	}
	for key, change := range changes {
		old := db.inputs[key]
		if old.present == !change.Remove && (change.Remove || old.value == change.Value) {
			continue
		}
		value := change.Value
		if change.Remove {
			value = ""
		}
		db.inputs[key] = input{value: value, present: !change.Remove, changed: db.revision}
	}
	for _, key := range retired {
		db.inputs[key] = input{changed: db.revision}
	}
	return true
}
