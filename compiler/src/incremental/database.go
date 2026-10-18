// Package incremental implements revisioned, tracked compiler queries. Values are
// immutable strings so retaining a result never shares mutable compiler state.
package incremental

import (
	"context"
	"fmt"
)

// Key identifies a query or input independently of source offsets. Kind separates
// dependency domains (body, interface, lookup set, target, and so on).
type Key struct{ Kind, Name string }

type Evaluator func(*Reader, string) (string, error)

type input struct {
	value   string
	present bool
	changed uint64
}
type dependency struct {
	key   Key
	query bool
}
type result struct {
	value             string
	dependencies      []dependency
	verified, changed uint64
}

// Stats distinguishes work executed from queries reused after dependency checks.
type Stats struct{ Executed, MemoHits, Validated, DependencyChecks, InputReads uint64 }

// EvaluationResult is one position-preserving result from EvaluateBatch.
type EvaluationResult struct {
	Value string
	Err   error
}

// Database serializes revisions and evaluation, giving each query a consistent
// input snapshot. Evaluators are immutable for the database lifetime. They must
// read all changing data through Reader, including failed name lookups.
// Parallel scheduling and portable result storage are separate layers.
type Database struct {
	gate       chan struct{}
	revision   uint64
	evaluators map[string]Evaluator
	inputs     map[Key]input
	results    map[Key]*result
	stats      Stats
}

func New(evaluators map[string]Evaluator) *Database {
	registry := make(map[string]Evaluator, len(evaluators))
	for kind, evaluate := range evaluators {
		registry[kind] = evaluate
	}
	return &Database{gate: make(chan struct{}, 1), revision: 1, evaluators: registry, inputs: map[Key]input{}, results: map[Key]*result{}}
}

// Change can distinguish removing an input from setting it to an empty value.
type Change struct {
	Key    Key
	Value  string
	Remove bool
}

// Apply commits a batch as one revision. A byte-identical batch is a no-op.
// Later entries for one key win, so a change followed by its reversal is a no-op.
func (db *Database) Apply(changes ...Change) bool {
	db.gate <- struct{}{}
	defer func() { <-db.gate }()
	if len(changes) == 1 {
		change := changes[0]
		old := db.inputs[change.Key]
		if old.present == !change.Remove && (change.Remove || old.value == change.Value) {
			return false
		}
	}
	pending := make(map[Key]Change, len(changes))
	for _, change := range changes {
		pending[change.Key] = change
	}
	return db.applyPending(pending)
}

// applySnapshot consumes a session's already deduplicated input inventory without
// constructing another full-size map. The caller owns the map; only its immutable
// string values are retained. As with Apply's slice, it must stay unchanged during
// the call. Public Apply still resolves duplicate entries with last-write wins.
func (db *Database) applySnapshot(changes map[Key]Change) bool {
	db.gate <- struct{}{}
	defer func() { <-db.gate }()
	return db.applyPending(changes)
}

