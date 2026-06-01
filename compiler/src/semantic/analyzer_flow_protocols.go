package semantic

import (
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) trackAffineValueTarget(expr ast.Expr, expected Type) {
	if expr == nil || !a.containsAffineHandleValues(expected, map[string]bool{}) {
		return
	}
	key, ok := a.lookupAffineValueKey(expr)
	if !ok {
		return
	}
	a.registerLiveProtocolValuePaths(key, expected)
}

func (a *Analyzer) registerLiveProtocolValuePaths(baseKey affineValueKey, t Type) {
	if baseKey.Root == nil {
		return
	}
	paths := a.protocolLiveLeafPaths(t, "", map[string]bool{})
	if len(paths) == 0 {
		return
	}
	if a.currentAffineValues == nil {
		a.currentAffineValues = map[affineValueKey]affineValueState{}
	}
	for path, liveType := range paths {
		key := affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, path)}
		state := a.currentAffineValues[key]
		state.LiveProtocolType = liveType
		state.LiveProtocolDescription = ""
		state.ConsumedBy = ""
		a.currentAffineValues[key] = state
	}
}

func (a *Analyzer) markLiveProtocolDescription(key affineValueKey, description string) {
	if key.Root == nil || description == "" {
		return
	}
	if a.currentAffineValues == nil {
		a.currentAffineValues = map[affineValueKey]affineValueState{}
	}
	state := a.currentAffineValues[key]
	state.LiveProtocolType = nil
	state.LiveProtocolDescription = description
	a.currentAffineValues[key] = state
}

func (a *Analyzer) markCreatedProtocolSymbol(sym *Symbol, value ast.Expr) {
	if sym == nil {
		return
	}
	description := protocolCreationDescription(value, sym.Type)
	if description == "" {
		return
	}
	a.markLiveProtocolDescription(affineValueKey{Root: sym}, description)
}

func (a *Analyzer) markCreatedProtocolTarget(target ast.Expr, value ast.Expr, expected Type) {
	description := protocolCreationDescription(value, expected)
	if description == "" {
		return
	}
	key, ok := a.lookupAffineValueKey(target)
	if !ok {
		return
	}
	a.markLiveProtocolDescription(key, description)
}

func protocolCreationDescription(value ast.Expr, expected Type) string {
	if !isBuiltinProtocolOwnerType(expected, "ThreadPool") {
		return ""
	}
	if !callExprHasName(value, "pool_new") {
		return ""
	}
	return "thread pool requiring shutdown"
}

func callExprHasName(expr ast.Expr, name string) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return callExprHasName(n.Inner, name)
	case *ast.CastExpr:
		return callExprHasName(n.Operand, name)
	case *ast.CallExpr:
		return callIdentName(n) == name
	default:
		return false
	}
}

func (a *Analyzer) clearLiveProtocolTracking(key affineValueKey) {
	if key.Root == nil || a.currentAffineValues == nil {
		return
	}
	for existingKey, existingState := range a.currentAffineValues {
		if existingKey.Root != key.Root {
			continue
		}
		if key.Path != "" && !affinePathContains(key.Path, existingKey.Path) {
			continue
		}
		existingState.LiveProtocolType = nil
		existingState.LiveProtocolDescription = ""
		a.currentAffineValues[existingKey] = existingState
	}
}

func joinAffinePath(base, suffix string) string {
	if base == "" {
		return suffix
	}
	if suffix == "" {
		return base
	}
	return base + "." + suffix
}

func directProtocolLeakKind(t Type) string {
	switch tt := t.(type) {
	case *StructType:
		// A user-declared `linear struct` is must-consume: it models a
		// transaction/handle that has to be consumed exactly once (e.g.
		// MapTxn -> commit or rollback). An `affine struct` (Droppable) is
		// move-only but may be dropped, so it carries no must-consume
		// obligation. Builtin affine carriers (Thread/Task/MutexGuard) are
		// handled below in their state-aware forms and must not be caught here.
		if tt.Affine && !tt.Builtin && !tt.Droppable {
			return "linear value"
		}
		return ""
	case *GenericInstanceType:
		switch tt.Name {
		case "Thread":
			if len(tt.Args) >= 2 && tt.Args[1].String() == "Joinable" {
				return "joinable thread handle"
			}
			return ""
		case "Task":
			if len(tt.Args) >= 2 && tt.Args[1].String() == "Pending" {
				return "pending task handle"
			}
			return ""
		case "MutexGuard":
			if len(tt.Args) >= 1 && tt.Args[0].String() == "Held" {
				return "held mutex guard"
			}
			return ""
		}
		// User-declared generic `linear` structs are also must-consume;
		// `affine` (Droppable) ones are not.
		if base, ok := tt.Base.(*StructType); ok && base.Affine && !base.Builtin && !base.Droppable {
			return "linear value"
		}
		return ""
	default:
		return ""
	}
}

func directProtocolCarrierType(t Type) bool {
	return directProtocolLeakKind(t) != "" || isBuiltinProtocolOwnerType(t, "TaskGroup") || isBuiltinProtocolOwnerType(t, "ThreadPool")
}

