package semantic

import (
	"fmt"
	"llcontext/src/lexer"
	"sort"
	"strings"
)

const FactTraceFormatVersion = "fact-trace-v2"
const (
	FactTraceSummaryMode = "summary"
	FactTraceFullMode    = "full"
	FactTraceTextFormat  = "text"
	FactTraceJSONFormat  = "json"
)

var SupportedFactTraceFilterKeys = []string{
	"alias",
	"class",
	"detail",
	"effect",
	"format",
	"function",
	"kind",
	"mode",
	"path",
	"reason",
	"region",
	"source",
	"sourcekind",
	"store",
	"target",
	"verb",
}
var SupportedFactTraceFilterMatchers = []string{
	"contains",
	"eq",
	"regex",
}

// FactClass names the orthogonal kinds of static knowledge the analyzer tracks
// about values. The current compiler still stores these facts in specialized
// structures such as GuardFactSet, OptimizationFacts, RefType, Shape, effects,
// and packed store state; this enum is the shared vocabulary for gradually
// unifying those systems.
type FactClass string

const (
	FactRepresentation FactClass = "representation"
	FactRefState       FactClass = "refstate"
	FactShape          FactClass = "shape"
	FactTypestate      FactClass = "typestate"
	FactStorage        FactClass = "storage"
	FactRegionDeps     FactClass = "region-deps"
	FactStoreDeps      FactClass = "store-deps"
	FactAliasClass     FactClass = "alias-class"
	FactUsage          FactClass = "usage"
	FactEffects        FactClass = "effects"
	FactErrorPath      FactClass = "error-path"
	FactOptimization   FactClass = "optimization"
	FactInterface      FactClass = "interface"
)

// FactTransformKind is the small algebra that operations should lower to when
// they change static knowledge. Keeping these names explicit makes it easier to
// explain why surface conveniences such as node[...] or freeze(move store) are
// still honest: they may hide syntax, but they must not hide fact transitions.
type FactTransformKind string

const (
	FactTransformProduce    FactTransformKind = "produce"
	FactTransformRefine     FactTransformKind = "refine"
	FactTransformWiden      FactTransformKind = "widen"
	FactTransformRecompute  FactTransformKind = "recompute"
	FactTransformConsume    FactTransformKind = "consume"
	FactTransformInvalidate FactTransformKind = "invalidate"
	FactTransformRebase     FactTransformKind = "rebase"
	FactTransformRequire    FactTransformKind = "require"
	FactTransformEnsure     FactTransformKind = "ensure"
)

type FactTransformSourceKind string

const (
	FactSourceUnknown    FactTransformSourceKind = "unknown"
	FactSourceGuard      FactTransformSourceKind = "guard"
	FactSourceFlowInstr  FactTransformSourceKind = "flow-instr"
	FactSourceSignature  FactTransformSourceKind = "signature"
	FactSourceCallWiden  FactTransformSourceKind = "call-widen"
	FactSourcePermission FactTransformSourceKind = "permission"
	FactSourceRegion     FactTransformSourceKind = "region"
	FactSourceStore      FactTransformSourceKind = "store"
	FactSourceErrorPath  FactTransformSourceKind = "error-path"
	FactSourceReturn     FactTransformSourceKind = "return"
)

type FactTransformDetail struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type StoreDependencyFacts struct {
	HasPackedStoreDeps          bool
	HasFrozenPackedStoreDeps    bool
	HasNonFrozenPackedStoreDeps bool
	HasNonStoreProvenance       bool
}
type PackedStoreProvenance = StoreDependencyFacts
type FactPath struct {
	Target string         `json:"target"`
	Root   string         `json:"root"`
	Path   string         `json:"path"`
	Steps  []FactPathStep `json:"steps"`
}
type FactPathStep struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}
type FactExitSummary struct {
	Normal []string `json:"normal"`
	Error  []string `json:"error"`
}
type FactAliasSet struct {
	ID            string   `json:"id"`
	Members       []string `json:"members"`
	AffectedPaths []string `json:"affected_paths"`
	Mutated       bool     `json:"mutated"`
}
type FactEffectSummary struct {
	Required []string `json:"required"`
	Provided []string `json:"provided"`
}

