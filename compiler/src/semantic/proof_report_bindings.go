package semantic

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// These identities are assigned by the parser's autoRegionName and
// runtimeRangeQuantifier producers. Matching a suffix alone grants no authority.
// Both independently parsed binder nodes must satisfy their concrete producer
// shape and retain their declaration-relative source identity.
func (r *RetainedAnalysis) prepareProofReportBindings(file *ast.File, marks map[string][][2]int) func(proofContribution, ProofFact) (ProofFact, string) {
	type sourceIdentity struct {
		Kind      reflect.Type
		File      string
		Line, Col int
	}
	type binderIdentity struct {
		Owner     sourceIdentity
		Kind      reflect.Type
		File      string
		Line, Col int
	}
	type binder struct {
		Name        string
		Conflicting bool
	}
	identity := func(node ast.Node, shift bool) sourceIdentity {
		p := node.Pos()
		if shift {
			p = retainedShiftPos(p, marks)
		}
		return sourceIdentity{reflect.TypeOf(node), p.File, p.Line, p.Col}
	}
	// The three eager source roots are walked sequentially before analysis can
	// mutate the fresh file. Reuse only the pointer-visit set across those walks;
	// the returned binder/blocker maps and the quantifier set remain per-root.
	// Release the largest map before returning the renderer so its backing table
	// does not remain live across the body update.
	seenScratch := make(map[uintptr]bool)
	collect := func(root *ast.File, shift bool) (map[binderIdentity]binder, map[string]bool) {
		result := map[binderIdentity]binder{}
		blocked := map[string]bool{}
		quantifiers := map[*ast.VarDeclStmt]bool{}
		seen := seenScratch
		if seen == nil {
			// The lazy baseline-source recovery is called after the eager scratch
			// has been released. It gets a private one-shot map, never stale keys.
			seen = make(map[uintptr]bool)
		} else {
			clear(seen)
		}
		var visit func(reflect.Value, sourceIdentity)
		visit = func(v reflect.Value, owner sourceIdentity) {
			if !v.IsValid() {
				return
			}
			// Exact Pos has one string and only scalar coordinates. Preserve the
			// File lookalike check; coordinates cannot carry binders or names.
			if proofBindingPositionSchemaValid && v.Type() == reflect.TypeFor[lexer.Pos]() {
				name := v.Field(0).String()
				if retainedLocalGensymName(name) {
					blocked[name] = true
				}
				return
			}
			switch v.Kind() {
			case reflect.Interface:
				if !v.IsNil() {
					visit(v.Elem(), owner)
				}
			case reflect.Pointer:
				if v.IsNil() || seen[v.Pointer()] {
					return
				}
				seen[v.Pointer()] = true
				if v.CanInterface() {
					if block, ok := v.Interface().(*ast.ExprBlock); ok {
						if binder := proofRuntimeQuantifierBinder(block); binder != nil {
							quantifiers[binder] = true
						}
					}
					if decl, ok := v.Interface().(ast.Decl); ok {
						owner = identity(decl, shift)
					}
					var node ast.Node
					var name string
					switch n := v.Interface().(type) {
					case *ast.RegionStmt:
						if n.Lazy && n.Name == fmt.Sprintf("__auto_%d", n.Position.Offset) {
							node, name = n, n.Name
						}
					case *ast.VarDeclStmt:
						if quantifiers[n] {
							node, name = n, n.Name
						}
					}
					if node != nil {
						p := node.Pos()
						if shift {
							p = retainedShiftPos(p, marks)
						}
						key := binderIdentity{owner, reflect.TypeOf(node), p.File, p.Line, p.Col}
						previous, present := result[key]
						result[key] = binder{name, present && (previous.Conflicting || previous.Name != name)}
					}
				}
				visit(v.Elem(), owner)
			case reflect.Struct:
				generated := ""
				if v.CanAddr() && v.Addr().CanInterface() {
					switch n := v.Addr().Interface().(type) {
					case *ast.RegionStmt:
						if n.Lazy && n.Name == fmt.Sprintf("__auto_%d", n.Position.Offset) {
							generated = n.Name
						}
					case *ast.VarDeclStmt:
						if quantifiers[n] {
							generated = n.Name
						}
					}
				}
				for i := 0; i < v.NumField(); i++ {
					field := v.Field(i)
					if field.Kind() == reflect.String {
						name := field.String()
						if !retainedLocalGensymName(name) {
							continue
						}
						// Ident.Name is a reference spelling; literals are source data.
						// Every other lookalike source name blocks authorization,
						// including fields, parameters and nested user binders.
						if v.Type() == reflect.TypeOf(ast.Ident{}) && v.Type().Field(i).Name == "Name" {
							continue
						}
						if v.Type() == reflect.TypeOf(ast.StringLit{}) {
							continue
						}
						if v.Type().Field(i).Name == "Name" && name == generated {
							continue
						}
						blocked[name] = true
						continue
					}
					visit(field, owner)
				}
			case reflect.String:
				if retainedLocalGensymName(v.String()) {
					blocked[v.String()] = true
				}
			case reflect.Slice:
				if v.Type().Elem().Kind() == reflect.Uint8 {
					return
				}
				for i := 0; i < v.Len(); i++ {
					visit(v.Index(i), owner)
				}
			}
		}
		visit(reflect.ValueOf(root), sourceIdentity{})
		return result, blocked
	}
	fresh, blocked := collect(file, false)
	// Live syntax can retain an older offset epoch. The pristine parse supplies
	// names from the prior published source epoch; both must bind to one producer.
	prior := []map[binderIdentity]binder{}
	for _, source := range []*ast.File{r.a.file, r.pristine} {
		old, userNames := collect(source, true)
		prior = append(prior, old)
		for name := range userNames {
			blocked[name] = true
		}
	}
	// collect is retained by ensureBaseline for a possible one-shot source parse.
	// Set the captured variable to nil now so this renderer does not retain the
	// eager scratch map throughout body analysis.
	seenScratch = nil
	type candidates map[string]map[string]bool
	byOwner := map[sourceIdentity]candidates{}
	add := func(owner sourceIdentity, from, to string) {
		if byOwner[owner] == nil {
			byOwner[owner] = candidates{}
		}
		if byOwner[owner][from] == nil {
			byOwner[owner][from] = map[string]bool{}
		}
		byOwner[owner][from][to] = true
	}
	for key, current := range fresh {
		if current.Conflicting {
			continue
		}
		add(key.Owner, current.Name, current.Name)
		for _, old := range prior {
			if before, present := old[key]; present && !before.Conflicting {
				add(key.Owner, before.Name, current.Name)
			}
		}
	}
	// Disk projection omits pristine bodies, while analyzed live syntax may
	// retain an older offset epoch. Recover only an unresolved source epoch,
	// once per adapter, from the exact bytes that produced the retained state.
	baselineSource, baselineFilename := r.src, file.Filename
	if r.pristine != nil {
		baselineFilename = r.pristine.Filename
	}
	baselineAttempted, baselineFailure := false, ""
	ensureBaseline := func() string {
		if baselineAttempted {
			return baselineFailure
		}
		baselineAttempted = true
		parsed, why := proofReportBindingSource(baselineFilename, baselineSource)
		if why != "" {
			baselineFailure = why
			return why
		}
		old, userNames := collect(parsed, true)
		for name := range userNames {
			blocked[name] = true
		}
		for key, current := range fresh {
			if current.Conflicting {
				continue
			}
			if previous, present := old[key]; present && !previous.Conflicting {
				add(key.Owner, previous.Name, current.Name)
			}
		}
		return ""
	}
	return func(row proofContribution, fact ProofFact) (ProofFact, string) {
		// Fixed subjects and ordinary source facts dominate most programs. Do
		// not build per-contribution binding unions or copy fact strings when
		// none can mention an offset-derived identifier.
		hasTokens := retainedGensym.MatchString(fact.Subject) || retainedGensym.MatchString(fact.Predicate) || retainedGensym.MatchString(fact.Missing)
		for _, values := range [][]string{fact.KnownFacts, fact.ClosedWorldFacts} {
			for _, text := range values {
				hasTokens = hasTokens || retainedGensym.MatchString(text)
			}
		}
		if !hasTokens {
			return fact, ""
		}
		allowedForRow := func() candidates {
			allowed := candidates{}
			owners := map[ast.Node]bool{row.Key.Owner: true}
			if row.Key.Site != nil {
				owners[row.Key.Site] = true
			}
			for origin := range row.Origins {
				owners[origin] = true
			}
			for owner := range owners {
				for name, targets := range byOwner[identity(owner, false)] {
					if allowed[name] == nil {
						allowed[name] = map[string]bool{}
					}
					for target := range targets {
						allowed[name][target] = true
					}
				}
			}
			return allowed
		}
		allowed := allowedForRow()
		rewrite := func(text string) (string, string) {
			var out strings.Builder
			for i := 0; i < len(text); {
				start := i
				if text[i] == '"' || text[i] == '\'' || text[i] == '`' {
					quote := text[i]
					closed := false
					i++
					for i < len(text) {
						if text[i] == '\\' && quote != '`' && i+1 < len(text) {
							i += 2
							continue
						}
						c := text[i]
						i++
						if c == quote {
							closed = true
							break
						}
					}
					if !closed && retainedLocalGensymRef.MatchString(text[start:i]) {
						return "", "unclassified quoted generated proof payload"
					}
					out.WriteString(text[start:i])
					continue
				}
				ident := func(c byte) bool {
					return c >= 0x80 || c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
				}
				if !ident(text[i]) {
					out.WriteByte(text[i])
					i++
					continue
				}
				for i < len(text) && ident(text[i]) {
					i++
				}
				token := text[start:i]
				if !retainedLocalGensymName(token) {
					if retainedGensym.FindString(token) == token {
						return "", "offset-derived proof naming family has no producer adapter"
					}
					out.WriteString(token)
					continue
				}
				if blocked[token] {
					return "", fmt.Sprintf("generated-looking proof token %s also names source material", token)
				}
				targets := allowed[token]
				if len(targets) != 1 && !baselineAttempted {
					if why := ensureBaseline(); why != "" {
						return "", why
					}
					allowed = allowedForRow()
					if blocked[token] {
						return "", fmt.Sprintf("generated-looking proof token %s also names source material", token)
					}
					targets = allowed[token]
				}
				if baselineFailure != "" {
					return "", baselineFailure
				}
				if len(targets) != 1 {
					return "", fmt.Sprintf("generated proof identifier %s has no unique source binder", token)
				}
				for target := range targets {
					out.WriteString(target)
				}
			}
			return out.String(), ""
		}
		var why string
		if fact.Subject, why = rewrite(fact.Subject); why != "" {
			return fact, why
		}
		if fact.Predicate, why = rewrite(fact.Predicate); why != "" {
			return fact, why
		}
		if fact.Missing, why = rewrite(fact.Missing); why != "" {
			return fact, why
		}
		if fact.KnownFacts != nil {
			values := make([]string, len(fact.KnownFacts))
			for i, text := range fact.KnownFacts {
				if values[i], why = rewrite(text); why != "" {
					return fact, why
				}
			}
			// inScopeKnownFacts is a sorted rendering, not a producer event sequence.
			sort.Strings(values)
			fact.KnownFacts = values
		}
		if fact.ClosedWorldFacts != nil {
			values := make([]string, len(fact.ClosedWorldFacts))
			for i, text := range fact.ClosedWorldFacts {
				if values[i], why = rewrite(text); why != "" {
					return fact, why
				}
			}
			fact.ClosedWorldFacts = values
		}
		return fact, ""
	}
}

