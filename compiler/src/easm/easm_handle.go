package easm

import (
	"fmt"
	"strings"
)

// Existential handle types (docs/104, TAL upgrade-ladder item 4).
//
// A routine that produces or consumes an opaque token (the `*_from_handle` family) deals in a value
// of existential type `∃α. α`: callers may carry the token around but must NOT inspect, dereference,
// or reinterpret its concrete representation. We model this surface as a new EASM carrier `Handle[K]`
// (an opaque, named handle type, parsed like `HostPtr[...]`). It is ABI-identical to an integer
// register value; the opacity is a verifier-only artifact, so routines that do not name the type are
// byte-for-byte unaffected.
//
// Three static rules are enforced (all additive — they only fire on routines that declare a
// `Handle[K]` parameter or return type):
//
//  1. handle-deref-forbidden — a register holding a `Handle[K]` value must never be used as a memory
//     base `(%reg)`. Opening the existential to read its representation is exactly the operation a
//     handle forbids.
//  2. handle-type-confusion — a `Handle[K]` value must never be reinterpreted as a `Handle[J]`
//     (K != J), nor as a concrete address-space carrier (`HostPtr[T]`/`GuestVAddr[T]`). Detected at
//     the declared boundaries: moving a handle-bearing register into the input register of a
//     differently-typed carrier/handle parameter, or returning a `Handle[K]` value where the routine
//     declares return type `Handle[J]`.
//  3. Handles flow only through declared boundaries: a value becomes a `Handle[K]` only by entering
//     through a `Handle[K]` parameter, and is consumed only at a matching `Handle[K]` boundary.
//
// The bulk of the logic lives here; easm.go contributes one small wiring hunk (verifyHandleTypes is
// invoked from verifyMachineRoleTypes) plus recognising `Handle[...]` in allowedSignatureType.

// isHandleCarrierType reports whether a signature type names an opaque handle, e.g. `Handle[Texture]`.
func isHandleCarrierType(name string) bool {
	return handleCarrierName(name) != ""
}

// handleCarrierName returns the K of a `Handle[K]` carrier, or "" if name is not a handle type.
func handleCarrierName(name string) string {
	name = strings.TrimSpace(name)
	const prefix = "Handle["
	if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, "]") && len(name) > len(prefix)+1 {
		inner := strings.TrimSpace(name[len(prefix) : len(name)-1])
		if inner != "" && !strings.ContainsAny(inner, "[]") {
			return inner
		}
	}
	return ""
}

// verifyHandleTypes enforces existential-handle discipline for a routine that declares any
// `Handle[K]` parameter or return type. Routines that name no handle type return immediately, so the
// pass is fully additive.
func verifyHandleTypes(path string, fn *Function) []Issue {
	// Map each parameter to its declared register and type.
	paramType := map[string]string{}
	for _, param := range fn.Params {
		paramType[strings.ToLower(strings.TrimSpace(param.Name))] = strings.TrimSpace(param.Type)
	}
	// inputRegType maps a canonical input register to the declared type of the parameter bound to it.
	inputRegType := map[string]string{}
	for _, input := range fn.Inputs {
		reg := registerAfterEquals(input)
		if reg == "" {
			continue
		}
		inputRegType[canonicalX86GPR(reg)] = paramType[strings.ToLower(bindingName(input))]
	}

	declaresHandle := isHandleCarrierType(fn.ReturnType)
	for _, t := range paramType {
		if isHandleCarrierType(t) {
			declaresHandle = true
		}
	}
	if !declaresHandle {
		return nil
	}

	// handleReg tracks the K of the live `Handle[K]` value currently in each canonical register.
	handleReg := map[string]string{}
	for reg, typ := range inputRegType {
		if k := handleCarrierName(typ); k != "" {
			handleReg[reg] = k
		}
	}

	var issues []Issue
	retType := handleCarrierName(fn.ReturnType)

	for _, inst := range fn.Instructions {
		if inst.Pseudo {
			updateHandleProvenance(handleReg, inst.Text)
			continue
		}
		// Rule 1: a handle value must never be a memory base (no opening the existential).
		for _, operand := range splitInstructionOperands(inst.Text) {
			if !operandIsMemory(operand) {
				continue
			}
			base := memoryBaseRegister(operand)
			if base == "" {
				continue
			}
			if k, ok := handleReg[base]; ok {
				issues = append(issues, Issue{Severity: "error", Code: "handle-deref-forbidden", File: path, Line: inst.Line, Message: fmt.Sprintf("memory base %%%s holds an opaque Handle[%s]; an existential handle cannot be dereferenced — pass or return it, or open it through a declared boundary", base, k)})
			}
		}
		// Rule 2: a handle moved into a differently-typed carrier/handle input register, or returned
		// where a different handle type is declared, is type confusion.
		issues = append(issues, handleConfusionForInstruction(path, inst, handleReg, inputRegType, retType)...)
		updateHandleProvenance(handleReg, inst.Text)
	}
	return issues
}

