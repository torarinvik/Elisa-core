package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"

	"elisacore/src/incremental"
)

const capabilityQueryKind = "mutable-borrow-capability-v1"
const capabilityMaxNodes = semanticTraversalDepthLimit
const capabilityMaxEdges = 4096
const capabilityMaxSourceBytes = 64 << 10
const capabilityMaxProducts = 1024
const capabilityMaxProductBytes = 8 << 20
const capabilityMaxLocalTypes = 65536

var errCapabilityMiss = errors.New("capability product requires current traversal")

type capabilityProductEntry struct {
	source, product string
	bytes           int
	use             uint64
}
type capabilityQueryCache struct {
	db        *incremental.Database
	entries   map[string]capabilityProductEntry
	bytes     int
	clock     uint64
	evictions int
	queries   incremental.Stats
}
type capabilityLocalResult struct{ value, declined bool }
type capabilityQueryInvocation struct {
	session *IncrementalAnalysisSession
	local   map[Type]capabilityLocalResult
}

func newCapabilityQueryCache() *capabilityQueryCache {
	return &capabilityQueryCache{entries: map[string]capabilityProductEntry{}, db: newCapabilityDatabase()}
}
func newCapabilityDatabase() *incremental.Database {
	return incremental.New(map[string]incremental.Evaluator{
		capabilityQueryKind: func(r *incremental.Reader, name string) (string, error) {
			source, ok := r.Input(incremental.Key{Kind: "capability-shape", Name: name})
			if !ok {
				return "", errCapabilityMiss
			}
			product, ok := r.Input(incremental.Key{Kind: "capability-product", Name: name})
			if !ok || len(product) != len(source)+1 || product[1:] != source || (product[0] != '0' && product[0] != '1') {
				return "", errCapabilityMiss
			}
			return product[:1], nil
		},
	})
}
func (c *capabilityQueryCache) evict(name string) {
	entry := c.entries[name]
	c.bytes -= entry.bytes
	delete(c.entries, name)
	c.db.Apply(incremental.Change{Key: incremental.Key{Kind: "capability-shape", Name: name}, Remove: true}, incremental.Change{Key: incremental.Key{Kind: "capability-product", Name: name}, Remove: true})
	c.db.EvictQuery(incremental.Key{Kind: capabilityQueryKind, Name: name})
	c.evictions++
}
func (c *capabilityQueryCache) lookup(source string) (string, bool) {
	digest := sha256.Sum256([]byte(source))
	name := hex.EncodeToString(digest[:])
	entry, ok := c.entries[name]
	if !ok || entry.source != source {
		return "", false
	}
	c.clock++
	entry.use = c.clock
	c.entries[name] = entry
	result, err := c.db.Evaluate(context.Background(), incremental.Key{Kind: capabilityQueryKind, Name: name})
	return result, err == nil && (result == "0" || result == "1")
}
func (c *capabilityQueryCache) store(source string, value bool) bool {
	if len(source) > capabilityMaxSourceBytes {
		return false
	}
	digest := sha256.Sum256([]byte(source))
	name := hex.EncodeToString(digest[:])
	product := "0" + source
	if value {
		product = "1" + source
	}
	size := len(source) + len(product) + 256
	if size > capabilityMaxProductBytes {
		return false
	}
	if _, exists := c.entries[name]; exists {
		c.evict(name)
	}
	for len(c.entries) >= capabilityMaxProducts || c.bytes > capabilityMaxProductBytes-size {
		oldest := ""
		use := ^uint64(0)
		for key, entry := range c.entries {
			if entry.use < use {
				oldest = key
				use = entry.use
			}
		}
		c.evict(oldest)
	}
	if c.evictions >= capabilityMaxProducts {
		old := c.db.Stats()
		c.queries.Executed += old.Executed
		c.queries.MemoHits += old.MemoHits
		c.queries.Validated += old.Validated
		c.queries.DependencyChecks += old.DependencyChecks
		c.queries.InputReads += old.InputReads
		c.db = newCapabilityDatabase()
		c.evictions = 0
		for key, entry := range c.entries {
			c.db.Apply(incremental.Change{Key: incremental.Key{Kind: "capability-shape", Name: key}, Value: entry.source}, incremental.Change{Key: incremental.Key{Kind: "capability-product", Name: key}, Value: entry.product})
		}
	}
	c.clock++
	c.entries[name] = capabilityProductEntry{source: source, product: product, bytes: size, use: c.clock}
	c.bytes += size
	c.db.Apply(incremental.Change{Key: incremental.Key{Kind: "capability-shape", Name: name}, Value: source}, incremental.Change{Key: incremental.Key{Kind: "capability-product", Name: name}, Value: product})
	_, err := c.db.Evaluate(context.Background(), incremental.Key{Kind: capabilityQueryKind, Name: name})
	return err == nil
}

