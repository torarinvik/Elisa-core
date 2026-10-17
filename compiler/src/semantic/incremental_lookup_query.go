package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"elisacore/src/incremental"
)

const lookupSelectionQueryKind = "resolved-name-selection-v1"
const lookupSelectionMaxBytes = 64 << 10

type lookupSelectionSpec struct {
	Context             lookupDependencyContext
	Kind, Request, Site string
}
type lookupSelectionRecipe struct {
	Candidates    []string
	LocalShadowed bool
}
type lookupSelectionProduct struct {
	Name  string
	Found bool
}

// This query resolves the selection domain only. Current Type/Symbol objects
// must be rebound separately; types, callee summaries, private-name diagnostic
// details and body facts require their own dependencies. Specs carry exact owned
// context, not a process-local pointer or a hash used as semantic authority.
func lookupSelectionKey(spec lookupSelectionSpec) (incremental.Key, error) {
	if len(spec.Context.Usings) > lookupTraceMaxCandidates {
		return incremental.Key{}, fmt.Errorf("too many lookup imports")
	}
	if spec.Kind == "enclosing" && (spec.Site == "" || strings.Contains(spec.Request, ".")) {
		return incremental.Key{}, fmt.Errorf("enclosing lookup requires current lexical site identity")
	}
	if spec.Request == "" {
		return incremental.Key{}, fmt.Errorf("empty lookup request")
	}
	switch spec.Kind {
	case "value", "type", "permission", "enclosing":
	default:
		return incremental.Key{}, fmt.Errorf("unknown lookup domain")
	}
	size := 0
	for _, text := range append([]string{spec.Kind, spec.Request, spec.Site, spec.Context.Namespace, spec.Context.Function, spec.Context.Origin, spec.Context.Phase}, spec.Context.Usings...) {
		if !utf8.ValidString(text) || len(text) > lookupSelectionMaxBytes/6-size {
			return incremental.Key{}, fmt.Errorf("lookup context exceeds encoding budget")
		}
		size += len(text)
	}
	data, err := json.Marshal(spec)
	if err != nil || len(data) > lookupSelectionMaxBytes {
		return incremental.Key{}, fmt.Errorf("invalid lookup context")
	}
	return incremental.Key{Kind: lookupSelectionQueryKind, Name: string(data)}, nil
}
func lookupRecipeInput(key incremental.Key) incremental.Key {
	return incremental.Key{Kind: "lookup-recipe/" + key.Kind, Name: key.Name}
}
func lookupBindingInput(key incremental.Key, name string) incremental.Key {
	return incremental.Key{Kind: "lookup-binding/" + key.Kind, Name: key.Name + "\x00" + name}
}

func decodeLookupSelection(data string, target any) error {
	if !utf8.ValidString(data) || len(data) > lookupSelectionMaxBytes {
		return fmt.Errorf("lookup input exceeds byte budget")
	}
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing lookup data")
	}
	return nil
}
func validateLookupRecipe(spec lookupSelectionSpec, recipe lookupSelectionRecipe) error {
	if spec.Kind == "enclosing" && len(recipe.Candidates) > 1 {
		return fmt.Errorf("multiple enclosing candidates")
	}
	if recipe.LocalShadowed && spec.Kind != "enclosing" {
		return fmt.Errorf("local shadow outside enclosing lookup")
	}
	if len(recipe.Candidates) > lookupTraceMaxCandidates {
		return fmt.Errorf("lookup candidate budget exceeded")
	}
	seen := map[string]bool{}
	size := 0
	for _, name := range recipe.Candidates {
		if name == "" || !utf8.ValidString(name) || seen[name] || len(name) > lookupSelectionMaxBytes/6-size {
			return fmt.Errorf("invalid lookup candidate")
		}
		seen[name] = true
		size += len(name)
	}
	return nil
}

