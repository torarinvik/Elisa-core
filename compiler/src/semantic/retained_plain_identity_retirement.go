package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"reflect"
	"strings"
)

// Retire only a fully certified last-header observation. The source declaration
// remains live: after withdrawing the exact old fact, rerun its normal scalar
// literal initializer producer, which decides whether a cold-live fact exists.
// Other global facts/types are neither swept nor guessed to be disposable.
func (r *RetainedAnalysis) preparePlainGlobalFactRetirement(pre *preBodyState, products []*retainedPlainDefaultProduct) (func() error, error) {
	if r.pre == nil || r.pre.plainIdentities == nil || pre == nil || pre.plainIdentities == nil {
		return nil, nil
	}
	wanted := map[string]retainedPlainIdentityEntry{}
	for key, entry := range r.pre.plainIdentities.Entries {
		if !strings.HasPrefix(key, "prebody-global-row:exprFacts:") || !strings.HasPrefix(entry.Value, "present:") {
			continue
		}
		if next, present := pre.plainIdentities.Entries[key]; present && strings.HasPrefix(next.Value, "present:") {
			continue
		}
		wanted[key] = entry
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	if pre.plainInputs == "" || pre.plainInputs != r.pre.plainInputs || len(pre.plainHeaders) != len(pre.keyOf) {
		return nil, fmt.Errorf("global fact retirement lacks complete header/source authority")
	}
	type target struct {
		expr     ast.Expr
		decl     ast.Decl
		encoder  *plainDefaultProductEncoder
		expected string
		rebuild  bool
	}
	targets := map[string]target{}
	encode := func(encoder *plainDefaultProductEncoder, expr ast.Expr) (string, error) {
		facts, present := r.a.lookupExprFacts(expr)
		if !present {
			return "absent", nil
		}
		encoder.seen = map[specializationPointer]int{}
		value, err := encoder.encode(reflect.ValueOf(facts))
		if err != nil {
			return "", err
		}
		return "present:" + value, nil
	}
	for _, product := range products {
		encoder := plainIdentityEncoderForProduct(r.a, product)
		for name := range product.GlobalDependencies {
			symbol := r.a.globalScope.Symbols[name]
			if symbol == nil {
				return nil, fmt.Errorf("retiring global fact lost source %s", name)
			}
			var decl ast.Decl
			var expr ast.Expr
			switch node := symbol.Node.(type) {
			case *ast.GlobalDecl:
				decl, expr = node, node.Value
			case *ast.ConstDecl:
				if node.Type != nil {
					decl, expr = node, node.Value
				}
			}
			if expr == nil {
				continue
			}
			anchor, known := encoder.anchors[expr]
			if !known {
				continue
			}
			key := "prebody-global-row:exprFacts:" + anchor
			entry, retiring := wanted[key]
			if !retiring {
				continue
			}
			// A contextual fixed-array initializer does not record a root fact:
			// analyzeValueExpr dispatches directly to analyzeListLitExprWithExpected.
			// Its root fact instead came from the header's immutable-binding lookup:
			// functionValueTypeForExpr -> analyzeExpr -> deferred fact recorder.
			// Complete current header receipts prove that the final such observer
			// is gone; plainDefaultProgramInputs excludes body/global call consumers.
			// Retract only that root, leaving all element facts and types untouched.
			// Scalar contextual producers are replayed separately below.
			rebuild := true
			switch typ := symbol.Type.(type) {
			case *BuiltinType:
				literal, integral := expr.(*ast.IntLit)
				if !integral || literal.Suffix != "" || !IsIntegralStorageType(typ) {
					return nil, fmt.Errorf("global fact %s lacks a classified contextual integer producer", name)
				}
			case *ArrayType:
				list, literal := expr.(*ast.ListLitExpr)
				_, builtin := typ.Elem.(*BuiltinType)
				if !literal || !builtin || !typ.HasConstSize || list.Brace || list.Owner != nil || len(list.Keys) != 0 || uint64(len(list.Elems)) != uint64(typ.ConstSize) {
					return nil, fmt.Errorf("global fact %s lacks a classified contextual fixed-array producer", name)
				}
				for _, spread := range list.Spreads {
					if spread {
						return nil, fmt.Errorf("global fact %s has a spread initializer", name)
					}
				}
				for _, element := range list.Elems {
					switch element.(type) {
					case *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
					default:
						return nil, fmt.Errorf("global fact %s has a nonliteral array element", name)
					}
				}
				rebuild = false
			default:
				return nil, fmt.Errorf("global fact %s has an unclassified declaration producer", name)
			}
			observed, err := encode(encoder, expr)
			if err != nil {
				return nil, err
			}
			if observed != "absent" && observed != entry.Value {
				return nil, fmt.Errorf("global fact %s gained a different full-program value", name)
			}
			if old, present := targets[key]; present && old.expr != expr {
				return nil, fmt.Errorf("global fact source identity aliases distinct syntax")
			}
			targets[key] = target{expr, decl, encoder, observed, rebuild}
		}
	}
	for key := range wanted {
		if _, known := targets[key]; !known {
			return nil, fmt.Errorf("global fact %s lacks a retiring certified owner", key)
		}
	}
	return func() error {
		var rebuild []scopedDecl
		decls := map[ast.Decl]bool{}
		// Revalidate every complete value before deleting ANY row.
		for _, item := range targets {
			observed, err := encode(item.encoder, item.expr)
			if err != nil {
				return err
			}
			if observed != item.expected {
				return fmt.Errorf("global initializer fact changed after retirement preflight")
			}
		}
		for _, item := range targets {
			if item.expected != "absent" {
				r.a.deleteExprFacts(item.expr)
				delete(r.result.ExprFacts, item.expr)
			}
			if item.rebuild {
				decls[item.decl] = true
			}
		}
		for _, scoped := range r.decls {
			if decls[scoped.Decl] {
				rebuild = append(rebuild, scoped)
				delete(decls, scoped.Decl)
			}
		}
		if len(decls) != 0 {
			return fmt.Errorf("retiring global initializer no longer has a live declaration")
		}
		errorsBefore := 0
		for _, diagnostic := range r.a.diagnostics {
			if diagnostic.Severity == DiagnosticSeverityError {
				errorsBefore++
			}
		}
		if len(rebuild) != 0 {
			r.a.analyzeDecls(rebuild)
		}
		errorsAfter := 0
		for _, diagnostic := range r.a.diagnostics {
			if diagnostic.Severity == DiagnosticSeverityError {
				errorsAfter++
			}
		}
		if errorsAfter != errorsBefore {
			return fmt.Errorf("unchanged scalar global producer emitted a new semantic error")
		}
		// Canonical Result storage may be a separate projection. Publish only the
		// producer's exact target rows, leaving every unrelated global fact intact.
		for _, item := range targets {
			if facts, present := r.a.lookupExprFacts(item.expr); present {
				if r.result.ExprFacts == nil {
					r.result.ExprFacts = map[ast.Expr]OptimizationFacts{}
				}
				r.result.ExprFacts[item.expr] = facts
			} else {
				delete(r.result.ExprFacts, item.expr)
			}
		}
		return nil
	}, nil
}

// Reopen only the certified lazy root consumed by a newly declared header.
// Normal collectValueSymbols will run its producer with the real parameter-default
// owner/context. Retaining the cached root type would skip that cold producer.
func (r *RetainedAnalysis) reopenPlainHeaderGlobalProducers(pre *preBodyState, added []scopedDecl) (func() error, error) {
	if pre == nil || pre.plainIdentities == nil || len(added) == 0 {
		return nil, nil
	}
	owners := map[string]bool{}
	for _, scoped := range added {
		fn, ok := scoped.Decl.(*ast.FuncDecl)
		if !ok || pre.keyOf[fn] == "" {
			return nil, fmt.Errorf("new header has no independent declaration key")
		}
		owners[pre.keyOf[fn]] = true
	}
	product := &retainedPlainDefaultProduct{Owner: added[0].Decl.(*ast.FuncDecl), Symbols: map[*Symbol]string{}, ExprIdentities: map[ast.Expr]string{}, GlobalDependencies: map[string]string{}}
	encoder := plainIdentityEncoderForProduct(r.a, product)
	type replay struct {
		expr     ast.Expr
		expected string
		decl     ast.Decl
	}
	var pending []replay
	for _, symbol := range r.a.globalScope.Symbols {
		if symbol == nil {
			continue
		}
		var expr ast.Expr
		var source ast.Decl
		switch decl := symbol.Node.(type) {
		case *ast.GlobalDecl:
			expr, source = decl.Value, decl
		case *ast.ConstDecl:
			if decl.Type != nil {
				expr, source = decl.Value, decl
			}
		}
		anchor, known := encoder.anchors[expr]
		if !known {
			continue
		}
		entry, wanted := pre.plainIdentities.Entries["prebody-global-row:exprFacts:"+anchor]
		if !wanted || !strings.HasPrefix(entry.Value, "present:") {
			continue
		}
		if _, present := r.a.lookupExprFacts(expr); present {
			continue
		}
		if pre.plainInputs == "" || r.pre.plainInputs != pre.plainInputs || len(pre.plainHeaders) != len(pre.keyOf) || entry.Residual || len(entry.Owners) == 0 {
			return nil, fmt.Errorf("lazy global producer lacks complete source/header authority")
		}
		for owner := range entry.Owners {
			if !owners[owner] {
				return nil, fmt.Errorf("lazy global producer has a non-new observer")
			}
		}
		array, fixed := symbol.Type.(*ArrayType)
		list, literal := expr.(*ast.ListLitExpr)
		if !fixed || !literal || !array.HasConstSize || list.Brace || list.Owner != nil || len(list.Keys) != 0 || uint64(len(list.Elems)) != uint64(array.ConstSize) {
			return nil, fmt.Errorf("uncertified lazy global producer %T", expr)
		}
		if _, builtin := array.Elem.(*BuiltinType); !builtin {
			return nil, fmt.Errorf("nonbuiltin lazy global array")
		}
		for _, spread := range list.Spreads {
			if spread {
				return nil, fmt.Errorf("spread lazy global array")
			}
		}
		for _, element := range list.Elems {
			switch element.(type) {
			case *ast.IntLit, *ast.FloatLit, *ast.BoolLit:
			default:
				return nil, fmt.Errorf("nonliteral lazy global element")
			}
		}
		cached, typed := r.a.lookupExprType(expr)
		if !typed || !SameType(cached, symbol.Type) {
			return nil, fmt.Errorf("lazy global root type changed")
		}
		pending = append(pending, replay{expr, entry.Value, source})
	}
	// All candidates are checked before changing any cache. Each root is typed
	// again by its actual new header; no child or unrelated global row is cleared.
	for _, item := range pending {
		r.a.deleteExprType(item.expr)
	}
	return func() error {
		// Replay may allocate a new identity; rebuild source-token bindings from
		// the completed live producer before comparing complete semantic values.
		encoder = plainIdentityEncoderForProduct(r.a, product)
		for _, item := range pending {
			facts, present := r.a.lookupExprFacts(item.expr)
			if !present {
				return fmt.Errorf("new header did not replay its lazy global producer")
			}
			encoder.seen = map[specializationPointer]int{}
			encoded, err := encoder.encode(reflect.ValueOf(facts))
			if err != nil {
				return err
			}
			if "present:"+encoded != item.expected {
				return fmt.Errorf("new header global fact differs from independent capture")
			}
		}
		// Cold order is declaration/header typing first, then contextual global
		// initializer analysis. The lazy root replay above inferred literal child
		// types; run the same normal declaration producer to restore its expected
		// element types. Do not normalize semantic values or rerun other globals.
		wanted := map[ast.Decl]bool{}
		for _, item := range pending {
			wanted[item.decl] = true
		}
		var contextual []scopedDecl
		for _, scoped := range r.decls {
			if wanted[scoped.Decl] {
				contextual = append(contextual, scoped)
				delete(wanted, scoped.Decl)
			}
		}
		if len(wanted) != 0 {
			return fmt.Errorf("replayed global has no canonical declaration producer")
		}
		diagnosticsBefore := len(r.a.diagnostics)
		if len(contextual) != 0 {
			r.a.analyzeDecls(contextual)
		}
		if len(r.a.diagnostics) != diagnosticsBefore {
			return fmt.Errorf("unchanged contextual global replay emitted diagnostics")
		}
		encoder = plainIdentityEncoderForProduct(r.a, product)
		for _, item := range pending {
			facts, present := r.a.lookupExprFacts(item.expr)
			if !present {
				return fmt.Errorf("contextual producer removed certified header root fact")
			}
			encoder.seen = map[specializationPointer]int{}
			encoded, err := encoder.encode(reflect.ValueOf(facts))
			if err != nil {
				return err
			}
			if "present:"+encoded != item.expected {
				return fmt.Errorf("contextual producer changed certified header root fact")
			}
			// Result may project separate maps. Publish exactly the replayed
			// initializer subtree after both real producer phases have completed.
			syntax := newSpecializationSyntax()
			syntax.prepare(reflect.ValueOf(item.expr))
			if err := syntax.validate(); err != nil {
				return err
			}
			for _, node := range syntax.prepared {
				expr, ok := node.(ast.Expr)
				if !ok {
					continue
				}
				if typ, present := r.a.lookupExprType(expr); present {
					if r.result.ExprTypes == nil {
						r.result.ExprTypes = map[ast.Expr]Type{}
					}
					r.result.ExprTypes[expr] = typ
				} else {
					delete(r.result.ExprTypes, expr)
				}
				if facts, present := r.a.lookupExprFacts(expr); present {
					if r.result.ExprFacts == nil {
						r.result.ExprFacts = map[ast.Expr]OptimizationFacts{}
					}
					r.result.ExprFacts[expr] = facts
				} else {
					delete(r.result.ExprFacts, expr)
				}
			}
		}
		return nil
	}, nil
}
