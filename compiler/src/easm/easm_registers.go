package easm

import (
	"fmt"
	"strconv"
	"strings"
)

func stackMod16FactPrecondition(pre string) (int, bool) {
	pre = strings.ToLower(strings.TrimSpace(pre))
	pre = strings.ReplaceAll(pre, " ", "")
	pre = strings.TrimPrefix(pre, "%")
	if before, after, ok := strings.Cut(pre, ":"); ok {
		before = strings.TrimPrefix(before, "%")
		if before != "rsp" && before != "esp" && before != "sp" {
			return 0, false
		}
		switch after {
		case "aligned16", "call_aligned", "mod16=0":
			return 0, true
		case "entry_aligned", "mod16=8":
			return 8, true
		}
	}
	return 0, false
}

func canonicalRegisterPrecondition(pre string) string {
	pre = strings.TrimSpace(pre)
	if pre == "" {
		return ""
	}
	if before, _, ok := strings.Cut(pre, ":"); ok {
		pre = before
	}
	if before, _, ok := strings.Cut(pre, "="); ok {
		pre = before
	}
	pre = strings.TrimPrefix(strings.TrimSpace(pre), "%")
	return canonicalX86GPR(pre)
}

func checkLabelPreconditions(path string, line int, transferKind string, label string, preconditions []string, state machineFactState) []Issue {
	var issues []Issue
	for _, pre := range preconditions {
		if expected, ok := segmentFactPrecondition(pre); ok {
			if state.FS != expected {
				actual := defaultString(state.FS, "unknown")
				issues = append(issues, Issue{Severity: "error", Code: "label-precondition-unsatisfied", File: path, Line: line, Message: fmt.Sprintf("%s to label %s requires fs:%s but current fs state is %s", transferKind, label, expected, actual)})
			}
			continue
		}
		if expected, ok := stackMod16FactPrecondition(pre); ok {
			if !state.StackMod16Known {
				issues = append(issues, Issue{Severity: "error", Code: "label-precondition-unsatisfied", File: path, Line: line, Message: fmt.Sprintf("%s to label %s requires rsp mod 16 = %d but current stack alignment is unknown", transferKind, label, expected)})
			} else if state.StackMod16 != expected {
				issues = append(issues, Issue{Severity: "error", Code: "label-precondition-unsatisfied", File: path, Line: line, Message: fmt.Sprintf("%s to label %s requires rsp mod 16 = %d but current rsp mod 16 = %d", transferKind, label, expected, state.StackMod16)})
			}
			continue
		}
		reg := canonicalRegisterPrecondition(pre)
		if reg == "" {
			continue
		}
		if !state.LiveRegs[reg] {
			issues = append(issues, Issue{Severity: "error", Code: "label-precondition-unsatisfied", File: path, Line: line, Message: fmt.Sprintf("%s to label %s does not satisfy required live register %s", transferKind, label, reg)})
		}
	}
	return issues
}

func directControlTarget(op string, text string) string {
	if op != "jmp" && op != "jmpq" && !isConditionalJump(op) {
		return ""
	}
	operands := splitInstructionOperands(text)
	if len(operands) == 0 {
		fields := strings.Fields(text)
		if len(fields) < 2 {
			return ""
		}
		operands = []string{fields[len(fields)-1]}
	}
	target := strings.TrimSpace(operands[len(operands)-1])
	target = strings.TrimPrefix(target, "*")
	target = strings.TrimSuffix(target, ";")
	if !isIdentifierLike(target) {
		return ""
	}
	return target
}

func bindingName(value string) string {
	pieces := strings.SplitN(value, "=", 2)
	fields := strings.Fields(strings.TrimSpace(pieces[0]))
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
}

func registerAfterEquals(value string) string {
	pieces := strings.SplitN(value, "=", 2)
	if len(pieces) != 2 {
		return ""
	}
	reg := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(pieces[1])), "%")
	if isRegisterName(reg) {
		return reg
	}
	return ""
}

