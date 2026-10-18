package incremental

import (
	"context"
	"fmt"
	"strings"
)

// Delta replaces only the named publications and explicitly removes owners.
// A publication key is its input-ownership unit. Omitted owners remain live.
// Reset starts an empty delta world, also when switching from full Publish.
// Unlike full Publish, an over-budget delta is rejected in its entirety.
// Retaining a complete frame proof is the caller's responsibility: use small
// name-cell owners rather than replacing a growing frame for each definition.
type Delta struct {
	Reset  bool
	Upsert []Publication
	Remove []Key
}

type deltaOwnership struct {
	owned map[Key][]Key
	refs  map[Key]map[Key]struct{}
}

func newDeltaOwnership() *deltaOwnership {
	return &deltaOwnership{owned: make(map[Key][]Key), refs: make(map[Key]map[Key]struct{})}
}

// deltaQueryCost includes logical ownership metadata as well as reservations.
// This is deliberately separate from full Publish's existing admission policy.
func deltaQueryCost(p Publication, count, limit int) (int, error) {
	cost := 0
	add := func(n int) bool {
		if n < 0 || n > limit-cost {
			return false
		}
		cost += n
		return true
	}
	if p.MaxResultBytes < 0 || p.MaxDependencies < 0 || p.MaxDependencies > limit/96 || count > limit/96 ||
		!add(keyCost(p.Key)) || !add(128) || !add(p.MaxResultBytes) || !add(p.MaxDependencies*96) || !add(count*96) {
		return 0, fmt.Errorf("delta query reservation exceeds byte budget")
	}
	return cost, nil
}

// PublishDelta prepares a coherent world in O(touched owner inputs + affected
// shared assertions), apart from occasional bounded-debt compaction. It never
// scans unrelated publications in the ordinary update path. All preparation
// failures invalidate old handles but leave retained DB/ownership data intact.
func (s *Session) PublishDelta(ctx context.Context, delta Delta) (Published, error) {
	return s.PublishDeltaPrepared(ctx, func() (Delta, error) { return delta, nil })
}

