package incremental

import (
	"container/heap"
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"
)

const SCCQueryKind = "dependency-scc-plan-v1"
const sccMaxNodes = 4096
const sccMaxEdges = 65536
const sccMaxBytes = 16 << 20

// SCCPlan partitions all current nodes. Component IDs follow member ordering;
// Order is deterministic and dependency-first. An edge goes reader -> dependency.
// Node identities must come from the compiler's current source/declaration world.
// A topology plan alone cannot authorize reuse of semantic rows or function bodies.
type SCCPlan struct {
	Components   [][]string
	Dependencies [][]int
	Order        []int
}

func sccInventory(graph string) Key { return Key{Kind: "scc-node-inventory-v1", Name: graph} }
func sccEdges(graph, node string) Key {
	encoded, _ := json.Marshal([2]string{graph, node})
	return Key{Kind: "scc-node-edges-v1", Name: string(encoded)}
}
func validSCCIdentity(value string) bool {
	return value != "" && utf8.ValidString(value) && len(value) <= 64<<10
}
func decodeSCCNames(encoded string) ([]string, error) {
	if len(encoded) > sccMaxBytes || !utf8.ValidString(encoded) {
		return nil, fmt.Errorf("SCC input byte budget exceeded")
	}
	var names []string
	if err := json.Unmarshal([]byte(encoded), &names); err != nil {
		return nil, err
	}
	if names == nil || len(names) > sccMaxNodes {
		return nil, fmt.Errorf("invalid SCC node list")
	}
	for i, name := range names {
		if !validSCCIdentity(name) || (i > 0 && names[i-1] >= name) {
			return nil, fmt.Errorf("noncanonical SCC node identities")
		}
	}
	canonical, _ := json.Marshal(names)
	if string(canonical) != encoded {
		return nil, fmt.Errorf("noncanonical SCC input")
	}
	return names, nil
}

// SCCPublication owns complete current inventory and adjacency, including empty
// lists. Removed nodes/edges must disappear from the next coherent publication;
// a previous successful trace is not authority for a new graph.
func SCCPublication(graph string, adjacency map[string][]string) (Publication, error) {
	if !validSCCIdentity(graph) || len(adjacency) > sccMaxNodes {
		return Publication{}, fmt.Errorf("SCC identity/node budget exceeded")
	}
	// Bound expansion and key storage before Marshal/copy/sort of adjacency.
	rawEdges, preflight := 0, 128
	for node, targets := range adjacency {
		if !validSCCIdentity(node) || len(targets) > sccMaxEdges {
			return Publication{}, fmt.Errorf("invalid SCC source")
		}
		rawEdges += len(targets)
		if rawEdges > sccMaxEdges {
			return Publication{}, fmt.Errorf("SCC source edge budget exceeded")
		}
		cost := 256 + 6*len(graph) + 12*len(node)
		for _, target := range targets {
			if _, present := adjacency[target]; !present {
				return Publication{}, fmt.Errorf("SCC target absent from current inventory")
			}
			cost += 16 + 6*len(target)
		}
		if cost > sccMaxBytes-preflight {
			return Publication{}, fmt.Errorf("SCC source byte budget exceeded")
		}
		preflight += cost
	}
	names := make([]string, 0, len(adjacency))
	for node := range adjacency {
		if !validSCCIdentity(node) {
			return Publication{}, fmt.Errorf("invalid SCC node identity")
		}
		names = append(names, node)
	}
	sort.Strings(names)
	encoded, _ := json.Marshal(names)
	inputs := []Change{{Key: sccInventory(graph), Value: string(encoded)}}
	edges, bytes := 0, len(encoded)
	for _, node := range names {
		targets := append([]string{}, adjacency[node]...)
		sort.Strings(targets)
		targets = compactSCCNames(targets)
		for _, target := range targets {
			if _, present := adjacency[target]; !present {
				return Publication{}, fmt.Errorf("SCC target absent from current inventory")
			}
		}
		edges += len(targets)
		if edges > sccMaxEdges {
			return Publication{}, fmt.Errorf("SCC edge budget exceeded")
		}
		value, _ := json.Marshal(targets)
		bytes += len(value)
		if bytes > sccMaxBytes {
			return Publication{}, fmt.Errorf("SCC graph byte budget exceeded")
		}
		inputs = append(inputs, Change{Key: sccEdges(graph, node), Value: string(value)})
	}
	// Every node identity appears once in Components; integer edges/order and
	// JSON metadata have explicit conservative reservations.
	// Keep reservations stable across edge-only edits. Shrinking a bound on a
	// redundant edge removal would retire the query memo before equal-output
	// pruning could preserve its users.
	maximum := len(encoded) + 512*len(names) + 24*min(sccMaxEdges, len(names)*len(names)) + 128
	if maximum > sccMaxBytes {
		return Publication{}, fmt.Errorf("SCC output reservation budget exceeded")
	}
	return Publication{Key: Key{Kind: SCCQueryKind, Name: graph}, Inputs: inputs, MaxResultBytes: maximum, MaxDependencies: len(inputs)}, nil
}
func compactSCCNames(names []string) []string {
	out := names[:0]
	for _, name := range names {
		if len(out) == 0 || out[len(out)-1] != name {
			out = append(out, name)
		}
	}
	return out
}
func SCCEvaluators() map[string]Evaluator {
	return map[string]Evaluator{SCCQueryKind: evaluateSCC, SCCComponentQueryKind: evaluateSCCComponent}
}

