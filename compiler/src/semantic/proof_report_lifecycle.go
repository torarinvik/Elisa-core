package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"reflect"
	"sort"
)

// Phase is the producer lifetime, not the location of the printed obligation.
// In particular a caller's ensure/lemma evidence can point into another file.
const (
	proofPhaseValueSymbols = iota + 1
	proofPhaseHeader
	proofPhaseProtocolHeader
	proofPhaseUses
	proofPhaseProtocolImpl
	proofPhaseProtocolLaw
	proofPhaseDeclaration
	proofPhaseBody
	proofPhasePostDefault
)

type proofContributionKey struct {
	Owner    ast.Node
	Phase    int
	Site     ast.Node
	Instance string
}

type proofContribution struct {
	Key     proofContributionKey
	Facts   []ProofFact
	Origins map[ast.Node]bool
}

type proofReportLifecycle struct {
	Segments       map[proofContributionKey]proofContribution
	Current        *proofContribution
	ProbeDepth     int
	Unknown        string
	Synthetic      *ast.FuncDecl
	SyntheticOwner ast.Node
	BaseEvents     int
}

func (a *Analyzer) canonicalProofReportNode(node ast.Node) ast.Node {
	if node == nil {
		return nil
	}
	// Declaration reads are a hot path. Handle their typed nil and synthetic
	// owner directly rather than reflecting over every declaration.
	if fn, ok := node.(*ast.FuncDecl); ok {
		if fn == nil {
			return nil
		}
		if a.proofLifecycle.Synthetic == fn && a.proofLifecycle.SyntheticOwner != nil {
			return a.proofLifecycle.SyntheticOwner
		}
		return a.canonicalFuncDecl(fn)
	}
	value := reflect.ValueOf(node)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil
	}
	if a.proofLifecycle.Synthetic != nil && node == a.proofLifecycle.Synthetic && a.proofLifecycle.SyntheticOwner != nil {
		return a.proofLifecycle.SyntheticOwner
	}
	return node
}

// An authoritative pass stages a replacement. Its old immutable contribution is
// retained until this invocation completes. Nested callees have separate frames.
func (a *Analyzer) beginProofContribution(owner ast.Node, phase int, site ast.Node, instance string) func() {
	owner = a.canonicalProofReportNode(owner)
	site = a.canonicalProofReportNode(site)
	prior := a.proofLifecycle.Current
	priorCategory := a.currentProofCategory
	a.currentProofCategory = ""
	pending := &proofContribution{Key: proofContributionKey{owner, phase, site, instance}}
	a.proofLifecycle.Current = pending
	return func() {
		a.proofLifecycle.Current = prior
		a.currentProofCategory = priorCategory
		if failure := recover(); failure != nil {
			a.proofLifecycle.Unknown = "proof report transaction panicked"
			panic(failure)
		}
		if a.proofLifecycle.ProbeDepth != 0 {
			return
		}
		if owner == nil {
			if len(pending.Facts) != 0 {
				a.proofLifecycle.Unknown = "proof report contribution has no exact source owner"
			}
			return
		}
		if len(pending.Facts) == 0 {
			delete(a.proofLifecycle.Segments, pending.Key)
			return
		}
		if pending.Origins == nil {
			pending.Origins = map[ast.Node]bool{}
		}
		pending.Origins[owner] = true
		if a.proofLifecycle.Segments == nil {
			a.proofLifecycle.Segments = map[proofContributionKey]proofContribution{}
		}
		a.proofLifecycle.Segments[pending.Key] = *pending
	}
}

func (a *Analyzer) beginFunctionProofContribution(fn *ast.FuncDecl, arguments []Type) func() {
	a.addProofReportOrigin(fn)
	if a.proofLifecycle.Synthetic == fn && a.proofLifecycle.SyntheticOwner != nil {
		// Only the designated synth inherits its explicitly bound source post
		// owner. Ordinary nested callees retain their own source lifetime.
		if current := a.proofLifecycle.Current; current != nil && current.Key.Owner == a.proofLifecycle.SyntheticOwner && current.Key.Phase == proofPhasePostDefault {
			return func() {}
		}
		return a.beginProofContribution(a.proofLifecycle.SyntheticOwner, proofPhasePostDefault, nil, "")
	}
	instance := ""
	for _, argument := range arguments {
		key, err := TypeSymbolKey(argument)
		if err != nil {
			a.proofLifecycle.Unknown = "proof report has an unsupported instantiation identity"
			key = fmt.Sprintf("unsupported:%T", argument)
		}
		instance += fmt.Sprintf("%d:%s", len(key), key)
	}
	return a.beginProofContribution(fn, proofPhaseBody, nil, instance)
}

func (a *Analyzer) addProofReportOrigin(origin ast.Node) {
	current := a.proofLifecycle.Current
	if a.proofLifecycle.ProbeDepth != 0 || current == nil || origin == nil {
		return
	}
	origin = a.canonicalProofReportNode(origin)
	if origin != nil {
		if origin == current.Key.Owner {
			return
		} // installed once at commit
		if current.Origins == nil {
			current.Origins = map[ast.Node]bool{}
		}
		if current.Origins[origin] {
			return
		}
		current.Origins[origin] = true
	}
}

// A probe suppresses only audited speculative semantic entrypoints. It restores
// category/dependencies as well as payload, and never replaces a committed owner.
// proofReportProbe is kept by value at the call site so speculative checks do
// not allocate a restoration closure. restore remains idempotent for callers
// that restore early as well as deferring cleanup.
type proofReportProbe struct {
	a              *Analyzer
	depth          int
	category       ProofObligationCategory
	unknown        string
	current        *proofContribution
	synthetic      *ast.FuncDecl
	syntheticOwner ast.Node
	length         int
	origins        map[ast.Node]bool
	restored       bool
}

