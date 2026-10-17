package semantic

import (
	"elisacore/src/incremental"
	"encoding/json"
	"fmt"
	"strconv"
	"unicode/utf8"
)

const lexicalSelectionKind = "lexical-selection-v1"
const lexicalMaxFrames = 256

type lexicalSelectionSpec struct {
	Site               FunctionSourceAnchor
	Role, Domain, Name string
}
type lexicalCell struct {
	Binding string
	Nil     bool
}
type lexicalSelectionProduct struct {
	Binding             string
	Present, Found, Nil bool
}
type lexicalFrameValue struct {
	Present, Nil bool
	Binding      string
}

// Selection identifies the current binding only. Resolved types, mutable flow
// facts and live analyzer contributions remain separate dependency domains.
func lexicalSelectionKey(spec lexicalSelectionSpec) (incremental.Key, error) {
	if spec.Site.FunctionKey == "" || spec.Site.Tree == ([32]byte{}) || spec.Site.Kind == "" || spec.Role == "" || spec.Name == "" {
		return incremental.Key{}, fmt.Errorf("lexical lookup requires anchored owner/site/role")
	}
	switch spec.Domain {
	case "symbol", "local-symbol", "type-param", "const-param", "shape-param", "assoc-type", "param-interface", "permission-param", "error-set-param", "region-param":
	default:
		return incremental.Key{}, fmt.Errorf("unsupported lexical lookup domain")
	}
	bytes := 0
	for _, s := range []string{spec.Site.FunctionKey, spec.Site.Kind, spec.Role, spec.Domain, spec.Name} {
		if !utf8.ValidString(s) || len(s) > lookupSelectionMaxBytes/6-bytes {
			return incremental.Key{}, fmt.Errorf("lexical key budget exceeded")
		}
		bytes += len(s)
	}
	data, err := json.Marshal(spec)
	if err != nil || len(data) > lookupSelectionMaxBytes {
		return incremental.Key{}, fmt.Errorf("invalid lexical key")
	}
	return incremental.Key{Kind: lexicalSelectionKind, Name: string(data)}, nil
}
func lexicalRecipeInput(key incremental.Key) incremental.Key {
	return incremental.Key{Kind: "lexical-recipe-v1", Name: key.Name}
}
func lexicalCellInput(key incremental.Key, frame int) incremental.Key {
	return incremental.Key{Kind: "lexical-cell-v1", Name: key.Name + "\x00" + strconv.Itoa(frame)}
}
func lexicalNilFound(domain string) bool { return domain != "param-interface" }
func evaluateLexicalSelection(r *incremental.Reader, name string) (string, error) {
	var spec lexicalSelectionSpec
	if err := decodeLookupSelection(name, &spec); err != nil {
		return "", err
	}
	key, err := lexicalSelectionKey(spec)
	if err != nil || key.Name != name {
		return "", fmt.Errorf("noncanonical lexical lookup")
	}
	encoded, present := r.Input(lexicalRecipeInput(key))
	if !present {
		return "", fmt.Errorf("unpublished lexical chain")
	}
	count, err := strconv.Atoi(encoded)
	if err != nil || count < 0 || count > lexicalMaxFrames || strconv.Itoa(count) != encoded {
		return "", fmt.Errorf("invalid lexical chain")
	}
	product := lexicalSelectionProduct{}
	for frame := 0; frame < count; frame++ {
		encoded, present := r.Input(lexicalCellInput(key, frame))
		if !present {
			continue
		}
		var cell lexicalCell
		if err := decodeLookupSelection(encoded, &cell); err != nil {
			return "", err
		}
		if (spec.Domain == "permission-param" || spec.Domain == "error-set-param" || spec.Domain == "region-param") && cell.Nil {
			return "", fmt.Errorf("invalid nil boolean binder")
		}
		canonical, _ := json.Marshal(cell)
		if string(canonical) != encoded || cell.Nil && cell.Binding != "" || !cell.Nil && cell.Binding == "" {
			return "", fmt.Errorf("invalid lexical cell")
		}
		product = lexicalSelectionProduct{Binding: cell.Binding, Present: true, Found: !cell.Nil || lexicalNilFound(spec.Domain), Nil: cell.Nil}
		break
	}
	data, err := json.Marshal(product)
	if err != nil || len(data) > lookupSelectionMaxBytes {
		return "", fmt.Errorf("lexical result budget exceeded")
	}
	return string(data), nil
}
func lexicalPublication(spec lexicalSelectionSpec, frames []lexicalFrameValue) (incremental.Publication, error) {
	key, err := lexicalSelectionKey(spec)
	if err != nil {
		return incremental.Publication{}, err
	}
	if len(frames) > lexicalMaxFrames {
		return incremental.Publication{}, fmt.Errorf("lexical frame budget exceeded")
	}
	inputs := []incremental.Change{{Key: lexicalRecipeInput(key), Value: strconv.Itoa(len(frames))}}
	resultBytes, totalBytes := 64, 0
	for i, frame := range frames {
		change := incremental.Change{Key: lexicalCellInput(key, i), Remove: !frame.Present}
		if frame.Present {
			if frame.Nil && frame.Binding != "" || !frame.Nil && frame.Binding == "" || !utf8.ValidString(frame.Binding) || len(frame.Binding) > lookupSelectionMaxBytes/6-totalBytes {
				return incremental.Publication{}, fmt.Errorf("invalid lexical identity")
			}
			if (spec.Domain == "permission-param" || spec.Domain == "error-set-param" || spec.Domain == "region-param") && frame.Nil {
				return incremental.Publication{}, fmt.Errorf("boolean binders cannot be nil blockers")
			}
			totalBytes += len(frame.Binding)
			data, _ := json.Marshal(lexicalCell{frame.Binding, frame.Nil})
			change.Value = string(data)
			output, _ := json.Marshal(lexicalSelectionProduct{Binding: frame.Binding, Present: false, Found: false, Nil: false})
			resultBytes = max(resultBytes, len(output))
		}
		inputs = append(inputs, change)
	}
	if resultBytes > lookupSelectionMaxBytes {
		return incremental.Publication{}, fmt.Errorf("lexical product too large")
	}
	return incremental.Publication{Key: key, Inputs: inputs, MaxResultBytes: resultBytes, MaxDependencies: len(inputs)}, nil
}
func lexicalQueryEvaluators() map[string]incremental.Evaluator {
	return map[string]incremental.Evaluator{lexicalSelectionKind: evaluateLexicalSelection}
}