func isRegisterName(value string) bool {
	reg := strings.ToLower(strings.TrimPrefix(value, "%"))
	if isXMMRegister(reg) || isAArch64SIMDRegister(reg) {
		return true
	}
	switch reg {
	case "rax", "rbx", "rcx", "rdx", "rsi", "rdi", "rsp", "rbp", "r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15",
		"eax", "ebx", "ecx", "edx", "esi", "edi", "esp", "ebp",
		"al", "ah", "ax", "bl", "bh", "bx", "cl", "ch", "cx", "dl", "dh", "dx",
		"x0", "x1", "x2", "x3", "x4", "x5", "x6", "x7", "x8", "x9", "x10", "x11", "x12", "x13", "x14", "x15", "x16", "x17", "x18", "x19", "x20", "x21", "x22", "x23", "x24", "x25", "x26", "x27", "x28", "x29", "x30",
		"w0", "w1", "w2", "w3", "w4", "w5", "w6", "w7", "w8", "w9", "w10", "w11", "w12", "w13", "w14", "w15", "w16", "w17", "w18", "w19", "w20", "w21", "w22", "w23", "w24", "w25", "w26", "w27", "w28", "w29", "w30",
		"sp":
		return true
	default:
		return false
	}
}

func targetAllowsRegister(target string, reg string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	reg = strings.ToLower(strings.TrimPrefix(reg, "%"))
	if target == "" || target == "any" {
		return true
	}
	switch {
	case strings.Contains(target, "x86_64") || strings.Contains(target, "amd64"):
		return isX86Register(reg)
	case strings.Contains(target, "aarch64") || strings.Contains(target, "arm64"):
		return isAArch64Register(reg)
	default:
		return true
	}
}

func returnTypeAllowsRegister(returnType string, reg string) bool {
	returnType = strings.TrimSpace(returnType)
	reg = strings.ToLower(strings.TrimPrefix(reg, "%"))
	switch returnType {
	case "f32", "f64":
		return isXMMRegister(reg) || isAArch64SIMDRegister(reg)
	case "void":
		return false
	default:
		return isIntegerReturnRegister(reg)
	}
}

func isIntegerReturnRegister(reg string) bool {
	switch reg {
	case "rax", "eax", "ax", "al", "x0", "w0":
		return true
	default:
		return false
	}
}

func isX86Register(reg string) bool {
	if isXMMRegister(reg) {
		return true
	}
	return isX86GPR(reg)
}

func isX86GPR(reg string) bool {
	reg = canonicalX86GPR(reg)
	switch reg {
	case "rax", "rbx", "rcx", "rdx", "rsi", "rdi", "rsp", "rbp", "r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15":
		return true
	default:
		return false
	}
}

func isAArch64Register(reg string) bool {
	if reg == "sp" {
		return true
	}
	if isAArch64SIMDRegister(reg) {
		return true
	}
	if len(reg) < 2 {
		return false
	}
	prefix := reg[0]
	if prefix != 'x' && prefix != 'w' {
		return false
	}
	n, err := strconv.Atoi(reg[1:])
	return err == nil && n >= 0 && n <= 30
}

func isXMMRegister(reg string) bool {
	if !strings.HasPrefix(reg, "xmm") {
		return false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(reg, "xmm"))
	return err == nil && n >= 0 && n <= 31
}

func isAArch64SIMDRegister(reg string) bool {
	if len(reg) < 2 {
		return false
	}
	prefix := reg[0]
	if prefix != 's' && prefix != 'd' && prefix != 'v' && prefix != 'q' {
		return false
	}
	n, err := strconv.Atoi(reg[1:])
	return err == nil && n >= 0 && n <= 31
}

func writesStackPointer(text string) bool {
	parts := splitInstructionOperands(text)
	if len(parts) < 2 {
		return false
	}
	dst := strings.TrimSpace(parts[len(parts)-1])
	dst = strings.TrimPrefix(strings.ToLower(dst), "%")
	return dst == "rsp" || dst == "esp" || dst == "sp"
}

func stackPointerSourceRegister(text string) string {
	parts := splitInstructionOperands(text)
	if len(parts) < 2 || !writesStackPointer(text) {
		return ""
	}
	src := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(parts[0])), "%")
	if isRegisterName(src) {
		return canonicalX86GPR(src)
	}
	return ""
}

func usesStackRegister(text string) bool {
	for _, operand := range splitInstructionOperands(text) {
		for _, token := range registerTokens(operand) {
			switch token {
			case "rsp", "esp", "sp":
				return true
			}
		}
	}
	return false
}

func targetAllowsCapability(target string, capability string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	capability = strings.ToLower(strings.TrimSpace(capability))
	if target == "" || target == "any" || capability == "" {
		return true
	}
	switch {
	case strings.HasPrefix(capability, "x86_64."):
		return strings.Contains(target, "x86_64") || strings.Contains(target, "amd64")
	case strings.HasPrefix(capability, "aarch64.") || strings.HasPrefix(capability, "arm64."):
		return strings.Contains(target, "aarch64") || strings.Contains(target, "arm64")
	default:
		return true
	}
}