// handleConfusionForInstruction detects reinterpretation of a `Handle[K]` value at a typed boundary.
func handleConfusionForInstruction(path string, inst Instruction, handleReg map[string]string, inputRegType map[string]string, retType string) []Issue {
	fields := strings.Fields(inst.Text)
	if len(fields) == 0 {
		return nil
	}
	op := normalizeOp(fields[0])
	if op != "mov" && op != "movq" && op != "movl" && op != "movw" && op != "movb" {
		return nil
	}
	parts := splitInstructionOperands(inst.Text)
	if len(parts) < 2 {
		return nil
	}
	src := registerOperand(parts[0])
	dst := registerOperand(parts[1])
	if src == "" || dst == "" {
		return nil
	}
	srcK, ok := handleReg[src]
	if !ok {
		return nil
	}
	// Moving a Handle[K] into a register that names a different carrier/handle parameter reinterprets
	// the opaque token as something it is not.
	if dstType, bound := inputRegType[dst]; bound {
		if dstK := handleCarrierName(dstType); dstK != "" {
			if dstK != srcK {
				return []Issue{{Severity: "error", Code: "handle-type-confusion", File: path, Line: inst.Line, Message: fmt.Sprintf("Handle[%s] value moved into %%%s, which carries Handle[%s]; opaque handles of different kinds are not interchangeable", srcK, dst, dstK)}}
			}
		} else if isAddressSpaceCarrierType(dstType) {
			return []Issue{{Severity: "error", Code: "handle-type-confusion", File: path, Line: inst.Line, Message: fmt.Sprintf("Handle[%s] value moved into %%%s, which carries a concrete pointer %s; an opaque handle cannot be reinterpreted as a pointer", srcK, dst, dstType)}}
		}
	}
	// Returning a Handle[K] in rax where the routine declares a different handle return type.
	if retType != "" && retType != srcK && canonicalX86GPR(dst) == "rax" {
		return []Issue{{Severity: "error", Code: "handle-type-confusion", File: path, Line: inst.Line, Message: fmt.Sprintf("returns Handle[%s] but the routine declares Handle[%s]; opaque handles of different kinds are not interchangeable", srcK, retType)}}
	}
	return nil
}

// updateHandleProvenance propagates Handle[K] kinds across simple register moves, and clears the kind
// from any register that is overwritten by a non-handle-preserving instruction.
func updateHandleProvenance(handleReg map[string]string, text string) {
	written := writtenRegisters(text)
	if len(written) == 0 {
		return
	}
	fields := strings.Fields(text)
	parts := splitInstructionOperands(text)
	if len(fields) > 0 && len(parts) >= 2 {
		op := normalizeOp(fields[0])
		switch op {
		case "mov", "movq", "movl", "movw", "movb":
			dst := canonicalX86GPR(written[0])
			if src := registerOperand(parts[0]); src != "" {
				if k, ok := handleReg[src]; ok {
					handleReg[dst] = k
					return
				}
			}
		}
	}
	// Any other write erases the handle kind: the register no longer provably holds the token.
	for _, reg := range written {
		delete(handleReg, canonicalX86GPR(reg))
	}
}