// capabilityTypeProjection captures only resolved inputs of the nongeneric
// capability predicate. It performs no substitution, lookup or diagnostics.
// At most traversalLimit distinct nodes makes every simple DFS path, including
// a closing cycle edge, fit the original traversal's depth bound in any map order.
func capabilityTypeProjection(root Type) (string, bool) {
	var out capabilityWriter
	out.tag(capabilityQueryKind)
	seen := map[Type]int{}
	edges := 0
	var visit func(Type) bool
	visit = func(typ Type) bool {
		edges++
		if edges > capabilityMaxEdges || out.failed {
			return false
		}
		if typ == nil {
			out.tag("nil")
			return true
		}
		if old, ok := seen[typ]; ok {
			out.tag("edge")
			out.integer(old)
			return true
		}
		if len(seen) >= capabilityMaxNodes {
			return false
		}
		seen[typ] = len(seen)
		out.tag("node")
		out.integer(seen[typ])
		fields := func(items map[string]Field) bool {
			if len(items) > capabilityMaxEdges-edges {
				return false
			}
			edges += len(items)
			names := make([]string, 0, len(items))
			nameBytes := 0
			for name := range items {
				if len(name) > capabilityMaxSourceBytes-nameBytes {
					return false
				}
				nameBytes += len(name)
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				field := items[name]
				if field.Ghost || field.Phantom {
					continue
				}
				out.text(name)
				if !visit(field.Type) {
					return false
				}
			}
			out.tag("end-fields")
			return true
		}
		switch t := typ.(type) {
		case *GenericInstanceType:
			return false
		case *AggregateStateType:
			if t == nil {
				return false
			}
			out.tag("state")
			return visit(t.Base)
		case *RefType:
			out.tag("ref")
			if t == nil {
				return visit(nil)
			}
			out.boolean(t.Mutable)
			return t.Mutable || visit(t.Elem)
		case *ViewType:
			out.tag("view")
			if t == nil {
				return visit(nil)
			}
			out.boolean(t.Mutable)
			return t.Mutable || visit(t.Elem)
		case *ArrayType:
			out.tag("array")
			if t == nil {
				return visit(nil)
			}
			return visit(t.Elem)
		case *DArrayType:
			out.tag("darray")
			if t == nil {
				return visit(nil)
			}
			return visit(t.Elem)
		case *OptionalType:
			out.tag("optional")
			if t == nil {
				return visit(nil)
			}
			return visit(t.Value)
		case *DictType:
			out.tag("dict")
			if t == nil {
				return visit(nil)
			}
			return visit(t.Key) && visit(t.Value)
		case *SetType:
			out.tag("set")
			if t == nil {
				return visit(nil)
			}
			return visit(t.Elem)
		case *DictEntryType:
			out.tag("dict-entry")
			if t == nil {
				return visit(nil)
			}
			out.boolean(t.Mutable)
			return t.Mutable || visit(t.Dict)
		case *TupleType:
			out.tag("tuple")
			if t == nil {
				return visit(nil)
			}
			out.integer(len(t.Fields))
			for _, field := range t.Fields {
				if !visit(field.Type) {
					return false
				}
			}
			return true
		case *ErrorUnionType:
			out.tag("error-union")
			if t == nil {
				return visit(nil)
			}
			if !visit(t.Value) {
				return false
			}
			if t.Errors != nil {
				if len(t.Errors.Payloads) > capabilityMaxEdges-edges {
					return false
				}
				edges += len(t.Errors.Payloads)
				names := make([]string, 0, len(t.Errors.Payloads))
				nameBytes := 0
				for name := range t.Errors.Payloads {
					if len(name) > capabilityMaxSourceBytes-nameBytes {
						return false
					}
					nameBytes += len(name)
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					out.text(name)
					payloads := t.Errors.Payloads[name]
					out.integer(len(payloads))
					for _, payload := range payloads {
						if !visit(payload) {
							return false
						}
					}
				}
			}
			out.tag("end-errors")
			return true
		case *StructType:
			out.tag("struct")
			if t == nil {
				return visit(nil)
			}
			return fields(t.Fields)
		case *EnumType:
			out.tag("enum")
			if t == nil {
				return visit(nil)
			}
			if !fields(t.Common) {
				return false
			}
			if len(t.Variants) > capabilityMaxEdges-edges {
				return false
			}
			edges += len(t.Variants)
			out.integer(len(t.Variants))
			for _, variant := range t.Variants {
				if variant == nil {
					out.tag("nil-variant")
					continue
				}
				out.integer(len(variant.Payload))
				for _, payload := range variant.Payload {
					if !visit(payload) {
						return false
					}
				}
			}
			return true
		case *PackedVariantViewType:
			out.tag("packed-view")
			if t == nil {
				return visit(nil)
			}
			if !visit(t.Enum) {
				return false
			}
			if t.Variant == nil {
				out.tag("nil-variant")
				return true
			}
			out.integer(len(t.Variant.Payload))
			for _, payload := range t.Variant.Payload {
				if !visit(payload) {
					return false
				}
			}
			return true
		case *PackedEnumStoreType:
			out.tag("packed-store")
			if t == nil {
				return visit(nil)
			}
			return visit(t.Enum) && visit(t.State)
		case *StoreRowsViewType:
			out.tag("store-rows")
			out.boolean(t != nil)
			return true
		case *StoreRowViewType:
			out.tag("store-row")
			out.boolean(t != nil)
			return true
		case *TypeParamType:
			out.tag("opaque-param")
			return true
		case *BuiltinType, *NeverType, *NullType, *InvalidType, *OpaqueType, *ConstEnumType, *FuncType, *SViewType, *CStrType, *ErrorSetType, *ConstParamType, *ConstValueType, *RegionParamType, *RegionValueType, *RefStorageValueType:
			out.tag("no-capability")
			return true
		default:
			return false
		}
	}
	if !visit(root) || out.failed {
		return "", false
	}
	return out.String(), true
}

