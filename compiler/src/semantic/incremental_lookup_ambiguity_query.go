package semantic

import (
	"elisacore/src/incremental"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const lookupAmbiguityQueryKind = "resolved-name-ambiguity-v1"

// The ambiguity domain visits imported suffixes even when name selection stops
// at its first match. Its output is the sorted diagnostic match set, or nil.
func lookupAmbiguityKey(spec lookupSelectionSpec) (incremental.Key, error) {
	if spec.Kind != "value" {
		return incremental.Key{}, fmt.Errorf("ambiguity requires a value lookup")
	}
	key, err := lookupSelectionKey(spec)
	key.Kind = lookupAmbiguityQueryKind
	return key, err
}
func lookupAmbiguityNames(spec lookupSelectionSpec) (string, []string, error) {
	if strings.Contains(spec.Request, ".") || len(spec.Context.Usings) < 2 {
		return "", nil, nil
	}
	remaining := lookupSelectionMaxBytes / 6
	reserve := func(prefix string) bool {
		size := len(prefix) + len(spec.Request)
		if prefix != "" {
			size++
		}
		if size > remaining {
			return false
		}
		remaining -= size
		return true
	}
	override := ""
	if spec.Context.Namespace != "" {
		if !reserve(spec.Context.Namespace) {
			return "", nil, fmt.Errorf("ambiguity names exceed byte budget")
		}
		override = joinQualifiedName(spec.Context.Namespace, spec.Request)
	}
	var imports []string
	seen := map[string]bool{}
	for _, using := range spec.Context.Usings {
		if !seen[using] {
			if !reserve(using) {
				return "", nil, fmt.Errorf("ambiguity names exceed byte budget")
			}
			seen[using] = true
			imports = append(imports, joinQualifiedName(using, spec.Request))
		}
	}
	return override, imports, nil
}
func (a *Analyzer) lookupAmbiguityInputs(name string) (incremental.Key, []incremental.Change, error) {
	spec := a.currentLookupSelectionSpec("value", name, "")
	key, err := lookupAmbiguityKey(spec)
	if err != nil {
		return incremental.Key{}, nil, err
	}
	override, imports, err := lookupAmbiguityNames(spec)
	if err != nil {
		return incremental.Key{}, nil, err
	}
	changes := []incremental.Change{{Key: lookupRecipeInput(key), Value: "ready"}}
	// A present namespace member (even a nil binding) suppresses using ambiguity.
	// Imported nil bindings do not count as visible matches.
	names := append([]string(nil), imports...)
	if override != "" {
		names = append(names, override)
	}
	for _, candidate := range names {
		symbol, present := a.globalScope.Lookup(candidate)
		visible := present && symbol != nil && a.globalNameIsVisible(symbol, candidate)
		state := "hidden"
		if visible {
			state = "visible"
		}
		changes = append(changes, incremental.Change{Key: lookupBindingInput(key, candidate), Value: state, Remove: !present})
	}
	return key, changes, nil
}
func evaluateLookupAmbiguity(r *incremental.Reader, name string) (string, error) {
	var spec lookupSelectionSpec
	if err := decodeLookupSelection(name, &spec); err != nil {
		return "", err
	}
	key, err := lookupAmbiguityKey(spec)
	if err != nil || key.Name != name {
		return "", fmt.Errorf("noncanonical ambiguity query")
	}
	ready, present := r.Input(lookupRecipeInput(key))
	if !present || ready != "ready" {
		return "", fmt.Errorf("unpublished ambiguity environment")
	}
	read := func(candidate string) (bool, bool, error) {
		state, present := r.Input(lookupBindingInput(key, candidate))
		if present && state != "visible" && state != "hidden" {
			return false, false, fmt.Errorf("invalid ambiguity binding")
		}
		return present, state == "visible" && present, nil
	}
	override, imports, err := lookupAmbiguityNames(spec)
	if err != nil {
		return "", err
	}
	var matches []string
	if override != "" {
		present, _, err := read(override)
		if err != nil {
			return "", err
		}
		if present {
			return "null", nil
		}
	}
	for _, candidate := range imports {
		_, visible, err := read(candidate)
		if err != nil {
			return "", err
		}
		if visible {
			matches = append(matches, candidate)
		}
	}
	if len(matches) < 2 {
		matches = nil
	} else {
		sort.Strings(matches)
	}
	data, err := json.Marshal(matches)
	if err != nil || len(data) > lookupSelectionMaxBytes {
		return "", fmt.Errorf("ambiguity result exceeds byte budget")
	}
	return string(data), nil
}
