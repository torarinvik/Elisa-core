package semantic

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
)

// RetainedAnalysis keeps a finished whole-program analysis alive so a later
// edit that only changes function bodies can be applied in place: the changed
// bodies are spliced into the analyzed AST and re-analyzed, then the
// whole-program passes run again. Everything else (types, symbols, unchanged
// bodies' facts and body-phase diagnostics) is reused.
//
// It only produces diagnostics. Callers that need code generation should run a
// fresh analysis once Update reports no errors, because spliced state was never
// validated against the backend.
type RetainedAnalysis struct {
	a        *Analyzer
	result   *Result
	decls    []scopedDecl
	post     func()
	pristine *ast.File                            // a parse of src, for comparison; see texts
	texts    map[*ast.FuncDecl]funcComparisonText // pristine functions whose bodies were spliced away
	src      []byte                               // the expanded source pristine was parsed from

	depth      int
	windows    map[*ast.FuncDecl][2]int // body-phase diagnostics emitted by each top-level analyzeFunc
	bodyEnd    int                      // len(diagnostics) when analyzeDecls returned
	lateFrames []returnBorrowLateFrame  // evaluateLateReturnBorrowFrames consumes them; keep a copy

	// Late return-borrow pass reuse (retained_return_borrow.go).
	rbStack     []*ast.FuncDecl
	rbDeps      map[*ast.FuncDecl]map[*ast.FuncDecl]bool // owner -> summaries it consulted
	rbSummaries map[*ast.FuncDecl]returnBorrowFlow       // final summaries of the last late pass
	frameDiags  map[*ast.FuncDecl][]Diagnostic           // each late frame's diagnostics
	rbInvalid   map[*ast.FuncDecl]bool                   // owners to recompute in this update
	rbUpdating  bool

	options AnalyzeOptions
	pre     *preBodyState // declaration-phase results the retained state was built from

	LastStats RetainedUpdateStats
}

// RetainedUpdateStats describes the last Update.
type RetainedUpdateStats struct {
	Changed                   int
	Shifted                   int
	Diff                      time.Duration
	Bodies                    time.Duration
	Total                     time.Duration
	Fallback                  string
	Pairing, Scratch, Compare time.Duration
	// Invalidated counts functions whose late return-borrow results were recomputed.
	Invalidated int
}

var (
	pendingRetainedMu sync.Mutex
	pendingRetained   *RetainedAnalysis
)

func takePendingRetained() *RetainedAnalysis {
	r := pendingRetained
	pendingRetained = nil
	return r
}

// AnalyzeRetained runs a full analysis of file and keeps it for Update.
// pristine must be a separate parse of the same source that analysis never
// touches. Calls are serialized; do not run other analyses concurrently.
func AnalyzeRetained(file, pristine *ast.File, src []byte, options AnalyzeOptions) (*Result, *RetainedAnalysis) {
	r := &RetainedAnalysis{options: options, pristine: pristine, src: src, windows: map[*ast.FuncDecl][2]int{}, rbDeps: map[*ast.FuncDecl]map[*ast.FuncDecl]bool{}, frameDiags: map[*ast.FuncDecl][]Diagnostic{}}
	pendingRetainedMu.Lock()
	pendingRetained = r
	result := AnalyzeWithOptions(file, options)
	pendingRetained = nil
	pendingRetainedMu.Unlock()
	if r.a == nil {
		return result, nil
	}
	r.result = result
	return result, r
}

func (r *RetainedAnalysis) capture(a *Analyzer, decls []scopedDecl, post func()) {
	r.a, r.decls, r.post = a, decls, post
}

