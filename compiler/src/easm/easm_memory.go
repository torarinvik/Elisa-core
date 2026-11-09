package easm

import (
	"strconv"
	"strings"
)

func calleeSavedPushPopProven(instructions []Instruction, reg string) bool {
	reg = canonicalX86GPR(reg)
	firstWrite := -1
	lastWrite := -1
	firstPush := -1
	lastPop := -1
	for i, inst := range instructions {
		op := normalizeOp(inst.Op)
		parts := splitInstructionOperands(inst.Text)
		if len(parts) == 1 {
			operand := canonicalX86GPR(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(parts[0])), "%"))
			if (op == "push" || op == "pushq") && operand == reg && firstPush < 0 {
				firstPush = i
			}
			if (op == "pop" || op == "popq") && operand == reg {
				lastPop = i
			}
		}
		if op != "pop" && op != "popq" && canonicalX86GPR(writtenRegister(inst.Text)) == reg {
			if firstWrite < 0 {
				firstWrite = i
			}
			lastWrite = i
		}
	}
	if firstWrite < 0 {
		return true
	}
	return firstPush >= 0 && firstPush < firstWrite && lastPop > lastWrite
}

func canonicalX86GPR(reg string) string {
	reg = strings.ToLower(strings.TrimPrefix(reg, "%"))
	switch reg {
	case "eax", "ax", "al", "ah":
		return "rax"
	case "ebx", "bx", "bl", "bh":
		return "rbx"
	case "ecx", "cx", "cl", "ch":
		return "rcx"
	case "edx", "dx", "dl", "dh":
		return "rdx"
	case "esi", "si", "sil":
		return "rsi"
	case "edi", "di", "dil":
		return "rdi"
	case "esp":
		return "rsp"
	case "ebp":
		return "rbp"
	default:
		for _, base := range []string{"r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15"} {
			if reg == base+"d" || reg == base+"w" || reg == base+"b" {
				return base
			}
		}
		return reg
	}
}

func usesHighByteRegister(text string) bool {
	lower := strings.ToLower(text)
	for _, reg := range []string{"%ah", "%bh", "%ch", "%dh"} {
		if strings.Contains(lower, reg) {
			return true
		}
	}
	return false
}

func hasAmbiguousOperandSize(op string, text string) bool {
	if strings.HasSuffix(op, "b") || strings.HasSuffix(op, "w") || strings.HasSuffix(op, "l") || strings.HasSuffix(op, "q") {
		return false
	}
	switch op {
	case "mov", "add", "sub", "and", "xor", "cmp", "test", "inc", "dec":
		return strings.Contains(text, "(") || strings.Contains(text, "$")
	default:
		return false
	}
}

func immediateOverflowsInstruction(op string, text string) bool {
	width := instructionWidthBits(op)
	if width == 0 {
		return false
	}
	for _, operand := range splitInstructionOperands(text) {
		value, ok := parseImmediate(operand)
		if !ok {
			continue
		}
		min := -(int64(1) << (width - 1))
		maxUnsigned := uint64(1)<<width - 1
		if value < 0 {
			if value < min {
				return true
			}
			continue
		}
		if uint64(value) > maxUnsigned {
			return true
		}
	}
	return false
}

func instructionWidthBits(op string) uint {
	switch {
	case strings.HasSuffix(op, "b"):
		return 8
	case strings.HasSuffix(op, "w"):
		return 16
	case strings.HasSuffix(op, "l"):
		return 32
	default:
		return 0
	}
}

func parseImmediate(operand string) (int64, bool) {
	operand = strings.TrimSpace(strings.TrimPrefix(operand, "$"))
	if operand == "" {
		return 0, false
	}
	if strings.HasPrefix(operand, "-") {
		value, err := strconv.ParseInt(operand, 0, 64)
		return value, err == nil
	}
	value, err := strconv.ParseUint(operand, 0, 64)
	if err != nil {
		return 0, false
	}
	if value > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1), true
	}
	return int64(value), true
}