// Publication must use the CURRENT complete ordered candidates/visibility from
// the declaration environment. A previously captured successful trace contains
// only its visited prefix and is not authority to publish a later environment.
// Missing bindings are removed inputs, so Reader.Input tracks their absence.
func lookupSelectionChanges(spec lookupSelectionSpec, reads []lookupCandidateRead, shadowed bool) (incremental.Key, []incremental.Change, error) {
	key, err := lookupSelectionKey(spec)
	if err != nil {
		return incremental.Key{}, nil, err
	}
	recipe := lookupSelectionRecipe{LocalShadowed: shadowed}
	if len(reads) > lookupTraceMaxCandidates {
		return incremental.Key{}, nil, fmt.Errorf("lookup candidate budget exceeded")
	}
	for _, read := range reads {
		recipe.Candidates = append(recipe.Candidates, read.Name)
		if !read.Present && read.Visible {
			return incremental.Key{}, nil, fmt.Errorf("absent binding marked visible")
		}
	}
	if err := validateLookupRecipe(spec, recipe); err != nil {
		return incremental.Key{}, nil, err
	}
	data, err := json.Marshal(recipe)
	if err != nil || len(data) > lookupSelectionMaxBytes {
		return incremental.Key{}, nil, fmt.Errorf("invalid lookup recipe")
	}
	changes := []incremental.Change{{Key: lookupRecipeInput(key), Value: string(data)}}
	for _, read := range reads {
		state := "hidden"
		if read.Visible {
			state = "visible"
		}
		changes = append(changes, incremental.Change{Key: lookupBindingInput(key, read.Name), Value: state, Remove: !read.Present})
	}
	return key, changes, nil
}

func evaluateLookupSelection(r *incremental.Reader, name string) (string, error) {
	var spec lookupSelectionSpec
	if err := decodeLookupSelection(name, &spec); err != nil {
		return "", err
	}
	key, err := lookupSelectionKey(spec)
	if err != nil || key.Name != name {
		return "", fmt.Errorf("noncanonical lookup query")
	}
	encoded, present := r.Input(lookupRecipeInput(key))
	if !present {
		return "", fmt.Errorf("unpublished lookup candidates")
	}
	var recipe lookupSelectionRecipe
	if err := decodeLookupSelection(encoded, &recipe); err != nil {
		return "", err
	}
	if err := validateLookupRecipe(spec, recipe); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(recipe)
	if err != nil || string(canonical) != encoded {
		return "", fmt.Errorf("noncanonical published lookup recipe")
	}
	product := lookupSelectionProduct{}
	if !recipe.LocalShadowed {
		for _, candidate := range recipe.Candidates {
			state, present := r.Input(lookupBindingInput(key, candidate))
			visible := false
			if present {
				switch state {
				case "visible":
					visible = true
				case "hidden":
				default:
					return "", fmt.Errorf("invalid published lookup binding")
				}
			}
			if visible {
				product.Name = candidate
				product.Found = true
				break
			}
		}
	}
	output, err := json.Marshal(product)
	if err != nil || len(output) > lookupSelectionMaxBytes {
		return "", fmt.Errorf("lookup product exceeds byte budget")
	}
	return string(output), nil
}

// The registry can be composed with function/type/callee evaluators in one DB,
// allowing a parent query to depend on the selected lookup through Reader.Query.
func newLookupSelectionDatabase() *incremental.Database {
	return incremental.New(map[string]incremental.Evaluator{lookupSelectionQueryKind: evaluateLookupSelection, lookupAmbiguityQueryKind: evaluateLookupAmbiguity})
}
func readLookupSelection(db *incremental.Database, key incremental.Key) (lookupSelectionProduct, error) {
	if key.Kind != lookupSelectionQueryKind {
		return lookupSelectionProduct{}, fmt.Errorf("not a name-selection query")
	}
	encoded, err := db.Evaluate(context.Background(), key)
	if err != nil {
		return lookupSelectionProduct{}, err
	}
	var product lookupSelectionProduct
	if err := decodeLookupSelection(encoded, &product); err != nil {
		return lookupSelectionProduct{}, err
	}
	return product, nil
}