// Update applies an edit given a fresh, unanalyzed parse of the edited source.
// Changed function bodies move from file into the retained AST, and file
// becomes the base the next edit is compared against, so the caller must not
// analyze file afterwards. A non-nil error names why the edit is not a
// body-only change; the retained state and file are then untouched, and the
// caller should start over with a full analysis.
func (r *RetainedAnalysis) Update(file, scratch *ast.File, src []byte) (*Result, error) {
	start := time.Now()
	r.LastStats = RetainedUpdateStats{}
	fail := func(format string, args ...any) (*Result, error) {
		r.LastStats.Fallback = fmt.Sprintf(format, args...)
		return nil, fmt.Errorf("%s", r.LastStats.Fallback)
	}
	a := r.a
	oldDecls, newDecls := retainedWalkDecls(r.pristine), retainedWalkDecls(file)
	if len(oldDecls) != len(newDecls) {
		return fail("declaration count changed (%d -> %d)", len(oldDecls), len(newDecls))
	}
	type fnKey struct {
		pos  string
		name string
	}
	// The analyzed AST also holds declarations the analyzer synthesized, so pair
	// its functions by source position and name rather than by index.
	liveFns := map[fnKey][]*ast.FuncDecl{}
	for _, d := range retainedWalkDecls(a.file) {
		if f, ok := d.(*ast.FuncDecl); ok {
			k := fnKey{retainedLineKey(f.Pos()), f.Name}
			liveFns[k] = append(liveFns[k], f)
		}
	}
	scoped := make(map[*ast.FuncDecl]scopedDecl, len(r.decls))
	for _, sd := range r.decls {
		if f, ok := sd.Decl.(*ast.FuncDecl); ok {
			scoped[f] = sd
		}
	}
	type splice struct {
		live *ast.FuncDecl
		edit *ast.FuncDecl // the function in file, found again in the prepared scratch parse
	}
	var splices []splice
	var shifted []ast.Decl
	newTexts := map[*ast.FuncDecl]funcComparisonText{}
	lineDelta := map[string]int{}
	marks := map[string][][2]int{} // per file: (old line, delta from that line on)
	oldSpans := retainedSpans(oldDecls, r.src)
	newSpans := retainedSpans(newDecls, src)
	for i := range oldDecls {
		od, nd := oldDecls[i], newDecls[i]
		op, np := od.Pos(), nd.Pos()
		if op.File != np.File || op.Col != np.Col {
			return fail("declaration %d moved", i)
		}
		// Any line movement must come from a declaration whose text changed; the
		// span compare below makes that declaration a splice.
		if d := np.Line - op.Line; d != lineDelta[op.File] {
			if i == 0 || oldSpans[i-1] == newSpans[i-1] || oldDecls[i-1].Pos().File != op.File {
				return fail("lines moved after an unchanged declaration at %s", retainedLineKey(op))
			}
			lineDelta[op.File] = d
			marks[op.File] = append(marks[op.File], [2]int{op.Line, d})
		}
		sameText := oldSpans[i] == newSpans[i]
		ofn, isFn := od.(*ast.FuncDecl)
		if !isFn {
			// A container's span is its own header up to its first member.
			if !sameText {
				return fail("declaration at %s changed", retainedLineKey(op))
			}
			continue
		}
		nfn, ok := nd.(*ast.FuncDecl)
		if !ok {
			return fail("declaration kind changed at %s", retainedLineKey(op))
		}
		cands := liveFns[fnKey{retainedLineKey(op), ofn.Name}]
		if len(cands) != 1 {
			return fail("%d analyzed candidates for %s at %s", len(cands), ofn.Name, retainedLineKey(op))
		}
		live := cands[0]
		if sameText {
			if lineDelta[op.File] != 0 {
				shifted = append(shifted, live)
			}
			continue
		}
		oldText, newText := r.comparisonText(ofn), retainedText(nfn)
		if oldText.header != newText.header {
			return fail("signature of %s changed", ofn.Name)
		}
		if oldText.gensyms != newText.gensyms {
			return fail("%s mentions renamed synthesized declarations", ofn.Name)
		}
		if _, known := scoped[live]; !known {
			return fail("%s is not a top-level analyzed function", ofn.Name)
		}
		splices = append(splices, splice{live, nfn})
		newTexts[nfn] = newText
	}
	// Bodies are analyzed in the form the declaration phase leaves them in, and
	// that phase's body-dependent results must be what the retained state holds.
	pairing := time.Since(start)
	pendingRetainedMu.Lock()
	scratchStart := time.Now()
	pre := scratchPreBody(scratch, r.options)
	r.LastStats.Scratch = time.Since(scratchStart)
	pendingRetainedMu.Unlock()
	compareStart := time.Now()
	diff := preBodyDifference(r.pre, pre, marks)
	r.LastStats.Compare = time.Since(compareStart)
	r.LastStats.Pairing = pairing
	if diff != "" {
		return fail("%s", diff)
	}
	bodies := make([][]ast.Stmt, len(splices))
	for i, s := range splices {
		prepared := pre.prepared[retainedLineKey(s.edit.Pos())+"|"+s.edit.Name]
		if prepared == nil {
			return fail("prepared form of %s not found", s.edit.Name)
		}
		bodies[i] = prepared.Body
	}
	// Past this point the edit is accepted; mutate the retained state.
	var changed []scopedDecl
	drop := map[int]bool{}
	for i, s := range splices {
		s.live.Body = bodies[i]
		changed = append(changed, scoped[s.live])
		if w, ok := r.windows[s.live]; ok {
			for k := w[0]; k < w[1]; k++ {
				drop[k] = true
			}
		}
	}
	for _, d := range shifted {
		retainedShiftDecl(d, marks)
	}
	r.LastStats.Diff = time.Since(start)
	// Keep the body-phase diagnostics of unchanged functions; the changed ones
	// and everything the whole-program passes emitted are recomputed.
	kept := make([]Diagnostic, 0, r.bodyEnd)
	for k := 0; k < r.bodyEnd && k < len(a.diagnostics); k++ {
		if !drop[k] {
			d := a.diagnostics[k]
			d.Pos = retainedShiftPos(d.Pos, marks)
			kept = append(kept, d)
		}
	}
	a.diagnostics = kept
	a.reportedDiagnostics = make(map[diagnosticIdentity]bool, len(kept))
	for _, d := range kept {
		a.reportedDiagnostics[diagnosticIdentity{Pos: d.Pos, Severity: d.Severity, Message: d.Message}] = true
	}
	a.warnOnceSeen = nil
	// Windows index into the diagnostics list, which was just compacted; the
	// changed functions get fresh windows below, the rest are re-based.
	r.rebaseWindows(drop)
	r.invalidateReturnBorrow(changed, marks)
	a.returnBorrowLateFrames = append([]returnBorrowLateFrame{}, r.lateFrames...)
	a.returnBorrowLateEnabled = true
	bodiesStart := time.Now()
	a.analyzeDecls(changed)
	r.LastStats.Bodies = time.Since(bodiesStart)
	r.rbUpdating = true
	r.post()
	r.rbUpdating = false
	a.closeSMT()
	// file is the next comparison base. Its spliced bodies now belong to the
	// analyzed AST and get rewritten there, so keep their text from before.
	r.pristine, r.src, r.texts, r.pre = file, src, newTexts, pre
	r.result.Diagnostics = a.diagnostics
	r.result.SMTProfile = a.smtStats
	r.LastStats.Changed = len(changed)
	r.LastStats.Shifted = len(shifted)
	r.LastStats.Total = time.Since(start)
	return r.result, nil
}

