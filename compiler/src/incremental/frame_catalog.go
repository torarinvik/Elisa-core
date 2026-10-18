package incremental

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// CatalogBinding identifies a current declaration slot, never a Symbol/Type/AST.
// A present nil binding blocks parent lookup just as a present nil scope entry.
// An omitted name is absent only under the caller-attested complete enumeration
// precondition of NewFrameCatalog; structural validation cannot establish it.
const frameCatalogIdentityBytes = 4096
const frameCatalogEncodedBytes = 64 << 10
const frameCatalogPublicationBytes = 1 << 20
const frameCatalogMaxDepth = 256

func validCatalogIdentity(value string) bool {
	return len(value) <= frameCatalogIdentityBytes && utf8.ValidString(value)
}

type CatalogBinding struct {
	Binding string
	Nil     bool
}
type CatalogFrame struct {
	ID, Parent string
	Names      map[string]CatalogBinding
}
type FrameCatalogLimits struct{ Frames, Bindings, Bytes, Depth int }

func DefaultFrameCatalogLimits() FrameCatalogLimits {
	return FrameCatalogLimits{32768, 262144, 128 << 20, 256}
}

type catalogFrame struct {
	parent string
	names  map[string]CatalogBinding
}

// FrameCatalog is an immutable invocation-local authority, not a persistent
// session payload. Stable frame/binding IDs must be validated by the compiler's
// source-anchor adapter. This layer validates structure and ownership only.
type FrameCatalog struct {
	frames                 map[string]catalogFrame
	bytes, bindings, depth int
}

// NewFrameCatalog requires caller-attested complete current frame/name
// enumeration. It detects malformed structure, missing referenced parents and
// cycles, but cannot detect a live name/frame omitted by the compiler collector.
// Do not use an observed lookup trace or a partially built scope as this input.
func NewFrameCatalog(frames []CatalogFrame, limits FrameCatalogLimits) (*FrameCatalog, error) {
	if limits.Frames < 0 || limits.Bindings < 0 || limits.Bytes <= 0 || limits.Depth <= 0 || limits.Depth > frameCatalogMaxDepth || len(frames) > limits.Frames || len(frames) > limits.Bytes/128 {
		return nil, fmt.Errorf("invalid/oversized frame catalog")
	}
	bytes, bindings := 0, 0
	add := func(n int) bool {
		if n < 0 || n > limits.Bytes-bytes {
			return false
		}
		bytes += n
		return true
	}
	// Validate before cloning any caller strings or maps. The caller keeps input
	// stable for this call; no supplied map survives construction.
	index := make(map[string]int, len(frames))
	for i, frame := range frames {
		if frame.ID == "" || !validCatalogIdentity(frame.ID) || !validCatalogIdentity(frame.Parent) {
			return nil, fmt.Errorf("invalid frame identity")
		}
		if _, ok := index[frame.ID]; ok {
			return nil, fmt.Errorf("duplicate frame identity")
		}
		if len(frame.Names) > limits.Bindings-bindings || !add(128) || !add(len(frame.ID)) || !add(len(frame.Parent)) {
			return nil, fmt.Errorf("frame catalog budget exceeded")
		}
		index[frame.ID] = i
		bindings += len(frame.Names)
		for name, binding := range frame.Names {
			if name == "" || !validCatalogIdentity(name) || !validCatalogIdentity(binding.Binding) || binding.Nil && binding.Binding != "" || !binding.Nil && binding.Binding == "" {
				return nil, fmt.Errorf("invalid catalog binding")
			}
			if !add(128) || !add(len(name)) || !add(len(binding.Binding)) {
				return nil, fmt.Errorf("frame catalog byte budget exceeded")
			}
		}
	}
	// Validate parent completeness and depth in linear work. Unfinished paths are
	// marked before following parents so cycles cannot consume unbounded work.
	maxDepth := 0
	depths := make(map[string]int, len(frames))
	visiting := make(map[string]bool)
	for _, frame := range frames {
		if depths[frame.ID] != 0 {
			continue
		}
		var path []string
		current := frame.ID
		for current != "" && depths[current] == 0 {
			if visiting[current] {
				return nil, fmt.Errorf("cyclic frame catalog")
			}
			at, ok := index[current]
			if !ok {
				return nil, fmt.Errorf("unpublished parent frame")
			}
			if len(path) >= limits.Depth {
				return nil, fmt.Errorf("frame catalog depth exceeded")
			}
			visiting[current] = true
			path = append(path, current)
			current = frames[at].Parent
		}
		depth := depths[current]
		for i := len(path) - 1; i >= 0; i-- {
			depth++
			if depth > limits.Depth {
				return nil, fmt.Errorf("frame catalog depth exceeded")
			}
			depths[path[i]] = depth
			maxDepth = max(maxDepth, depth)
			delete(visiting, path[i])
		}
	}
	catalog := &FrameCatalog{frames: make(map[string]catalogFrame, len(frames)), bytes: bytes, bindings: bindings, depth: maxDepth}
	for _, frame := range frames {
		id := strings.Clone(frame.ID)
		names := make(map[string]CatalogBinding, len(frame.Names))
		for name, binding := range frame.Names {
			binding.Binding = strings.Clone(binding.Binding)
			names[strings.Clone(name)] = binding
		}
		catalog.frames[id] = catalogFrame{parent: strings.Clone(frame.Parent), names: names}
	}
	return catalog, nil
}

