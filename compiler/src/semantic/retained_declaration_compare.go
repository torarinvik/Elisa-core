package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"os"
	"reflect"
)

type retainedSplice struct{ live, edit *ast.FuncDecl }
type retainedComparison struct {
	splices                             []retainedSplice
	shadowEdits                         [][3]*ast.FuncDecl
	movedSplices, shifted, shiftedOther []ast.Decl
	pairedNew, pairedOld                map[*ast.FuncDecl]*ast.FuncDecl
	replaced                            map[string]bool
	structPairs                         [][2]*ast.StructDecl
	constPairs                          [][2]*ast.ConstDecl
	constEnumPairs                      [][2]*ast.ConstEnumDecl
	enumPairs, hierarchyPairs           [][2]*ast.EnumDecl
	newTexts                            map[*ast.FuncDecl]funcComparisonText
	lineDelta                           map[string]int
	marks                               map[string][][2]int
	oldSpans, newSpans                  []string
	removed, added                      []*ast.FuncDecl
}

// compareRetainedDeclarations classifies edits without mutating the retained analysis.
func (r *RetainedAnalysis) compareRetainedDeclarations(oldDecls, newDecls []ast.Decl, src []byte, paired retainedDeclarationPairing, names retainedDeclarationNameIndex, topLevel, oldTop map[ast.Decl]bool, memberOf map[ast.Decl]*ast.NamespaceDecl, namespaceOf map[*ast.NamespaceDecl]string, liveOther map[declKey][]ast.Decl, liveFns map[fnKey][]*ast.FuncDecl, scoped map[*ast.FuncDecl]scopedDecl) (retainedComparison, error) {
	removed, pairs, added := paired.removed, paired.pairs, paired.added
	insertedBefore, overloadNames := paired.insertedBefore, paired.overloadNames
	oldFunctionNames, newFunctionNames := names.oldFunctionNames, names.newFunctionNames
	newFuncNameCount := names.newFuncNameCount
	var splices []retainedSplice
	var shadowEdits [][3]*ast.FuncDecl             // live, old parse, new parse of each edited body (fast-prepare shadow)
	var movedSplices []ast.Decl                    // edited functions that also moved: header shifts, body is replaced
	pairedNew := map[*ast.FuncDecl]*ast.FuncDecl{} // live function -> its declaration in file
	pairedOld := map[*ast.FuncDecl]*ast.FuncDecl{}
	replaced := map[string]bool{} // fingerprint keys of functions whose signature changed
	var shifted []ast.Decl
	var shiftedOther []ast.Decl
	var structPairs [][2]*ast.StructDecl // edited plain structs: (previous, edited)
	var constPairs [][2]*ast.ConstDecl   // typed consts with a new value: (previous, edited)
	var constEnumPairs [][2]*ast.ConstEnumDecl
	var enumPairs [][2]*ast.EnumDecl      // edited plain enums: (previous, edited)
	var hierarchyPairs [][2]*ast.EnumDecl // edited refinements of an enum hierarchy
	newTexts := map[*ast.FuncDecl]funcComparisonText{}
	lineDelta := map[string]int{}
	marks := map[string][][2]int{} // per file: (old line, delta from that line on)
	oldSpans := retainedSpans(oldDecls, r.src)
	newSpans := retainedSpans(newDecls, src)
	for k, pair := range pairs {
		i, j := pair[0], pair[1]
		od, nd := oldDecls[i], newDecls[j]
		op, np := od.Pos(), nd.Pos()
		if op.File != np.File || op.Col != np.Col {
			return retainedComparison{}, fmt.Errorf("declaration %d moved", i)
		}
		// Any line movement must come from a declaration whose text changed; the
		// span compare below makes that declaration a splice. A span ends at the next
		// `#line` directive, so the text above a file region's first declaration is in no
		// span and holds no declaration: a movement there (comments, blank lines) is a
		// plain shift.
		if d := np.Line - op.Line; d != lineDelta[op.File] {
			if !insertedBefore[k] && k > 0 && oldSpans[i-1] == newSpans[pairs[k-1][1]] && oldDecls[i-1].Pos().File == op.File {
				return retainedComparison{}, fmt.Errorf("lines moved after an unchanged declaration at %s", retainedLineKey(op))
			}
			lineDelta[op.File] = d
			marks[op.File] = append(marks[op.File], [2]int{op.Line, d})
		}
		sameText := oldSpans[i] == newSpans[j]
		ofn, isFn := od.(*ast.FuncDecl)
		if !isFn {
			// A container's span is its own header up to its first member. Comment and blank
			// lines trailing a declaration move with an insertion after it; they are not part of
			// what the declaration declares.
			if sameText || retainedTrimTrailing(oldSpans[i]) == retainedTrimTrailing(newSpans[j]) {
				if lineDelta[op.File] != 0 {
					candidates := liveOther[declKey{retainedLineKey(op), reflect.TypeOf(od)}]
					if len(candidates) != 1 {
						return retainedComparison{}, fmt.Errorf("%d analyzed candidates for %T at %s", len(candidates), od, retainedLineKey(op))
					}
					shiftedOther = append(shiftedOther, candidates[0])
				}
				continue
			}
			if !sameText {
				if osd, ok := od.(*ast.StructDecl); ok {
					if nsd, ok := nd.(*ast.StructDecl); ok && osd.Name == nsd.Name && retainedStructFieldEditAllowed(osd, nsd) {
						structPairs = append(structPairs, [2]*ast.StructDecl{osd, nsd})
						continue
					}
				}
				if oed, ok := od.(*ast.EnumDecl); ok {
					if ned, ok := nd.(*ast.EnumDecl); ok && oed.Name == ned.Name && retainedPlainEnum(oed) && retainedPlainEnum(ned) {
						enumPairs = append(enumPairs, [2]*ast.EnumDecl{oed, ned})
						continue
					}
					if ned, ok := nd.(*ast.EnumDecl); ok && oed.Name == ned.Name && oed.Parent == ned.Parent &&
						retainedRefinementEnum(oed) && retainedRefinementEnum(ned) {
						hierarchyPairs = append(hierarchyPairs, [2]*ast.EnumDecl{oed, ned})
						continue
					}
				}
				if oe, ok := od.(*ast.ConstEnumDecl); ok {
					if ne, ok := nd.(*ast.ConstEnumDecl); ok && retainedConstEnumEditAllowed(oe, ne, oldSpans[i], newSpans[j]) {
						constEnumPairs = append(constEnumPairs, [2]*ast.ConstEnumDecl{oe, ne})
						continue
					}
				}
				if ocd, ok := od.(*ast.ConstDecl); ok {
					if ncd, ok := nd.(*ast.ConstDecl); ok && retainedConstValueEdit(ocd, ncd, oldSpans[i], newSpans[j]) {
						constPairs = append(constPairs, [2]*ast.ConstDecl{ocd, ncd})
						continue
					}
				}
				if os.Getenv("ELISACORE_RETAINED_DEBUG_SPANS") != "" {
					fmt.Fprintf(os.Stderr, "[span-old %T]\n%q\n[span-new]\n%q\n", od, oldSpans[i], newSpans[j])
				}
				return retainedComparison{}, fmt.Errorf("declaration at %s changed", retainedLineKey(op))
			}
			continue
		}
		nfn, ok := nd.(*ast.FuncDecl)
		if !ok {
			return retainedComparison{}, fmt.Errorf("declaration kind changed at %s", retainedLineKey(op))
		}
		cands := liveFns[fnKey{retainedLineKey(op), ofn.Name}]
		if len(cands) != 1 {
			return retainedComparison{}, fmt.Errorf("%d analyzed candidates for %s at %s", len(cands), ofn.Name, retainedLineKey(op))
		}
		live := cands[0]
		pairedNew[live] = nfn
		pairedOld[live] = ofn
		forceReplace := overloadNames[oldFunctionNames[ofn]] || overloadNames[newFunctionNames[nfn]]
		if sameText && !forceReplace {
			if lineDelta[op.File] != 0 {
				shifted = append(shifted, live)
			}
			continue
		}
		oldText, newText := r.comparisonText(ofn), retainedText(nfn)
		if oldText.header != newText.header || forceReplace {
			// A new signature is a removal of the old declaration and an addition
			// of the new one: callers are re-analyzed, the new one is checked
			// against scratch.
			// A member of a `module`/`extend` block is replaced in its block the same way.
			sameScope := oldTop[ofn] && topLevel[nfn]
			namespace := ""
			if block := memberOf[nfn]; !sameScope && block != nil && !oldTop[ofn] && !topLevel[nfn] {
				if sd, known := scoped[live]; known && sd.Namespace == namespaceOf[block] {
					sameScope, namespace = true, sd.Namespace
				}
			}
			genericRename := retainedGenericFunctionBinderRename(ofn, nfn)
			if !sameScope || (!(retainedPlainFunction(ofn) && retainedPlainFunction(nfn)) && !genericRename) {
				return retainedComparison{}, fmt.Errorf("signature of %s changed", ofn.Name)
			}
			if genericRename && retainedGenericFunctionHasNonBodyUsers(oldDecls, ofn) {
				return retainedComparison{}, fmt.Errorf("generic function %s has non-body consumers", ofn.Name)
			}
			removed = append(removed, ofn)
			added = append(added, nfn)
			replaced[namespace+"::"+ofn.Name+"#0"] = true
			if forceReplace {
				// Every occurrence of an overloaded name is re-declared.
				for k := 1; k < newFuncNameCount[newFunctionNames[nfn]]; k++ {
					replaced[fmt.Sprintf("%s::%s#%d", namespace, ofn.Name, k)] = true
				}
			}
			continue
		}
		if oldText.gensyms != newText.gensyms {
			return retainedComparison{}, fmt.Errorf("%s mentions renamed synthesized declarations", ofn.Name)
		}
		if _, known := scoped[live]; !known {
			return retainedComparison{}, fmt.Errorf("%s is not a top-level analyzed function", ofn.Name)
		}
		if np.Line != op.Line {
			movedSplices = append(movedSplices, live)
		}
		splices = append(splices, retainedSplice{live, nfn})
		shadowEdits = append(shadowEdits, [3]*ast.FuncDecl{live, ofn, nfn})
		newTexts[nfn] = newText
	}
	return retainedComparison{splices, shadowEdits, movedSplices, shifted, shiftedOther, pairedNew, pairedOld, replaced, structPairs, constPairs, constEnumPairs, enumPairs, hierarchyPairs, newTexts, lineDelta, marks, oldSpans, newSpans, removed, added}, nil
}
