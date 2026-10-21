package semantic

import "testing"

func TestProgramViewFromLocalPackedMatchArgument(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "program_match_call.elisa", `global region source
enum Node layout(handle: u32) @source:
    Empty
    Name(sview @source)
    Pair(Node, darray[Node])
def node_name(node: Node) -> sview @source:
    match node:
        Node.Name(name):
            return name
        _:
            return ""
def consume(name: sview @source) -> bool:
    return name != ""
def caller(nodes: darray[Node]&) -> bool:
    can Abort.Panic:
        target: mutable Node = nodes[0]
        target <- nodes[0]
        predicate: Node = target
        return match predicate:
            Node.Pair(object, arguments):
                name: sview @source = node_name(object)
                consume(name) if arguments.count == 2 else false
            _:
                false
`)
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("program-lived match view rejected: %v", errors)
	}
}
