package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"llcontext/src/lexer"
	"llcontext/src/semantic"
)

func generateFactTraceReport(result *semantic.Result, filter string) (string, error) {
	if result == nil || result.GlobalScope == nil {
		return "", nil
	}
	traceFilter, err := parseFactTraceFilter(filter)
	if err != nil {
		return "", err
	}
	if traceFilter.JSONMode() {
		return formatJSONFactTraceReport(result, traceFilter)
	}
	return formatTextFactTraceReport(result, traceFilter), nil
}

func formatJSONFactTraceReport(result *semantic.Result, traceFilter factTraceFilter) (string, error) {
	payload := factTraceJSONReport{
		Version:  semantic.FactTraceFormatVersion,
		Mode:     factTraceReportMode(traceFilter),
		Format:   semantic.FactTraceJSONFormat,
		Filters:  supportedFactTraceFilterKeys(),
		Matchers: supportedFactTraceFilterMatchers(),
	}
	for _, entry := range collectFactTraceEntries(result, traceFilter) {
		item := factTraceJSONFunction{
			Name:     entry.Name,
			Snapshot: entry.Analysis.FactSnapshot,
			Exits:    entry.Analysis.FactExitSummary,
			Aliases:  entry.Analysis.AliasSets,
			Effects:  entry.Analysis.EffectSummary,
			Summary:  semantic.FormatFactTransformSummary(entry.Transforms),
		}
		if !traceFilter.SummaryMode() {
			item.Transforms = factTraceJSONTransforms(entry.Transforms)
			item.TextSummary = semantic.FormatFactTransforms(entry.Transforms)
		}
		payload.Functions = append(payload.Functions, item)
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

type factTraceJSONReport struct {
	Version   string                  `json:"version"`
	Mode      string                  `json:"mode"`
	Format    string                  `json:"format"`
	Filters   []string                `json:"filters"`
	Matchers  []string                `json:"matchers"`
	Functions []factTraceJSONFunction `json:"functions"`
}

type factTraceJSONFunction struct {
	Name        string                     `json:"name"`
	Snapshot    semantic.FactSnapshot      `json:"snapshot"`
	Exits       semantic.FactExitSummary   `json:"exits"`
	Aliases     []semantic.FactAliasSet    `json:"aliases,omitempty"`
	Effects     semantic.FactEffectSummary `json:"effects"`
	Summary     string                     `json:"summary"`
	Transforms  []factTraceJSONTransform   `json:"transforms,omitempty"`
	TextSummary string                     `json:"text_summary,omitempty"`
}

type factTraceJSONPosition struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"end_line,omitempty"`
	EndColumn int    `json:"end_column,omitempty"`
}

type factTraceJSONTransform struct {
	Kind       string                         `json:"kind"`
	Classes    []string                       `json:"classes,omitempty"`
	Target     string                         `json:"target,omitempty"`
	Source     string                         `json:"source,omitempty"`
	SourcePos  *factTraceJSONPosition         `json:"source_pos,omitempty"`
	SourceKind string                         `json:"source_kind,omitempty"`
	Details    []semantic.FactTransformDetail `json:"details,omitempty"`
	Reason     string                         `json:"reason,omitempty"`
	Text       string                         `json:"text"`
}

func factTraceReportMode(traceFilter factTraceFilter) string {
	if traceFilter.SummaryMode() {
		return semantic.FactTraceSummaryMode
	}
	return semantic.FactTraceFullMode
}

func factTraceJSONTransforms(transforms []semantic.FactTransform) []factTraceJSONTransform {
	transforms = semantic.CanonicalFactTransforms(transforms)
	out := make([]factTraceJSONTransform, 0, len(transforms))
	for _, transform := range transforms {
		classes := make([]string, 0, len(transform.Classes))
		for _, class := range transform.Classes {
			classes = append(classes, class.String())
		}
		item := factTraceJSONTransform{
			Kind:       transform.Kind.String(),
			Classes:    classes,
			Target:     transform.Target,
			Source:     transform.Source,
			SourceKind: transform.SourceKind.String(),
			Details:    transform.Details,
			Reason:     transform.Reason,
			Text:       semantic.FormatFactTransform(transform),
		}
		if !transform.SourcePos.IsZero() {
			item.SourcePos = newFactTraceJSONPosition(transform.SourcePos)
		}
		out = append(out, item)
	}
	return out
}

func newFactTraceJSONPosition(pos lexer.Pos) *factTraceJSONPosition {
	if pos.IsZero() {
		return nil
	}
	item := &factTraceJSONPosition{
		File:   pos.File,
		Line:   pos.Line,
		Column: pos.Col,
	}
	if pos.EndLine != 0 && pos.EndCol != 0 {
		item.EndLine = pos.EndLine
		item.EndColumn = pos.EndCol
	}
	return item
}

func formatTextFactTraceReport(result *semantic.Result, traceFilter factTraceFilter) string {
	var out bytes.Buffer
	out.WriteString("=== facts ===\n")
	out.WriteString(semantic.FormatFactTraceContract())
	out.WriteByte('\n')
	for _, entry := range collectFactTraceEntries(result, traceFilter) {
		fmt.Fprintf(&out, "func %s\n", entry.Name)
		if snapshot := semantic.FormatFactSnapshot(entry.Analysis.FactSnapshot); snapshot != "" {
			fmt.Fprintf(&out, "  snapshot: %s\n", snapshot)
		}
		if exits := semantic.FormatFactExitSummary(entry.Analysis.FactExitSummary); exits != "" {
			fmt.Fprintf(&out, "  exits: %s\n", exits)
		}
		if aliases := semantic.FormatFactAliasSets(entry.Analysis.AliasSets); aliases != "" {
			fmt.Fprintf(&out, "  aliases:\n%s", indentReportBlock(aliases, "    "))
		}
		if effects := semantic.FormatFactEffectSummary(entry.Analysis.EffectSummary); effects != "" {
			fmt.Fprintf(&out, "  effects: %s\n", effects)
		}
		if traceFilter.SummaryMode() {
			fmt.Fprintf(&out, "  summary: %s\n", semantic.FormatFactTransformSummary(entry.Transforms))
			continue
		}
		if summary := semantic.FormatFactTransforms(entry.Transforms); summary != "" {
			fmt.Fprintf(&out, "  transforms: %s\n", summary)
		}
		if groups := semantic.FormatFactTransformGroups(entry.Transforms); groups != "" {
			fmt.Fprintf(&out, "  groups:\n%s", indentReportBlock(groups, "    "))
		}
		if explanations := semantic.FormatFactExplanations(entry.Transforms); explanations != "" {
			fmt.Fprintf(&out, "  explanations:\n%s", indentReportBlock(explanations, "    "))
		}
	}
	return out.String()
}

type factTraceEntry struct {
	Name       string
	Analysis   *semantic.FunctionAnalysis
	Transforms []semantic.FactTransform
}

func collectFactTraceEntries(result *semantic.Result, traceFilter factTraceFilter) []factTraceEntry {
	if result == nil || result.GlobalScope == nil {
		return nil
	}
	funcNames := make([]string, 0)
	for name, sym := range result.GlobalScope.Symbols {
		if sym == nil || (sym.Kind != semantic.SymbolFunc && sym.Kind != semantic.SymbolExternFunc) {
			continue
		}
		if !traceFilter.FunctionNameCandidate(name) {
			continue
		}
		funcNames = append(funcNames, name)
	}
	sort.Strings(funcNames)
	entries := make([]factTraceEntry, 0, len(funcNames))
	for _, name := range funcNames {
		analysis, ok := result.FunctionAnalysisByName(name)
		if !ok || analysis == nil {
			continue
		}
		transforms := traceFilter.FilterTransforms(analysis.FactTransforms)
		if !traceFilter.MatchesFunction(name, analysis, transforms) {
			continue
		}
		entries = append(entries, factTraceEntry{Name: name, Analysis: analysis, Transforms: transforms})
	}
	return entries
}

type factTraceFilter struct {
	keys   map[string][]factTraceMatchTerm
	active bool
}

type factTraceMatchOperator string

const (
	factTraceMatchContains factTraceMatchOperator = "contains"
	factTraceMatchEquals   factTraceMatchOperator = "eq"
	factTraceMatchRegex    factTraceMatchOperator = "regex"
)

type factTraceMatchTerm struct {
	Operator factTraceMatchOperator
	Value    string
	Pattern  *regexp.Regexp
}

func parseFactTraceFilter(input string) (factTraceFilter, error) {
	filter := factTraceFilter{keys: map[string][]factTraceMatchTerm{}}
	allowed := supportedFactTraceFilterKeySet()
	for _, term := range strings.FieldsFunc(input, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		filter.active = true
		key, value, ok := strings.Cut(term, "=")
		if !ok {
			return factTraceFilter{}, fmt.Errorf("malformed fact trace filter %q: expected key=operator:value", term)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			return factTraceFilter{}, fmt.Errorf("malformed fact trace filter %q: expected key=operator:value with non-empty key and value", term)
		}
		if !allowed[key] {
			return factTraceFilter{}, fmt.Errorf("unsupported fact trace filter key %q (supported: %s)", key, strings.Join(supportedFactTraceFilterKeys(), ", "))
		}
		match, err := parseFactTraceMatchTerm(term, value)
		if err != nil {
			return factTraceFilter{}, err
		}
		filter.keys[key] = append(filter.keys[key], match)
	}
	return filter, nil
}

func parseFactTraceMatchTerm(term string, raw string) (factTraceMatchTerm, error) {
	operator, value, ok := strings.Cut(raw, ":")
	if !ok {
		return factTraceMatchTerm{}, fmt.Errorf("malformed fact trace filter %q: expected key=operator:value", term)
	}
	operator = strings.ToLower(strings.TrimSpace(operator))
	value = strings.TrimSpace(value)
	if operator == "" || value == "" {
		return factTraceMatchTerm{}, fmt.Errorf("malformed fact trace filter %q: expected key=operator:value with non-empty operator and value", term)
	}
	match := factTraceMatchTerm{Operator: factTraceMatchOperator(operator), Value: value}
	switch match.Operator {
	case factTraceMatchContains, factTraceMatchEquals:
		return match, nil
	case factTraceMatchRegex:
		pattern, err := regexp.Compile(value)
		if err != nil {
			return factTraceMatchTerm{}, fmt.Errorf("invalid regex in fact trace filter %q: %w", term, err)
		}
		match.Pattern = pattern
		return match, nil
	default:
		return factTraceMatchTerm{}, fmt.Errorf("unsupported fact trace filter operator %q in %q (supported: %s)", operator, term, strings.Join(supportedFactTraceFilterMatchers(), ", "))
	}
}

func supportedFactTraceFilterKeys() []string {
	keys := append([]string(nil), semantic.SupportedFactTraceFilterKeys...)
	sort.Strings(keys)
	return keys
}

func supportedFactTraceFilterMatchers() []string {
	matchers := append([]string(nil), semantic.SupportedFactTraceFilterMatchers...)
	sort.Strings(matchers)
	return matchers
}

func supportedFactTraceFilterKeySet() map[string]bool {
	keys := supportedFactTraceFilterKeys()
	out := make(map[string]bool, len(keys))
	for _, key := range keys {
		out[key] = true
	}
	return out
}

func (f factTraceFilter) SummaryMode() bool {
	return factTraceTermsMatchAny(f.keys["mode"], semantic.FactTraceSummaryMode, "compact")
}

func (f factTraceFilter) JSONMode() bool {
	return factTraceTermsMatchAny(f.keys["format"], semantic.FactTraceJSONFormat)
}

func (f factTraceFilter) FunctionNameCandidate(name string) bool {
	return f.matchesFunctionNameKey(name)
}

func (f factTraceFilter) MatchesFunction(name string, analysis *semantic.FunctionAnalysis, transforms []semantic.FactTransform) bool {
	if !f.active {
		return true
	}
	if !f.matchesFunctionNameKey(name) {
		return false
	}
	if len(f.transformFilterKeys()) == 0 {
		return true
	}
	if len(transforms) != 0 {
		return true
	}
	if analysis == nil {
		return false
	}
	return f.matchesSupplementalFilters(analysis)
}

func (f factTraceFilter) FilterTransforms(transforms []semantic.FactTransform) []semantic.FactTransform {
	if !f.active || len(f.transformFilterKeys()) == 0 {
		return transforms
	}
	out := make([]semantic.FactTransform, 0, len(transforms))
	for _, transform := range transforms {
		if f.matchesTransform(transform) {
			out = append(out, transform)
		}
	}
	return out
}

func (f factTraceFilter) matchesTransform(transform semantic.FactTransform) bool {
	for key, values := range f.transformFilterKeys() {
		matched := false
		for _, value := range values {
			if factTraceTransformFieldMatches(transform, key, value) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func (f factTraceFilter) matchesFunctionNameKey(name string) bool {
	values := f.keys["function"]
	if len(values) == 0 {
		return true
	}
	return factTraceTermsMatchText(values, name)
}

func (f factTraceFilter) transformFilterKeys() map[string][]factTraceMatchTerm {
	out := map[string][]factTraceMatchTerm{}
	for key, values := range f.keys {
		switch key {
		case "function", "mode", "format":
			continue
		default:
			out[key] = values
		}
	}
	return out
}

func factTraceTransformFieldMatches(transform semantic.FactTransform, key string, value factTraceMatchTerm) bool {
	switch key {
	case "kind", "verb":
		return value.Matches(string(transform.Kind))
	case "class":
		for _, class := range transform.Classes {
			if value.Matches(string(class)) {
				return true
			}
		}
		return false
	case "target", "path":
		return value.Matches(transform.Target)
	case "source":
		return value.Matches(transform.Source)
	case "sourcekind":
		return value.Matches(string(transform.SourceKind))
	case "reason":
		return value.Matches(transform.Reason)
	case "detail":
		for _, detail := range transform.Details {
			if value.Matches(detail.Name + "=" + detail.Value) {
				return true
			}
		}
		return false
	case "alias":
		return value.Matches(transform.Target) || value.Matches(semantic.FormatFactTransform(transform))
	case "effect":
		return value.Matches(transform.Target) && hasReportFactClass(transform.Classes, semantic.FactEffects)
	case "region":
		return value.Matches(transform.Target) && hasReportFactClass(transform.Classes, semantic.FactRegionDeps)
	case "store":
		return (value.Matches(transform.Target) || value.Matches(transform.Source)) && hasReportFactClass(transform.Classes, semantic.FactStoreDeps)
	default:
		return value.Matches(semantic.FormatFactTransform(transform))
	}
}

func (term factTraceMatchTerm) Matches(candidate string) bool {
	switch term.Operator {
	case factTraceMatchEquals:
		return candidate == term.Value
	case factTraceMatchRegex:
		return term.Pattern != nil && term.Pattern.MatchString(candidate)
	default:
		return strings.Contains(candidate, term.Value)
	}
}

func hasReportFactClass(classes []semantic.FactClass, target semantic.FactClass) bool {
	for _, class := range classes {
		if class == target {
			return true
		}
	}
	return false
}

func factTraceTermsMatchText(terms []factTraceMatchTerm, text string) bool {
	if len(terms) == 0 {
		return false
	}
	for _, term := range terms {
		if term.Matches(text) {
			return true
		}
	}
	return false
}

func factTraceTermsMatchAny(terms []factTraceMatchTerm, candidates ...string) bool {
	if len(terms) == 0 {
		return false
	}
	return factTraceTermsMatchCandidates(terms, candidates)
}

func factTraceTermsMatchCandidates(terms []factTraceMatchTerm, candidates []string) bool {
	if len(terms) == 0 {
		return false
	}
	for _, candidate := range candidates {
		if factTraceTermsMatchText(terms, candidate) {
			return true
		}
	}
	return false
}

func (f factTraceFilter) matchesSupplementalFilters(analysis *semantic.FunctionAnalysis) bool {
	if analysis == nil {
		return false
	}
	for key, terms := range f.transformFilterKeys() {
		switch key {
		case "alias":
			if !factTraceTermsMatchCandidates(terms, factTraceAliasSetCandidates(analysis.AliasSets)) {
				return false
			}
		case "effect":
			if !factTraceTermsMatchCandidates(terms, factTraceEffectSummaryCandidates(analysis.EffectSummary)) {
				return false
			}
		case "target", "path", "region", "store":
			if !factTraceTermsMatchCandidates(terms, factTraceSnapshotCandidates(analysis.FactSnapshot, key)) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func factTraceAliasSetCandidates(sets []semantic.FactAliasSet) []string {
	candidates := make([]string, 0, len(sets)*4+1)
	for _, set := range sets {
		if set.ID != "" {
			candidates = append(candidates, set.ID)
		}
		candidates = append(candidates, set.Members...)
		candidates = append(candidates, set.AffectedPaths...)
	}
	if formatted := semantic.FormatFactAliasSets(sets); formatted != "" {
		candidates = append(candidates, formatted)
	}
	return candidates
}

func factTraceEffectSummaryCandidates(summary semantic.FactEffectSummary) []string {
	candidates := make([]string, 0, len(summary.Required)+len(summary.Provided)+1)
	candidates = append(candidates, summary.Required...)
	candidates = append(candidates, summary.Provided...)
	if formatted := semantic.FormatFactEffectSummary(summary); formatted != "" {
		candidates = append(candidates, formatted)
	}
	return candidates
}

func factTraceSnapshotCandidates(snapshot semantic.FactSnapshot, key string) []string {
	switch key {
	case "target", "path":
		candidates := make([]string, 0, len(snapshot.PathFacts)*4+len(snapshot.Parameters)+len(snapshot.Returns)+len(snapshot.Consumed)+len(snapshot.Produced)+len(snapshot.Ensured)+len(snapshot.Refined)+len(snapshot.Widened))
		candidates = append(candidates, snapshot.Parameters...)
		candidates = append(candidates, snapshot.Returns...)
		candidates = append(candidates, snapshot.Consumed...)
		candidates = append(candidates, snapshot.Produced...)
		candidates = append(candidates, snapshot.Ensured...)
		candidates = append(candidates, snapshot.Refined...)
		candidates = append(candidates, snapshot.Widened...)
		for _, path := range snapshot.PathFacts {
			candidates = append(candidates, path.Target, path.Root, path.Path, semantic.FormatFactPath(path))
		}
		return candidates
	case "region":
		candidates := make([]string, 0, len(snapshot.RegionDeps)*2+len(snapshot.InvalidatedRegions))
		candidates = append(candidates, snapshot.InvalidatedRegions...)
		for _, region := range snapshot.RegionDeps {
			candidates = append(candidates, region)
			if idx := strings.Index(region, "["); idx > 0 {
				candidates = append(candidates, region[:idx])
			}
		}
		return candidates
	case "store":
		candidates := make([]string, 0, len(snapshot.StoreDeps)+len(snapshot.HandleStoreDeps)*3+len(snapshot.RebasedStores))
		candidates = append(candidates, snapshot.StoreDeps...)
		candidates = append(candidates, snapshot.RebasedStores...)
		for _, dep := range snapshot.HandleStoreDeps {
			candidates = append(candidates, dep)
			if left, right, ok := strings.Cut(dep, "<-"); ok {
				candidates = append(candidates, left, right)
			}
		}
		return candidates
	default:
		return nil
	}
}
