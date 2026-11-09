package easm

import (
	"fmt"
	"strings"
)

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func setOf(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		for _, field := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
			if field != "" {
				out[strings.ToLower(strings.TrimSpace(field))] = true
			}
		}
	}
	return out
}

func normalizeOp(op string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(op), ";"))
}

func hasOp(instructions []Instruction, op string) bool {
	for _, inst := range instructions {
		if normalizeOp(inst.Op) == op {
			return true
		}
	}
	return false
}

func terminalOpIs(instructions []Instruction, ops ...string) bool {
	if len(instructions) == 0 {
		return false
	}
	last := normalizeOp(instructions[len(instructions)-1].Op)
	for _, op := range ops {
		if last == op {
			return true
		}
	}
	return false
}

func hasCallImmediatelyBeforeRet(instructions []Instruction) bool {
	for i := 1; i < len(instructions); i++ {
		prev := normalizeOp(instructions[i-1].Op)
		cur := normalizeOp(instructions[i].Op)
		if (prev == "call" || prev == "callq") && (cur == "ret" || cur == "retq") {
			return true
		}
	}
	return false
}

func hasReturnOutput(outputs []string) bool {
	for _, output := range outputs {
		if bindingName(output) == "ret" && registerAfterEquals(output) != "" {
			return true
		}
	}
	return false
}

func returnOutputRegister(outputs []string) string {
	for _, output := range outputs {
		if bindingName(output) == "ret" {
			return canonicalX86GPR(registerAfterEquals(output))
		}
	}
	return ""
}

func inputRegisterSet(inputs []string) map[string]bool {
	out := map[string]bool{}
	for _, input := range inputs {
		if reg := registerAfterEquals(input); reg != "" {
			out[canonicalX86GPR(reg)] = true
		}
	}
	return out
}

func inputRegisterNameSet(inputs []string) map[string]string {
	out := map[string]string{}
	for _, input := range inputs {
		name := bindingName(input)
		if reg := registerAfterEquals(input); reg != "" && name != "" {
			out[canonicalX86GPR(reg)] = name
		}
	}
	return out
}

func outputRegisterSet(outputs []string) map[string]bool {
	out := map[string]bool{}
	for _, output := range outputs {
		if reg := registerAfterEquals(output); reg != "" {
			out[canonicalX86GPR(reg)] = true
		}
	}
	return out
}

func verifyABI(path string, fn *Function) []Issue {
	switch strings.ToLower(strings.TrimSpace(fn.ABI)) {
	case "", "c", "sysv", "sysv_x86_64", "ps4_sysv":
		return nil
	default:
		return []Issue{{Severity: "error", Code: "unknown-abi", File: path, Line: fn.Line, Message: fmt.Sprintf("unknown EASM ABI %s", fn.ABI)}}
	}
}

func verifySignatureTypes(path string, fn *Function) []Issue {
	var issues []Issue
	if !allowedSignatureType(fn.ReturnType) {
		issues = append(issues, Issue{Severity: "error", Code: "invalid-signature-type", File: path, Line: fn.Line, Message: fmt.Sprintf("unsupported EASM return type %s", fn.ReturnType)})
	}
	for _, param := range fn.Params {
		if !allowedSignatureType(param.Type) || param.Type == "void" {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-signature-type", File: path, Line: fn.Line, Message: fmt.Sprintf("unsupported EASM parameter type %s", param.Type)})
		}
	}
	return issues
}

func allowedSignatureType(name string) bool {
	name = strings.TrimSpace(name)
	switch strings.TrimSpace(name) {
	case "void", "bool", "char", "int", "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "usize", "uintptr", "f32", "f64":
		return true
	default:
		return isEASMRoleType(name) || isAddressSpaceCarrierType(name) || isHandleCarrierType(name)
	}
}

func isEASMRoleType(name string) bool {
	name = strings.TrimSpace(name)
	switch name {
	case "GuestEntryPoint", "GuestCallable", "GuestThreadEntry", "GuestThreadArg", "GuestThreadResult", "GuestPC",
		"HostCallable", "NativeCallable", "ExitFunction",
		"GuestStackTop", "GuestFsSelector", "HostFsSelector", "GuestGsSelector", "HostGsSelector",
		"PublishedExecutableAddr", "WritableExecutableAddr",
		"HostStackPointer", "SegmentSelfPointer", "HostThreadId", "SignalContextPtr", "MachineContextPtr":
		return true
	default:
		return false
	}
}

func isAddressSpaceCarrierType(name string) bool {
	name = strings.TrimSpace(name)
	for _, prefix := range []string{"GuestVAddr[", "HostPtr[", "NativeMappedGuestPtr["} {
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, "]") && len(name) > len(prefix)+1 {
			return true
		}
	}
	return false
}