func isBuiltinProtocolOwnerType(t Type, name string) bool {
	switch tt := t.(type) {
	case *StructType:
		return tt.Builtin && tt.Name == name
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		return ok && base.Builtin && base.Name == name
	default:
		return false
	}
}

func (a *Analyzer) containsTrackedProtocolCarrierValues(t Type, seen map[string]bool) bool {
	if t == nil {
		return false
	}
	if isAffineHandleType(t) || directProtocolCarrierType(t) {
		return true
	}
	key := t.String()
	if seen[key] {
		return false
	}
	seen[key] = true
	switch tt := t.(type) {
	case *ArrayType:
		return a.containsTrackedProtocolCarrierValues(tt.Elem, seen)
	case *DArrayType:
		return a.containsTrackedProtocolCarrierValues(tt.Elem, seen)
	case *ViewType:
		return a.containsTrackedProtocolCarrierValues(tt.Elem, seen)
	case *DArrayViewType:
		return a.containsTrackedProtocolCarrierValues(tt.Elem, seen)
	case *DictType:
		return a.containsTrackedProtocolCarrierValues(tt.Key, seen) || a.containsTrackedProtocolCarrierValues(tt.Value, seen)
	case *EnumType:
		for _, field := range tt.Common {
			if a.containsTrackedProtocolCarrierValues(field.Type, seen) {
				return true
			}
		}
		for _, variant := range tt.Variants {
			for _, payloadType := range variant.Payload {
				if a.containsTrackedProtocolCarrierValues(payloadType, seen) {
					return true
				}
			}
		}
		return false
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := map[string]Type{}
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				if a.containsTrackedProtocolCarrierValues(fieldType, seen) {
					return true
				}
			}
			return false
		}
		for _, arg := range tt.Args {
			if a.containsTrackedProtocolCarrierValues(arg, seen) {
				return true
			}
		}
		return a.containsTrackedProtocolCarrierValues(tt.Base, seen)
	case *StructType:
		for _, field := range tt.Fields {
			if a.containsTrackedProtocolCarrierValues(field.Type, seen) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func joinProtocolLeakKinds(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " or " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + ", or " + parts[len(parts)-1]
	}
}

func (a *Analyzer) protocolKindsInType(t Type, seen map[string]bool) (bool, bool, bool) {
	if t == nil {
		return false, false, false
	}
	switch directProtocolLeakKind(t) {
	case "joinable thread handle":
		return true, false, false
	case "pending task handle":
		return false, true, false
	case "held mutex guard":
		return false, false, true
	}
	key := t.String()
	if seen[key] {
		return false, false, false
	}
	seen[key] = true
	switch tt := t.(type) {
	case *ArrayType:
		return a.protocolKindsInType(tt.Elem, seen)
	case *DArrayType:
		return a.protocolKindsInType(tt.Elem, seen)
	case *ViewType:
		return a.protocolKindsInType(tt.Elem, seen)
	case *DArrayViewType:
		return a.protocolKindsInType(tt.Elem, seen)
	case *DictType:
		leftThread, leftTask, leftGuard := a.protocolKindsInType(tt.Key, seen)
		rightThread, rightTask, rightGuard := a.protocolKindsInType(tt.Value, seen)
		return leftThread || rightThread, leftTask || rightTask, leftGuard || rightGuard
	case *StructType:
		var hasThread, hasTask, hasGuard bool
		for _, field := range tt.Fields {
			fieldThread, fieldTask, fieldGuard := a.protocolKindsInType(field.Type, seen)
			hasThread = hasThread || fieldThread
			hasTask = hasTask || fieldTask
			hasGuard = hasGuard || fieldGuard
		}
		return hasThread, hasTask, hasGuard
	case *EnumType:
		var hasThread, hasTask, hasGuard bool
		for _, field := range tt.Common {
			fieldThread, fieldTask, fieldGuard := a.protocolKindsInType(field.Type, seen)
			hasThread = hasThread || fieldThread
			hasTask = hasTask || fieldTask
			hasGuard = hasGuard || fieldGuard
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				payloadThread, payloadTask, payloadGuard := a.protocolKindsInType(payload, seen)
				hasThread = hasThread || payloadThread
				hasTask = hasTask || payloadTask
				hasGuard = hasGuard || payloadGuard
			}
		}
		return hasThread, hasTask, hasGuard
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := map[string]Type{}
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			var hasThread, hasTask, hasGuard bool
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				fieldThread, fieldTask, fieldGuard := a.protocolKindsInType(fieldType, seen)
				hasThread = hasThread || fieldThread
				hasTask = hasTask || fieldTask
				hasGuard = hasGuard || fieldGuard
			}
			return hasThread, hasTask, hasGuard
		}
		var hasThread, hasTask, hasGuard bool
		for _, arg := range tt.Args {
			argThread, argTask, argGuard := a.protocolKindsInType(arg, seen)
			hasThread = hasThread || argThread
			hasTask = hasTask || argTask
			hasGuard = hasGuard || argGuard
		}
		baseThread, baseTask, baseGuard := a.protocolKindsInType(tt.Base, seen)
		return hasThread || baseThread, hasTask || baseTask, hasGuard || baseGuard
	default:
		return false, false, false
	}
}

