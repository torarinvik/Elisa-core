package easm

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

func verifyFunction(path string, target string, fn *Function, layouts map[string]*Layout) []Issue {
	var issues []Issue
	if fn.Name == "" {
		return append(issues, Issue{Severity: "error", Code: "missing-name", File: path, Line: fn.Line, Message: "EASM export is missing a symbol name"})
	}
	stackSet := setOf(fn.Stack)
	controlSet := setOf(fn.Control)
	clobberSet := setOf(fn.Clobbers)
	requireSet := setOf(fn.Requires)
	preserveSet := setOf(fn.Preserves)
	outputSet := outputRegisterSet(fn.Outputs)
	returnReg := returnOutputRegister(fn.Outputs)
	inputRegs := inputRegisterSet(fn.Inputs)
	inputRegNames := inputRegisterNameSet(fn.Inputs)
	labelContracts := labelContractMap(fn.Labels)
	definedLabels := bodyLabelDefinitions(fn.Instructions)
	seenLabelDefinitions := map[string]int{}
	state := machineFactState{LiveRegs: map[string]bool{}, KnownUInt: map[string]uint64{}}
	for reg := range inputRegs {
		state.LiveRegs[reg] = true
	}
	state = applyMachineFactPreconditions(state, fn.Facts)
	returnsVoid := strings.TrimSpace(fn.ReturnType) == "void"
	stackDelta := 0
	maxEntryStackPopDelta := 0
	stackMod16 := 8
	stackMod16Known := strings.Contains(strings.ToLower(target), "x86_64") || strings.Contains(strings.ToLower(target), "amd64")
	state.StackMod16 = stackMod16
	state.StackMod16Known = stackMod16Known
	maxStackAllocation := 0
	touchesStack := false
	mutatesStack := false
	usesCall := false
	usesJmp := false
	usesRet := false
	writesSP := false
	writesSPFromOwnedInput := false
	writesSegment := false
	mayFault := false
	flagsLive := false
	wrotePartialReturn := false
	directionFlagSet := false
	rdtscSeen := false
	lfenceBeforeRDTSC := false
	lfenceAfterRDTSC := false
	lfenceSeen := false
	clobberedByCall := map[string]int{}
	returnRegWritten := false
	inputRegRead := map[string]bool{}
	inputRegOverwritten := map[string]bool{}
	// Control-flow-merge bookkeeping (docs/104 increment 2, dataflow joins). We record the machine
	// state at every jump site (a predecessor edge of its target label) and at every label entry (the
	// state the linear walk carries into the label), plus whether the label is reachable by fallthrough.
	// After the walk, checkMergeConsistency verifies the walk's post-label state is justified by every
	// incoming edge -- the join condition a linear walk cannot enforce inline.
	mergeJumpPreds := map[string][]easmMergeSnap{}
	mergeLabelEntry := map[string]easmMergeSnap{}
	mergeFallReachable := map[string]bool{}
	prevFallsThrough := true
	for _, inst := range fn.Instructions {
		op := normalizeOp(inst.Op)
		if inst.Label != "" {
			if firstLine, exists := seenLabelDefinitions[inst.Label]; exists {
				issues = append(issues, Issue{Severity: "error", Code: "duplicate-label", File: path, Line: inst.Line, Message: fmt.Sprintf("label %s duplicates earlier label on line %d", inst.Label, firstLine)})
			} else {
				seenLabelDefinitions[inst.Label] = inst.Line
			}
			// Snapshot the state the linear walk carries into this label, and whether the textual
			// predecessor falls through to it. A label itself flows into the following instruction.
			mergeLabelEntry[inst.Label] = easmMergeSnap{live: easmCopyLive(state.LiveRegs), fs: state.FS, stackMod16: stackMod16, stackMod16Known: stackMod16Known, known: easmCopyKnownUInt(state.KnownUInt), line: inst.Line}
			if prevFallsThrough {
				mergeFallReachable[inst.Label] = true
			}
			prevFallsThrough = true
			if contract, ok := labelContracts[inst.Label]; ok {
				state = withStackMod16(state, stackMod16, stackMod16Known)
				issues = append(issues, checkLabelPreconditions(path, inst.Line, "fallthrough", inst.Label, contract.Preconditions, state)...)
				state = applyMachineFactPreconditions(machineFactState{}, contract.Preconditions)
				stackMod16 = state.StackMod16
				stackMod16Known = state.StackMod16Known
			}
			continue
		}
		if inst.Pseudo && normalizeOp(inst.Op) == "state" {
			state = applyMachineStateAssertion(state, inst.Text)
			if stack, ok := stackMod16FactPrecondition(inst.Text); ok {
				stackMod16 = stack
				stackMod16Known = true
			}
			continue
		}
		if inst.Pseudo {
			continue
		}
		if !allowedOps[op] && !isConditionalJump(op) {
			issues = append(issues, Issue{Severity: "error", Code: "unsupported-instruction", File: path, Line: inst.Line, Message: fmt.Sprintf("unsupported EASM instruction %q", inst.Op)})
		}
		// Totality of the declared transition relation: an allowed opcode reaching the walker without
		// a declared rule is a relation gap, not a program error -- the verifier would otherwise mutate
		// machine state for it ad-hoc. Conditional jumps are a recognized family without a table row.
		if allowedOps[op] {
			if _, ok := lookupOpRule(op); !ok {
				issues = append(issues, Issue{Severity: "error", Code: "opcode-rule-missing", File: path, Line: inst.Line, Message: fmt.Sprintf("opcode %q is allowed but has no declared transition rule", inst.Op)})
			}
		}
		if cap := opCapability(op); cap != "" && !requireSet[cap] {
			issues = append(issues, Issue{Severity: "error", Code: "missing-capability", File: path, Line: inst.Line, Message: fmt.Sprintf("instruction %q requires capability %s", inst.Op, cap)})
		}
		if usesStackRegister(inst.Text) || strings.HasPrefix(op, "push") || strings.HasPrefix(op, "pop") {
			touchesStack = true
		}
		if writesMemory(inst.Text) && !clobberSet["memory"] && !clobberSet["memory.write"] {
			issues = append(issues, Issue{Severity: "error", Code: "memory-write-without-clobber", File: path, Line: inst.Line, Message: "memory write requires a memory or memory.write clobber"})
		}
		if readsMemory(inst.Text) && !clobberSet["memory"] && !clobberSet["memory.read"] {
			issues = append(issues, Issue{Severity: "error", Code: "memory-read-without-clobber", File: path, Line: inst.Line, Message: "memory load requires a memory or memory.read clobber"})
		}
		if hasMemoryOperand(inst.Text) || strings.HasPrefix(op, "call") || op == "trap" {
			mayFault = true
		}
		if hasAmbiguousOperandSize(op, inst.Text) && !requireSet["operand_size.inferred"] {
			issues = append(issues, Issue{Severity: "error", Code: "ambiguous-operand-size", File: path, Line: inst.Line, Message: fmt.Sprintf("instruction %q must use an explicit size suffix or require operand_size.inferred", inst.Op)})
		}
		if immediateOverflowsInstruction(op, inst.Text) && !requireSet["immediate.truncation"] {
			issues = append(issues, Issue{Severity: "error", Code: "immediate-truncation", File: path, Line: inst.Line, Message: "immediate does not fit the instruction width without truncation/sign-extension intent"})
		}
		if hasSuspiciousAbsoluteAddress(inst.Text) && !requireSet["fixed_address"] {
			issues = append(issues, Issue{Severity: "error", Code: "hard-coded-address", File: path, Line: inst.Line, Message: "hard-coded absolute address requires fixed_address capability"})
		}
		if usesSymbolAddress(inst.Text) && !requireSet["relocation.symbol"] && !requireSet["pic"] {
			issues = append(issues, Issue{Severity: "error", Code: "symbol-relocation-intent-missing", File: path, Line: inst.Line, Message: "symbol address/value use requires relocation.symbol or pic intent"})
		}
		for _, seg := range slices.Sorted(maps.Keys(segmentOverridesUsed(inst.Text))) { // map: sort for a stable report order
			if !hasSegmentCapability(requireSet, seg) {
				issues = append(issues, Issue{Severity: "error", Code: "segment-access-intent-missing", File: path, Line: inst.Line, Message: fmt.Sprintf("%s segment access requires x86_64.segment.%s or x86_64.segment", seg, seg)})
			}
		}
		for _, seg := range slices.Sorted(maps.Keys(segmentRegistersUsed(inst.Text))) { // map: sort for a stable report order
			if !hasSegmentCapability(requireSet, seg) {
				issues = append(issues, Issue{Severity: "error", Code: "segment-register-intent-missing", File: path, Line: inst.Line, Message: fmt.Sprintf("%s segment register use requires x86_64.segment.%s or x86_64.segment", seg, seg)})
			}
		}
		if writesSegmentRegister(inst.Text) && !requireSet["x86_64.segment.write"] {
			issues = append(issues, Issue{Severity: "error", Code: "segment-register-write-intent-missing", File: path, Line: inst.Line, Message: "writing fs/gs requires x86_64.segment.write intent"})
		}
		if writesSegmentRegister(inst.Text) && !clobberSet["memory"] {
			issues = append(issues, Issue{Severity: "error", Code: "segment-register-write-without-memory-clobber", File: path, Line: inst.Line, Message: "writing fs/gs changes TLS addressing and requires memory clobber"})
		}
		if writesSegmentRegister(inst.Text) {
			writesSegment = true
			state.FS = ""
		}
		// Every register an instruction reads must be established before this point -- a
		// declared input, written earlier in the body, a preserved callee-saved value, or a
		// structural machine register (rsp/rip). Reading anything else consumes whatever
		// indeterminate value the caller happened to leave. Registers a prior call trashed
		// are owned by the more specific caller-saved-use-after-call check below.
		for _, reg := range registersReadBy(inst.Text) {
			if reg == "rsp" || reg == "rip" {
				continue
			}
			if _, trashedByCall := clobberedByCall[reg]; trashedByCall {
				continue
			}
			if state.LiveRegs[reg] || preserveSet[reg] || preserveSet["callee_saved"] {
				continue
			}
			issues = append(issues, Issue{Severity: "error", Code: "register-read-uninitialized", File: path, Line: inst.Line, Message: fmt.Sprintf("register %s is read but not established (declare it as an input, write it earlier, or preserve it)", reg)})
		}
		for _, written := range writtenRegisters(inst.Text) {
			canonical := canonicalX86GPR(written)
			if isX86GPR(canonical) {
				state.LiveRegs[canonical] = true
				updateKnownRegisterValue(state, inst.Text, canonical)
			}
			if returnReg != "" && canonical == returnReg {
				returnRegWritten = true
			}
			if inputRegs[canonical] && !inputRegRead[canonical] {
				inputRegOverwritten[canonical] = true
			}
			if isX86GPR(canonical) && !clobberSet[canonical] && !outputSet[canonical] {
				issues = append(issues, Issue{Severity: "error", Code: "register-write-without-clobber", File: path, Line: inst.Line, Message: fmt.Sprintf("register %s is written but not declared as a clobber or output", canonical)})
			}
		}
		if isIndirectControlTransfer(op, inst.Text) && !requireSet["control.indirect"] {
			issues = append(issues, Issue{Severity: "error", Code: "indirect-control-intent-missing", File: path, Line: inst.Line, Message: "indirect call/jmp requires control.indirect intent"})
		}
		if isIndirectControlTransfer(op, inst.Text) && !requireSet["control.tiny_target.unchecked"] {
			if reg := indirectControlTargetRegister(inst.Text); reg != "" {
				if value, ok := state.KnownUInt[reg]; ok && value > 0 && value < 0x10000 {
					issues = append(issues, Issue{Severity: "error", Code: "tiny-indirect-control-target", File: path, Line: inst.Line, Message: fmt.Sprintf("indirect %s target %s is known tiny value 0x%x; require control.tiny_target.unchecked only for intentional sentinels", op, reg, value)})
				}
			}
		}
		if isIndirectControlTransfer(op, inst.Text) && !requireSet["control.poison_target.unchecked"] {
			if reg := indirectControlTargetRegister(inst.Text); reg != "" {
				if value, ok := state.KnownUInt[reg]; ok && isNonCanonicalX86Address(value) {
					issues = append(issues, Issue{Severity: "error", Code: "poison-indirect-control-target", File: path, Line: inst.Line, Message: fmt.Sprintf("indirect %s target %s is known non-canonical poison-like value 0x%x; require control.poison_target.unchecked only for intentional tests", op, reg, value)})
				}
			}
		}
		if isDirectSymbolControlTransfer(op, inst.Text) && !definedLabels[directControlTarget(op, inst.Text)] && !requireSet["control.direct"] && !requireSet["relocation.symbol"] {
			issues = append(issues, Issue{Severity: "error", Code: "direct-control-intent-missing", File: path, Line: inst.Line, Message: "direct symbolic call/jmp requires control.direct or relocation.symbol intent"})
		}
		if writesPartialReturnRegister(inst.Text) {
			wrotePartialReturn = true
		}
		if usesReservedRegister(target, inst.Text, requireSet) {
			issues = append(issues, Issue{Severity: "error", Code: "reserved-register-use", File: path, Line: inst.Line, Message: "target-reserved register requires an explicit platform capability"})
		}
		// Implicit reads (e.g. cpuid's leaf in eax / subleaf in ecx) are invisible in the
		// operand text. Require each to be a declared input or written earlier so a function
		// cannot silently consume an indeterminate caller-left value. Checked before the
		// implicit-clobber pass below, which then marks the same registers dead for
		// subsequent instructions.
		for _, reg := range implicitUses(op) {
			canonical := canonicalX86GPR(reg)
			if inputRegs[canonical] {
				inputRegRead[canonical] = true
				continue
			}
			if state.LiveRegs[canonical] {
				continue
			}
			issues = append(issues, Issue{Severity: "error", Code: "implicit-read-uninitialized", File: path, Line: inst.Line, Message: fmt.Sprintf("instruction %q implicitly reads %s, which is not a declared input or written earlier in the body", inst.Op, canonical)})
		}
		for _, reg := range implicitClobbers(op) {
			canonical := canonicalX86GPR(reg)
			if implicitResultDefines(op) && isX86GPR(canonical) {
				state.LiveRegs[canonical] = true // instruction writes a defined result here
			} else {
				delete(state.LiveRegs, canonical)
			}
			delete(state.KnownUInt, canonical)
			if returnReg != "" && canonicalX86GPR(reg) == returnReg {
				returnRegWritten = true
			}
			if !clobberSet[reg] {
				issues = append(issues, Issue{Severity: "error", Code: "implicit-clobber-missing", File: path, Line: inst.Line, Message: fmt.Sprintf("instruction %q implicitly clobbers %s", inst.Op, reg)})
			}
		}
		if usesHighByteRegister(inst.Text) && !requireSet["x86_64.legacy_high_byte"] {
			issues = append(issues, Issue{Severity: "error", Code: "high-byte-register", File: path, Line: inst.Line, Message: "high-byte registers require x86_64.legacy_high_byte because they interact poorly with modern x86-64 encodings"})
		}
		if len(clobberedByCall) != 0 && !requireSet["call.caller_saved_liveness.unchecked"] {
			for _, reg := range callerSavedGPRs() {
				if callLine, ok := clobberedByCall[reg]; ok && instructionReadsRegisterBeforeWriting(inst.Text, reg) {
					issues = append(issues, Issue{Severity: "error", Code: "caller-saved-use-after-call", File: path, Line: inst.Line, Message: fmt.Sprintf("register %s is read after call on line %d; caller-saved registers must be reloaded or explicitly unchecked", reg, callLine)})
				}
			}
		}
		for reg := range inputRegs {
			if !inputRegRead[reg] && !inputRegOverwritten[reg] && instructionReadsRegisterBeforeWriting(inst.Text, reg) {
				inputRegRead[reg] = true
			}
		}
		if instructionClobbersFlags(op) && !clobberSet["cc"] && !clobberSet["flags"] {
			issues = append(issues, Issue{Severity: "error", Code: "cc-clobber-missing", File: path, Line: inst.Line, Message: fmt.Sprintf("instruction %q changes condition codes but clobbers does not include cc or flags", inst.Op)})
		}
		switch {
		case strings.HasPrefix(op, "push"):
			mutatesStack = true
			state.StackTopValue, state.StackTopKnown = pushedKnownValue(state, inst.Text)
			stackDelta -= 8
			if stackMod16Known {
				stackMod16 = mod16(stackMod16 - 8)
			}
		case strings.HasPrefix(op, "pop"):
			mutatesStack = true
			state.StackTopKnown = false
			stackDelta += 8
			if stackDelta > maxEntryStackPopDelta {
				maxEntryStackPopDelta = stackDelta
			}
			if stackMod16Known {
				stackMod16 = mod16(stackMod16 + 8)
			}
		case op == "sub" || op == "subq":
			flagsLive = false
			if usesStackRegister(inst.Text) {
				mutatesStack = true
				amount := immediateValue(inst.Text)
				stackDelta -= amount
				if stackMod16Known {
					stackMod16 = mod16(stackMod16 - amount)
				}
				if amount > maxStackAllocation {
					maxStackAllocation = amount
				}
			}
		case op == "add" || op == "addq":
			flagsLive = false
			if usesStackRegister(inst.Text) {
				mutatesStack = true
				amount := immediateValue(inst.Text)
				stackDelta += amount
				if stackDelta > maxEntryStackPopDelta {
					maxEntryStackPopDelta = stackDelta
				}
				if stackMod16Known {
					stackMod16 = mod16(stackMod16 + amount)
				}
			}
		case op == "and" || op == "andq":
			flagsLive = false
			if usesStackRegister(inst.Text) {
				mutatesStack = true
				stackDelta = 0
				if stackAndAlignsTo16(inst.Text) {
					stackMod16 = 0
					stackMod16Known = true
				} else {
					stackMod16Known = false
				}
			}
		case op == "mov" || op == "movq" || op == "lea":
			if writesStackPointer(inst.Text) {
				mutatesStack = true
				writesSP = true
				if param := inputRegNames[stackPointerSourceRegister(inst.Text)]; strings.Contains(param, "stack") {
					writesSPFromOwnedInput = true
				}
				stackMod16Known = false
			}
		case strings.HasPrefix(op, "call"):
			if stackMod16Known && stackMod16 != 0 && !requireSet["stack.call_alignment.unchecked"] {
				issues = append(issues, Issue{Severity: "error", Code: "call-stack-misaligned", File: path, Line: inst.Line, Message: fmt.Sprintf("call executes with symbolic rsp mod 16 = %d; SysV callers must align rsp to 16 before call", stackMod16)})
			}
			if !stackMod16Known && !requireSet["stack.call_alignment.unchecked"] {
				issues = append(issues, Issue{Severity: "error", Code: "call-stack-alignment-unknown", File: path, Line: inst.Line, Message: "call executes after an untracked stack-pointer write; require stack.call_alignment.unchecked only after manual ABI proof"})
			}
			usesCall = true
			flagsLive = false
			for _, reg := range callerSavedGPRs() {
				clobberedByCall[reg] = inst.Line
				delete(state.LiveRegs, reg)
				delete(state.KnownUInt, reg)
			}
		case op == "jmp" || op == "jmpq":
			if writesSegment && state.FS == "" && !requireSet["x86_64.segment.state.unchecked"] {
				issues = append(issues, Issue{Severity: "error", Code: "segment-transfer-state-unknown", File: path, Line: inst.Line, Message: "control transfer after writing fs/gs requires an explicit machine-state assertion such as state fs: host or state fs: guest"})
			}
			if target := directControlTarget(op, inst.Text); target != "" {
				if contract, ok := labelContracts[target]; ok {
					state = withStackMod16(state, stackMod16, stackMod16Known)
					issues = append(issues, checkLabelPreconditions(path, inst.Line, "jmp", target, contract.Preconditions, state)...)
				}
				if !definedLabels[target] {
					usesJmp = true
				}
			} else {
				usesJmp = true
			}
		case op == "ret" || op == "retq":
			if writesSegment && state.FS == "" && !requireSet["x86_64.segment.state.unchecked"] {
				issues = append(issues, Issue{Severity: "error", Code: "segment-transfer-state-unknown", File: path, Line: inst.Line, Message: "return after writing fs/gs requires an explicit machine-state assertion such as state fs: host or state fs: guest"})
			}
			if state.StackTopKnown && state.StackTopValue > 0 && state.StackTopValue < 0x10000 && !requireSet["control.tiny_target.unchecked"] {
				issues = append(issues, Issue{Severity: "error", Code: "tiny-return-target", File: path, Line: inst.Line, Message: fmt.Sprintf("ret target is known tiny value 0x%x; require control.tiny_target.unchecked only for intentional sentinels", state.StackTopValue)})
			}
			if state.StackTopKnown && isNonCanonicalX86Address(state.StackTopValue) && !requireSet["control.poison_target.unchecked"] {
				issues = append(issues, Issue{Severity: "error", Code: "poison-return-target", File: path, Line: inst.Line, Message: fmt.Sprintf("ret target is known non-canonical poison-like value 0x%x; require control.poison_target.unchecked only for intentional tests", state.StackTopValue)})
			}
			usesRet = true
		case op == "std":
			directionFlagSet = true
		case op == "cld":
			directionFlagSet = false
		case op == "lfence":
			if rdtscSeen {
				lfenceAfterRDTSC = true
			} else {
				lfenceSeen = true
			}
		case op == "rdtsc":
			rdtscSeen = true
			lfenceBeforeRDTSC = lfenceSeen
		case op == "cmp" || op == "cmpq" || op == "test" || op == "testq":
			flagsLive = true
		case isConditionalJump(op):
			if target := directControlTarget(op, inst.Text); target != "" {
				if contract, ok := labelContracts[target]; ok {
					state = withStackMod16(state, stackMod16, stackMod16Known)
					issues = append(issues, checkLabelPreconditions(path, inst.Line, op, target, contract.Preconditions, state)...)
				}
			}
			if !flagsLive {
				issues = append(issues, Issue{Severity: "error", Code: "stale-flags-branch", File: path, Line: inst.Line, Message: "conditional jump uses flags that are not known live from cmp/test"})
			}
			if isSignedConditionalJump(op) && !requireSet["compare.signed"] {
				issues = append(issues, Issue{Severity: "error", Code: "signed-branch-intent-missing", File: path, Line: inst.Line, Message: "signed conditional jump requires compare.signed intent"})
			}
			if isUnsignedConditionalJump(op) && !requireSet["compare.unsigned"] {
				issues = append(issues, Issue{Severity: "error", Code: "unsigned-branch-intent-missing", File: path, Line: inst.Line, Message: "unsigned conditional jump requires compare.unsigned intent"})
			}
		case instructionClobbersFlags(op):
			flagsLive = false
		}
		for _, written := range writtenRegisters(inst.Text) {
			delete(clobberedByCall, canonicalX86GPR(written))
		}
		// Record a predecessor edge for any direct jump's target, and whether this instruction lets
		// control fall through to the textually-following one (jmp and ret terminate the straight-line path).
		if op == "jmp" || op == "jmpq" || isConditionalJump(op) {
			if tgt := directControlTarget(op, inst.Text); tgt != "" {
				mergeJumpPreds[tgt] = append(mergeJumpPreds[tgt], easmMergeSnap{live: easmCopyLive(state.LiveRegs), fs: state.FS, stackMod16: stackMod16, stackMod16Known: stackMod16Known, known: easmCopyKnownUInt(state.KnownUInt), line: inst.Line})
			}
		}
		prevFallsThrough = !(op == "jmp" || op == "jmpq" || op == "ret" || op == "retq")
	}
	issues = append(issues, checkMergeConsistency(path, fn, mergeJumpPreds, mergeLabelEntry, mergeFallReachable, labelContracts)...)
	if verifyFunctionStateHook != nil {
		verifyFunctionStateHook(path, fn, easmCopyMachineFactState(state))
	}
	issues = append(issues, verifyABI(path, fn)...)
	issues = append(issues, verifyContractTokens(path, fn)...)
	issues = append(issues, verifySignatureTypes(path, fn)...)
	issues = append(issues, verifyMachineRoleTypes(path, fn, requireSet, controlSet, layouts)...)
	issues = append(issues, verifyBindings(path, target, fn)...)
	issues = append(issues, verifyRegisterLists(path, target, fn)...)
	issues = append(issues, verifyDuplicateContractAtoms(path, fn)...)
	issues = append(issues, verifyEntryFacts(path, fn)...)
	issues = append(issues, verifyLabelContracts(path, fn, definedLabels)...)
	if len(fn.Instructions) == 0 {
		issues = append(issues, Issue{Severity: "error", Code: "missing-body", File: path, Line: fn.Line, Message: "EASM export must contain a body"})
	}
	if len(fn.Stack) == 0 {
		issues = append(issues, Issue{Severity: "error", Code: "missing-stack-contract", File: path, Line: fn.Line, Message: "EASM export must declare stack behavior"})
	}
	if len(fn.Control) == 0 {
		issues = append(issues, Issue{Severity: "error", Code: "missing-control-contract", File: path, Line: fn.Line, Message: "EASM export must declare control behavior"})
	}
	if controlSet["returns"] && controlSet["noreturn"] {
		issues = append(issues, Issue{Severity: "error", Code: "conflicting-control-contract", File: path, Line: fn.Line, Message: "EASM export cannot declare both returns and noreturn"})
	}
	if controlSet["tail_jumps"] && !usesJmp {
		issues = append(issues, Issue{Severity: "error", Code: "tail-jumps-without-jump", File: path, Line: fn.Line, Message: "tail_jumps control contract requires a jmp instruction"})
	}
	if controlSet["may_fault"] && !mayFault {
		issues = append(issues, Issue{Severity: "error", Code: "may-fault-without-faulting-op", File: path, Line: fn.Line, Message: "may_fault control contract requires memory, call, or trap behavior"})
	}
	if !returnsVoid && !hasReturnOutput(fn.Outputs) {
		issues = append(issues, Issue{Severity: "error", Code: "missing-return-output", File: path, Line: fn.Line, Message: "non-void EASM export must declare outputs: ret = <register>"})
	}
	if !returnsVoid && returnReg != "" && !returnRegWritten && !requireSet["return.register.preinitialized"] {
		issues = append(issues, Issue{Severity: "error", Code: "return-register-not-written", File: path, Line: fn.Line, Message: fmt.Sprintf("non-void EASM export declares ret = %s but the body does not write it", returnReg)})
	}
	if !requireSet["input.unused"] {
		for _, reg := range slices.Sorted(maps.Keys(inputRegs)) { // map: sort for a stable report order
			if !inputRegRead[reg] {
				issues = append(issues, Issue{Severity: "error", Code: "input-register-unused", File: path, Line: fn.Line, Message: fmt.Sprintf("input register %s is declared but not read before being overwritten or returning", reg)})
			}
		}
	}
	if returnsVoid && hasReturnOutput(fn.Outputs) {
		issues = append(issues, Issue{Severity: "error", Code: "void-return-output", File: path, Line: fn.Line, Message: "void EASM export cannot declare a ret output"})
	}
	for _, req := range fn.Requires {
		if !targetAllowsCapability(target, req) {
			issues = append(issues, Issue{Severity: "error", Code: "target-capability-mismatch", File: path, Line: fn.Line, Message: fmt.Sprintf("target %s does not allow capability %s", defaultString(target, "any"), req)})
		}
	}
	if touchesStack && !stackSet["synthetic"] && !stackSet["switches"] && !stackSet["unchanged"] && !stackSet["noreturn"] {
		issues = append(issues, Issue{Severity: "error", Code: "stack-effect-undeclared", File: path, Line: fn.Line, Message: "function touches stack but stack contract does not declare unchanged, synthetic, switches, or noreturn"})
	}
	if mutatesStack && !clobberSet["memory"] {
		issues = append(issues, Issue{Severity: "error", Code: "stack-without-memory-clobber", File: path, Line: fn.Line, Message: "stack mutation requires memory clobber"})
	}
	if usesCall && !stackSet["aligned"] && !stackSet["switches"] && !stackSet["synthetic"] {
		issues = append(issues, Issue{Severity: "error", Code: "call-without-stack-contract", File: path, Line: fn.Line, Message: "call requires an aligned, synthetic, or switching stack contract"})
	}
	if maxStackAllocation > 4096 && !stackSet["probed"] {
		issues = append(issues, Issue{Severity: "error", Code: "large-stack-adjust-without-probe", File: path, Line: fn.Line, Message: "large stack allocation requires stack: probed"})
	}
	if wrotePartialReturn && returnsWideInteger(fn.ReturnType) {
		issues = append(issues, Issue{Severity: "error", Code: "partial-register-return", File: path, Line: fn.Line, Message: "wide integer return is written through a partial return register"})
	}
	if directionFlagSet && controlSet["returns"] {
		issues = append(issues, Issue{Severity: "error", Code: "direction-flag-not-restored", File: path, Line: fn.Line, Message: "returning function leaves the x86 direction flag set; use cld before returning"})
	}
	if rdtscSeen && (!lfenceBeforeRDTSC || !lfenceAfterRDTSC) && !requireSet["x86_64.rdtsc.unordered"] {
		issues = append(issues, Issue{Severity: "error", Code: "rdtsc-without-fence", File: path, Line: fn.Line, Message: "rdtsc requires lfence before and after, or explicit x86_64.rdtsc.unordered"})
	}
	if controlSet["returns"] && stackSet["unchanged"] && stackDelta != 0 {
		issues = append(issues, Issue{Severity: "error", Code: "returning-stack-leak", File: path, Line: fn.Line, Message: fmt.Sprintf("returning function leaves symbolic stack delta %d", stackDelta)})
	}
	if maxEntryStackPopDelta > 0 && !stackSet["switches"] && !stackSet["synthetic"] && !requireSet["stack.entry_pop.unchecked"] {
		issues = append(issues, Issue{Severity: "error", Code: "entry-stack-pop", File: path, Line: fn.Line, Message: fmt.Sprintf("function pops %d bytes above its entry stack before proving ownership; use stack.entry_pop.unchecked only after ABI proof", maxEntryStackPopDelta)})
	}
	if controlSet["returns"] && stackSet["unchanged"] && writesSP {
		issues = append(issues, Issue{Severity: "error", Code: "stack-pointer-write-unchanged", File: path, Line: fn.Line, Message: "stack: unchanged function writes the stack pointer directly"})
	}
	if writesSPFromOwnedInput && !stackSet["owns"] {
		issues = append(issues, Issue{Severity: "error", Code: "stack-switch-without-ownership-contract", File: path, Line: fn.Line, Message: "writing rsp from a stack-like input requires stack: owns"})
	}
	if controlSet["returns"] && writesSegment && !requireSet["x86_64.segment.restore"] && !requireSet["x86_64.segment.persistent"] {
		issues = append(issues, Issue{Severity: "error", Code: "segment-register-return-without-lifetime-contract", File: path, Line: fn.Line, Message: "returning after writing fs/gs requires x86_64.segment.restore or x86_64.segment.persistent intent"})
	}
	if stackSet["synthetic"] && usesCall && !usesJmp {
		issues = append(issues, Issue{Severity: "error", Code: "guest-entry-call-mangles-stack", File: path, Line: fn.Line, Message: "synthetic stack handoff must tail jump instead of call"})
	}
	if controlSet["noreturn"] && usesRet {
		issues = append(issues, Issue{Severity: "error", Code: "noreturn-can-return", File: path, Line: fn.Line, Message: "noreturn function contains ret"})
	}
	if controlSet["noreturn"] && !terminalOpIs(fn.Instructions, "jmp", "jmpq", "trap") {
		issues = append(issues, Issue{Severity: "error", Code: "noreturn-missing-terminal", File: path, Line: fn.Line, Message: "noreturn function must end in jmp or trap"})
	}
	if controlSet["noreturn"] && terminalOpIs(fn.Instructions, "jmp", "jmpq") && !controlSet["tail_jumps"] {
		issues = append(issues, Issue{Severity: "error", Code: "noreturn-jump-without-tail-contract", File: path, Line: fn.Line, Message: "noreturn jmp requires tail_jumps control contract"})
	}
	if controlSet["returns"] && usesJmp && !controlSet["tail_jumps"] {
		issues = append(issues, Issue{Severity: "error", Code: "returning-unqualified-jump", File: path, Line: fn.Line, Message: "returning function contains jmp without tail_jumps control contract"})
	}
	if controlSet["returns"] && !usesRet {
		issues = append(issues, Issue{Severity: "error", Code: "returns-missing-ret", File: path, Line: fn.Line, Message: "returning function must contain ret"})
	}
	if controlSet["returns"] && usesRet && !terminalOpIs(fn.Instructions, "ret", "retq") {
		issues = append(issues, Issue{Severity: "error", Code: "return-not-terminal", File: path, Line: fn.Line, Message: "returning function must end with ret"})
	}
	if controlSet["returns"] && hasCallImmediatelyBeforeRet(fn.Instructions) && !requireSet["call.return_address_choreography.unchecked"] {
		issues = append(issues, Issue{Severity: "error", Code: "call-immediately-before-ret", File: path, Line: fn.Line, Message: "call followed immediately by ret requires explicit call.return_address_choreography.unchecked intent"})
	}
	if controlSet["tail_jumps"] && clobberSet["rbp"] && !requireSet["frame_pointer.handoff.unchecked"] {
		issues = append(issues, Issue{Severity: "error", Code: "tail-jump-frame-pointer-clobber", File: path, Line: fn.Line, Message: "tail-jump handoff clobbers rbp; preserve the incoming frame chain or require frame_pointer.handoff.unchecked after ABI proof"})
	}
	for _, reg := range []string{"rbx", "rbp", "r12", "r13", "r14", "r15"} {
		if clobberSet[reg] && !preserveSet[reg] && !preserveSet["callee_saved"] {
			issues = append(issues, Issue{Severity: "error", Code: "callee-saved-not-preserved", File: path, Line: fn.Line, Message: fmt.Sprintf("callee-saved register %s is clobbered without preservation contract", reg)})
		}
		if controlSet["returns"] && clobberSet[reg] && (preserveSet[reg] || preserveSet["callee_saved"]) && !requireSet["callee_saved.preservation.unchecked"] && !calleeSavedPushPopProven(fn.Instructions, reg) {
			issues = append(issues, Issue{Severity: "error", Code: "callee-saved-preservation-unproven", File: path, Line: fn.Line, Message: fmt.Sprintf("callee-saved register %s preservation must be proven by push/pop or require callee_saved.preservation.unchecked", reg)})
		}
	}
	_ = target
	issues = append(issues, verifyPreservationParametricGate(path, fn, hasErrorIssue(issues))...)
	return issues
}