// rebaseWindows maps the recorded diagnostic windows onto the compacted list.
func (r *RetainedAnalysis) rebaseWindows(drop map[int]bool) {
	if len(drop) == 0 {
		return
	}
	remap := func(k int) int {
		n := 0
		for d := range drop {
			if d < k {
				n++
			}
		}
		return k - n
	}
	for fn, w := range r.windows {
		if drop[w[0]] {
			delete(r.windows, fn)
			continue
		}
		r.windows[fn] = [2]int{remap(w[0]), remap(w[1])}
	}
}

// funcComparisonText is what Update compares for a function whose source changed.
type funcComparisonText struct {
	header  string // signature, gensyms normalized
	gensyms string // offset-named identifiers the function mentions
}

func retainedText(fn *ast.FuncDecl) funcComparisonText {
	return funcComparisonText{
		header:  retainedNormalized(retainedHeader(fn)),
		gensyms: retainedSharedGensyms(unparse.FormatDecl(fn)),
	}
}

func (r *RetainedAnalysis) comparisonText(fn *ast.FuncDecl) funcComparisonText {
	if text, ok := r.texts[fn]; ok {
		return text
	}
	return retainedText(fn)
}

// The parser names synthesized declarations and auto arenas after their byte
// offset (`__auto_10116619`, `__MachineMode_<hash>_10116619`), so an edit renames
// them in every later declaration of the expansion without changing it.
var retainedGensym = regexp.MustCompile(`(__[A-Za-z][A-Za-z0-9_]*?)_[0-9]+\b`)