func (a *Analyzer) containsProtocolLeakValues(t Type) bool {
	hasThread, hasTask, hasGuard := a.protocolKindsInType(t, map[string]bool{})
	return hasThread || hasTask || hasGuard
}

func (a *Analyzer) protocolLeakDescription(t Type) string {
	if kind := directProtocolLeakKind(t); kind != "" {
		return kind
	}
	hasThread, hasTask, hasGuard := a.protocolKindsInType(t, map[string]bool{})
	parts := make([]string, 0, 3)
	if hasThread {
		parts = append(parts, "joinable thread handles")
	}
	if hasTask {
		parts = append(parts, "pending task handles")
	}
	if hasGuard {
		parts = append(parts, "held mutex guards")
	}
	if len(parts) == 0 {
		return "linear value"
	}
	return "value containing " + joinProtocolLeakKinds(parts)
}

func (a *Analyzer) protocolLiveLeafPaths(t Type, prefix string, seen map[string]bool) map[string]Type {
	if t == nil {
		return nil
	}
	if kind := directProtocolLeakKind(t); kind != "" {
		return map[string]Type{prefix: t}
	}
	key := t.String()
	if seen[key] {
		return nil
	}
	seen[key] = true
	switch tt := t.(type) {
	case *StructType:
		paths := map[string]Type{}
		for _, field := range tt.Fields {
			if !a.containsProtocolLeakValues(field.Type) {
				continue
			}
			for childPath, liveType := range a.protocolLiveLeafPaths(field.Type, field.Name, mapsCloneBool(seen)) {
				paths[joinAffinePath(prefix, childPath)] = liveType
			}
		}
		return paths
	case *EnumType:
		paths := map[string]Type{}
		for _, field := range tt.Common {
			if !a.containsProtocolLeakValues(field.Type) {
				continue
			}
			for childPath, liveType := range a.protocolLiveLeafPaths(field.Type, field.Name, mapsCloneBool(seen)) {
				paths[joinAffinePath(prefix, childPath)] = liveType
			}
		}
		for _, variant := range tt.Variants {
			for i, payloadType := range variant.Payload {
				label := variant.PayloadLabel(i)
				if label == "" || !a.containsProtocolLeakValues(payloadType) {
					continue
				}
				for childPath, liveType := range a.protocolLiveLeafPaths(payloadType, label, mapsCloneBool(seen)) {
					paths[joinAffinePath(prefix, childPath)] = liveType
				}
			}
		}
		if len(paths) != 0 {
			return paths
		}
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := map[string]Type{}
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			paths := map[string]Type{}
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				if !a.containsProtocolLeakValues(fieldType) {
					continue
				}
				for childPath, liveType := range a.protocolLiveLeafPaths(fieldType, field.Name, mapsCloneBool(seen)) {
					paths[joinAffinePath(prefix, childPath)] = liveType
				}
			}
			return paths
		}
	}
	if a.containsProtocolLeakValues(t) {
		return map[string]Type{prefix: t}
	}
	return nil
}

func (a *Analyzer) recordAffineDestructureConsumption(expr ast.Expr, actual Type, reason string) {
	if expr == nil || actual == nil {
		return
	}
	if !a.containsAffineHandleValues(actual, map[string]bool{}) {
		return
	}
	key, ok := a.lookupAffineValueKey(expr)
	if !ok {
		return
	}
	a.recordAffineConsumption(key, reason)
}

func mapsCloneBool(src map[string]bool) map[string]bool {
	if src == nil {
		return nil
	}
	cloned := make(map[string]bool, len(src))
	for key, value := range src {
		cloned[key] = value
	}
	return cloned
}

func (a *Analyzer) reportUnconsumedProtocolValues() {
	if a.currentAffineValues == nil {
		return
	}
	for key, state := range a.currentAffineValues {
		if key.Root == nil || (state.LiveProtocolType == nil && state.LiveProtocolDescription == "") {
			continue
		}
		if key.Root.Kind == SymbolRegion && key.Path == "" {
			if regionState, ok := a.currentRegions[key.Root]; ok && regionState.Allocated && !regionState.Destroyed {
				continue
			}
		}
		pos := lexer.Pos{}
		if key.Root.Node != nil {
			pos = key.Root.Node.Pos()
		}
		description := state.LiveProtocolDescription
		if description == "" {
			description = a.protocolLeakDescription(state.LiveProtocolType)
		}
		a.errorf(pos, "%s %q must be consumed before scope exit", description, affineValueDisplayNameFromKey(key))
	}
}

func affineValueDisplayNameFromKey(key affineValueKey) string {
	if key.Root == nil {
		return "<value>"
	}
	if key.Path == "" {
		return key.Root.Name
	}
	return key.Root.Name + "." + key.Path
}