// runtimeRangeQuantifier shares ONE result Ident between ExprBlock.Value and
// the witness/counterexample assignment target. An equivalent user-written do
// block parses distinct reference nodes and cannot establish this producer
// identity merely by choosing an offset-looking variable name.
func proofRuntimeQuantifierBinder(block *ast.ExprBlock) *ast.VarDeclStmt {
	if block == nil || len(block.Stmts) != 2 {
		return nil
	}
	binder, ok := block.Stmts[0].(*ast.VarDeclStmt)
	if !ok || !binder.Mutable || binder.Position != block.Position || binder.Name != fmt.Sprintf("__quantifier_%d", block.Position.Offset) {
		return nil
	}
	initial, ok := binder.Value.(*ast.BoolLit)
	if !ok || initial.Position != block.Position {
		return nil
	}
	result, ok := block.Value.(*ast.Ident)
	if !ok || result.Name != binder.Name || result.Position != block.Position {
		return nil
	}
	loop, ok := block.Stmts[1].(*ast.ForStmt)
	if !ok || loop.Position != block.Position || len(loop.Body) != 1 {
		return nil
	}
	condition, ok := loop.Body[0].(*ast.IfStmt)
	if !ok || condition.Position != block.Position || len(condition.Then) != 2 {
		return nil
	}
	assignment, ok := condition.Then[0].(*ast.AssignStmt)
	if !ok || assignment.Position != block.Position || assignment.Target != result {
		return nil
	}
	final, ok := assignment.Value.(*ast.BoolLit)
	if !ok || final.Position != block.Position || final.Value == initial.Value {
		return nil
	}
	stop, ok := condition.Then[1].(*ast.BreakStmt)
	if !ok || stop.Position != block.Position {
		return nil
	}
	return binder
}