// applyPending runs under the database gate. Checking the complete inventory
// before writing preserves one atomic revision and byte-identical no-op batches.
func (db *Database) applyPending(pending map[Key]Change) bool {
	modified := false
	for key, change := range pending {
		old := db.inputs[key]
		if old.present != !change.Remove || (!change.Remove && old.value != change.Value) {
			modified = true
			break
		}
	}
	if !modified {
		return false
	}
	db.revision++
	for key, change := range pending {
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
	return true
}

// EvictQuery releases a memoized value. Advancing the revision makes parents
// revalidate discarded children before reusing their own values.
func (db *Database) EvictQuery(key Key) bool {
	db.gate <- struct{}{}
	defer func() { <-db.gate }()
	if _, ok := db.results[key]; !ok {
		return false
	}
	delete(db.results, key)
	db.revision++
	return true
}

func (db *Database) Stats() Stats {
	db.gate <- struct{}{}
	defer func() { <-db.gate }()
	return db.stats
}

// EntryCounts includes tombstone inputs and supports bounded cache maintenance.
func (db *Database) EntryCounts() (inputs, queries int) {
	db.gate <- struct{}{}
	defer func() { <-db.gate }()
	return len(db.inputs), len(db.results)
}

// Evaluate validates only the requested dependency closure. Semantic diagnostics
// belong in the immutable result value; errors mean evaluation could not finish
// (cancellation, an unsupported query, or a cycle) and are never memoized.
// Evaluators cannot suppress dependency errors: those invalidate their result too.
func (db *Database) Evaluate(ctx context.Context, key Key) (string, error) {
	select {
	case db.gate <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-db.gate }()
	return db.evaluate(ctx, key, nil)
}

// EvaluateBatch validates each requested dependency closure while holding one
// database snapshot. Results preserve key order; one query error does not
// prevent independent later queries from running. This is useful when an
// analysis phase discovers many independent unit products at once.
func (db *Database) EvaluateBatch(ctx context.Context, keys []Key) []EvaluationResult {
	if len(keys) == 0 {
		return nil
	}
	results := make([]EvaluationResult, len(keys))
	select {
	case db.gate <- struct{}{}:
	case <-ctx.Done():
		err := ctx.Err()
		for i := range results {
			results[i].Err = err
		}
		return results
	}
	defer func() { <-db.gate }()
	// Evaluation removes every active key on return, including errors. Reuse
	// this invocation-local stack across the serial roots in one snapshot.
	active := map[Key]bool{}
	for i, key := range keys {
		results[i].Value, results[i].Err = db.evaluate(ctx, key, active)
		if err := ctx.Err(); err != nil {
			for remaining := i + 1; remaining < len(results); remaining++ {
				results[remaining].Err = err
			}
			break
		}
	}
	return results
}

// Reader is scoped to one evaluator. Never retain it or use it concurrently.
type Reader struct {
	db                  *Database
	context             context.Context
	active              map[Key]bool
	dependencies        []dependency
	seen                map[dependency]bool
	failure             error
	inputAuthority      map[Key]Key
	queryAuthority      map[Key]Key
	boundedDependencies bool
	maxDependencies     int
}

// Most compiler queries read only a few dependencies. Keep their ordered slice
// as the deduplication set, allocating a hash index only for larger closures.
const smallDependencySet = 8

func (r *Reader) hasDependency(dep dependency) bool {
	if r.seen != nil {
		return r.seen[dep]
	}
	for _, previous := range r.dependencies {
		if previous == dep {
			return true
		}
	}
	return false
}

func (r *Reader) remember(dep dependency) bool {
	if r.failure != nil {
		return false
	}
	if r.hasDependency(dep) {
		return true
	}
	if r.boundedDependencies && len(r.dependencies) >= r.maxDependencies {
		r.failure = ErrDependencyBudget
		return false
	}
	if r.seen == nil && len(r.dependencies) >= smallDependencySet {
		r.seen = make(map[dependency]bool, 2*smallDependencySet)
		for _, previous := range r.dependencies {
			r.seen[previous] = true
		}
	}
	if r.seen != nil {
		r.seen[dep] = true
	}
	r.dependencies = append(r.dependencies, dep)
	return true
}
func (r *Reader) Input(key Key) (string, bool) {
	if r.failure != nil {
		return "", false
	}
	if r.inputAuthority != nil {
		canonical, ok := r.inputAuthority[key]
		if !ok {
			r.failure = fmt.Errorf("%w: %v", ErrUnpublishedInput, key)
			return "", false
		}
		key = canonical
	}
	if !r.remember(dependency{key: key}) {
		return "", false
	}
	r.db.stats.InputReads++
	value := r.db.inputs[key]
	return value.value, value.present
}
func (r *Reader) Query(key Key) (string, error) {
	if r.failure != nil {
		return "", r.failure
	}
	if r.queryAuthority != nil {
		canonical, ok := r.queryAuthority[key]
		if !ok {
			r.failure = fmt.Errorf("%w: %v", ErrUnpublishedQuery, key)
			return "", r.failure
		}
		key = canonical
	}
	if r.boundedDependencies && !r.hasDependency(dependency{key: key, query: true}) && len(r.dependencies) >= r.maxDependencies {
		r.failure = ErrDependencyBudget
		return "", r.failure
	}
	value, err := r.db.evaluate(r.context, key, r.active)
	if err == nil {
		if !r.remember(dependency{key: key, query: true}) {
			return "", r.failure
		}
	} else if r.failure == nil {
		r.failure = err
	}
	return value, err
}

// Context is for cancellation and tracing only. Semantic options and source
// authority must be tracked Inputs, never values obtained from Context.Value.
func (r *Reader) Context() context.Context { return r.context }

// CycleError requires a compiler client to evaluate an interprocedural SCC as a
// single query/fixpoint, rather than accepting a partially computed body result.
type CycleError struct{ Key Key }

func (e *CycleError) Error() string {
	return fmt.Sprintf("incremental query cycle: %s:%s", e.Key.Kind, e.Key.Name)
}

func (db *Database) evaluate(ctx context.Context, key Key, active map[Key]bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if active[key] {
		return "", &CycleError{key}
	}
	old, known := db.results[key]
	if known && old.verified == db.revision {
		db.stats.MemoHits++
		return old.value, nil
	}
	// Root memo hits need no cycle-detection storage. Nested calls and batch
	// roots already carry their invocation-local active set.
	if active == nil {
		active = map[Key]bool{}
	}
	active[key] = true
	defer delete(active, key)
	if known {
		unchanged := true
		for _, dep := range old.dependencies {
			db.stats.DependencyChecks++
			changed := uint64(0)
			if dep.query {
				if _, err := db.evaluate(ctx, dep.key, active); err != nil {
					return "", err
				}
				changed = db.results[dep.key].changed
			} else {
				changed = db.inputs[dep.key].changed
			}
			if changed > old.verified {
				unchanged = false
				break
			}
		}
		if unchanged {
			old.verified = db.revision
			db.stats.Validated++
			return old.value, nil
		}
	}
	evaluator, ok := db.evaluators[key.Kind]
	if !ok || evaluator == nil {
		return "", fmt.Errorf("unknown incremental query kind %q", key.Kind)
	}
	reader := &Reader{db: db, context: ctx, active: active}
	db.stats.Executed++
	value, err := evaluator(reader, key.Name)
	if reader.failure != nil {
		return "", reader.failure
	}
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	changed := db.revision
	if known && old.value == value {
		changed = old.changed
	}
	db.results[key] = &result{value: value, dependencies: reader.dependencies, verified: db.revision, changed: changed}
	return value, nil
}

// RetireInputs invalidates readers even when an input was already absent.
// Ownership retirement is different from publishing an unchanged negative
// binding: readers must re-establish that their input is still published.
// Tombstones remain until the owning session compacts its database.
func (db *Database) RetireInputs(keys ...Key) {
	if len(keys) == 0 {
		return
	}
	db.gate <- struct{}{}
	defer func() { <-db.gate }()
	db.revision++
	for _, key := range keys {
		db.inputs[key] = input{changed: db.revision}
	}
}
