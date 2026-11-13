package graphalgo

import (
	"reflect"
	"testing"
)

func TestWorkUnitsWaitsForEveryDependencyChunk(t *testing.T) {
	deps := [][]int{{1}, {0}, {0}, {2}}
	weights := []int{1, 2, 3, 4}
	units, unitOf, componentOf, components, unitDeps, unitWeights := WorkUnits(deps, weights, [][]int{{0, 1}, {2}, {3}}, 1)
	if !reflect.DeepEqual(units, [][]int{{0}, {1}, {2}, {3}}) || !reflect.DeepEqual(unitOf, []int{0, 1, 2, 3}) || !reflect.DeepEqual(componentOf, []int{0, 0, 1, 2}) || !reflect.DeepEqual(components[0], []int{0, 1}) || !reflect.DeepEqual(unitDeps[2], []int{0, 1}) || !reflect.DeepEqual(unitDeps[3], []int{2}) || !reflect.DeepEqual(unitWeights, []int{51, 52, 53, 54}) {
		t.Fatalf("unexpected schedule: units=%v unitOf=%v componentOf=%v components=%v deps=%v weights=%v", units, unitOf, componentOf, components, unitDeps, unitWeights)
	}
}