// PublishDeltaPrepared invalidates the old generation before constructing current
// inputs, including when preparation fails. The callback runs under the session
// gate and must not call back into this session. It must publish the complete set
// of changed/removed owners for its current environment before a handle is served.
func (s *Session) PublishDeltaPrepared(ctx context.Context, prepare func() (Delta, error)) (result Published, err error) {
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return result, ctx.Err()
	}
	defer func() { <-s.gate }()
	if err = ctx.Err(); err != nil {
		return result, err
	}
	s.epoch++
	s.ready = false
	s.stats.Publications++
	defer func() {
		if err != nil {
			s.stats.Rejected++
		}
	}()
	if prepare == nil {
		return result, fmt.Errorf("nil delta preparation")
	}
	delta, err := prepare()
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if len(delta.Upsert) > max(1024, s.limits.Queries*4) || len(delta.Remove) > max(1024, s.limits.Queries*4)-len(delta.Upsert) {
		return result, fmt.Errorf("delta query work budget exceeded")
	}
	ownership := s.delta
	live, inputs := s.live, s.inputs
	if delta.Reset {
		ownership = newDeltaOwnership()
		live, inputs = map[Key]sessionQuery{}, map[Key]Change{}
	} else if ownership == nil {
		if len(live) != 0 || len(inputs) != 0 {
			return result, fmt.Errorf("delta requires explicit reset after full publication")
		}
		ownership = newDeltaOwnership()
	}
	// nil plans mean explicit removal. Replacing and removing the same owner is
	// rejected rather than giving slice/map ordering semantic significance.
	plans := make(map[Key]*publicationPlan)
	workEdges := max(4096, s.limits.InputEdges*4)
	work := 0
	for _, key := range delta.Remove {
		if _, ok := plans[key]; ok {
			continue
		}
		plans[key] = nil
	}
	for _, p := range delta.Upsert {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if _, ok := s.evaluators[p.Key.Kind]; !ok {
			return result, fmt.Errorf("unknown delta query kind")
		}
		if p.MaxResultBytes < 0 || p.MaxDependencies < 0 {
			return result, fmt.Errorf("negative delta reservation")
		}
		if len(p.Inputs) > workEdges-work {
			return result, fmt.Errorf("delta input work budget exceeded")
		}
		work += len(p.Inputs)
		unique := make(map[Key]Change, len(p.Inputs))
		plan := publicationPlan{publication: p}
		for _, change := range p.Inputs {
			if change.Remove {
				change.Value = ""
			}
			if old, ok := unique[change.Key]; ok {
				if !sameChange(old, change) {
					return result, fmt.Errorf("conflicting delta owner input")
				}
				continue
			}
			unique[change.Key] = change
			plan.inputs = append(plan.inputs, change)
		}
		plan.publication.Inputs = plan.inputs
		if old, ok := plans[p.Key]; ok {
			if old == nil || !samePlan(*old, plan) {
				return result, fmt.Errorf("conflicting delta owner")
			}
			continue
		}
		plans[p.Key] = &plan
	}
	// For each affected input, stage only assertions supplied by touched owners.
	// Untouched owners are checked through the reverse index, without probing
	// unrelated inputs. One shared cell cannot silently change under another owner.
	assertions := make(map[Key]map[Key]Change)
	touched := make(map[Key]struct{})
	bytes, edges, dependencies, queries, inputCount := s.bytes, s.edges, s.dependencies, len(live), len(inputs)
	if delta.Reset {
		bytes, edges, dependencies = 0, 0, 0
	}
	for key, plan := range plans {
		if old, ok := live[key]; ok {
			oldP := Publication{Key: key, MaxResultBytes: old.resultBytes, MaxDependencies: old.dependencies}
			oldCost, costErr := deltaQueryCost(oldP, len(ownership.owned[key]), s.limits.Bytes)
			if costErr != nil {
				return result, costErr
			}
			bytes -= oldCost
			edges -= len(ownership.owned[key])
			dependencies -= old.dependencies
			queries--
			for _, input := range ownership.owned[key] {
				touched[input] = struct{}{}
			}
		}
		if plan == nil {
			continue
		}
		if _, costErr := deltaQueryCost(plan.publication, len(plan.inputs), s.limits.Bytes); costErr != nil {
			return result, costErr
		}
		for _, change := range plan.inputs {
			touched[change.Key] = struct{}{}
			if assertions[change.Key] == nil {
				assertions[change.Key] = make(map[Key]Change)
			}
			assertions[change.Key][key] = change
		}
	}
	changes := make(map[Key]Change, len(touched))
	var retired []Key
	for key := range touched {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if old, ok := inputs[key]; ok {
			bytes -= inputCost(old)
			inputCount--
		}
		var selected Change
		has := false
		for owner := range ownership.refs[key] {
			if _, ok := plans[owner]; ok {
				continue
			}
			selected = inputs[key]
			has = true
			break
		}
		for _, change := range assertions[key] {
			if has && !sameChange(selected, change) {
				return result, fmt.Errorf("conflicting shared delta input")
			}
			selected = change
			has = true
		}
		if !has {
			if _, ok := inputs[key]; ok {
				retired = append(retired, inputs[key].Key)
			}
			continue
		}
		changes[key] = selected
	}
	// All old owner reservations have now been removed. Additions are nonnegative,
	// so deterministic all-or-nothing admission never depends on map iteration.
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		cost, _ := deltaQueryCost(plan.publication, len(plan.inputs), s.limits.Bytes)
		if cost > s.limits.Bytes-bytes || len(plan.inputs) > s.limits.InputEdges-edges || plan.publication.MaxDependencies > s.limits.Dependencies-dependencies || queries >= s.limits.Queries {
			return result, fmt.Errorf("delta reservation budget exceeded")
		}
		bytes += cost
		edges += len(plan.inputs)
		dependencies += plan.publication.MaxDependencies
		queries++
	}
	// Subtract all affected old cells before adding any replacement cells; removals
	// elsewhere in the same transaction can fund an insertion deterministically.
	for _, change := range changes {
		cost := inputCost(change)
		if cost < 0 || cost > s.limits.Bytes-bytes || inputCount >= s.limits.Inputs {
			return result, fmt.Errorf("delta input budget exceeded")
		}
		bytes += cost
		inputCount++
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	// Preparation ends here. Admission succeeded; own admitted strings before
	// installing references. Existing live canonical keys/values are reused.
	queryKeys, inputKeys := s.queryKeys, s.inputKeys
	if delta.Reset {
		queryKeys, inputKeys = map[Key]Key{}, map[Key]Key{}
	}
	canonicalChanges := make(map[Key]Change, len(changes))
	for raw, change := range changes {
		key, ok := inputKeys[raw]
		if !ok {
			key = cloneKey(raw)
		}
		change.Key = key
		if old, ok := inputs[raw]; ok && sameChange(old, change) {
			change.Value = old.Value
		} else {
			change.Value = strings.Clone(change.Value)
		}
		canonicalChanges[key] = change
	}
	changes = canonicalChanges
	compact := false
	addDebt := func(n int) {
		if n < 0 || n > s.limits.Bytes-s.debtBytes {
			compact = true
			return
		}
		s.debtBytes += n
	}
	var evicted []Key
	retiredQueries := 0
	if delta.Reset {
		retiredQueries = len(s.live)
	}
	for raw, plan := range plans {
		key, existed := queryKeys[raw]
		if !existed && plan == nil {
			continue
		}
		if !existed {
			key = cloneKey(raw)
		}
		old, wasLive := live[raw]
		if wasLive {
			for _, input := range ownership.owned[raw] {
				delete(ownership.refs[input], key)
				if _, retained := assertions[input][raw]; !retained {
					addDebt(96)
				}
			}
		}
		if plan == nil {
			if wasLive {
				evicted = append(evicted, key)
				retiredQueries++
				addDebt(keyCost(key) + 128)
			}
			delete(live, key)
			delete(queryKeys, key)
			delete(ownership.owned, key)
			continue
		}
		if wasLive && (old.resultBytes != plan.publication.MaxResultBytes || old.dependencies != plan.publication.MaxDependencies) {
			evicted = append(evicted, key)
		}
		entry := sessionQuery{resultBytes: plan.publication.MaxResultBytes, dependencies: plan.publication.MaxDependencies}
		live[key] = entry
		queryKeys[key] = key
		owned := make([]Key, 0, len(plan.inputs))
		for _, change := range plan.inputs {
			canonical := changes[change.Key].Key
			if ownership.refs[canonical] == nil {
				ownership.refs[canonical] = make(map[Key]struct{})
			}
			ownership.refs[canonical][key] = struct{}{}
			owned = append(owned, canonical)
		}
		ownership.owned[key] = owned
	}
	for _, key := range retired {
		delete(inputs, key)
		delete(inputKeys, key)
		delete(ownership.refs, key)
		addDebt(keyCost(key))
		s.debtInputs++
	}
	for key, change := range changes {
		inputs[key] = change
		inputKeys[key] = key
	}
	s.live, s.inputs, s.queryKeys, s.inputKeys = live, inputs, queryKeys, inputKeys
	s.delta = ownership
	s.bytes = bytes
	s.edges = edges
	s.dependencies = dependencies
	s.stats.RetiredQueries += uint64(retiredQueries)
	if delta.Reset || compact || s.debtInputs > s.limits.Inputs {
		if !delta.Reset {
			s.compactDeltaMaps()
			inputs = s.inputs
		}
		s.totals = sumStats(s.totals, s.db.Stats())
		s.db = s.newDatabase()
		s.debtBytes = 0
		s.debtInputs = 0
		if !delta.Reset {
			s.stats.Compactions++
		}
		s.db.applySnapshot(inputs)
	} else {
		s.db.applyTransaction(changes, retired, evicted)
	}
	s.ready = true
	result.Revision = Revision{s, s.epoch}
	return result, nil
}

// compactDeltaMaps releases deleted-entry capacity in session maps and shared
// ownership maps as well as the DB tombstones. Values already own their strings.
func (s *Session) compactDeltaMaps() {
	live := make(map[Key]sessionQuery, len(s.live))
	inputs := make(map[Key]Change, len(s.inputs))
	queryKeys := make(map[Key]Key, len(s.queryKeys))
	inputKeys := make(map[Key]Key, len(s.inputKeys))
	for key, value := range s.live {
		live[key] = value
		queryKeys[key] = key
	}
	for key, value := range s.inputs {
		inputs[key] = value
		inputKeys[key] = key
	}
	ownership := newDeltaOwnership()
	for key, owned := range s.delta.owned {
		ownership.owned[key] = owned
	}
	for key, owners := range s.delta.refs {
		refs := make(map[Key]struct{}, len(owners))
		for owner := range owners {
			refs[owner] = struct{}{}
		}
		ownership.refs[key] = refs
	}
	s.live, s.inputs, s.queryKeys, s.inputKeys, s.delta = live, inputs, queryKeys, inputKeys, ownership
}
