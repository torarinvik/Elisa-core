package semantic

import (
	"elisacore/src/ast"
	"strings"
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
		a.refreshTerminalTypestateTracking(sym, a.currentTrackedValueType(sym))
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

func terminalTypestateLeakKind(t Type) string {
	base, ok := trackedNamedStateStructBase(t)
	if !ok || base == nil || len(base.TerminalStateCases) == 0 {
		return ""
	}
	current, ok := trackedNamedStateCurrentArg(t)
	if !ok || current == nil {
		return ""
	}
	if namedStateSubsetOf(current, base.TerminalStateCases) {
		return ""
	}
	return "typestate value left in non-terminal state"
}

func isTerminalTypestate(t Type) bool {
	base, ok := trackedNamedStateStructBase(t)
	if !ok || base == nil || len(base.TerminalStateCases) == 0 {
		return false
	}
	current, ok := trackedNamedStateCurrentArg(t)
	return ok && current != nil && namedStateSubsetOf(current, base.TerminalStateCases)
}

func namedStateSubsetOf(t Type, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = true
	}
	switch tt := t.(type) {
	case *StructStateCaseType:
		return allowedSet[tt.Case]
	case *StructStateSetType:
		if len(tt.Cases) == 0 {
			return false
		}
		for _, name := range tt.Cases {
			if !allowedSet[name] {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func directProtocolCarrierType(t Type) bool {
	return directProtocolLeakKind(t) != "" || terminalTypestateLeakKind(t) != "" || isBuiltinProtocolOwnerType(t, "TaskGroup") || isBuiltinProtocolOwnerType(t, "ThreadPool")
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
	if scalar, ok := t.(*BuiltinType); ok {
		// Builtins cannot carry protocol state. Preserve the same seen entry
		// as the general path without running the protocol/type switches.
		seen[scalar.Name] = true
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
