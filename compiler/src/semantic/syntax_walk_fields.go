package semantic

import (
	"reflect"

	"elisacore/src/lexer"
	"elisacore/src/syntaxplan"
)

var syntaxWalkPositionType = reflect.TypeOf(lexer.Pos{})

// syntaxWalkFields preserves the analyzer's traversal policy while sharing its
// immutable reflection plans with other syntax walkers.
func syntaxWalkFields(typ reflect.Type) []int { return syntaxplan.Fields(typ) }