func (c *FrameCatalog) AccountedBytes() int {
	if c == nil {
		return 0
	}
	return c.bytes
}

const frameCatalogLookupKind = "frame-catalog-lookup-v1"
const frameCatalogNameKind = "frame-catalog-name-v1"
const frameCatalogParentKind = "frame-catalog-parent-v1"
const frameCatalogCellOwnerKind = "frame-catalog-cell-owner-v1"

// FrameLookup has scope selection semantics only: global visibility, flow facts,
// types, and binder-specific nil rules require their own query domains.
type FrameLookup struct{ Frame, Name string }
type FrameSelection struct {
	Frame, Binding string
	Present, Nil   bool
}

func catalogName(frame, name string) string {
	data, _ := json.Marshal([2]string{frame, name})
	return string(data)
}

// addFrameCatalogBudget adds to the conservative allocation estimate without
// allowing either integer overflow or an estimate above the publication cap.
func addFrameCatalogBudget(total *int, amount int) bool {
	if amount < 0 || *total < 0 || *total > frameCatalogPublicationBytes || amount > frameCatalogPublicationBytes-*total {
		return false
	}
	*total += amount
	return true
}

func addFrameCatalogScaledBudget(total *int, value, scale int) bool {
	if value < 0 || scale < 0 || *total < 0 || *total > frameCatalogPublicationBytes {
		return false
	}
	remaining := frameCatalogPublicationBytes - *total
	if scale != 0 && value > remaining/scale {
		return false
	}
	return addFrameCatalogBudget(total, value*scale)
}

// addCatalogNameEncodedBudget accounts for a [frame, name] JSON key. JSON can
// expand each input byte to a six-byte escape; seven fixed bytes cover quotes,
// brackets, and comma.
func addCatalogNameEncodedBudget(total *int, frame, name string) bool {
	return addFrameCatalogBudget(total, 7) &&
		addFrameCatalogScaledBudget(total, len(frame), 6) &&
		addFrameCatalogScaledBudget(total, len(name), 6)
}

// catalogName marshals to a byte slice and then converts that slice to an owned
// string. Count both encoded copies in the publication preflight.
func addCatalogNameMaterializationBudget(total *int, frame, name string) bool {
	return addFrameCatalogBudget(total, 14) &&
		addFrameCatalogScaledBudget(total, len(frame), 12) &&
		addFrameCatalogScaledBudget(total, len(name), 12)
}

