package semantic

import (
	"fmt"
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Capture exact independent-source witnesses BEFORE any retained mutation. A
// struct edit can install authored syntax in the live graph and then lower it;
// walking that graph at the final callback would change source authority.
// Fixed-size slabs avoid geometric recopying of the complete position stream.
// They impose no bound on the number of witnesses or admitted source shapes.
type proofReportCoordinateWitnesses struct{ slabs [][]lexer.Pos }

const proofReportCoordinateWitnessSlabSize = 1024

func captureProofReportCoordinateWitnesses(file *ast.File) proofReportCoordinateWitnesses {
	witnesses := proofReportCoordinateWitnesses{}
	var previous lexer.Pos
	havePrevious := false
	addPosition := func(p lexer.Pos) {
		if p.IsZero() {
			return
		}
		// node.Pos() and its Position field commonly emit the same tuple
		// consecutively. Replaying an identical tuple is idempotent for full,
		// start-only and endpoint ambiguity indexes. Keep every differing
		// witness (including same printed span with conflicting offsets).
		if havePrevious && p == previous {
			return
		}
		previous, havePrevious = p, true
		if len(witnesses.slabs) == 0 || len(witnesses.slabs[len(witnesses.slabs)-1]) == proofReportCoordinateWitnessSlabSize {
			witnesses.slabs = append(witnesses.slabs, make([]lexer.Pos, 0, proofReportCoordinateWitnessSlabSize))
		}
		last := len(witnesses.slabs) - 1
		witnesses.slabs[last] = append(witnesses.slabs[last], p)
	}
	seen := map[uintptr]bool{}
	var visit func(reflect.Value)
	visit = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		if v.Type() == retainedPosType {
			// Some producers intentionally use only a token start. Preserve that
			// contract; never manufacture an end span during rendering.
			addPosition(v.Interface().(lexer.Pos))
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				visit(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if v.CanInterface() {
				if node, ok := v.Interface().(ast.Node); ok {
					addPosition(node.Pos())
				}
			}
			visit(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				visit(v.Field(i))
			}
		case reflect.Slice:
			if v.Type().Elem().Kind() == reflect.Uint8 {
				return
			}
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		}
	}
	visit(reflect.ValueOf(file))
	return witnesses
}

type proofReportCoordinateRequest struct {
	Pos   lexer.Pos
	Shift bool
}

// Collect the COMPLETE final journal before building any queried-coordinate
// index. A newly emitted fact is a request too; old rows alone are insufficient.
// Use a set because many facts share the same source position and shift policy.
// Update serializes analysis and relocation; this collection and its consumers
// run synchronously after every proof producer has finished.
func collectProofReportCoordinateRequests(current, old map[proofContributionKey]proofContribution) map[proofReportCoordinateRequest]struct{} {
	requests := make(map[proofReportCoordinateRequest]struct{})
	for key, row := range current {
		baseline, exists := old[key]
		unchanged := exists && len(baseline.Facts) != 0 && len(baseline.Facts) == len(row.Facts) && &baseline.Facts[0] == &row.Facts[0]
		for _, fact := range row.Facts {
			requests[proofReportCoordinateRequest{fact.Pos, unchanged}] = struct{}{}
		}
	}
	return requests
}

// Query indexes contain only keys demanded by complete final contributions.
// This renderer consumes immutable witnesses, never a potentially lowered AST.
func (witnesses proofReportCoordinateWitnesses) requestedRenderer(marks map[string][][2]int, requests map[proofReportCoordinateRequest]struct{}) func(lexer.Pos, bool) (lexer.Pos, string) {
	type printedSpan struct {
		File                       string
		Line, Col, EndLine, EndCol int
	}
	printed := func(p lexer.Pos) printedSpan { return printedSpan{p.File, p.Line, p.Col, p.EndLine, p.EndCol} }
	fresh := make(map[printedSpan]lexer.Pos, len(requests)*2)
	ambiguous := make(map[printedSpan]bool)
	type endpoint struct {
		File      string
		Line, Col int
	}
	wantedSpans := make(map[printedSpan]bool, len(requests)*2)
	wantedBoundaries := make(map[endpoint]bool, len(requests)*2)
	for request := range requests {
		p := request.Pos
		if p.IsZero() {
			continue
		}
		if request.Shift {
			start := retainedShiftPos(lexer.Pos{File: p.File, Line: p.Line, Col: p.Col, Offset: p.Offset}, marks)
			if p.EndLine != 0 {
				end := retainedShiftPos(lexer.Pos{File: p.File, Line: p.EndLine, Col: p.EndCol, Offset: p.EndOffset}, marks)
				p.EndLine = end.Line
			}
			p.Line = start.Line
		}
		wantedSpans[printed(p)] = true
		wantedBoundaries[endpoint{p.File, p.Line, p.Col}] = true
		if p.EndLine != 0 || p.EndCol != 0 || p.EndOffset != 0 {
			wantedBoundaries[endpoint{p.File, p.EndLine, p.EndCol}] = true
		}
	}
	boundaries := make(map[endpoint]int, len(requests)*2)
	ambiguousBoundary := make(map[endpoint]bool)
	addBoundary := func(key endpoint, offset int) {
		if !wantedBoundaries[key] {
			return
		}
		if previous, present := boundaries[key]; present && previous != offset {
			ambiguousBoundary[key] = true
		}
		boundaries[key] = offset
	}
	add := func(p lexer.Pos) {
		key := printed(p)
		if !wantedSpans[key] {
			return
		}
		if previous, exists := fresh[key]; exists && previous != p {
			ambiguous[key] = true
		}
		fresh[key] = p
	}
	addPosition := func(p lexer.Pos) {
		if p.IsZero() {
			return
		}
		add(p)
		addBoundary(endpoint{p.File, p.Line, p.Col}, p.Offset)
		if p.EndLine != 0 && p.EndCol != 0 {
			addBoundary(endpoint{p.File, p.EndLine, p.EndCol}, p.EndOffset)
		}
		p.EndLine, p.EndCol, p.EndOffset = 0, 0, 0
		add(p)
	}
	// Replay EVERY authored witness; filtering affects storage only. Full-span,
	// start-only and independent end-boundary contributions remain identical.
	for _, slab := range witnesses.slabs {
		for _, position := range slab {
			addPosition(position)
		}
	}

	return func(p lexer.Pos, shift bool) (lexer.Pos, string) {
		if _, collected := requests[proofReportCoordinateRequest{p, shift}]; !collected {
			return p, fmt.Sprintf("proof report coordinate request was not collected: %s", p)
		}
		if p.IsZero() {
			return p, ""
		}
		if shift {
			start := retainedShiftPos(lexer.Pos{File: p.File, Line: p.Line, Col: p.Col, Offset: p.Offset}, marks)
			if p.EndLine != 0 {
				end := retainedShiftPos(lexer.Pos{File: p.File, Line: p.EndLine, Col: p.EndCol, Offset: p.EndOffset}, marks)
				p.EndLine = end.Line
			}
			p.Line = start.Line
		}
		key := printed(p)
		if ambiguous[key] {
			return p, fmt.Sprintf("ambiguous edited syntax span %s", p)
		}
		if next, ok := fresh[key]; ok {
			return next, ""
		}
		// A producer may compose a source span from distinct syntax anchors.
		// Bind its endpoints independently, without assuming the combined span
		// itself is stored on a syntax node or estimating byte displacement.
		start := endpoint{p.File, p.Line, p.Col}
		offset, present := boundaries[start]
		if !present || ambiguousBoundary[start] {
			return p, fmt.Sprintf("no unique edited syntax start for %s", p)
		}
		if p.EndLine == 0 && p.EndCol == 0 && p.EndOffset == 0 {
			p.Offset = offset
			return p, ""
		}
		end := endpoint{p.File, p.EndLine, p.EndCol}
		endOffset, endPresent := boundaries[end]
		if !endPresent || ambiguousBoundary[end] || p.EndOffset == 0 {
			return p, fmt.Sprintf("no unique edited syntax end for %s", p)
		}
		p.Offset, p.EndOffset = offset, endOffset
		return p, ""
	}
}