func verifyContractTokens(path string, fn *Function) []Issue {
	var issues []Issue
	for _, token := range contractFields(fn.Stack) {
		if !allowedStackToken(token) {
			issues = append(issues, Issue{Severity: "error", Code: "unknown-stack-contract", File: path, Line: fn.Line, Message: fmt.Sprintf("unknown stack contract %s", token)})
		}
	}
	for _, token := range contractFields(fn.Control) {
		if !allowedControlToken(token) {
			issues = append(issues, Issue{Severity: "error", Code: "unknown-control-contract", File: path, Line: fn.Line, Message: fmt.Sprintf("unknown control contract %s", token)})
		}
	}
	for _, token := range contractFields(fn.Requires) {
		if !allowedRequireToken(token) {
			issues = append(issues, Issue{Severity: "error", Code: "unknown-require-capability", File: path, Line: fn.Line, Message: fmt.Sprintf("unknown requires capability %s", token)})
		}
	}
	return issues
}

func contractFields(values []string) []string {
	var out []string
	for _, value := range values {
		fields := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\t' || r == '\n' })
		for _, field := range fields {
			for _, part := range strings.Fields(strings.TrimSpace(field)) {
				out = append(out, strings.ToLower(part))
			}
		}
	}
	return out
}

func allowedStackToken(token string) bool {
	switch token {
	case "unchanged", "aligned", "16", "synthetic", "switches", "owns", "noreturn", "probed":
		return true
	default:
		return false
	}
}

func allowedControlToken(token string) bool {
	switch token {
	case "returns", "noreturn", "tail_jumps", "may_fault":
		return true
	default:
		return false
	}
}

func allowedRequireToken(token string) bool {
	switch token {
	case "aarch64.cntvct",
		"aarch64.platform_register.x18",
		"aarch64.yield",
		"callee_saved.preservation.unchecked",
		"call.caller_saved_liveness.unchecked",
		"call.return_address_choreography.unchecked",
		"compare.signed",
		"compare.unsigned",
		"control.direct",
		"control.indirect",
		"control.poison_target.unchecked",
		"control.target.untyped",
		"control.tiny_target.unchecked",
		"debug.trap",
		"fixed_address",
		"frame_pointer.handoff.unchecked",
		"input.unused",
		"immediate.truncation",
		"memory.base.untyped",
		"operand_size.inferred",
		"pic",
		"relocation.symbol",
		"return.register.preinitialized",
		"riscv.reserved_registers",
		"stack.call_alignment.unchecked",
		"stack.entry_pop.unchecked",
		"stack.owner.untyped",
		"x86_64.atomic.rmw",
		"x86_64.cpuid",
		"x86_64.fpu_control",
		"x86_64.legacy_high_byte",
		"x86_64.rdtsc",
		"x86_64.rdtsc.unordered",
		"x86_64.segment",
		"x86_64.segment.fs",
		"x86_64.segment.gs",
		"x86_64.segment.persistent",
		"x86_64.segment.restore",
		"x86_64.segment.selector.untyped",
		"x86_64.segment.state.unchecked",
		"x86_64.segment.write",
		"x86_64.simd_state",
		"x86_64.sse.lfence",
		"x86_64.sse.pause":
		return true
	default:
		return false
	}
}

