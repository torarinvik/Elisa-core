package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestGenerateUsesRuntimePolicyAndDeterministicSchema(t *testing.T) {
	source := []byte(`package semantic
type Analyzer struct {
 z map[interface{}]interface{}
 a, b map[[0]int]*int
 resources map[string][]int
 unknown namedMap
 exprTypes map[int]int
 exprFacts map[int]int
 iterBinderElementStates map[int]int
 functionAnalyses map[int]int
}`)
	one, err := generate(source)
	if err != nil {
		t.Fatal(err)
	}
	two, err := generate(source)
	if err != nil || !bytes.Equal(one, two) {
		t.Fatal("generator output is not deterministic")
	}
	for _, name := range []string{"a", "b", "resources", "z"} {
		if !strings.Contains(string(one), "dst."+name+" = maps.Clone(src."+name+")") {
			t.Fatalf("direct map %s omitted", name)
		}
	}
	for _, name := range []string{"unknown", "exprTypes", "exprFacts", "iterBinderElementStates", "functionAnalyses"} {
		if strings.Contains(string(one), "dst."+name) {
			t.Fatalf("special/unknown field %s optimized", name)
		}
	}
	if !strings.Contains(string(one), "field.Kind() != reflect.Map || typeHoldsMapInline(field.Type().Elem())") {
		t.Fatal("exact existing resource predicate no longer guards typed copies")
	}
}

func TestGenerateDeclinesUnknownSchema(t *testing.T) {
	for _, source := range []string{"package semantic", "package semantic; type Analyzer int"} {
		if _, err := generate([]byte(source)); err == nil {
			t.Fatal("unknown Analyzer schema accepted")
		}
	}
}
