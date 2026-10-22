package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"reflect"
	"sort"
)

const (
	maxASTStorageWalkDepth = 2048
	maxASTStorageWalkWork  = 1_000_000
)

// firstASTStorageOverlap explains a generated snapshot decline. It runs only
// for a few declarations when the explicit parallel-decline diagnostic is on.
func firstASTStorageOverlap(decl ast.Decl) string {
	return firstASTStorageOverlapWithLimits(decl, maxASTStorageWalkDepth, maxASTStorageWalkWork)
}

// The limits are parameters so tests can exercise the incomplete-scan path
// without allocating an enormous AST graph.
func firstASTStorageOverlapWithLimits(decl ast.Decl, maxDepth, maxWork int) string {
	type identity struct {
		kind             reflect.Kind
		typ              reflect.Type
		pointer          uintptr
		length, capacity int
	}
	type storage struct {
		start, end uintptr
		path       string
	}
	seen := map[identity]bool{}
	var ranges []storage
	work := 0
	depthLimitReached := false
	workLimitReached := false
	var walk func(reflect.Value, string, int)
	walk = func(v reflect.Value, path string, depth int) {
		if !v.IsValid() {
			return
		}
		if depth > maxDepth {
			depthLimitReached = true
			return
		}
		if work >= maxWork {
			workLimitReached = true
			return
		}
		work++
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem(), path, depth+1)
			}
		case reflect.Pointer:
			if v.IsNil() || v.Type().Elem().PkgPath() != astGraphPkgPath {
				return
			}
			key := identity{kind: v.Kind(), typ: v.Type(), pointer: v.Pointer()}
			if seen[key] {
				return
			}
			seen[key] = true
			if size := v.Type().Elem().Size(); size != 0 {
				ranges = append(ranges, storage{key.pointer, key.pointer + size, path})
			}
			walk(v.Elem(), path, depth+1)
		case reflect.Struct:
			if v.Type().PkgPath() != astGraphPkgPath {
				return
			}
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i), path+"."+v.Type().Field(i).Name, depth+1)
			}
		case reflect.Slice:
			if v.IsNil() {
				return
			}
			key := identity{v.Kind(), v.Type(), v.Pointer(), v.Len(), v.Cap()}
			if seen[key] {
				return
			}
			seen[key] = true
			if size := v.Type().Elem().Size() * uintptr(v.Cap()); size != 0 {
				ranges = append(ranges, storage{key.pointer, key.pointer + size, path})
			}
			full := v.Slice(0, v.Cap())
			for i := 0; i < full.Len(); i++ {
				walk(full.Index(i), fmt.Sprintf("%s[%d]", path, i), depth+1)
			}
		case reflect.Map:
			if v.IsNil() {
				return
			}
			key := identity{kind: v.Kind(), typ: v.Type(), pointer: uintptr(v.UnsafePointer())}
			if seen[key] {
				return
			}
			seen[key] = true
			it := v.MapRange()
			for it.Next() {
				walk(it.Key(), path+"{key}", depth+1)
				walk(it.Value(), path+"{value}", depth+1)
			}
		}
	}
	walk(reflect.ValueOf(decl), fmt.Sprintf("%T", decl), 0)
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	for i := 1; i < len(ranges); i++ {
		if ranges[i].start < ranges[i-1].end {
			return ranges[i-1].path + " overlaps " + ranges[i].path
		}
	}
	if depthLimitReached && workLimitReached {
		return "AST storage walk incomplete: depth and work limits exceeded"
	}
	if depthLimitReached {
		return "AST storage walk incomplete: depth limit exceeded"
	}
	if workLimitReached {
		return "AST storage walk incomplete: work limit exceeded"
	}
	return "no overlapping AST node or slice storage found"
}
