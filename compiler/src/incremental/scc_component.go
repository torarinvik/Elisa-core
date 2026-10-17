package incremental

import (
	"encoding/json"
	"fmt"
)

const SCCComponentQueryKind = "dependency-scc-component-v1"
const sccComponentMaxBytes = 64 << 10

// SCCComponent names a recursive group and its direct dependency groups without
// graph-wide numeric IDs. It describes topology, not semantic summary validity.
type SCCComponent struct {
	Members      []string
	Dependencies [][]string
}

// SCCComponentPublication admits a node projection alongside SCCPublication.
// A fixed reservation preserves memo identity across unrelated inventory edits.
// Products larger than 64 KiB decline; callers must fall back to fresh planning.
func SCCComponentPublication(graph, node string) (Publication, error) {
	if !validSCCIdentity(graph) || !validSCCIdentity(node) {
		return Publication{}, fmt.Errorf("invalid SCC projection identity")
	}
	encoded, _ := json.Marshal([2]string{graph, node})
	return Publication{Key: Key{Kind: SCCComponentQueryKind, Name: string(encoded)}, MaxDependencies: 1, MaxResultBytes: sccComponentMaxBytes}, nil
}

func evaluateSCCComponent(r *Reader, name string) (string, error) {
	var identity [2]string
	if len(name) > 12*(64<<10)+32 || json.Unmarshal([]byte(name), &identity) != nil || !validSCCIdentity(identity[0]) || !validSCCIdentity(identity[1]) {
		return "", fmt.Errorf("invalid SCC projection key")
	}
	canonical, _ := json.Marshal(identity)
	if string(canonical) != name {
		return "", fmt.Errorf("noncanonical SCC projection key")
	}
	encoded, err := r.Query(Key{Kind: SCCQueryKind, Name: identity[0]})
	if err != nil {
		return "", err
	}
	if len(encoded) > sccMaxBytes {
		return "", fmt.Errorf("SCC plan exceeds budget")
	}
	var plan SCCPlan
	if err = json.Unmarshal([]byte(encoded), &plan); err != nil {
		return "", err
	}
	for i, members := range plan.Components {
		for _, member := range members {
			if member != identity[1] {
				continue
			}
			if i >= len(plan.Dependencies) {
				return "", fmt.Errorf("invalid SCC dependencies")
			}
			result := SCCComponent{Members: members, Dependencies: [][]string{}}
			for _, dep := range plan.Dependencies[i] {
				if dep < 0 || dep >= len(plan.Components) {
					return "", fmt.Errorf("invalid SCC dependency")
				}
				result.Dependencies = append(result.Dependencies, plan.Components[dep])
			}
			output, err := json.Marshal(result)
			if err != nil || len(output) > sccComponentMaxBytes {
				return "", fmt.Errorf("SCC component exceeds budget")
			}
			return string(output), nil
		}
	}
	return "", fmt.Errorf("SCC node unavailable")
}