func isConditionalJump(op string) bool {
	switch op {
	case "je", "jz", "jne", "jnz", "jl", "jle", "jg", "jge", "jb", "jbe", "ja", "jae":
		return true
	default:
		return false
	}
}

func isSignedConditionalJump(op string) bool {
	switch op {
	case "jl", "jle", "jg", "jge":
		return true
	default:
		return false
	}
}

func isUnsignedConditionalJump(op string) bool {
	switch op {
	case "jb", "jbe", "ja", "jae":
		return true
	default:
		return false
	}
}

func instructionClobbersFlags(op string) bool {
	rule, ok := lookupOpRule(op)
	return ok && rule.ClobbersFlags
}

func implicitClobbers(op string) []string {
	if rule, ok := lookupOpRule(op); ok && len(rule.ImplicitClobbers) != 0 {
		return rule.ImplicitClobbers
	}
	return nil
}

// implicitUses returns the registers an instruction reads WITHOUT them appearing as an
// explicit operand -- e.g. cpuid consumes the leaf in eax and the subleaf in ecx. Such
// reads are invisible in the assembly text, so unless the validator knows about them it
// cannot tell whether the register holds a value the function established (a declared
// input or an earlier write) or an indeterminate value the caller happened to leave. The
// table is cross-checked against LLVM MC's implicit_uses in easm_mc_effects_test.go so it
// can never silently miss a read. Returned names are canonical 64-bit forms.
func implicitUses(op string) []string {
	if rule, ok := lookupOpRule(op); ok && len(rule.ImplicitReads) != 0 {
		return rule.ImplicitReads
	}
	return nil
}

// implicitResultDefines reports whether an instruction's implicit clobbers are defined
// results it writes (cpuid, rdtsc) rather than registers it trashes to an indeterminate
// value (a call's caller-saved set). Result writes establish the registers for later
// reads; trashing leaves them unreadable until re-established.
func implicitResultDefines(op string) bool {
	rule, ok := lookupOpRule(op)
	return ok && rule.ResultDefines
}

func callerSavedGPRs() []string {
	return []string{"rax", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10", "r11"}
}

func instructionReadsRegisterBeforeWriting(text string, reg string) bool {
	reg = canonicalX86GPR(reg)
	parts := splitInstructionOperands(text)
	if len(parts) == 0 {
		return false
	}
	readOperands := parts
	if len(parts) >= 2 {
		dst := strings.TrimSpace(parts[len(parts)-1])
		dstReg := canonicalX86GPR(strings.TrimPrefix(strings.ToLower(dst), "%"))
		if dstReg == reg && instructionOverwritesDestination(text) {
			readOperands = parts[:len(parts)-1]
		}
	}
	for _, operand := range readOperands {
		for _, token := range registerTokens(operand) {
			if canonicalX86GPR(token) == reg {
				return true
			}
		}
	}
	return false
}

func instructionOverwritesDestination(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	switch normalizeOp(fields[0]) {
	case "mov", "movq", "movl", "movw", "movb", "lea", "leaq", "leal", "pop", "popq":
		return true
	case "xor", "xorq":
		parts := splitInstructionOperands(text)
		return len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
	default:
		return isMoveExtendOp(normalizeOp(fields[0]))
	}
}

func isMoveExtendOp(op string) bool {
	switch op {
	case "movsx", "movsxd", "movsbw", "movsbl", "movsbq", "movswl", "movswq", "movslq",
		"movzx", "movzbw", "movzbl", "movzbq", "movzwl", "movzwq":
		return true
	default:
		return false
	}
}

// isSelfZeroingIdiom reports whether the instruction is a recognized constant-zeroing
// idiom (xorq %r,%r / subq %r,%r) whose result is independent of the register's prior
// value. Such an instruction establishes the register (to zero) without reading it.
func isSelfZeroingIdiom(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	switch normalizeOp(fields[0]) {
	case "xor", "xorq", "xorl", "xorw", "xorb", "sub", "subq", "subl", "subw", "subb":
		parts := splitInstructionOperands(text)
		return len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
	}
	return false
}

// registersReadBy returns the canonical GPRs whose value the instruction consumes: every
// source-operand register, plus the base/index registers of any memory operand (address
// computation reads them, even when that operand is the destination). It excludes a
// destination register that is purely written (mov/lea/pop) and the operands of a
// self-zeroing idiom, neither of which depends on the prior value. This is the read set the
// validator requires to be established -- a declared input, written earlier, or preserved.
func registersReadBy(text string) []string {
	parts := splitInstructionOperands(text)
	if len(parts) == 0 || isSelfZeroingIdiom(text) {
		return nil
	}
	overwrites := instructionOverwritesDestination(text)
	seen := map[string]bool{}
	var out []string
	addToken := func(tok string) {
		reg := canonicalX86GPR(tok)
		if isX86GPR(reg) && !seen[reg] {
			seen[reg] = true
			out = append(out, reg)
		}
	}
	for i, operand := range parts {
		isMemory := strings.Contains(operand, "(")
		isDest := i == len(parts)-1
		if !isMemory && isDest && overwrites {
			continue // destination written outright; its prior value is not read
		}
		for _, tok := range registerTokens(operand) {
			addToken(tok)
		}
	}
	return out
}

func writtenRegister(text string) string {
	regs := writtenRegisters(text)
	if len(regs) == 0 {
		return ""
	}
	return regs[0]
}

func writtenRegisters(text string) []string {
	parts := splitInstructionOperands(text)
	if len(parts) == 0 {
		return nil
	}
	fields := strings.Fields(text)
	op := ""
	if len(fields) > 0 {
		op = normalizeOp(fields[0])
	}
	if strings.HasPrefix(op, "push") || op == "call" || op == "callq" || op == "jmp" || op == "jmpq" || op == "ret" || op == "retq" {
		return nil
	}
	if op == "cmp" || op == "cmpq" || op == "test" || op == "testq" || isConditionalJump(op) {
		return nil
	}
	if op == "xchg" || op == "xchgl" || op == "xchgq" {
		seen := map[string]bool{}
		var out []string
		for _, part := range parts {
			reg := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(part)), "%")
			if !isRegisterName(reg) {
				continue
			}
			reg = canonicalX86GPR(reg)
			if seen[reg] {
				continue
			}
			seen[reg] = true
			out = append(out, reg)
		}
		return out
	}
	dst := strings.TrimSpace(parts[len(parts)-1])
	dst = strings.TrimPrefix(strings.ToLower(dst), "%")
	if isRegisterName(dst) {
		return []string{dst}
	}
	return nil
}