func (a *Analyzer) beginProofReportProbe() proofReportProbe {
	p := proofReportProbe{a: a, depth: a.proofLifecycle.ProbeDepth, category: a.currentProofCategory,
		unknown: a.proofLifecycle.Unknown, current: a.proofLifecycle.Current,
		synthetic: a.proofLifecycle.Synthetic, syntheticOwner: a.proofLifecycle.SyntheticOwner,
		length: len(a.proofReport)}
	if p.current != nil {
		p.origins = p.current.Origins
	}
	a.proofLifecycle.ProbeDepth++
	return p
}

func (p *proofReportProbe) restore() {
	if p.restored {
		return
	}
	p.restored = true
	a := p.a
	a.proofLifecycle.ProbeDepth = p.depth
	a.proofLifecycle.Current = p.current
	a.proofLifecycle.Unknown = p.unknown
	a.proofLifecycle.Synthetic, a.proofLifecycle.SyntheticOwner = p.synthetic, p.syntheticOwner
	a.currentProofCategory = p.category
	a.proofReport = a.proofReport[:p.length]
	if p.current != nil {
		p.current.Origins = p.origins
	}
}

func (a *Analyzer) appendProofFact(fact ProofFact) {
	if a.proofLifecycle.ProbeDepth != 0 {
		return
	}
	a.proofReport = append(a.proofReport, fact)
	current := a.proofLifecycle.Current
	if current == nil || current.Key.Owner == nil {
		a.proofLifecycle.Unknown = fmt.Sprintf("proof report recorded outside a classified source contribution at %s (%s / %s)", fact.Pos.String(), fact.Subject, fact.Predicate)
		return
	}
	if a.currentIndexProofOrigin != nil {
		a.addProofReportOrigin(a.currentIndexProofOrigin)
	}
	current.Facts = append(current.Facts, fact)
}

func (a *Analyzer) retractProofContributions(owner ast.Node) {
	if a.proofLifecycle.ProbeDepth != 0 {
		return
	}
	owner = a.canonicalProofReportNode(owner)
	for key := range a.proofLifecycle.Segments {
		if key.Owner == owner {
			delete(a.proofLifecycle.Segments, key)
		}
	}
}

func (a *Analyzer) clearProofPhase(phase int) {
	if a.proofLifecycle.ProbeDepth != 0 {
		return
	}
	for key := range a.proofLifecycle.Segments {
		if key.Phase == phase {
			delete(a.proofLifecycle.Segments, key)
		}
	}
}

// Deterministic production order replaces incidental worker completion order.
// Facts within each contribution are never sorted, collapsed or deduplicated.
func (a *Analyzer) publishLiveProofReport() {
	rank := map[ast.Node]int{}
	if a.file != nil {
		for i, decl := range retainedWalkDecls(a.file) {
			rank[decl] = i + 1
		}
	}
	keys := make([]proofContributionKey, 0, len(a.proofLifecycle.Segments))
	for key, segment := range a.proofLifecycle.Segments {
		if len(segment.Facts) != 0 && (rank[key.Owner] == 0 || key.Site != nil && rank[key.Site] == 0) {
			a.proofLifecycle.Unknown = fmt.Sprintf("proof report owner %T at %s is absent from the current declaration graph", key.Owner, key.Owner.Pos().String())
		}
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		x, y := keys[i], keys[j]
		if x.Phase != y.Phase {
			return x.Phase < y.Phase
		}
		if rank[x.Owner] != rank[y.Owner] {
			return rank[x.Owner] < rank[y.Owner]
		}
		xp, yp := x.Owner.Pos(), y.Owner.Pos()
		if xp.File != yp.File {
			return xp.File < yp.File
		}
		if xp.Line != yp.Line {
			return xp.Line < yp.Line
		}
		if xp.Col != yp.Col {
			return xp.Col < yp.Col
		}
		if rank[x.Site] != rank[y.Site] {
			return rank[x.Site] < rank[y.Site]
		}
		if x.Instance != y.Instance {
			return x.Instance < y.Instance
		}
		if x.Site == nil {
			return y.Site != nil
		}
		if y.Site == nil {
			return false
		}
		xp, yp = x.Site.Pos(), y.Site.Pos()
		if xp.File != yp.File {
			return xp.File < yp.File
		}
		if xp.Line != yp.Line {
			return xp.Line < yp.Line
		}
		return xp.Col < yp.Col
	})
	var facts []ProofFact
	for _, key := range keys {
		facts = append(facts, a.proofLifecycle.Segments[key].Facts...)
	}
	// Unknown contexts remain observable in fresh analysis, but cannot be reused.
	if a.proofLifecycle.Unknown != "" {
		return
	}
	a.proofReport = facts
}

func (a *Analyzer) forkProofReportLifecycle() proofReportLifecycle {
	state := proofReportLifecycle{Unknown: a.proofLifecycle.Unknown, BaseEvents: len(a.proofReport)}
	if len(a.proofLifecycle.Segments) != 0 {
		state.Segments = make(map[proofContributionKey]proofContribution, len(a.proofLifecycle.Segments))
	}
	if a.proofLifecycle.Current != nil || a.proofLifecycle.ProbeDepth != 0 || a.proofLifecycle.Synthetic != nil || a.proofLifecycle.SyntheticOwner != nil {
		state.Unknown = "worker fork started inside a proof-report transaction"
	}
	for key, value := range a.proofLifecycle.Segments {
		state.Segments[key] = value
	}
	return state
}
