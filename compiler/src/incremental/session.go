package incremental

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrStaleRevision = errors.New("incremental revision unavailable")
var ErrUnpublishedInput = errors.New("incremental input not published")
var ErrUnpublishedQuery = errors.New("incremental query not admitted")
var ErrDependencyBudget = errors.New("incremental dependency reservation exceeded")
var ErrResultBudget = errors.New("incremental result reservation exceeded")

// Limits bound live payload, ownership edges, and memo dependency reservations.
// Retired key metadata is compacted before its additional debt exceeds Bytes or
// Inputs. Actual Go allocator/RSS overhead and evaluator computation are separate.
type Limits struct{ Queries, Inputs, InputEdges, Dependencies, Bytes int }

func DefaultLimits() Limits { return Limits{32768, 262144, 262144, 262144, 128 << 20} }

// Publication declares one query's storage reservation and owned inputs. Inputs
// shared by publications must agree, including explicit negative (Remove) inputs.
// Evaluators may read other admitted queries/inputs in this coherent snapshot.
type Publication struct {
	Key                             Key
	Inputs                          []Change
	MaxResultBytes, MaxDependencies int
}
type sessionQuery struct {
	resultBytes, dependencies int
}
type publicationPlan struct {
	publication Publication
	inputs      []Change
}

type Session struct {
	gate                       chan struct{}
	limits                     Limits
	evaluators                 map[string]Evaluator
	db                         *Database
	live                       map[Key]sessionQuery
	inputs                     map[Key]Change
	queryKeys, inputKeys       map[Key]Key
	epoch                      uint64
	ready                      bool
	bytes, edges, dependencies int
	debtBytes, debtInputs      int
	totals                     Stats
	stats                      SessionStats
}
type Revision struct {
	session *Session
	epoch   uint64
}
type Published struct {
	Revision Revision
	Skipped  []Key
}
type SessionStats struct {
	Publications, Rejected, Skipped, Compactions, RetiredQueries                               uint64
	Ready                                                                                      bool
	RetainedQueries, RetainedInputs, InputEdges, Dependencies, AccountedBytes, RetirementBytes int
	DatabaseInputs, DatabaseQueries                                                            int
	Queries                                                                                    Stats
}