func usesSymbolAddress(text string) bool {
	fields := strings.Fields(text)
	if len(fields) > 0 && (isConditionalJump(normalizeOp(fields[0])) || normalizeOp(fields[0]) == "jmp" || normalizeOp(fields[0]) == "jmpq" || strings.HasPrefix(normalizeOp(fields[0]), "call")) {
		return false
	}
	if len(fields) > 0 && normalizeOp(fields[0]) == "mrs" {
		return false
	}
	for _, operand := range splitInstructionOperands(text) {
		operand = strings.TrimSpace(strings.TrimPrefix(operand, "$"))
		operand = strings.TrimPrefix(operand, "*")
		if operand == "" || strings.HasPrefix(operand, "%") || strings.HasPrefix(operand, "0x") || strings.HasPrefix(operand, "-") {
			continue
		}
		if strings.Contains(operand, "(") {
			base := strings.TrimSpace(operand[:strings.Index(operand, "(")])
			return base != "" && !isNumericLiteral(base)
		}
		if isIdentifierLike(operand) {
			return true
		}
	}
	return false
}

func usesSegmentOverride(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "%fs:") || strings.Contains(lower, "%gs:") || strings.Contains(lower, "fs:") || strings.Contains(lower, "gs:")
}

func segmentOverridesUsed(text string) map[string]bool {
	lower := strings.ToLower(text)
	used := map[string]bool{}
	if strings.Contains(lower, "%fs:") || strings.Contains(lower, "fs:") {
		used["fs"] = true
	}
	if strings.Contains(lower, "%gs:") || strings.Contains(lower, "gs:") {
		used["gs"] = true
	}
	return used
}

func hasSegmentCapability(requireSet map[string]bool, seg string) bool {
	return requireSet["x86_64.segment"] || requireSet["x86_64.segment."+seg]
}

func usesSegmentRegister(text string) bool {
	return len(segmentRegistersUsed(text)) != 0
}

func segmentRegistersUsed(text string) map[string]bool {
	used := map[string]bool{}
	for _, operand := range splitInstructionOperands(text) {
		reg := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(operand)), "%")
		if reg == "fs" {
			used["fs"] = true
		}
		if reg == "gs" {
			used["gs"] = true
		}
	}
	return used
}

func writesSegmentRegister(text string) bool {
	parts := splitInstructionOperands(text)
	if len(parts) < 2 {
		return false
	}
	dst := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(parts[len(parts)-1])), "%")
	return dst == "fs" || dst == "gs"
}

func isIndirectControlTransfer(op string, text string) bool {
	if op != "jmp" && op != "jmpq" && op != "call" && op != "callq" {
		return false
	}
	parts := splitInstructionOperands(text)
	if len(parts) == 0 {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(parts[0]), "*")
}

func indirectControlTargetRegister(text string) string {
	parts := splitInstructionOperands(text)
	if len(parts) == 0 {
		return ""
	}
	target := strings.TrimSpace(parts[0])
	target = strings.TrimPrefix(target, "*")
	target = strings.TrimPrefix(target, "%")
	if isRegisterName(target) {
		return canonicalX86GPR(target)
	}
	return ""
}

func isDirectSymbolControlTransfer(op string, text string) bool {
	if op != "jmp" && op != "jmpq" && op != "call" && op != "callq" {
		return false
	}
	parts := splitInstructionOperands(text)
	if len(parts) == 0 {
		return false
	}
	target := strings.TrimSpace(parts[0])
	if target == "" || strings.HasPrefix(target, "*") || strings.HasPrefix(target, "%") || strings.HasPrefix(target, "$") || isNumericLiteral(target) {
		return false
	}
	return isIdentifierLike(target)
}

func stackAndAlignsTo16(text string) bool {
	parts := splitInstructionOperands(text)
	if len(parts) < 2 {
		return false
	}
	dst := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(parts[len(parts)-1])), "%")
	if dst != "rsp" && dst != "esp" {
		return false
	}
	value, ok := parseImmediate(parts[0])
	return ok && value == -16
}

func mod16(value int) int {
	value %= 16
	if value < 0 {
		value += 16
	}
	return value
}

