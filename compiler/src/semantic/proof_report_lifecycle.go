package semantic

import (
	"fmt"
	"reflect"
	"sort"

	"elisacore/src/ast"
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
	value := reflect.ValueOf(node)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil
	}
	if a.proofLifecycle.Synthetic != nil && node == a.proofLifecycle.Synthetic && a.proofLifecycle.SyntheticOwner != nil {
		return a.proofLifecycle.SyntheticOwner
	}
	if fn, ok := node.(*ast.FuncDecl); ok {
		if fn == nil {
			return nil
		}
		return a.canonicalFuncDecl(fn)
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
	if a.proofLifecycle.ProbeDepth != 0 {
		return
	}
	origin = a.canonicalProofReportNode(origin)
	if current := a.proofLifecycle.Current; current != nil && origin != nil {
		if origin == current.Key.Owner {
			return
		} // installed once at commit
		if current.Origins == nil {
			current.Origins = map[ast.Node]bool{}
		}
		current.Origins[origin] = true
	}
}

// A probe suppresses only audited speculative semantic entrypoints. It restores
// category/dependencies as well as payload, and never replaces a committed owner.
func (a *Analyzer) beginProofReportProbe() func() {
	priorDepth, category := a.proofLifecycle.ProbeDepth, a.currentProofCategory
	priorUnknown, current := a.proofLifecycle.Unknown, a.proofLifecycle.Current
	priorSynthetic, priorSyntheticOwner := a.proofLifecycle.Synthetic, a.proofLifecycle.SyntheticOwner
	n := len(a.proofReport)
	// All dependency mutations go through addProofReportOrigin, which is inert
	// at probe depth. Preserve this immutable map identity instead of copying
	// the growing body dependency set for every speculative typing operation.
	var origins map[ast.Node]bool
	if current != nil {
		origins = current.Origins
	}
	a.proofLifecycle.ProbeDepth++
	restored := false
	return func() {
		if restored {
			return
		}
		restored = true
		a.proofLifecycle.ProbeDepth = priorDepth
		a.proofLifecycle.Current = current
		a.proofLifecycle.Unknown = priorUnknown
		a.proofLifecycle.Synthetic, a.proofLifecycle.SyntheticOwner = priorSynthetic, priorSyntheticOwner
		a.currentProofCategory = category
		a.proofReport = a.proofReport[:n]
		if current != nil {
			current.Origins = origins
		}
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

func (a *Analyzer) mergeWorkerProofContributions(worker *Analyzer, p *parallelWorkerState) {
	if n := worker.proofLifecycle.BaseEvents; n <= len(worker.proofReport) {
		a.proofReport = append(a.proofReport, worker.proofReport[n:]...)
	}
	if worker.proofLifecycle.Unknown != "" {
		a.proofLifecycle.Unknown = worker.proofLifecycle.Unknown
	}
	for key, segment := range worker.proofLifecycle.Segments {
		if key.Phase != proofPhaseBody {
			if len(segment.Facts) != 0 {
				baseline, present := a.proofLifecycle.Segments[key]
				if !present || !reflect.DeepEqual(baseline, segment) {
					a.proofLifecycle.Unknown = "worker produced an unsupported non-body report contribution"
				}
			}
			continue
		}
		fn, ok := key.Owner.(*ast.FuncDecl)
		if !ok {
			a.proofLifecycle.Unknown = "worker published a non-function body contribution"
			continue
		}
		fn = a.canonicalFuncDecl(fn)
		owner, known := p.ownerOf(fn)
		if !known || owner != p.worker {
			continue
		}
		key.Owner = fn
		key.Site = a.canonicalProofReportNode(key.Site)
		segment.Key = key
		origins := map[ast.Node]bool{}
		for origin := range segment.Origins {
			if f, ok := origin.(*ast.FuncDecl); ok {
				if original := p.original[f]; original != nil {
					origin = original
				}
			}
			origins[origin] = true
		}
		segment.Origins = origins
		if a.proofLifecycle.Segments == nil {
			a.proofLifecycle.Segments = map[proofContributionKey]proofContribution{}
		}
		a.proofLifecycle.Segments[key] = segment
	}
}

// Declaration contributions are imported from the independent pre-body capture.
// Rendered-position relocation still requires an independent certified adapter.
func (r *RetainedAnalysis) admitProofReportUpdate(splices []*ast.FuncDecl, removed []*ast.FuncDecl, locationChanged, declarationsChanged bool) string {
	a := r.a
	if why := a.proofReportCheckpointError(); why != "" {
		return why
	}
	if len(a.proofReport) != 0 && len(a.proofLifecycle.Segments) == 0 {
		return "proof report has no contribution journal"
	}
	if len(a.proofReport) == 0 {
		return ""
	}
	if locationChanged {
		return "proof report source relocation requires fresh producer analysis"
	}
	changed := map[ast.Node]bool{}
	for _, s := range splices {
		changed[s] = true
	}
	for _, fn := range removed {
		changed[fn] = true
	}
	for _, segment := range a.proofLifecycle.Segments {
		if segment.Key.Phase == proofPhasePostDefault {
			continue
		} // post phase 1 is replaced in every Update
		if len(segment.Facts) == 0 || changed[segment.Key.Owner] {
			continue
		}
		for origin := range segment.Origins {
			if changed[origin] {
				return "proof report dependent contribution requires reanalysis"
			}
		}
	}
	return ""
}

func (a *Analyzer) proofReportAuthorityError() string {
	if a.proofLifecycle.Unknown != "" {
		return a.proofLifecycle.Unknown
	}
	if len(a.proofLifecycle.Segments) == 0 {
		if len(a.proofReport) != 0 {
			return "proof report has no contribution journal"
		}
		return ""
	}
	owners := map[ast.Node]bool{}
	if a.file != nil {
		for _, decl := range retainedWalkDecls(a.file) {
			owners[decl] = true
		}
	}
	for key, segment := range a.proofLifecycle.Segments {
		if len(segment.Facts) != 0 && (!owners[key.Owner] || key.Site != nil && !owners[key.Site]) {
			return "proof report contribution is outside the current declaration graph"
		}
		if key.Owner == nil || key.Phase < proofPhaseValueSymbols || key.Phase > proofPhasePostDefault || segment.Key != key {
			return "proof report has an invalid source contribution identity"
		}
		if len(segment.Facts) != 0 && !segment.Origins[key.Owner] {
			return "proof report contribution has no owner origin"
		}
	}
	return ""
}

func (a *Analyzer) addProofInterfaceOrigins(iface *StaticInterface) {
	seen := map[*StaticInterface]bool{}
	var visit func(*StaticInterface)
	visit = func(current *StaticInterface) {
		if current == nil || seen[current] {
			return
		}
		seen[current] = true
		a.addProofReportOrigin(current.Decl)
		for _, law := range current.Laws {
			a.addProofReportOrigin(law)
		}
		for _, member := range current.Methods {
			if member != nil {
				// The interface owns bodiless signature views, which may be
				// synthetic. A default additionally has an exact source body.
				a.addProofReportOrigin(member.Default)
			}
		}
		for _, associated := range current.AssociatedTypes {
			a.addProofReportOrigin(associated)
		}
		for _, base := range current.Bases {
			visit(a.staticInterfaces[base])
		}
	}
	visit(iface)
}

func (a *Analyzer) proofReportCheckpointError() string {
	if a.proofLifecycle.Current != nil || a.proofLifecycle.ProbeDepth != 0 || a.proofLifecycle.Synthetic != nil || a.proofLifecycle.SyntheticOwner != nil || a.currentProofCategory != "" {
		return "an active proof-report transaction is not serializable"
	}
	return a.proofReportAuthorityError()
}

// A declaration product may use an empty-report domain only when emptiness is
// proven at an idle boundary. Unknown/unowned or pending work is not "empty".
func (a *Analyzer) hasLiveProofContributions() bool {
	if a == nil {
		return true
	}
	if a.proofReportCheckpointError() != "" {
		return true
	}
	return len(a.proofReport) != 0 || len(a.proofLifecycle.Segments) != 0
}

// A pre-body capture must retain value copies of committed rows. The scratch
// analyzer continues after capture, so retaining its mutable journal map would
// accidentally import body/post-pass observations as declaration authority.
func (a *Analyzer) captureProofPrefix() ([]proofContribution, string) {
	if why := a.proofReportCheckpointError(); why != "" {
		return nil, why
	}
	var prefix []proofContribution
	for key, row := range a.proofLifecycle.Segments {
		if key.Phase >= proofPhaseDeclaration {
			continue
		}
		prefix = append(prefix, row)
	}
	return prefix, ""
}

// importProofPrefix rebinds source declarations, never semantic types or body
// facts. Source kind and full printed declaration position must identify exactly
// one live declaration. Offsets are deliberately excluded: retained AST offsets
// are stable, whereas the independent capture uses the new expanded buffer.
// Binding all rows before publishing makes this replacement transactional.
func (a *Analyzer) importProofPrefix(pre *preBodyState) string {
	if pre == nil {
		return "missing proof-report declaration capture"
	}
	if pre.proofPrefixError != "" {
		return pre.proofPrefixError
	}
	type identity struct {
		Kind      reflect.Type
		File      string
		Line, Col int
	}
	identityOf := func(node ast.Node) identity {
		p := node.Pos()
		return identity{reflect.TypeOf(node), p.File, p.Line, p.Col}
	}
	live := map[identity][]ast.Node{}
	for _, decl := range retainedWalkDecls(a.file) {
		id := identityOf(decl)
		live[id] = append(live[id], decl)
	}
	bind := func(node ast.Node) (ast.Node, bool) {
		if node == nil {
			return nil, true
		}
		matches := live[identityOf(node)]
		if len(matches) != 1 {
			return nil, false
		}
		return matches[0], true
	}
	imported := make(map[proofContributionKey]proofContribution, len(pre.proofPrefix))
	for _, row := range pre.proofPrefix {
		var ok bool
		key := row.Key
		key.Owner, ok = bind(key.Owner)
		if !ok {
			return "proof-report declaration owner cannot be rebound uniquely"
		}
		key.Site, ok = bind(key.Site)
		if !ok {
			return "proof-report declaration site cannot be rebound uniquely"
		}
		origins := make(map[ast.Node]bool, len(row.Origins))
		for source := range row.Origins {
			origin, present := bind(source)
			if !present {
				return "proof-report declaration origin cannot be rebound uniquely"
			}
			origins[origin] = true
		}
		if _, duplicate := imported[key]; duplicate {
			return "proof-report declaration identities collide"
		}
		row.Key, row.Origins = key, origins
		imported[key] = row
	}
	// Empty captures are authoritative too: removed declaration obligations must
	// disappear rather than surviving merely because no replacement was emitted.
	for key := range a.proofLifecycle.Segments {
		if key.Phase < proofPhaseDeclaration {
			delete(a.proofLifecycle.Segments, key)
		}
	}
	if len(imported) != 0 && a.proofLifecycle.Segments == nil {
		a.proofLifecycle.Segments = map[proofContributionKey]proofContribution{}
	}
	for key, row := range imported {
		a.proofLifecycle.Segments[key] = row
	}
	return ""
}

// Specialization never publishes this construction analyzer or its reports.
// newSpecializationPublication starts from the canonical Result, retaining its
// complete public report, summary and foreign query fallback. Concrete producer
// observations are still journaled locally with their exact owner/phase/instance
// and are rolled back normally by probes. Body workers use the full fork instead.
func (a *Analyzer) specializationProofReportLifecycle() proofReportLifecycle {
	state := proofReportLifecycle{Unknown: a.proofLifecycle.Unknown}
	if a.proofLifecycle.Current != nil || a.proofLifecycle.ProbeDepth != 0 || a.proofLifecycle.Synthetic != nil || a.proofLifecycle.SyntheticOwner != nil || a.currentProofCategory != "" {
		state.Unknown = "specialization fork started inside an active proof-report transaction"
	}
	return state
}

// Activity is a construction boundary, independent of coverage. A nonempty
// Unknown flag records missing report provenance and blocks retained/snapshot
// admission; it is NOT proof authority and cannot invalidate an otherwise fully
// checked fresh program. Specialization's published view retains the canonical
// complete report. Active caller/probe/synthetic/category state, however, must
// never enter construction or be hidden by a previously memoized product.
func (a *Analyzer) specializationProofReportActivityError() string {
	if a.proofLifecycle.Current != nil || a.proofLifecycle.ProbeDepth != 0 || a.proofLifecycle.Synthetic != nil || a.proofLifecycle.SyntheticOwner != nil || a.currentProofCategory != "" {
		return "an active proof-report context"
	}
	return ""
}