type sccVisit struct{ node, next int }
type sccReady []int

func (q sccReady) Len() int           { return len(q) }
func (q sccReady) Less(i, j int) bool { return q[i] < q[j] }
func (q sccReady) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *sccReady) Push(value any)    { *q = append(*q, value.(int)) }
func (q *sccReady) Pop() any {
	old := *q
	n := len(old) - 1
	value := old[n]
	*q = old[:n]
	return value
}

func evaluateSCC(r *Reader, graph string) (string, error) {
	if !validSCCIdentity(graph) {
		return "", fmt.Errorf("invalid SCC graph identity")
	}
	encoded, present := r.Input(sccInventory(graph))
	if !present {
		return "", fmt.Errorf("SCC graph unavailable")
	}
	names, err := decodeSCCNames(encoded)
	if err != nil {
		return "", err
	}
	ids := map[string]int{}
	for i, name := range names {
		ids[name] = i
	}
	edges := make([][]int, len(names))
	reverse := make([][]int, len(names))
	total, bytes := 0, len(encoded)
	for i, name := range names {
		if err := r.context.Err(); err != nil {
			return "", err
		}
		value, present := r.Input(sccEdges(graph, name))
		if !present {
			return "", fmt.Errorf("SCC adjacency unavailable")
		}
		bytes += len(value)
		if bytes > sccMaxBytes {
			return "", fmt.Errorf("SCC graph byte budget exceeded")
		}
		targets, err := decodeSCCNames(value)
		if err != nil {
			return "", err
		}
		total += len(targets)
		if total > sccMaxEdges {
			return "", fmt.Errorf("SCC edge budget exceeded")
		}
		for _, target := range targets {
			j, present := ids[target]
			if !present {
				return "", fmt.Errorf("SCC target unavailable")
			}
			edges[i] = append(edges[i], j)
			reverse[j] = append(reverse[j], i)
		}
	}
	// Iterative Kosaraju avoids recursion on long dependency chains.
	visited := make([]bool, len(names))
	post := make([]int, 0, len(names))
	for root := range names {
		if visited[root] {
			continue
		}
		visited[root] = true
		stack := []sccVisit{{node: root}}
		for len(stack) > 0 {
			if err := r.context.Err(); err != nil {
				return "", err
			}
			last := &stack[len(stack)-1]
			if last.next < len(edges[last.node]) {
				child := edges[last.node][last.next]
				last.next++
				if !visited[child] {
					visited[child] = true
					stack = append(stack, sccVisit{node: child})
				}
			} else {
				post = append(post, last.node)
				stack = stack[:len(stack)-1]
			}
		}
	}
	clear(visited)
	components := [][]string{}
	for i := len(post) - 1; i >= 0; i-- {
		root := post[i]
		if visited[root] {
			continue
		}
		visited[root] = true
		stack := []int{root}
		members := []string{}
		for len(stack) > 0 {
			node := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			members = append(members, names[node])
			for _, child := range reverse[node] {
				if !visited[child] {
					visited[child] = true
					stack = append(stack, child)
				}
			}
		}
		sort.Strings(members)
		components = append(components, members)
	}
	sort.Slice(components, func(i, j int) bool { return components[i][0] < components[j][0] })
	componentOf := make([]int, len(names))
	for i, members := range components {
		for _, name := range members {
			componentOf[ids[name]] = i
		}
	}
	plan := SCCPlan{Components: components, Dependencies: make([][]int, len(components)), Order: []int{}}
	users := make([][]int, len(components))
	remaining := make([]int, len(components))
	for component, members := range components {
		deps := map[int]bool{}
		for _, name := range members {
			for _, target := range edges[ids[name]] {
				dependency := componentOf[target]
				if dependency != component {
					deps[dependency] = true
				}
			}
		}
		plan.Dependencies[component] = []int{}
		for dependency := range deps {
			plan.Dependencies[component] = append(plan.Dependencies[component], dependency)
			users[dependency] = append(users[dependency], component)
		}
		sort.Ints(plan.Dependencies[component])
		remaining[component] = len(deps)
	}
	ready := &sccReady{}
	heap.Init(ready)
	for i, count := range remaining {
		if count == 0 {
			heap.Push(ready, i)
		}
	}
	for ready.Len() > 0 {
		component := heap.Pop(ready).(int)
		plan.Order = append(plan.Order, component)
		for _, user := range users[component] {
			remaining[user]--
			if remaining[user] == 0 {
				heap.Push(ready, user)
			}
		}
	}
	if len(plan.Order) != len(components) {
		return "", fmt.Errorf("invalid SCC condensation cycle")
	}
	output, err := json.Marshal(plan)
	if err != nil || len(output) > sccMaxBytes {
		return "", fmt.Errorf("SCC product budget exceeded")
	}
	return string(output), nil
}
