package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"sort"
)

type retainedAdditionResult struct {
	functions, values []scopedDecl
	appendedTop       bool
}

// retainedDeclareAdditions installs validated scratch declarations in their lexical scopes.
func (r *RetainedAnalysis) retainedDeclareAdditions(a *Analyzer, file, scratch *ast.File, pre *preBodyState, paired retainedDeclarationPairing, added []*ast.FuncDecl, newDeclIndex map[ast.Decl]int, topLevel map[ast.Decl]bool, memberOf map[ast.Decl]*ast.NamespaceDecl, namespaceOf map[*ast.NamespaceDecl]string, valuePlan retainedValueDependencyPlan, appendedInto map[*ast.NamespaceDecl]bool, appendedTop, plainManaged bool) (retainedAdditionResult, error) {
	addedConsts, addedStructs := paired.addedConsts, paired.addedStructs
	addedConstPrepared, addedStructPrepared := valuePlan.addedConstPrepared, valuePlan.addedStructPrepared
	// Added functions are declared in the live analysis as the declaration phase
	// declared them in scratch; their results must then match scratch's exactly.
	var addedScoped []scopedDecl
	var addedInto []*ast.NamespaceDecl // live block that receives each added member; nil at top level
	// place scopes an added declaration (nd in file, prepared its scratch form) in the live
	// analysis: its block, namespace, usings and visibility.
	var usingsOf map[ast.Decl][]string
	var liveBlocks map[ast.Decl]ast.Decl // edited block -> its live counterpart
	place := func(nd, prepared ast.Decl, name string) (scopedDecl, *ast.NamespaceDecl, string) {
		if usingsOf == nil {
			// Usings are the block's inherited usings plus its own `using` lines. Two
			// blocks of one namespace can hold different lines, so they come from the
			// edited file's own block structure.
			usingsOf = a.retainedDeclUsings(file)
			_, liveBlocks, _ = a.retainedLiveUsings(a.file, file)
		}
		sd := scopedDecl{Decl: prepared}
		usings, ok := usingsOf[nd]
		if !ok {
			return sd, nil, fmt.Sprintf("usings of added %s not found", name)
		}
		sd.Usings = usings
		var into *ast.NamespaceDecl
		if block := memberOf[nd]; block != nil && !topLevel[nd] {
			sd.Namespace = namespaceOf[block]
			if into, _ = liveBlocks[block].(*ast.NamespaceDecl); into == nil {
				return sd, nil, fmt.Sprintf("block of added %s not found in the analysis", name)
			}
			visibility := pre.addedVisibility[prepared]
			if scratch != nil && pre.addedPrepared == nil {
				visibility = scratch.DeclVisibility[prepared]
			}
			switch visibility {
			case "private":
				sd.Private = true
			case "":
				if file.DeclVisibility[nd] != "" {
					return sd, nil, fmt.Sprintf("visibility of added %s not found", name)
				}
			}
			if visibility != "" {
				if a.declVisibility == nil {
					a.declVisibility = map[ast.Decl]string{}
				}
				a.declVisibility[prepared] = visibility
			}
		}
		return sd, into, ""
	}
	if len(added) != 0 {
		// Declared in source order, as the declaration phase registers them: the first of
		// an overloaded name owns its bare symbol.
		sort.SliceStable(added, func(x, y int) bool { return newDeclIndex[added[x]] < newDeclIndex[added[y]] })
		for _, nf := range added {
			key := retainedLineKey(nf.Pos()) + "|" + nf.Name
			prepared := pre.prepared[key]
			if prepared == nil {
				prepared = pre.addedPrepared[key]
			}
			if prepared == nil {
				return retainedAdditionResult{}, fmt.Errorf("prepared form of added %s not found", nf.Name)
			}
			sd, into, why := place(nf, prepared, nf.Name)
			if why != "" {
				return retainedAdditionResult{}, fmt.Errorf("%s", why)
			}
			addedScoped = append(addedScoped, sd)
			addedInto = append(addedInto, into)
		}
		// Declaring mutates the live analysis: from here a mismatch poisons it.
		var verifyPlainGlobalReplay func() error
		if plainManaged {
			var err error
			verifyPlainGlobalReplay, err = r.reopenPlainHeaderGlobalProducers(pre, addedScoped)
			if err != nil {
				return retainedAdditionResult{}, fmt.Errorf("plain header global replay: %v", err)
			}
		}
		a.collectValueSymbols(addedScoped)

		if verifyPlainGlobalReplay != nil {
			if err := verifyPlainGlobalReplay(); err != nil {
				return retainedAdditionResult{}, fmt.Errorf("plain header global replay: %v", err)
			}
		}
		for _, sd := range addedScoped {
			fn := sd.Decl.(*ast.FuncDecl)
			if t, ok := pre.targets[fn]; ok {
				a.paramStoreTargets[fn] = t
			}
			if idx, ok := pre.regionIndex[fn]; ok {
				a.regionParamCalleeParamIndex[fn.Name] = idx
			}
		}
		// The classification resets its program-wide tables to the decls it is given; for an
		// added function that is only the added ones, so the rest of the program's entries are
		// kept and the added functions' merged in.
		callerArena, storeBuilders := a.regionPolyReturnsCallerArena, a.packedStoreBuilders
		a.classifyRegionPolymorphicFunctions(addedScoped)
		for ft, v := range callerArena {
			if _, set := a.regionPolyReturnsCallerArena[ft]; !set {
				if a.regionPolyReturnsCallerArena == nil {
					a.regionPolyReturnsCallerArena = map[*FuncType]bool{}
				}
				a.regionPolyReturnsCallerArena[ft] = v
			}
		}
		for ft, v := range storeBuilders {
			if _, set := a.packedStoreBuilders[ft]; !set {
				if a.packedStoreBuilders == nil {
					a.packedStoreBuilders = map[*FuncType]map[string]string{}
				}
				a.packedStoreBuilders[ft] = v
			}
		}
		for _, sd := range addedScoped {
			fn := sd.Decl.(*ast.FuncDecl)
			want := pre.fingerprint[pre.keyOf[fn]]
			if got := a.preBodyFunctionFingerprint(fn); got != want {
				r.a = nil
				return retainedAdditionResult{}, fmt.Errorf("added %s declared differently than a full analysis%s", fn.Name, firstFieldDifference(want, got))
			}
		}
		for i, sd := range addedScoped {
			if addedInto[i] != nil {
				addedInto[i].Decls = append(addedInto[i].Decls, sd.Decl)
				appendedInto[addedInto[i]] = true
			} else {
				a.file.Decls = append(a.file.Decls, sd.Decl)
				appendedTop = true
			}
		}
		r.decls = append(r.decls, addedScoped...)
		if plainManaged && a.fillMayAdoptSet != nil {
			// Exact canonical owner membership is established by the append above.
			// Preserve the materialized whole-program classification across new
			// closed no-call owners. Missing means conservative true to readers,
			// so an actual new false producer must be explicitly represented.
			for _, sd := range addedScoped {
				fn := sd.Decl.(*ast.FuncDecl)
				if err := plainDefaultProductShape(a, fn); err != nil {
					return retainedAdditionResult{}, fmt.Errorf("plain fill-adoption producer: %v", err)
				}
				a.fillMayAdoptSet[fn] = false
			}
		}
	}
	if plainManaged {
		for _, sd := range addedScoped {
			fn := sd.Decl.(*ast.FuncDecl)
			want, known := pre.plainHeaders[pre.keyOf[fn]]
			if !known {
				return retainedAdditionResult{}, fmt.Errorf("added %s lacks a certified scratch header product", fn.Name)
			}
			got, err := captureRetainedPlainDefaultProductPhase(a, fn, true)
			if err != nil {
				return retainedAdditionResult{}, fmt.Errorf("added %s header contribution: %v", fn.Name, err)
			}
			if got.Certificate != want.Certificate {
				return retainedAdditionResult{}, fmt.Errorf("added %s header facts differ from independent scratch analysis", fn.Name)
			}
		}
	}
	// Added consts are scoped like added functions; declaring them cannot disagree with
	// scratch, whose declaration-phase diagnostics already matched.
	var addedConstScoped, addedStructScoped []scopedDecl
	var addedConstInto, addedStructInto []*ast.NamespaceDecl
	for k, cd := range addedConsts {
		sd, into, why := place(cd, addedConstPrepared[k], retainedValueDeclName(cd))
		if why != "" {
			return retainedAdditionResult{}, fmt.Errorf("%s", why)
		}
		addedConstScoped = append(addedConstScoped, sd)
		addedConstInto = append(addedConstInto, into)
	}
	for k, st := range addedStructs {
		sd, into, why := place(st, addedStructPrepared[k], st.Name)
		if why != "" {
			return retainedAdditionResult{}, fmt.Errorf("%s", why)
		}
		if _, exists := a.namedTypes[joinQualifiedName(sd.Namespace, st.Name)]; exists {
			return retainedAdditionResult{}, fmt.Errorf("added struct %s shadows a declared type", st.Name)
		}
		addedStructScoped = append(addedStructScoped, sd)
		addedStructInto = append(addedStructInto, into)
	}
	if len(addedConstScoped) != 0 {
		for k, sd := range addedConstScoped {
			if addedConstInto[k] != nil {
				addedConstInto[k].Decls = append(addedConstInto[k].Decls, sd.Decl)
				appendedInto[addedConstInto[k]] = true
			} else {
				a.file.Decls = append(a.file.Decls, sd.Decl)
				appendedTop = true
			}
		}
		a.collectValueSymbols(addedConstScoped)
		r.decls = append(r.decls, addedConstScoped...)
	}
	// Added structs are declared as the declaration phase declares them.
	if len(addedStructScoped) != 0 {
		for k, sd := range addedStructScoped {
			if addedStructInto[k] != nil {
				addedStructInto[k].Decls = append(addedStructInto[k].Decls, sd.Decl)
				appendedInto[addedStructInto[k]] = true
			} else {
				a.file.Decls = append(a.file.Decls, sd.Decl)
				appendedTop = true
			}
		}
		a.collectNamedTypes(addedStructScoped)
		a.populateStructFields(addedStructScoped)
		a.clearRetainedTypeMemos()
		r.decls = append(r.decls, addedStructScoped...)
	}
	return retainedAdditionResult{addedScoped, addedConstScoped, appendedTop}, nil
}