func (a *Analyzer) hasMutableBorrowCapability(typ Type) bool {
	compute := func() bool {
		return a.typeContainsMutableBorrowCapability(typ, map[Type]bool{}, map[*StructType][][]Type{}, 0)
	}
	switch t := typ.(type) {
	case *BuiltinType, *NeverType, *NullType, *InvalidType, *OpaqueType, *ConstEnumType, *FuncType, *SViewType, *CStrType, *ErrorSetType, *ConstParamType, *ConstValueType, *RegionParamType, *RegionValueType, *RefStorageValueType, *TypeParamType:
		return compute()
	case *RefType:
		if t == nil || t.Mutable {
			return compute()
		}
	case *ViewType:
		if t == nil || t.Mutable {
			return compute()
		}
	}
	invocation := a.capabilityQueries
	if invocation == nil || !a.typeShapesFrozen || typ == nil {
		return compute()
	}
	stats := &invocation.session.stats
	stats.CapabilityReads++
	if local, ok := invocation.local[typ]; ok {
		if !local.declined {
			stats.CapabilityLocalHits++
			return local.value
		}
		return compute()
	}
	source, ok := capabilityTypeProjection(typ)
	if !ok {
		stats.CapabilityProjectionDeclines++
		if len(invocation.local) < capabilityMaxLocalTypes {
			invocation.local[typ] = capabilityLocalResult{declined: true}
		}
		return compute()
	}
	cache := invocation.session.capabilities
	if cache == nil {
		cache = newCapabilityQueryCache()
		invocation.session.capabilities = cache
	}
	encoded, hit := cache.lookup(source)
	value := false
	if hit {
		value = encoded == "1"
		stats.CapabilityProductsReused++
	} else {
		before := a.semanticLimitHits
		value = compute()
		stats.CapabilityComputed++
		if before != a.semanticLimitHits {
			return value
		}
		if cache.store(source, value) {
			stats.CapabilityProductsPublished++
		}
	}
	if len(invocation.local) < capabilityMaxLocalTypes {
		invocation.local[typ] = capabilityLocalResult{value: value}
	}
	return value
}

// Every write is checked before growing the encoded input, including strings
// attached to collection entries which do not contain a child Type.
type capabilityWriter struct {
	strings.Builder
	failed bool
}

func (w *capabilityWriter) reserve(n int) bool {
	if w.failed || n < 0 || n > capabilityMaxSourceBytes-w.Len() {
		w.failed = true
		return false
	}
	return true
}
func (w *capabilityWriter) tag(value string) {
	if len(value) >= capabilityMaxSourceBytes || !w.reserve(len(value)+1) {
		w.failed = true
		return
	}
	w.WriteString(value)
	w.WriteByte('|')
}
func (w *capabilityWriter) integer(value int) { w.tag(strconv.Itoa(value)) }
func (w *capabilityWriter) boolean(value bool) {
	if value {
		w.tag("1")
	} else {
		w.tag("0")
	}
}
func (w *capabilityWriter) text(value string) {
	if len(value) > capabilityMaxSourceBytes {
		w.failed = true
		return
	}
	size := strconv.Itoa(len(value))
	if !w.reserve(len(value) + len(size) + 2) {
		return
	}
	w.WriteString(size)
	w.WriteByte(':')
	w.WriteString(value)
	w.WriteByte('|')
}
