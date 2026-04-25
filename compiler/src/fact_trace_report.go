package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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
		Version: semantic.FactTraceFormatVersion,
		Mode:    semantic.FactTraceJSONMode,
		Filters: supportedFactTraceFilterKeys(),
	}
	for _, entry := range collectFactTraceEntries(result, traceFilter) {
		payload.Functions = append(payload.Functions, factTraceJSONFunction{
			Name:        entry.Name,
			Snapshot:    entry.Analysis.FactSnapshot,
			Exits:       entry.Analysis.FactExitSummary,
			Aliases:     entry.Analysis.AliasSets,
			Effects:     entry.Analysis.EffectSummary,
			Summary:     semantic.FormatFactTransformSummary(entry.Transforms),
			Transforms:  factTraceJSONTransforms(entry.Transforms),
			TextSummary: semantic.FormatFactTransforms(entry.Transforms),
		})
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
	Filters   []string                `json:"filters"`
	Functions []factTraceJSONFunction `json:"functions"`
}

type factTraceJSONFunction struct {
	Name        string                     `json:"name"`
	Snapshot    semantic.FactSnapshot      `json:"snapshot"`
	Exits       semantic.FactExitSummary   `json:"exits"`
	Aliases     []semantic.FactAliasSet    `json:"aliases,omitempty"`
	Effects     semantic.FactEffectSummary `json:"effects"`
	Summary     string                     `json:"summary"`
	Transforms  []factTraceJSONTransform   `json:"transforms"`
	TextSummary string                     `json:"text_summary,omitempty"`
}

type factTraceJSONTransform struct {
	Kind       string                         `json:"kind"`
	Classes    []string                       `json:"classes,omitempty"`
	Target     string                         `json:"target,omitempty"`
	Source     string                         `json:"source,omitempty"`
	SourcePos  string                         `json:"source_pos,omitempty"`
	SourceKind string                         `json:"source_kind,omitempty"`
	Details    []semantic.FactTransformDetail `json:"details,omitempty"`
	Reason     string                         `json:"reason,omitempty"`
	Text       string                         `json:"text"`
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
			item.SourcePos = transform.SourcePos.String()
		}
		out = append(out, item)
	}
	return out
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
	functionTerms []string
	keys          map[string][]string
	active        bool
}

func parseFactTraceFilter(input string) (factTraceFilter, error) {
	filter := factTraceFilter{keys: map[string][]string{}}
	allowed := supportedFactTraceFilterKeySet()
	for _, term := range strings.FieldsFunc(input, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		filter.active = true
		if key, value, ok := strings.Cut(term, "="); ok {
			key = strings.ToLower(strings.TrimSpace(key))
			value = strings.TrimSpace(value)
			if key == "" || value == "" {
				return factTraceFilter{}, fmt.Errorf("malformed fact trace filter %q: expected key=value with non-empty key and value", term)
			}
			if !allowed[key] {
				return factTraceFilter{}, fmt.Errorf("unsupported fact trace filter key %q (supported: %s)", key, strings.Join(supportedFactTraceFilterKeys(), ", "))
			}
			filter.keys[key] = append(filter.keys[key], value)
			continue
		}
		filter.functionTerms = append(filter.functionTerms, term)
	}
	return filter, nil
}

func supportedFactTraceFilterKeys() []string {
	keys := append([]string(nil), semantic.SupportedFactTraceFilterKeys...)
	sort.Strings(keys)
	return keys
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
	for _, value := range f.keys["mode"] {
		if strings.EqualFold(value, "summary") || strings.EqualFold(value, "compact") {
			return true
		}
	}
	return false
}

func (f factTraceFilter) JSONMode() bool {
	for _, value := range f.keys["mode"] {
		if strings.EqualFold(value, semantic.FactTraceJSONMode) {
			return true
		}
	}
	return false
}

func (f factTraceFilter) FunctionNameCandidate(name string) bool {
	if !f.matchesFunctionNameKey(name) {
		return false
	}
	if len(f.functionTerms) == 0 {
		return true
	}
	for _, term := range f.functionTerms {
		if strings.Contains(name, term) {
			return true
		}
	}
	return len(f.keys) != 0
}

func (f factTraceFilter) MatchesFunction(name string, analysis *semantic.FunctionAnalysis, transforms []semantic.FactTransform) bool {
	if !f.active {
		return true
	}
	if !f.matchesFunctionNameKey(name) {
		return false
	}
	for _, term := range f.functionTerms {
		if strings.Contains(name, term) {
			return true
		}
	}
	if len(f.transformFilterKeys()) == 0 {
		return true
	}
	if len(f.keys) == 0 {
		return false
	}
	if len(transforms) != 0 {
		return true
	}
	if analysis == nil {
		return false
	}
	return f.matchesAliasSets(analysis.AliasSets) || f.matchesEffectSummary(analysis.EffectSummary) || f.matchesSnapshot(analysis.FactSnapshot)
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
	for _, value := range values {
		if strings.Contains(name, value) {
			return true
		}
	}
	return false
}

func (f factTraceFilter) transformFilterKeys() map[string][]string {
	out := map[string][]string{}
	for key, values := range f.keys {
		switch key {
		case "function", "mode":
			continue
		default:
			out[key] = values
		}
	}
	return out
}

func factTraceTransformFieldMatches(transform semantic.FactTransform, key string, value string) bool {
	switch key {
	case "kind", "verb":
		return string(transform.Kind) == value
	case "class", "fact", "factclass":
		for _, class := range transform.Classes {
			if string(class) == value {
				return true
			}
		}
		return false
	case "target", "path":
		return strings.Contains(transform.Target, value)
	case "source":
		return strings.Contains(transform.Source, value)
	case "sourcekind":
		return string(transform.SourceKind) == value
	case "reason":
		return strings.Contains(transform.Reason, value)
	case "detail":
		for _, detail := range transform.Details {
			if strings.Contains(detail.Name+"="+detail.Value, value) {
				return true
			}
		}
		return false
	case "alias":
		return strings.Contains(transform.Target, value) || strings.Contains(semantic.FormatFactTransform(transform), value)
	case "effect":
		return strings.Contains(transform.Target, value) && hasReportFactClass(transform.Classes, semantic.FactEffects)
	case "region":
		return strings.Contains(transform.Target, value) && hasReportFactClass(transform.Classes, semantic.FactRegionDeps)
	case "store":
		return (strings.Contains(transform.Target, value) || strings.Contains(transform.Source, value)) && hasReportFactClass(transform.Classes, semantic.FactStoreDeps)
	default:
		return strings.Contains(semantic.FormatFactTransform(transform), value)
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

func (f factTraceFilter) matchesAliasSets(sets []semantic.FactAliasSet) bool {
	values := f.keys["alias"]
	if len(values) == 0 {
		return false
	}
	text := semantic.FormatFactAliasSets(sets)
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func (f factTraceFilter) matchesEffectSummary(summary semantic.FactEffectSummary) bool {
	values := f.keys["effect"]
	if len(values) == 0 {
		return false
	}
	text := semantic.FormatFactEffectSummary(summary)
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func (f factTraceFilter) matchesSnapshot(snapshot semantic.FactSnapshot) bool {
	for _, key := range []string{"target", "path", "region", "store"} {
		values := f.keys[key]
		if len(values) == 0 {
			continue
		}
		text := semantic.FormatFactSnapshot(snapshot)
		for _, value := range values {
			if strings.Contains(text, value) {
				return true
			}
		}
	}
	return false
}
