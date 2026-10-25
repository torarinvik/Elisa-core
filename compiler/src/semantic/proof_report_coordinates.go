package semantic

import (
	"fmt"
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// prepareProofReportCoordinates builds a source-coordinate rendering adapter.
// Contribution ownership is already recorded by the producers; coordinates
// never select an owner. A rendered span must exist uniquely in the independently
// parsed edited syntax. This deliberately does not guess byte shifts from line
// counts, rewrite arbitrary strings, or copy any scratch semantic evidence.
func (r *RetainedAnalysis) prepareProofReportCoordinates(file *ast.File, marks map[string][][2]int) (func(*Analyzer) string, string) {
	old := make(map[proofContributionKey]proofContribution, len(r.a.proofLifecycle.Segments))
	for key, row := range r.a.proofLifecycle.Segments {
		old[key] = row
	}
	sameFacts := func(left, right []ProofFact) bool {
		return len(left) != 0 && len(left) == len(right) && &left[0] == &right[0]
	}
	witnesses := captureProofReportCoordinateWitnesses(file)
	renderBindings := r.prepareProofReportBindings(file, marks)

	return func(a *Analyzer) string {
		requests := collectProofReportCoordinateRequests(a.proofLifecycle.Segments, old)
		render := witnesses.requestedRenderer(marks, requests)
		replacements := make(map[proofContributionKey]proofContribution, len(a.proofLifecycle.Segments))
		for key, row := range a.proofLifecycle.Segments {
			baseline, exists := old[key]
			unchanged := exists && sameFacts(baseline.Facts, row.Facts)
			facts := make([]ProofFact, len(row.Facts))
			for i, fact := range row.Facts {
				next, why := render(fact.Pos, unchanged)
				if why != "" {
					return why
				}
				fact, why = renderBindings(row, fact)
				if why != "" {
					return why
				}
				fact.Pos = next
				facts[i] = fact
			}
			row.Facts = facts
			replacements[key] = row
		}
		// No row is changed until every retained and newly emitted span is bound.
		a.proofLifecycle.Segments = replacements
		return ""
	}, ""
}

// proofReportSyntaxRenderer renders only coordinates anchored by the independent
// syntax graph. It has no access to semantic proof authority or contributions.
func proofReportSyntaxRenderer(file *ast.File, marks map[string][][2]int) func(lexer.Pos, bool) (lexer.Pos, string) {
	type printedSpan struct {
		File                       string
		Line, Col, EndLine, EndCol int
	}
	printed := func(p lexer.Pos) printedSpan { return printedSpan{p.File, p.Line, p.Col, p.EndLine, p.EndCol} }
	fresh := map[printedSpan]lexer.Pos{}
	ambiguous := map[printedSpan]bool{}
	type endpoint struct {
		File      string
		Line, Col int
	}
	boundaries := map[endpoint]int{}
	ambiguousBoundary := map[endpoint]bool{}
	addBoundary := func(key endpoint, offset int) {
		if previous, present := boundaries[key]; present && previous != offset {
			ambiguousBoundary[key] = true
		}
		boundaries[key] = offset
	}
	add := func(p lexer.Pos) {
		key := printed(p)
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
	return func(p lexer.Pos, shift bool) (lexer.Pos, string) {
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
