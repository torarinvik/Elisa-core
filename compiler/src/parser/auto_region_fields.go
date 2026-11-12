package parser

import (
	"elisacore/src/parserregionscan"
	"reflect"
)

func autoRegionChildFields(t reflect.Type) []int { return parserregionscan.ChildFields(t) }