func verifyBindings(path string, target string, fn *Function) []Issue {
	var issues []Issue
	paramNames := map[string]bool{}
	for _, param := range fn.Params {
		paramNames[param.Name] = true
	}
	seenInputs := map[string]bool{}
	for _, input := range fn.Inputs {
		name := bindingName(input)
		if name == "" {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-input-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("invalid input binding %q", input)})
			continue
		}
		if !paramNames[name] {
			issues = append(issues, Issue{Severity: "error", Code: "unknown-input-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("input binding %s does not name a parameter", name)})
		}
		if seenInputs[name] {
			issues = append(issues, Issue{Severity: "error", Code: "duplicate-input-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("duplicate input binding %s", name)})
		}
		seenInputs[name] = true
		if reg := registerAfterEquals(input); reg == "" {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-register-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("input binding %s must use = <register>", name)})
		} else if !targetAllowsRegister(target, reg) {
			issues = append(issues, Issue{Severity: "error", Code: "register-target-mismatch", File: path, Line: fn.Line, Message: fmt.Sprintf("input binding %s uses register %s outside target %s", name, reg, defaultString(target, "any"))})
		}
	}
	for _, param := range fn.Params {
		if !seenInputs[param.Name] {
			issues = append(issues, Issue{Severity: "error", Code: "missing-input-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("parameter %s must be declared in inputs", param.Name)})
		}
	}
	seenOutputs := map[string]bool{}
	for _, output := range fn.Outputs {
		name := bindingName(output)
		if name == "" {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-output-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("invalid output binding %q", output)})
			continue
		}
		if name != "ret" {
			issues = append(issues, Issue{Severity: "error", Code: "unknown-output-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("EASM v1 only supports ret output binding, got %s", name)})
		}
		if seenOutputs[name] {
			issues = append(issues, Issue{Severity: "error", Code: "duplicate-output-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("duplicate output binding %s", name)})
		}
		seenOutputs[name] = true
		if reg := registerAfterEquals(output); reg == "" {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-register-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("output binding %s must use = <register>", name)})
		} else {
			if !targetAllowsRegister(target, reg) {
				issues = append(issues, Issue{Severity: "error", Code: "register-target-mismatch", File: path, Line: fn.Line, Message: fmt.Sprintf("output binding %s uses register %s outside target %s", name, reg, defaultString(target, "any"))})
			}
			if name == "ret" && !returnTypeAllowsRegister(fn.ReturnType, reg) {
				issues = append(issues, Issue{Severity: "error", Code: "return-register-mismatch", File: path, Line: fn.Line, Message: fmt.Sprintf("return type %s cannot use return register %s", fn.ReturnType, reg)})
			}
		}
	}
	return issues
}

func verifyRegisterLists(path string, target string, fn *Function) []Issue {
	var issues []Issue
	for _, clobber := range contractFields(fn.Clobbers) {
		if clobber == "memory" || clobber == "memory.read" || clobber == "memory.write" || clobber == "cc" || clobber == "flags" {
			continue
		}
		reg := strings.TrimPrefix(clobber, "%")
		if !isRegisterName(reg) {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-clobber-register", File: path, Line: fn.Line, Message: fmt.Sprintf("unknown clobber register %s", clobber)})
			continue
		}
		if !targetAllowsRegister(target, reg) {
			issues = append(issues, Issue{Severity: "error", Code: "register-target-mismatch", File: path, Line: fn.Line, Message: fmt.Sprintf("clobber register %s is outside target %s", reg, defaultString(target, "any"))})
		}
	}
	for _, preserve := range contractFields(fn.Preserves) {
		if preserve == "callee_saved" {
			continue
		}
		reg := strings.TrimPrefix(preserve, "%")
		if !isRegisterName(reg) {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-preserve-register", File: path, Line: fn.Line, Message: fmt.Sprintf("unknown preserve register %s", preserve)})
			continue
		}
		if !targetAllowsRegister(target, reg) {
			issues = append(issues, Issue{Severity: "error", Code: "register-target-mismatch", File: path, Line: fn.Line, Message: fmt.Sprintf("preserve register %s is outside target %s", reg, defaultString(target, "any"))})
		}
	}
	return issues
}

func verifyDuplicateContractAtoms(path string, fn *Function) []Issue {
	var issues []Issue
	sections := []struct {
		name   string
		values []string
	}{
		{name: "facts", values: fn.Facts},
		{name: "clobbers", values: fn.Clobbers},
		{name: "preserves", values: fn.Preserves},
		{name: "stack", values: fn.Stack},
		{name: "control", values: fn.Control},
		{name: "requires", values: fn.Requires},
	}
	for _, section := range sections {
		seen := map[string]bool{}
		for _, atom := range contractFields(section.values) {
			if seen[atom] {
				issues = append(issues, Issue{Severity: "error", Code: "duplicate-contract-atom", File: path, Line: fn.Line, Message: fmt.Sprintf("%s repeats %s", section.name, atom)})
			}
			seen[atom] = true
		}
	}
	clobbers := setOf(fn.Clobbers)
	for _, preserve := range contractFields(fn.Preserves) {
		if preserve == "callee_saved" {
			continue
		}
		if clobbers[preserve] {
			continue
		}
		issues = append(issues, Issue{Severity: "error", Code: "preserve-without-clobber", File: path, Line: fn.Line, Message: fmt.Sprintf("preserves declares %s but clobbers does not", preserve)})
	}
	return issues
}

func verifyEntryFacts(path string, fn *Function) []Issue {
	var issues []Issue
	for _, fact := range fn.Facts {
		if !supportedMachineFact(fact) {
			issues = append(issues, Issue{Severity: "error", Code: "unsupported-entry-fact", File: path, Line: fn.Line, Message: fmt.Sprintf("unsupported EASM entry fact %q", fact)})
		}
	}
	for _, inst := range fn.Instructions {
		if inst.Pseudo && normalizeOp(inst.Op) == "state" && !supportedMachineFact(inst.Text) {
			issues = append(issues, Issue{Severity: "error", Code: "unsupported-state-assertion", File: path, Line: inst.Line, Message: fmt.Sprintf("unsupported EASM state assertion %q", inst.Text)})
		}
	}
	return issues
}

func verifyLabelContracts(path string, fn *Function, definedLabels map[string]bool) []Issue {
	var issues []Issue
	seen := map[string]int{}
	for _, contract := range fn.Labels {
		if contract.Name == "" || !isIdentifierLike(contract.Name) {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-label-contract", File: path, Line: contract.Line, Message: "label contract must name an internal assembly label"})
			continue
		}
		if firstLine, exists := seen[contract.Name]; exists {
			issues = append(issues, Issue{Severity: "error", Code: "duplicate-label-contract", File: path, Line: contract.Line, Message: fmt.Sprintf("label contract %s duplicates earlier contract on line %d", contract.Name, firstLine)})
			continue
		}
		seen[contract.Name] = contract.Line
		if _, ok := definedLabels[contract.Name]; !ok {
			issues = append(issues, Issue{Severity: "error", Code: "label-contract-without-label", File: path, Line: contract.Line, Message: fmt.Sprintf("label contract %s has no matching body label", contract.Name)})
		}
		if len(contract.Preconditions) == 0 {
			issues = append(issues, Issue{Severity: "error", Code: "empty-label-precondition", File: path, Line: contract.Line, Message: fmt.Sprintf("label contract %s must require at least one machine-state precondition", contract.Name)})
		}
		for _, pre := range contract.Preconditions {
			if supportedMachineFact(pre) {
				continue
			}
			reg := canonicalRegisterPrecondition(pre)
			if reg == "" || !isX86GPR(reg) {
				issues = append(issues, Issue{Severity: "error", Code: "unsupported-label-precondition", File: path, Line: contract.Line, Message: fmt.Sprintf("label contract %s uses unsupported machine-state precondition %q", contract.Name, pre)})
			}
		}
	}
	return issues
}

func labelContractMap(contracts []LabelContract) map[string]LabelContract {
	out := map[string]LabelContract{}
	for _, contract := range contracts {
		if contract.Name != "" {
			out[contract.Name] = contract
		}
	}
	return out
}

func bodyLabelDefinitions(instructions []Instruction) map[string]bool {
	out := map[string]bool{}
	for _, inst := range instructions {
		if inst.Label != "" {
			out[inst.Label] = true
		}
	}
	return out
}

func canonicalRegisterPreconditionSet(preconditions []string) map[string]bool {
	out := map[string]bool{}
	for _, pre := range preconditions {
		if reg := canonicalRegisterPrecondition(pre); reg != "" {
			out[reg] = true
		}
	}
	return out
}

func applyMachineFactPreconditions(state machineFactState, preconditions []string) machineFactState {
	out := machineFactState{LiveRegs: map[string]bool{}, KnownUInt: map[string]uint64{}, FS: state.FS, StackMod16: state.StackMod16, StackMod16Known: state.StackMod16Known}
	for reg := range state.LiveRegs {
		out.LiveRegs[reg] = true
	}
	for reg, value := range state.KnownUInt {
		out.KnownUInt[reg] = value
	}
	for _, pre := range preconditions {
		if expected, ok := segmentFactPrecondition(pre); ok {
			out.FS = expected
			continue
		}
		if mod, ok := stackMod16FactPrecondition(pre); ok {
			out.StackMod16 = mod
			out.StackMod16Known = true
			continue
		}
		if reg := canonicalRegisterPrecondition(pre); reg != "" && isX86GPR(reg) {
			out.LiveRegs[reg] = true
		}
	}
	return out
}

func applyMachineStateAssertion(state machineFactState, assertion string) machineFactState {
	if state.LiveRegs == nil {
		state.LiveRegs = map[string]bool{}
	}
	if state.KnownUInt == nil {
		state.KnownUInt = map[string]uint64{}
	}
	if expected, ok := segmentFactPrecondition(assertion); ok {
		state.FS = expected
	}
	if mod, ok := stackMod16FactPrecondition(assertion); ok {
		state.StackMod16 = mod
		state.StackMod16Known = true
	}
	return state
}

func supportedMachineFact(fact string) bool {
	if _, ok := segmentFactPrecondition(fact); ok {
		return true
	}
	_, ok := stackMod16FactPrecondition(fact)
	return ok
}

func withStackMod16(state machineFactState, mod int, known bool) machineFactState {
	state.StackMod16 = mod
	state.StackMod16Known = known
	return state
}

func segmentFactPrecondition(pre string) (string, bool) {
	pre = strings.ToLower(strings.TrimSpace(pre))
	pre = strings.ReplaceAll(pre, " ", "")
	pre = strings.TrimPrefix(pre, "%")
	for _, sep := range []string{":", "="} {
		if before, after, ok := strings.Cut(pre, sep); ok {
			before = strings.TrimPrefix(before, "%")
			if before == "fs" && (after == "host" || after == "guest") {
				return after, true
			}
		}
	}
	return "", false
}