// Auto arenas are bindings local to the body that declares them.
var retainedLocalGensym = regexp.MustCompile(`^__auto_[0-9]+$`)

func retainedNormalized(text string) string { return retainedGensym.ReplaceAllString(text, "${1}_") }

// retainedSharedGensyms lists the offset-named identifiers a body mentions that
// may name declarations outside it. A spliced body keeps its new names while the
// retained declarations keep the old ones, so these must not change.
func retainedSharedGensyms(text string) string {
	var out []string
	for _, name := range retainedGensym.FindAllString(text, -1) {
		if !retainedLocalGensym.MatchString(name) {
			out = append(out, name)
		}
	}
	return strings.Join(out, " ")
}

func retainedHeader(fn *ast.FuncDecl) string {
	c := *fn
	c.Body = nil
	return unparse.FormatDecl(&c)
}

func retainedLineKey(p lexer.Pos) string { return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col) }

var retainedDeclType = reflect.TypeOf((*ast.Decl)(nil)).Elem()

// retainedWalkDecls lists every declaration in source order, descending into
// declaration containers (modules, impls, conditional blocks) but not bodies.
func retainedWalkDecls(file *ast.File) []ast.Decl {
	var out []ast.Decl
	var visit func(v reflect.Value)
	visit = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return
			}
			if v.Type().Implements(retainedDeclType) {
				d := v.Interface().(ast.Decl)
				out = append(out, d)
				if _, isFn := d.(*ast.FuncDecl); isFn {
					return
				}
			}
			visit(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				switch f := v.Field(i); f.Kind() {
				case reflect.Slice, reflect.Interface, reflect.Pointer:
					visit(f)
				}
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
	if file == nil {
		return nil
	}
	for i := range file.Decls {
		visit(reflect.ValueOf(&file.Decls[i]).Elem())
	}
	return out
}

var retainedPosType = reflect.TypeOf(lexer.Pos{})

func retainedShiftPos(p lexer.Pos, marks map[string][][2]int) lexer.Pos {
	ms := marks[p.File]
	if len(ms) == 0 || p.Line == 0 {
		return p
	}
	d := 0
	for _, m := range ms {
		if p.Line >= m[0] {
			d = m[1]
		}
	}
	if d != 0 {
		p.Line += d
		if p.EndLine != 0 {
			p.EndLine += d
		}
	}
	return p
}

// retainedShiftDecl moves every position inside an unchanged declaration by
// the line delta of the edit above it. Byte offsets are left as they were: they
// index the expanded buffer, are never printed, and stay consistent within a
// declaration.
func retainedShiftDecl(decl ast.Decl, marks map[string][][2]int) {
	seen := map[uintptr]bool{}
	var visit func(v reflect.Value)
	visit = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			visit(v.Elem())
		case reflect.Interface:
			if !v.IsNil() && v.Elem().Kind() == reflect.Pointer {
				visit(v.Elem())
			}
		case reflect.Struct:
			if v.Type() == retainedPosType {
				if v.CanSet() {
					v.Set(reflect.ValueOf(retainedShiftPos(v.Interface().(lexer.Pos), marks)))
				}
				return
			}
			for i := 0; i < v.NumField(); i++ {
				visit(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		}
	}
	visit(reflect.ValueOf(decl))
}

// retainedSpans returns each declaration's source text: from its start to the
// next declaration's start in the expanded buffer (the last runs to the end).
// A container's span is therefore its own header, and comments and blank lines
// count as changes, so a declaration whose positions moved is re-analyzed.
func retainedSpans(decls []ast.Decl, src []byte) []string {
	out := make([]string, len(decls))
	for i, d := range decls {
		start := d.Pos().Offset
		end := len(src)
		if i+1 < len(decls) {
			end = decls[i+1].Pos().Offset
		}
		if start < 0 || start > end || end > len(src) {
			out[i] = fmt.Sprintf("\x00unordered %d", i) // never equal: forces a fallback or splice
			continue
		}
		out[i] = string(src[start:end])
	}
	return out
}