// FactTransform is a lightweight descriptive record used by diagnostics,
// reports, and future analyzer cleanup work. It intentionally does not own the
// fact payload yet; existing precise structures remain the source of truth until
// each subsystem migrates onto the shared model.
type FactTransform struct {
	Kind       FactTransformKind
	Classes    []FactClass
	Target     string
	Source     string
	SourcePos  lexer.Pos
	SourceKind FactTransformSourceKind
	Details    []FactTransformDetail
	Reason     string
}
type FactSnapshot struct {
	Parameters         []string   `json:"parameters"`
	Returns            []string   `json:"returns"`
	Consumed           []string   `json:"consumed"`
	Produced           []string   `json:"produced"`
	InvalidatedRegions []string   `json:"invalidated_regions"`
	RebasedStores      []string   `json:"rebased_stores"`
	RequiredEffects    []string   `json:"required_effects"`
	RequiredInterfaces []string   `json:"required_interfaces"`
	Ensured            []string   `json:"ensured"`
	Refined            []string   `json:"refined"`
	Widened            []string   `json:"widened"`
	ErrorExits         []string   `json:"error_exits"`
	StoreDeps          []string   `json:"store_deps"`
	PathFacts          []FactPath `json:"path_facts"`
	AliasClasses       []string   `json:"alias_classes"`
	HandleStoreDeps    []string   `json:"handle_store_deps"`
	RegionDeps         []string   `json:"region_deps"`
}

func FormatFactTraceContract() string {
	keys := append([]string(nil), SupportedFactTraceFilterKeys...)
	sort.Strings(keys)
	matchers := append([]string(nil), SupportedFactTraceFilterMatchers...)
	sort.Strings(matchers)
	return "contract: version=" + FactTraceFormatVersion + " order=kind,target,class,reason,source summary=mode=eq:" + FactTraceSummaryMode + " json=format=eq:" + FactTraceJSONFormat + " matchers=" + strings.Join(matchers, "|") + " filters=" + strings.Join(keys, "|")
}

type RefinementFacts = GuardFactSet