// preflightLookupPublication walks the immutable parent chain before any JSON
// keys, cells, or results are materialized. Besides encoded payloads, the
// estimate reserves per-input structure overhead so the number of ancestors is
// bounded by the same aggregate cap.
func (c *FrameCatalog) lookupPublicationBudget(lookup FrameLookup) (int, error) {
	if lookup.Frame == "" || lookup.Name == "" || !validCatalogIdentity(lookup.Frame) || !validCatalogIdentity(lookup.Name) {
		return 0, fmt.Errorf("invalid frame lookup")
	}
	budget := 0
	if !addFrameCatalogBudget(&budget, 128) || !addCatalogNameMaterializationBudget(&budget, lookup.Frame, lookup.Name) {
		return 0, fmt.Errorf("catalog publication byte budget exceeded")
	}
	current := lookup.Frame
	for steps := 0; current != ""; steps++ {
		if steps >= c.depth {
			return 0, fmt.Errorf("frame lookup depth exceeded")
		}
		frame, ok := c.frames[current]
		if !ok {
			return 0, fmt.Errorf("unpublished lookup frame")
		}
		binding, present := frame.names[lookup.Name]
		// Name-cell key and value, parent key and value, plus the two input
		// records and slice growth. Include serialization for the selected result;
		// preparation stops at that binding.
		if !addCatalogNameMaterializationBudget(&budget, current, lookup.Name) ||
			present && (!addFrameCatalogBudget(&budget, 64) || !addFrameCatalogScaledBudget(&budget, len(binding.Binding), 12)) ||
			!addFrameCatalogBudget(&budget, len(current)) ||
			!addFrameCatalogBudget(&budget, len(frame.parent)) ||
			!addFrameCatalogBudget(&budget, 256) {
			return 0, fmt.Errorf("catalog publication byte budget exceeded")
		}
		// Separate cell owners encode each input identity into their own key.
		// Include both marshal bytes and owned string copies conservatively.
		nameBound := 0
		if !addCatalogNameEncodedBudget(&nameBound, current, lookup.Name) ||
			!addFrameCatalogBudget(&budget, 512) ||
			!addFrameCatalogScaledBudget(&budget, nameBound, 12) ||
			!addFrameCatalogScaledBudget(&budget, len(current), 12) {
			return 0, fmt.Errorf("catalog publication byte budget exceeded")
		}
		if present {
			resultBound := 64
			if !addFrameCatalogScaledBudget(&resultBound, len(current), 6) ||
				!addFrameCatalogScaledBudget(&resultBound, len(binding.Binding), 6) ||
				resultBound > frameCatalogEncodedBytes ||
				!addFrameCatalogBudget(&budget, resultBound) {
				return 0, fmt.Errorf("catalog publication byte budget exceeded")
			}
			break
		}
		current = frame.parent
	}
	return budget, nil
}

func FrameLookupKey(lookup FrameLookup) (Key, error) {
	if lookup.Frame == "" || lookup.Name == "" || !validCatalogIdentity(lookup.Frame) || !validCatalogIdentity(lookup.Name) {
		return Key{}, fmt.Errorf("invalid frame lookup")
	}
	encodedBound := 0
	if !addCatalogNameEncodedBudget(&encodedBound, lookup.Frame, lookup.Name) || encodedBound > frameCatalogEncodedBytes {
		return Key{}, fmt.Errorf("oversized frame lookup recipe")
	}
	return Key{frameCatalogLookupKind, catalogName(lookup.Frame, lookup.Name)}, nil
}
func FrameCatalogEvaluators() map[string]Evaluator {
	return map[string]Evaluator{frameCatalogLookupKind: evaluateFrameCatalogLookup, frameCatalogCellOwnerKind: func(*Reader, string) (string, error) { return "", nil }}
}

// CatalogLookupPlan keeps global catalog-cell authority separate from lookup
// owners. Cell owners consume Session query slots via an inert evaluator; they
// carry no result/dependency reservation. A collector explicitly retires them.
type CatalogLookupPlan struct {
	Query Publication
	Cells []Publication
}

func catalogCellOwner(change Change) Publication {
	return Publication{Key: Key{frameCatalogCellOwnerKind, catalogName(change.Key.Kind, change.Key.Name)}, Inputs: []Change{change}}
}

// NamePublication certifies a current positive, nil or negative selected-name
// cell. Previously admitted cells need refresh after relevant declaration edits
// even when no lookup reruns. Structural validation cannot discover those edits.
func (c *FrameCatalog) NamePublication(frameID, name string) (Publication, error) {
	if c == nil || c.frames == nil || name == "" || !validCatalogIdentity(name) {
		return Publication{}, fmt.Errorf("invalid catalog name request")
	}
	frame, ok := c.frames[frameID]
	if !ok {
		return Publication{}, fmt.Errorf("unpublished catalog frame")
	}
	change := Change{Key: Key{frameCatalogNameKind, catalogName(frameID, name)}}
	if binding, present := frame.names[name]; present {
		data, _ := json.Marshal(binding)
		change.Value = string(data)
	} else {
		change.Remove = true
	}
	return catalogCellOwner(change), nil
}

func (c *FrameCatalog) ParentPublication(frameID string) (Publication, error) {
	if c == nil || c.frames == nil {
		return Publication{}, fmt.Errorf("incomplete catalog")
	}
	frame, ok := c.frames[frameID]
	if !ok {
		return Publication{}, fmt.Errorf("unpublished catalog frame")
	}
	return catalogCellOwner(Change{Key: Key{frameCatalogParentKind, frameID}, Value: frame.parent}), nil
}