// Prepare inputs from the CURRENT declaration metadata without analyzing bodies.
// The caller commits these with the rest of its coherent input revision before
// requesting parents. Type/callee inputs are not published by this helper.
func (a *Analyzer) currentLookupSelectionSpec(kind, name, site string) lookupSelectionSpec {
	namespace := a.currentNamespace
	function := ""
	if a.currentFuncType != nil {
		function = a.currentFuncType.Name
		if namespace == "" {
			if i := strings.LastIndex(function, "."); i >= 0 {
				namespace = function[:i]
			}
		}
	}
	origin := ""
	if a.file != nil {
		origin = a.file.Filename
	}
	// Effective namespace contains the function-name fallback used by visibility.
	// Individual owner labels do not affect global selection and are not keys.
	return lookupSelectionSpec{Kind: kind, Request: name, Site: site, Context: lookupDependencyContext{Namespace: namespace, Origin: origin, Phase: "selection", Usings: a.currentUsings}}
}

func (a *Analyzer) lookupSelectionInputs(kind, name, site string) (incremental.Key, []incremental.Change, error) {
	spec := a.currentLookupSelectionSpec(kind, name, site)
	if _, err := lookupSelectionKey(spec); err != nil {
		return incremental.Key{}, nil, err
	}
	var names []string
	shadowed := false
	if kind == "enclosing" {
		_, shadowed = a.lookupLocalSymbol(name)
		names = nil
		if spec.Context.Namespace != "" {
			names = []string{joinQualifiedName(spec.Context.Namespace, name)}
		}
	} else {
		if err := a.preflightLookupSelectionCandidates(spec); err != nil {
			return incremental.Key{}, nil, err
		}
		names = a.visibleNameCandidates(name)
	}
	if len(names) > lookupTraceMaxCandidates {
		return incremental.Key{}, nil, fmt.Errorf("lookup candidate budget exceeded")
	}
	reads := make([]lookupCandidateRead, 0, len(names))
	for _, candidate := range names {
		present, visible := false, false
		switch kind {
		case "value", "enclosing":
			symbol, found := a.globalScope.Lookup(candidate)
			present = found
			visible = present && (symbol == nil || a.globalNameIsVisible(symbol, candidate))
			if kind == "enclosing" && symbol == nil {
				visible = false
			}
		case "type":
			_, present = a.namedTypes[candidate]
			visible = present && a.typeNameIsVisible(candidate)
		case "permission":
			_, present = a.permissions[candidate]
			visible = present
		}
		reads = append(reads, lookupCandidateRead{candidate, present, visible})
	}
	return lookupSelectionChanges(spec, reads, shadowed)
}

// Bound potential generated names before the ordinary candidate generator can
// allocate a long request once for every import/namespace prefix. Counting
// duplicates conservatively can decline a query but cannot change resolution.
func (a *Analyzer) preflightLookupSelectionCandidates(spec lookupSelectionSpec) error {
	remaining, count := lookupSelectionMaxBytes/6, 0
	reserve := func(left, right string, dot bool) bool {
		count++
		size := len(left) + len(right)
		if dot && left != "" && right != "" {
			size++
		}
		if count > lookupTraceMaxCandidates || size > remaining {
			return false
		}
		remaining -= size
		return true
	}
	fail := func() error { return fmt.Errorf("generated lookup candidates exceed budget") }
	name := spec.Request
	for lexical := spec.Context.Namespace; lexical != ""; {
		if !reserve(lexical, name, true) {
			return fail()
		}
		if i := strings.LastIndex(lexical, "."); i >= 0 {
			lexical = lexical[:i]
		} else {
			break
		}
	}
	if !strings.Contains(name, ".") {
		for _, using := range spec.Context.Usings {
			if !reserve(using, name, true) {
				return fail()
			}
		}
		if target, ok := a.importAliases[name]; ok {
			if !reserve(target, "", false) {
				return fail()
			}
		}
	} else if i := strings.Index(name, "."); i > 0 {
		if canon, ok := a.moduleAliases[name[:i]]; ok {
			if !reserve(canon, name[i:], false) {
				return fail()
			}
		}
	}
	if !reserve(name, "", false) {
		return fail()
	}
	return nil
}