func NewSession(evaluators map[string]Evaluator, limits Limits) (*Session, error) {
	if limits.Queries <= 0 || limits.Inputs < 0 || limits.InputEdges < 0 || limits.Dependencies < 0 || limits.Bytes <= 0 || limits.Queries > int(^uint(0)>>1)/4 || limits.InputEdges > int(^uint(0)>>1)/4 {
		return nil, fmt.Errorf("invalid incremental session limits")
	}
	s := &Session{gate: make(chan struct{}, 1), limits: limits, evaluators: map[string]Evaluator{}, live: map[Key]sessionQuery{}, inputs: map[Key]Change{}, queryKeys: map[Key]Key{}, inputKeys: map[Key]Key{}}
	for kind, evaluate := range evaluators {
		if evaluate == nil {
			return nil, fmt.Errorf("nil query evaluator")
		}
		s.evaluators[strings.Clone(kind)] = evaluate
	}
	s.db = s.newDatabase()
	return s, nil
}
func (s *Session) newDatabase() *Database {
	registry := map[string]Evaluator{}
	for kind, evaluate := range s.evaluators {
		kind, evaluate := kind, evaluate
		registry[kind] = func(r *Reader, name string) (string, error) {
			entry, ok := s.live[Key{kind, name}]
			if !ok {
				return "", ErrUnpublishedQuery
			}
			r.inputAuthority = s.inputKeys
			r.queryAuthority = s.queryKeys
			r.boundedDependencies = true
			r.maxDependencies = entry.dependencies
			value, err := evaluate(r, name)
			if err != nil {
				return "", err
			}
			if len(value) > entry.resultBytes {
				return "", ErrResultBudget
			}
			return strings.Clone(value), nil
		}
	}
	return New(registry)
}
func sameChange(a, b Change) bool { return a.Remove == b.Remove && (a.Remove || a.Value == b.Value) }
func samePlan(a, b publicationPlan) bool {
	if a.publication.MaxResultBytes != b.publication.MaxResultBytes || a.publication.MaxDependencies != b.publication.MaxDependencies || len(a.inputs) != len(b.inputs) {
		return false
	}
	// Duplicate publications are uncommon. Bound linear comparison to small
	// input sets; large sets retain linear expected work through hashing.
	if len(a.inputs) > 16 {
		lookup := make(map[Key]Change, len(b.inputs))
		for _, change := range b.inputs {
			lookup[change.Key] = change
		}
		for _, value := range a.inputs {
			other, ok := lookup[value.Key]
			if !ok || !sameChange(value, other) {
				return false
			}
		}
	} else {
		for _, value := range a.inputs {
			found := false
			for _, other := range b.inputs {
				if value.Key == other.Key {
					found = sameChange(value, other)
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}
func cloneKey(key Key) Key   { return Key{strings.Clone(key.Kind), strings.Clone(key.Name)} }
func inputCost(c Change) int { return len(c.Key.Kind) + len(c.Key.Name) + len(c.Value) + 128 }
func keyCost(k Key) int      { return len(k.Kind) + len(k.Name) + 128 }
func sumStats(a, b Stats) Stats {
	return Stats{a.Executed + b.Executed, a.MemoHits + b.MemoHits, a.Validated + b.Validated, a.DependencyChecks + b.DependencyChecks, a.InputReads + b.InputReads}
}

// Publish replaces the entire admitted snapshot. Existing queries get admission
// priority; over-budget queries are explicitly skipped, never served from the old
// world. Invalid/conflicting batches leave DB data unchanged but invalidate old
// handles. Caller cancellation before acquiring the gate does not publish a world.
func (s *Session) Publish(ctx context.Context, publications []Publication) (Published, error) {
	return s.PublishPrepared(ctx, func() ([]Publication, error) { return publications, nil })
}

// PublishPrepared invalidates the previous generation BEFORE building inputs.
// Preparation failures therefore cannot leave an old handle usable for a new
// environment. The callback must not call back into this same session.
func (s *Session) PublishPrepared(ctx context.Context, prepare func() ([]Publication, error)) (result Published, err error) {
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return result, ctx.Err()
	}
	defer func() { <-s.gate }()
	if err := ctx.Err(); err != nil {
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
		return result, fmt.Errorf("nil publication preparation")
	}
	publications, err := prepare()
	if err != nil {
		return result, err
	}
	workQueries := max(1024, s.limits.Queries*4)
	workEdges := max(4096, s.limits.InputEdges*4)
	if len(publications) > workQueries {
		return result, fmt.Errorf("publication work budget exceeded")
	}
	plans := map[Key]publicationPlan{}
	allInputs := map[Key]Change{}
	var order []Key
	work := 0
	for _, publication := range publications {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if _, ok := s.evaluators[publication.Key.Kind]; !ok {
			return result, fmt.Errorf("unknown publication kind")
		}
		if publication.MaxResultBytes < 0 || publication.MaxDependencies < 0 {
			return result, fmt.Errorf("negative query reservation")
		}
		if len(publication.Inputs) > workEdges-work {
			return result, fmt.Errorf("publication edge work budget exceeded")
		}
		work += len(publication.Inputs)
		plan := publicationPlan{publication: publication, inputs: make([]Change, 0, len(publication.Inputs))}
		var seenInputs map[Key]bool
		if len(publication.Inputs) > 16 {
			seenInputs = make(map[Key]bool, len(publication.Inputs))
		}
		for _, change := range publication.Inputs {
			if change.Remove {
				change.Value = ""
			}
			if old, ok := allInputs[change.Key]; ok && !sameChange(old, change) {
				return result, fmt.Errorf("conflicting snapshot input")
			}
			// Snapshot-wide conflict checking above also covers duplicates within
			// a query. Small lists avoid a separate hash-map allocation per query.
			duplicate := false
			if seenInputs != nil {
				duplicate = seenInputs[change.Key]
				seenInputs[change.Key] = true
			} else {
				for _, previous := range plan.inputs {
					if previous.Key == change.Key {
						duplicate = true
						break
					}
				}
			}
			if !duplicate {
				plan.inputs = append(plan.inputs, change)
			}
			allInputs[change.Key] = change
		}
		if old, ok := plans[publication.Key]; ok {
			if !samePlan(old, plan) {
				return result, fmt.Errorf("conflicting query publication")
			}
			continue
		}
		plans[publication.Key] = plan
		order = append(order, publication.Key)
	}
	selected := map[Key]publicationPlan{}
	selectedInputs := map[Key]Change{}
	bytes, edges, dependencies := 0, 0, 0
	for _, retained := range []bool{true, false} {
		for _, key := range order {
			_, existed := s.live[key]
			if existed != retained {
				continue
			}
			plan := plans[key]
			p := plan.publication
			cost := 0
			fits := true
			add := func(n int) {
				if n < 0 || n > s.limits.Bytes-cost {
					fits = false
					return
				}
				cost += n
			}
			if p.MaxDependencies > s.limits.Bytes/96 || len(plan.inputs) > s.limits.Bytes/40 {
				fits = false
			} else {
				add(p.MaxDependencies * 96)
				add(len(plan.inputs) * 40)
			}
			add(keyCost(key))
			add(p.MaxResultBytes)
			newInputs := 0
			for _, change := range plan.inputs {
				input := change.Key
				if _, ok := selectedInputs[input]; !ok {
					newInputs++
					add(inputCost(change))
				}
			}
			if !fits || len(selected) >= s.limits.Queries || newInputs > s.limits.Inputs-len(selectedInputs) || len(plan.inputs) > s.limits.InputEdges-edges || p.MaxDependencies > s.limits.Dependencies-dependencies || cost > s.limits.Bytes-bytes {
				result.Skipped = append(result.Skipped, key)
				continue
			}
			selected[key] = plan
			for _, change := range plan.inputs {
				input := change.Key
				selectedInputs[input] = change
			}
			bytes += cost
			edges += len(plan.inputs)
			dependencies += p.MaxDependencies
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	// Clone admitted strings only; oversized rejected caller payloads are never
	// copied into the retained cache. Reuse canonical key storage while it is live.
	inputs := make(map[Key]Change, len(selectedInputs))
	inputKeys := make(map[Key]Key, len(selectedInputs))
	for raw, change := range selectedInputs {
		key, ok := s.inputKeys[raw]
		if !ok {
			key = cloneKey(raw)
		}
		change.Key = key
		if old, ok := s.inputs[raw]; ok && sameChange(old, change) {
			change.Value = old.Value
		} else {
			change.Value = strings.Clone(change.Value)
		}
		inputs[key] = change
		inputKeys[key] = key
	}
	live := make(map[Key]sessionQuery, len(selected))
	queryKeys := make(map[Key]Key, len(selected))
	for raw, plan := range selected {
		key, ok := s.queryKeys[raw]
		if !ok {
			key = cloneKey(raw)
		}
		entry := sessionQuery{resultBytes: plan.publication.MaxResultBytes, dependencies: plan.publication.MaxDependencies}
		live[key] = entry
		queryKeys[key] = key
	}
	var retired []Key
	for key := range s.inputs {
		if _, ok := inputs[key]; !ok {
			retired = append(retired, key)
			s.debtBytes += keyCost(key)
			s.debtInputs++
		}
	}
	for key, old := range s.live {
		current, ok := live[key]
		if !ok || current.resultBytes != old.resultBytes || current.dependencies != old.dependencies {
			s.db.EvictQuery(key)
		}
		if !ok {
			s.stats.RetiredQueries++
			s.debtBytes += keyCost(key)
		}
	}
	s.live = live
	s.inputs = inputs
	s.inputKeys = inputKeys
	s.queryKeys = queryKeys
	s.bytes = bytes
	s.edges = edges
	s.dependencies = dependencies
	if s.debtBytes > s.limits.Bytes || s.debtInputs > s.limits.Inputs {
		s.totals = sumStats(s.totals, s.db.Stats())
		s.db = s.newDatabase()
		s.debtBytes = 0
		s.debtInputs = 0
		s.stats.Compactions++
	} else {
		s.db.RetireInputs(retired...)
	}
	changes := make([]Change, 0, len(inputs))
	for _, change := range inputs {
		changes = append(changes, change)
	}
	s.db.Apply(changes...)
	s.ready = true
	s.stats.Skipped += uint64(len(result.Skipped))
	result.Revision = Revision{s, s.epoch}
	return result, nil
}
func (r Revision) Evaluate(ctx context.Context, key Key) (string, error) {
	s := r.session
	if s == nil {
		return "", ErrStaleRevision
	}
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-s.gate }()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !s.ready || r.epoch != s.epoch {
		return "", ErrStaleRevision
	}
	canonical, ok := s.queryKeys[key]
	if !ok {
		return "", ErrUnpublishedQuery
	}
	return s.db.Evaluate(ctx, canonical)
}
func (s *Session) Stats() SessionStats {
	s.gate <- struct{}{}
	defer func() { <-s.gate }()
	result := s.stats
	result.Ready = s.ready
	result.RetainedQueries = len(s.live)
	result.RetainedInputs = len(s.inputs)
	result.InputEdges = s.edges
	result.Dependencies = s.dependencies
	result.AccountedBytes = s.bytes
	result.RetirementBytes = s.debtBytes
	result.DatabaseInputs, result.DatabaseQueries = s.db.EntryCounts()
	result.Queries = sumStats(s.totals, s.db.Stats())
	return result
}
