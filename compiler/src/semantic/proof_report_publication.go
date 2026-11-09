package semantic

import (
	"elisacore/src/ast"
	"reflect"
)

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