// PrepareLookup derives cells from caller-attested complete current enumeration.
// Each cell has one stable owner; lookup owners assert no shared cells. Session
// retains only owned strings, never this invocation-local catalog. Prepare via
// PublishDeltaPrepared so catalog failure invalidates old handles. Use LookupDelta
// to bound batches rather than accumulating independently bounded plans forever.
func (c *FrameCatalog) PrepareLookup(lookup FrameLookup) (CatalogLookupPlan, error) {
	if c == nil || c.frames == nil {
		return CatalogLookupPlan{}, fmt.Errorf("incomplete frame catalog")
	}
	if _, err := c.lookupPublicationBudget(lookup); err != nil {
		return CatalogLookupPlan{}, err
	}
	key, err := FrameLookupKey(lookup)
	if err != nil {
		return CatalogLookupPlan{}, err
	}
	plan := CatalogLookupPlan{Query: Publication{Key: key, MaxResultBytes: 64}}
	for current := lookup.Frame; current != ""; {
		frame := c.frames[current]
		nameOwner, err := c.NamePublication(current, lookup.Name)
		if err != nil {
			return CatalogLookupPlan{}, err
		}
		plan.Cells = append(plan.Cells, nameOwner)
		if binding, present := frame.names[lookup.Name]; present {
			product, _ := json.Marshal(FrameSelection{current, binding.Binding, true, binding.Nil})
			plan.Query.MaxResultBytes = max(plan.Query.MaxResultBytes, len(product))
			break
		}
		parentOwner, err := c.ParentPublication(current)
		if err != nil {
			return CatalogLookupPlan{}, err
		}
		plan.Cells = append(plan.Cells, parentOwner)
		current = frame.parent
	}
	plan.Query.MaxDependencies = len(plan.Cells)
	return plan, nil
}

func evaluateFrameCatalogLookup(r *Reader, name string) (string, error) {
	if len(name) > frameCatalogEncodedBytes {
		return "", fmt.Errorf("oversized frame lookup recipe")
	}
	var lookup [2]string
	if err := json.Unmarshal([]byte(name), &lookup); err != nil || lookup[0] == "" || lookup[1] == "" || !validCatalogIdentity(lookup[0]) || !validCatalogIdentity(lookup[1]) || catalogName(lookup[0], lookup[1]) != name {
		return "", fmt.Errorf("invalid frame lookup recipe")
	}
	current := lookup[0]
	seen := map[string]bool{}
	for steps := 0; current != ""; steps++ {
		if steps >= frameCatalogMaxDepth {
			return "", fmt.Errorf("published frame depth exceeded")
		}
		if seen[current] {
			return "", fmt.Errorf("cyclic published frame chain")
		}
		seen[current] = true
		encoded, present := r.Input(Key{frameCatalogNameKind, catalogName(current, lookup[1])})
		if present {
			if len(encoded) > frameCatalogEncodedBytes {
				return "", fmt.Errorf("oversized name cell")
			}
			var binding CatalogBinding
			if err := json.Unmarshal([]byte(encoded), &binding); err != nil || !validCatalogIdentity(binding.Binding) || binding.Nil && binding.Binding != "" || !binding.Nil && binding.Binding == "" {
				return "", fmt.Errorf("invalid published name cell")
			}
			canonical, _ := json.Marshal(binding)
			if string(canonical) != encoded {
				return "", fmt.Errorf("noncanonical name cell")
			}
			product, _ := json.Marshal(FrameSelection{current, binding.Binding, true, binding.Nil})
			return string(product), nil
		}
		parent, admitted := r.Input(Key{frameCatalogParentKind, current})
		if !admitted {
			return "", fmt.Errorf("unpublished parent edge")
		}
		if !validCatalogIdentity(parent) {
			return "", fmt.Errorf("invalid published parent identity")
		}
		current = parent
	}
	product, _ := json.Marshal(FrameSelection{})
	return string(product), nil
}

func DecodeFrameSelection(value string) (FrameSelection, error) {
	if len(value) > frameCatalogEncodedBytes {
		return FrameSelection{}, fmt.Errorf("oversized frame selection")
	}
	var selection FrameSelection
	if err := json.Unmarshal([]byte(value), &selection); err != nil {
		return selection, err
	}
	canonical, _ := json.Marshal(selection)
	if string(canonical) != value || !validCatalogIdentity(selection.Frame) || !validCatalogIdentity(selection.Binding) || selection.Present && (selection.Frame == "" || selection.Nil && selection.Binding != "" || !selection.Nil && selection.Binding == "") || !selection.Present && (selection.Frame != "" || selection.Binding != "" || selection.Nil) {
		return FrameSelection{}, fmt.Errorf("invalid frame selection")
	}
	return selection, nil
}