func updateKnownRegisterValue(state machineFactState, text string, reg string) {
	if state.KnownUInt == nil {
		state.KnownUInt = map[string]uint64{}
	}
	delete(state.KnownUInt, reg)
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return
	}
	op := normalizeOp(fields[0])
	parts := splitInstructionOperands(text)
	if len(parts) < 2 {
		return
	}
	if (op == "mov" || op == "movq" || op == "movl") && canonicalX86GPR(strings.TrimPrefix(strings.TrimSpace(parts[len(parts)-1]), "%")) == reg {
		if value, ok := parseImmediateLiteral(parts[0]); ok {
			state.KnownUInt[reg] = value
		}
		return
	}
	if (op == "xor" || op == "xorq") && len(parts) == 2 {
		left := canonicalX86GPR(strings.TrimPrefix(strings.TrimSpace(parts[0]), "%"))
		right := canonicalX86GPR(strings.TrimPrefix(strings.TrimSpace(parts[1]), "%"))
		if left != "" && left == right && right == reg {
			state.KnownUInt[reg] = 0
		}
	}
}

func parseImmediateLiteral(value string) (uint64, bool) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "$")
	value = strings.TrimPrefix(value, "#")
	value = strings.TrimSuffix(value, ";")
	value = strings.ReplaceAll(value, "_", "")
	if value == "" || strings.HasPrefix(value, "-") {
		return 0, false
	}
	parsed, err := strconv.ParseUint(value, 0, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func pushedKnownValue(state machineFactState, text string) (uint64, bool) {
	parts := splitInstructionOperands(text)
	if len(parts) != 1 {
		return 0, false
	}
	operand := strings.TrimSpace(parts[0])
	if value, ok := parseImmediateLiteral(operand); ok {
		return value, true
	}
	reg := canonicalX86GPR(strings.TrimPrefix(operand, "%"))
	if reg == "" {
		return 0, false
	}
	value, ok := state.KnownUInt[reg]
	return value, ok
}

func isNonCanonicalX86Address(value uint64) bool {
	sign := (value >> 47) & 1
	upper := value >> 48
	if sign == 0 {
		return upper != 0
	}
	return upper != 0xffff
}