func usesReservedRegister(target string, text string, requireSet map[string]bool) bool {
	target = strings.ToLower(target)
	switch {
	case strings.Contains(target, "aarch64") || strings.Contains(target, "arm64"):
		return instructionUsesRegister(text, "x18") && !requireSet["aarch64.platform_register.x18"]
	case strings.Contains(target, "riscv"):
		return (instructionUsesRegister(text, "gp") || instructionUsesRegister(text, "tp")) && !requireSet["riscv.reserved_registers"]
	default:
		return false
	}
}

func instructionUsesRegister(text string, reg string) bool {
	reg = strings.ToLower(strings.TrimPrefix(reg, "%"))
	for _, operand := range splitInstructionOperands(text) {
		for _, token := range registerTokens(operand) {
			if token == reg {
				return true
			}
		}
	}
	return false
}

func registerTokens(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r == '%' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	var out []string
	for _, field := range fields {
		field = strings.TrimPrefix(field, "%")
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func writesMemory(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	op := normalizeOp(fields[0])
	parts := splitInstructionOperands(text)
	if len(parts) == 1 {
		switch op {
		case "inc", "incq", "dec", "decq", "pop", "popq":
			return operandIsMemory(parts[0])
		case "fnstcw", "stmxcsr":
			return operandIsMemory(parts[0])
		}
		return false
	}
	if len(parts) < 2 {
		return false
	}
	if op == "xchg" || op == "xchgl" || op == "xchgq" {
		return hasMemoryOperand(text)
	}
	dst := strings.TrimSpace(parts[len(parts)-1])
	return strings.Contains(dst, "(") && strings.Contains(dst, ")")
}

func hasMemoryOperand(text string) bool {
	for _, operand := range splitInstructionOperands(text) {
		if strings.Contains(operand, "(") && strings.Contains(operand, ")") {
			return true
		}
	}
	return usesSegmentOverride(text)
}

// memoryBaseRegister returns the canonical base register of a memory operand of the form
// disp(base, index, scale), or "" if it has no GPR base (a segment-relative reference or an
// absolute address). Only the base is returned -- the index register is a scalar offset, not
// a pointer, so it is not subject to pointer-provenance typing.
// layoutTypeWidth maps a layout field's declared type to its size in bytes. Pointer-class carrier
// and role types are pointer-width (8); selectors are 16-bit. Returns (0,false) when the width is
// unknown, in which case the field's offset is still checked but its access width is not.
func layoutTypeWidth(typ string) (int, bool) {
	t := strings.TrimSpace(typ)
	switch t {
	case "u8", "i8", "bool", "char":
		return 1, true
	case "u16", "i16":
		return 2, true
	case "u32", "i32", "f32":
		return 4, true
	case "u64", "i64", "usize", "uintptr", "f64":
		return 8, true
	}
	if strings.Contains(t, "Selector") {
		return 2, true
	}
	if isAddressSpaceCarrierType(t) || isEASMRoleType(t) {
		return 8, true
	}
	return 0, false
}

// layoutElemName returns the element name of a layout-typed carrier (`HostPtr[Name]` ->
// "Name"), or "" if the type is not a carrier.
func layoutElemName(typ string) string {
	t := strings.TrimSpace(typ)
	for _, prefix := range []string{"GuestVAddr[", "HostPtr[", "NativeMappedGuestPtr["} {
		if strings.HasPrefix(t, prefix) && strings.HasSuffix(t, "]") {
			return strings.TrimSpace(t[len(prefix) : len(t)-1])
		}
	}
	return ""
}

// memoryDisplacement extracts the constant displacement of a `disp(base,...)` memory operand.
// Returns (0,true) for a bare `(%base)` and (0,false) for a symbolic/non-constant displacement,
// which cannot be statically typed against a layout.
func memoryDisplacement(operand string) (int64, bool) {
	open := strings.Index(operand, "(")
	if open < 0 {
		return 0, false
	}
	disp := strings.TrimSpace(operand[:open])
	if disp == "" {
		return 0, true
	}
	v, err := strconv.ParseInt(disp, 0, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// accessWidthBytes returns the memory access width implied by an AT&T mnemonic suffix
// (b/w/l/q -> 1/2/4/8). Returns (0,false) when there is no explicit width suffix.
func accessWidthBytes(op string) (int, bool) {
	switch {
	case strings.HasSuffix(op, "b"):
		return 1, true
	case strings.HasSuffix(op, "w"):
		return 2, true
	case strings.HasSuffix(op, "l"):
		return 4, true
	case strings.HasSuffix(op, "q"):
		return 8, true
	}
	return 0, false
}

func layoutFieldAt(layout *Layout, offset int64) (LayoutField, bool) {
	for _, f := range layout.Fields {
		if f.Offset == offset {
			return f, true
		}
	}
	return LayoutField{}, false
}

func memoryBaseRegister(operand string) string {
	open := strings.Index(operand, "(")
	if open < 0 {
		return ""
	}
	rel := strings.Index(operand[open:], ")")
	if rel < 0 {
		return ""
	}
	inner := operand[open+1 : open+rel]
	fields := strings.Split(inner, ",")
	base := strings.TrimPrefix(strings.TrimSpace(fields[0]), "%")
	if base == "" {
		return ""
	}
	reg := canonicalX86GPR(base)
	if !isX86GPR(reg) {
		return ""
	}
	return reg
}

// operandIsMemory reports whether a single operand denotes a memory location: a
// parenthesized base/index expression or a segment-relative reference.
func operandIsMemory(operand string) bool {
	if strings.Contains(operand, "(") && strings.Contains(operand, ")") {
		return true
	}
	return usesSegmentOverride(operand)
}

// readsMemory reports whether a two-or-more-operand instruction loads a value from memory:
// a memory source operand, or a memory destination that is read-modify-written (add/sub/...
// to memory). lea computes an address without touching memory; a pure store (mov to memory)
// writes without reading; and xchg with a memory operand both reads and writes it.
//
// Single-operand stack instructions (push/pop) are intentionally excluded: they are
// governed by the stack contract. Single-operand FPU-control memory forms still participate
// in memory direction checks: fldcw/ldmxcsr read memory, fnstcw/stmxcsr write memory.
func readsMemory(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	op := normalizeOp(fields[0])
	if op == "lea" || op == "leaq" || op == "leal" {
		return false
	}
	parts := splitInstructionOperands(text)
	if len(parts) == 1 {
		switch op {
		case "inc", "incq", "dec", "decq", "push", "pushq", "call", "callq", "jmp", "jmpq":
			return operandIsMemory(parts[0])
		case "fldcw", "ldmxcsr":
			return operandIsMemory(parts[0])
		}
		return false
	}
	if len(parts) < 2 {
		return false
	}
	if op == "xchg" || op == "xchgl" || op == "xchgq" {
		return hasMemoryOperand(text)
	}
	for i, operand := range parts {
		if !operandIsMemory(operand) {
			continue
		}
		if i != len(parts)-1 {
			return true // memory source operand -> load
		}
		if !instructionOverwritesDestination(text) {
			return true // read-modify-write of a memory destination
		}
	}
	return false
}

func hasSuspiciousAbsoluteAddress(text string) bool {
	for _, operand := range splitInstructionOperands(text) {
		operand = strings.TrimSpace(strings.ToLower(operand))
		if strings.HasPrefix(operand, "0x") && !strings.Contains(operand, "(") {
			return true
		}
		if strings.Contains(operand, "(0x") {
			return true
		}
	}
	return false
}

func isIdentifierLike(value string) bool {
	for i, r := range value {
		if i == 0 {
			if !(r == '_' || r == '.' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
				return false
			}
			continue
		}
		if !(r == '_' || r == '.' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return value != ""
}

func isNumericLiteral(value string) bool {
	_, err := strconv.ParseInt(value, 0, 64)
	if err == nil {
		return true
	}
	_, err = strconv.ParseUint(value, 0, 64)
	return err == nil
}