var proofReportBindingSourceSequence atomic.Uint64

// Parse syntax evidence only. A reserved non-filesystem name isolates the lexer
// parity recorder from the current request; attribution directives retain their
// exact include filenames, and only the synthetic root token name is rebound.
func proofReportBindingSource(filename string, source []byte) (*ast.File, string) {
	isolated := fmt.Sprintf("\x00proof-report-binding-baseline:%d", proofReportBindingSourceSequence.Add(1))
	l := lexer.New(isolated, source)
	defer lexer.DropParitySource(isolated)
	tokens := l.Tokenize()
	if len(l.Errors()) != 0 {
		return nil, "retained generated binding source does not lex"
	}
	for i := range tokens {
		if tokens[i].Pos.File == isolated {
			tokens[i].Pos.File = filename
		}
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if len(p.Errors()) != 0 {
		return nil, "retained generated binding source does not parse"
	}
	return file, ""
}

// A future Pos schema change must retain the original full visitor.
var proofBindingPositionSchemaValid = proofBindingPositionSchemaMatches(reflect.TypeFor[lexer.Pos]())

func proofBindingPositionSchemaMatches(typ reflect.Type) bool {
	if typ == nil || typ.Kind() != reflect.Struct || typ.NumField() != 7 {
		return false
	}
	names := []string{"File", "Line", "Col", "Offset", "EndLine", "EndCol", "EndOffset"}
	for i, name := range names {
		field := typ.Field(i)
		expected := reflect.TypeFor[int]()
		if i == 0 {
			expected = reflect.TypeFor[string]()
		}
		if field.Name != name || field.Type != expected || field.Anonymous || field.Tag != "" || field.PkgPath != "" {
			return false
		}
	}
	return true
}