func NewRefinementFacts() RefinementFacts {
	return NewGuardFactSet()
}
func FormatFactTransforms(transforms []FactTransform) string {
	if len(transforms) == 0 {
		return ""
	}
	parts := make([]string, 0, len(transforms))
	for _, transform := range transforms {
		if text := FormatFactTransform(transform); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, "; ") + "]"
}
func FormatFactTransformSummary(transforms []FactTransform) string {
	if len(transforms) == 0 {
		return "transforms=0"
	}
	kindCounts := map[FactTransformKind]int{}
	classCounts := map[FactClass]int{}
	for _, transform := range transforms {
		if transform.Kind != "" {
			kindCounts[transform.Kind]++
		}
		for _, class := range transform.Classes {
			if class != "" {
				classCounts[class]++
			}
		}
	}
	parts := []string{fmt.Sprintf("transforms=%d", len(transforms))}
	if len(kindCounts) != 0 {
		keys := make([]string, 0, len(kindCounts))
		for kind := range kindCounts {
			keys = append(keys, kind.String())
		}
		sort.Strings(keys)
		items := make([]string, 0, len(keys))
		for _, key := range keys {
			items = append(items, fmt.Sprintf("%s:%d", key, kindCounts[FactTransformKind(key)]))
		}
		parts = append(parts, "kinds=["+strings.Join(items, ", ")+"]")
	}
	if len(classCounts) != 0 {
		keys := make([]string, 0, len(classCounts))
		for class := range classCounts {
			keys = append(keys, class.String())
		}
		sort.Strings(keys)
		items := make([]string, 0, len(keys))
		for _, key := range keys {
			items = append(items, fmt.Sprintf("%s:%d", key, classCounts[FactClass(key)]))
		}
		parts = append(parts, "classes=["+strings.Join(items, ", ")+"]")
	}
	return strings.Join(parts, " ")
}
func FormatFactTransform(transform FactTransform) string {
	if transform.Kind == "" {
		return ""
	}
	var out strings.Builder
	out.WriteString(transform.Kind.String())
	if transform.Target != "" {
		out.WriteByte(' ')
		out.WriteString(transform.Target)
	}
	if len(transform.Classes) != 0 {
		classes := make([]string, 0, len(transform.Classes))
		for _, class := range transform.Classes {
			classes = append(classes, class.String())
		}
		out.WriteString(" [")
		out.WriteString(strings.Join(classes, ","))
		out.WriteByte(']')
	}
	if transform.Source != "" {
		out.WriteString(" <- ")
		out.WriteString(transform.Source)
	}
	if !transform.SourcePos.IsZero() {
		out.WriteString(" @ ")
		out.WriteString(transform.SourcePos.String())
	}
	if len(transform.Details) != 0 {
		details := make([]string, 0, len(transform.Details))
		for _, detail := range transform.Details {
			if detail.Name == "" || detail.Value == "" {
				continue
			}
			details = append(details, detail.Name+"="+detail.Value)
		}
		if len(details) != 0 {
			out.WriteString(" {")
			out.WriteString(strings.Join(details, ","))
			out.WriteByte('}')
		}
	}
	if transform.Reason != "" {
		out.WriteString(" (")
		out.WriteString(transform.Reason)
		out.WriteByte(')')
	}
	return out.String()
}
func FormatFactExplanations(transforms []FactTransform) string {
	if len(transforms) == 0 {
		return ""
	}
	lines := make([]string, 0, len(transforms))
	for _, transform := range transforms {
		if text := ExplainFactTransform(transform); text != "" {
			lines = append(lines, text)
		}
	}
	return strings.Join(lines, "\n")
}
func ExplainFactTransform(transform FactTransform) string {
	switch transform.Kind {
	case FactTransformWiden:
		before := factTransformDetailValue(transform.Details, "before")
		after := factTransformDetailValue(transform.Details, "after")
		label := "widen " + transform.Target
		if len(transform.Classes) != 0 {
			label = "widen " + factClassListKey(transform.Classes) + " facts for " + transform.Target
		}
		parts := []string{label}
		if before != "" || after != "" {
			parts = append(parts, "from "+emptyFactDetailFallback(before, "<unknown>")+" to "+emptyFactDetailFallback(after, "<widened>"))
		}
		if transform.Source != "" {
			parts = append(parts, "after "+transform.Source)
		}
		if transform.Reason != "" {
			parts = append(parts, "because "+transform.Reason)
		}
		return strings.Join(parts, " ")
	case FactTransformInvalidate:
		if hasFactClass(transform.Classes, FactRegionDeps) {
			return "invalidate region facts for " + transform.Target + factExplanationSourceSuffix(transform) + factExplanationReasonSuffix(transform)
		}
	case FactTransformRebase:
		if hasFactClass(transform.Classes, FactStoreDeps) {
			return "rebase store dependency facts for " + transform.Target + factExplanationSourceSuffix(transform) + factExplanationReasonSuffix(transform)
		}
	case FactTransformConsume:
		if hasFactClass(transform.Classes, FactUsage) {
			return "consume usage facts for " + transform.Target + factExplanationReasonSuffix(transform)
		}
	case FactTransformRecompute:
		if len(transform.Classes) != 0 {
			return "recompute " + factClassListKey(transform.Classes) + " facts for " + transform.Target + factExplanationSourceSuffix(transform) + factExplanationReasonSuffix(transform)
		}
	case FactTransformRequire:
		if hasFactClass(transform.Classes, FactEffects) {
			return "require effect authority fact " + transform.Target
		}
	}
	return ""
}
func factExplanationSourceSuffix(transform FactTransform) string {
	if transform.Source == "" {
		return ""
	}
	return " from " + transform.Source
}
func factExplanationReasonSuffix(transform FactTransform) string {
	if transform.Reason == "" {
		return ""
	}
	return " because " + transform.Reason
}
func emptyFactDetailFallback(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
func factTransformDetailValue(details []FactTransformDetail, name string) string {
	for _, detail := range details {
		if detail.Name == name {
			return detail.Value
		}
	}
	return ""
}
func FormatFactTransformGroups(transforms []FactTransform) string {
	groups := GroupFactTransforms(transforms)
	if len(groups) == 0 {
		return ""
	}
	labels := []struct {
		Kind  FactTransformKind
		Label string
	}{
		{FactTransformRequire, "requires"},
		{FactTransformEnsure, "ensures"},
		{FactTransformRefine, "refines"},
		{FactTransformWiden, "widens"},
		{FactTransformProduce, "produces"},
		{FactTransformConsume, "consumes"},
		{FactTransformInvalidate, "invalidates"},
		{FactTransformRebase, "rebases"},
		{FactTransformRecompute, "recomputes"},
	}
	lines := make([]string, 0, len(labels))
	for _, label := range labels {
		items := groups[label.Kind]
		if len(items) == 0 {
			continue
		}
		parts := make([]string, 0, len(items))
		for _, item := range items {
			if text := FormatFactTransform(item); text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) != 0 {
			lines = append(lines, label.Label+": "+strings.Join(parts, "; "))
		}
	}
	return strings.Join(lines, "\n")
}
func GroupFactTransforms(transforms []FactTransform) map[FactTransformKind][]FactTransform {
	if len(transforms) == 0 {
		return nil
	}
	groups := map[FactTransformKind][]FactTransform{}
	for _, transform := range transforms {
		if transform.Kind == "" {
			continue
		}
		groups[transform.Kind] = append(groups[transform.Kind], transform)
	}
	if len(groups) == 0 {
		return nil
	}
	return groups
}
func FormatFactSnapshot(snapshot FactSnapshot) string {
	parts := make([]string, 0, 14)
	appendPart := func(label string, values []string) {
		values = canonicalStringList(values)
		if len(values) != 0 {
			parts = append(parts, label+"=["+strings.Join(values, ", ")+"]")
		}
	}
	appendPathPart := func(label string, values []FactPath) {
		values = canonicalFactPaths(values)
		if len(values) == 0 {
			return
		}
		partsText := make([]string, 0, len(values))
		for _, value := range values {
			partsText = append(partsText, FormatFactPath(value))
		}
		parts = append(parts, label+"=["+strings.Join(partsText, "; ")+"]")
	}
	appendPart("params", snapshot.Parameters)
	appendPart("returns", snapshot.Returns)
	appendPart("consumed", snapshot.Consumed)
	appendPart("produced", snapshot.Produced)
	appendPart("invalidated_regions", snapshot.InvalidatedRegions)
	appendPart("rebased_stores", snapshot.RebasedStores)
	appendPart("required_effects", snapshot.RequiredEffects)
	appendPart("required_interfaces", snapshot.RequiredInterfaces)
	appendPart("ensured", snapshot.Ensured)
	appendPart("refined", snapshot.Refined)
	appendPart("widened", snapshot.Widened)
	appendPart("error_exits", snapshot.ErrorExits)
	appendPart("store_deps", snapshot.StoreDeps)
	appendPathPart("path_facts", snapshot.PathFacts)
	appendPart("alias_classes", snapshot.AliasClasses)
	appendPart("handle_store_deps", snapshot.HandleStoreDeps)
	appendPart("region_deps", snapshot.RegionDeps)
	return strings.Join(parts, " ")
}
func FormatFactPath(path FactPath) string {
	if path.Target == "" {
		return ""
	}
	parts := []string{"root=" + path.Root}
	if path.Path != "" {
		parts = append(parts, "path="+path.Path)
	}
	if len(path.Steps) != 0 {
		parts = append(parts, "steps="+formatFactPathSteps(path.Steps))
	}
	return path.Target + "{" + strings.Join(parts, ",") + "}"
}
func NewFactPath(target string) FactPath {
	if target == "" || strings.HasPrefix(target, "<") {
		return FactPath{}
	}
	root := factPathRoot(target)
	if root == "" {
		return FactPath{}
	}
	path := strings.TrimPrefix(target[len(root):], ".")
	return FactPath{Target: target, Root: root, Path: path, Steps: FactPathStepsFromPath(path)}
}
func NewReturnFactPath() FactPath {
	return FactPath{Target: "<return>.value", Root: "<return>", Path: "value", Steps: []FactPathStep{{Kind: "result", Name: "value"}}}
}
func FactPathStepsFromPath(path string) []FactPathStep {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	steps := make([]FactPathStep, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		kind := "field"
		if strings.Contains(part, "[") {
			kind = "index"
		}
		steps = append(steps, FactPathStep{Kind: kind, Name: part})
	}
	return steps
}
func factPathRoot(target string) string {
	if target == "" {
		return ""
	}
	if idx := strings.IndexAny(target, ".[ "); idx >= 0 {
		return target[:idx]
	}
	return target
}
func formatFactPathSteps(steps []FactPathStep) string {
	if len(steps) == 0 {
		return ""
	}
	parts := make([]string, 0, len(steps))
	for _, step := range steps {
		if step.Kind == "" && step.Name == "" {
			continue
		}
		if step.Kind == "" {
			parts = append(parts, step.Name)
			continue
		}
		parts = append(parts, step.Kind+":"+step.Name)
	}
	return strings.Join(parts, "/")
}
func FormatFactExitSummary(summary FactExitSummary) string {
	normal := canonicalStringList(summary.Normal)
	errorExits := canonicalStringList(summary.Error)
	parts := make([]string, 0, 2)
	if len(normal) != 0 {
		parts = append(parts, "normal=["+strings.Join(normal, "; ")+"]")
	}
	if len(errorExits) != 0 {
		parts = append(parts, "error=["+strings.Join(errorExits, "; ")+"]")
	}
	return strings.Join(parts, " ")
}
func FormatFactAliasSets(sets []FactAliasSet) string {
	if len(sets) == 0 {
		return ""
	}
	lines := make([]string, 0, len(sets))
	for _, set := range sets {
		members := canonicalStringList(set.Members)
		if set.ID == "" || len(members) == 0 {
			continue
		}
		mutated := "stable"
		if set.Mutated {
			mutated = "mutated"
		}
		line := set.ID + ": {" + strings.Join(members, ", ") + "} " + mutated
		if affected := canonicalStringList(set.AffectedPaths); len(affected) != 0 {
			line += " affects=[" + strings.Join(affected, ", ") + "]"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
func FormatFactEffectSummary(summary FactEffectSummary) string {
	parts := make([]string, 0, 2)
	if required := canonicalStringList(summary.Required); len(required) != 0 {
		sort.Strings(required)
		parts = append(parts, "required=["+strings.Join(required, ", ")+"]")
	}
	if provided := canonicalStringList(summary.Provided); len(provided) != 0 {
		sort.Strings(provided)
		parts = append(parts, "provided=["+strings.Join(provided, ", ")+"]")
	}
	return strings.Join(parts, " ")
}
