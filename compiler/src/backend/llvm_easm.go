//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
static LLVMValueRef elisacoreGetInlineAsmLocal(LLVMTypeRef Ty, const char* AsmString, size_t AsmStringSize, const char* Constraints, size_t ConstraintsSize, LLVMBool HasSideEffects, LLVMBool IsAlignStack) {
	return LLVMGetInlineAsm(Ty, (char*)AsmString, AsmStringSize, (char*)Constraints, ConstraintsSize, HasSideEffects, IsAlignStack, LLVMInlineAsmDialectATT, 0);
}
*/
import "C"

import (
	"elisacore/src/easm"
	"elisacore/src/semantic"
	"fmt"
	"os"
	"strings"
	"unsafe"
)

func (g *llvmGenerator) emitEASMModule(module *easm.Module) error {
	if module == nil {
		return nil
	}
	for i := range module.Functions {
		if err := g.emitEASMFunction(&module.Functions[i]); err != nil {
			return err
		}
	}
	return nil
}

// appendAppleX64SegmentReservations emits the .zerofill segment reservations that
// pair with the -segaddr link flags in targetLinkerArgs (native_exec.go), mirroring
// shadPS4's asm(".zerofill ...") in address_space.cpp/tls.cpp. Without these the
// segaddr flags are no-ops and the runtime MAP_FIXED reservations collide with
// dyld/Rosetta. Apple x86_64 only.
func (g *llvmGenerator) appendAppleX64SegmentReservations(targetTriple string) {
	// Opt-in (compile-time): the reservation segments make the guest's fixed
	// addresses real RW memory at load, which is required for the faithful
	// AddressSpace reservation (ELISA_AS_NATIVE_RESERVE) to map there without
	// hanging under Rosetta. But it changes the memory landscape for the default
	// byte-buffer path, so it is gated to match the live reservation.
	if os.Getenv("ELISACORE_X64_SEGMENT_RESERVE") == "" {
		return
	}
	triple := strings.ToLower(strings.TrimSpace(targetTriple))
	if !strings.Contains(triple, "darwin") && !strings.Contains(triple, "macos") {
		return
	}
	if !strings.Contains(triple, "x86_64") && !strings.Contains(triple, "amd64") {
		return
	}
	// Emit ONLY into the main program object. The runtime and debug-referee support
	// objects are linked into the same binary, and ld SUMS identically-named zerofill
	// sections across objects -- so emitting here in every module would multiply the
	// reserved sizes (e.g. USER_AREA would double to 210TB, extend above image_base,
	// and overlap the binary's own segments). address_space_arena is defined only in
	// the main program's core/address_space.elisa, so it identifies the main module.
	if g.result == nil || g.result.GlobalScope == nil {
		return
	}
	if _, ok := g.result.GlobalScope.Lookup("address_space_arena"); !ok {
		return
	}
	directives := ".zerofill TCB_SPACE,TCB_SPACE,__tcb_space,0x3FC000\n" +
		".zerofill SYSTEM_MANAGED,SYSTEM_MANAGED,__SYSTEM_MANAGED,0x7FFBFC000\n" +
		".zerofill SYSTEM_RESERVED,SYSTEM_RESERVED,__SYSTEM_RESERVED,0x7C0004000\n" +
		".zerofill USER_AREA,USER_AREA,__USER_AREA,0x5F9000000000"
	cAsm := cString(directives)
	defer C.free(unsafe.Pointer(cAsm))
	C.LLVMAppendModuleInlineAsm(g.module, cAsm, C.size_t(len(directives)))
}

func (g *llvmGenerator) emitEASMFunction(fn *easm.Function) error {
	if fn == nil {
		return nil
	}
	_, semanticType, err := g.lowerEASMFunctionType(fn)
	if err != nil {
		return fmt.Errorf("EASM %s: %w", fn.Name, err)
	}
	if err := g.validateEASMFunctionEffects(fn); err != nil {
		return fmt.Errorf("EASM %s: %w", fn.Name, err)
	}
	value, err := g.ensureFunctionDeclared(fn.Name, semanticType)
	if err != nil {
		return fmt.Errorf("EASM %s extern signature mismatch: %w", fn.Name, err)
	}
	if C.LLVMCountBasicBlocks(value) != 0 {
		return fmt.Errorf("EASM %s conflicts with an already-defined function", fn.Name)
	}
	entry := C.LLVMAppendBasicBlockInContext(g.context, value, cStringFree("entry"))
	builder := C.LLVMCreateBuilderInContext(g.context)
	defer C.LLVMDisposeBuilder(builder)
	C.LLVMPositionBuilderAtEnd(builder, entry)

	asmText := easmInlineAsmText(fn)
	constraints := easmInlineAsmConstraints(fn)
	// Paranoia: prove the lowering communicates every clobber the validator relied on
	// to LLVM. A divergence here is a silent register/flags miscompile, so fail loudly.
	if err := verifyEASMConstraintsCoverClobbers(fn, constraints); err != nil {
		return err
	}
	asmC := cString(asmText)
	defer C.free(unsafe.Pointer(asmC))
	constraintsC := cString(constraints)
	defer C.free(unsafe.Pointer(constraintsC))
	inlineAsmType, err := g.easmInlineAsmFunctionType(fn, semanticType)
	if err != nil {
		return err
	}
	inlineAsm := C.elisacoreGetInlineAsmLocal(inlineAsmType, asmC, C.size_t(len(asmText)), constraintsC, C.size_t(len(constraints)), 1, easmStackAlignFlag(fn))
	args := make([]C.LLVMValueRef, len(fn.Params))
	for i := range fn.Params {
		args[i] = C.LLVMGetParam(value, C.unsigned(i))
	}
	if len(args) == 0 {
		i32Type := C.LLVMInt32TypeInContext(g.context)
		args = append(args, C.LLVMConstInt(i32Type, 0, 0))
	}
	callName := "easm.call"
	argPtr := llvmValueSlicePtr(args)
	var dummyArg C.LLVMValueRef
	if len(args) == 0 {
		argPtr = &dummyArg
	}
	call := C.LLVMBuildCall2(builder, inlineAsmType, inlineAsm, argPtr, C.unsigned(len(args)), cStringFree(callName))
	if easmHasControl(fn, "noreturn") {
		C.LLVMBuildUnreachable(builder)
		return nil
	}
	if strings.TrimSpace(fn.ReturnType) == "void" {
		C.LLVMBuildRetVoid(builder)
	} else {
		C.LLVMBuildRet(builder, call)
	}
	return nil
}

func (g *llvmGenerator) easmInlineAsmFunctionType(fn *easm.Function, declared *semantic.FuncType) (C.LLVMTypeRef, error) {
	if strings.TrimSpace(fn.ReturnType) != "void" && len(declared.Params) != 0 {
		return g.lowerFunctionType(declared)
	}
	params := append([]semantic.Type(nil), declared.Params...)
	if len(params) == 0 {
		params = append(params, &semantic.BuiltinType{Name: "i32"})
	}
	if strings.TrimSpace(fn.ReturnType) != "void" {
		dummy := &semantic.FuncType{Name: fn.Name + "$easm_inline", Params: params, Return: declared.Return, CallConv: declared.CallConv}
		return g.lowerFunctionType(dummy)
	}
	dummy := &semantic.FuncType{Name: fn.Name + "$easm_inline", Params: params, Return: &semantic.BuiltinType{Name: "i32"}, CallConv: declared.CallConv}
	return g.lowerFunctionType(dummy)
}

func (g *llvmGenerator) lowerEASMFunctionType(fn *easm.Function) (C.LLVMTypeRef, *semantic.FuncType, error) {
	params := make([]semantic.Type, 0, len(fn.Params))
	for _, param := range fn.Params {
		t, err := g.easmSemanticType(param.Type)
		if err != nil {
			return nil, nil, err
		}
		params = append(params, t)
	}
	ret, err := g.easmSemanticType(fn.ReturnType)
	if err != nil {
		return nil, nil, err
	}
	semanticType := &semantic.FuncType{Name: fn.Name, Params: params, Return: ret, CallConv: easmCallConv(fn.ABI)}
	llvmType, err := g.lowerFunctionType(semanticType)
	return llvmType, semanticType, err
}

func (g *llvmGenerator) validateEASMFunctionEffects(fn *easm.Function) error {
	required := easmDeclaredEffectPermissions(fn)
	if len(required) == 0 || g == nil || g.result == nil || g.result.GlobalScope == nil {
		return nil
	}
	sym, ok := g.result.GlobalScope.Lookup(fn.Name)
	if !ok || sym == nil {
		return nil
	}
	fnType, ok := sym.Type.(*semantic.FuncType)
	if !ok || fnType == nil {
		return nil
	}
	declared := map[string]bool{}
	for _, permission := range fnType.PermissionRefs {
		name := strings.TrimSpace(permission.Name)
		if permission.Member != "" {
			name += "." + strings.TrimSpace(permission.Member)
		}
		if name != "" {
			declared[name] = true
		}
	}
	var missing []string
	for _, permission := range required {
		if !declared[permission] {
			missing = append(missing, permission)
		}
	}
	if len(missing) != 0 {
		return fmt.Errorf("declares can[%s] but matching Elisa extern does not expose can[%s]", strings.Join(required, ", "), strings.Join(missing, ", "))
	}
	return nil
}

func easmDeclaredEffectPermissions(fn *easm.Function) []string {
	if fn == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(permission string) {
		permission = strings.TrimSpace(permission)
		if permission != "" && !seen[permission] {
			seen[permission] = true
			out = append(out, permission)
		}
	}
	for _, effect := range fn.Effects {
		effect = strings.TrimSpace(effect)
		effect = strings.TrimPrefix(effect, "can ")
		effect = strings.TrimPrefix(effect, "can[")
		effect = strings.TrimSuffix(effect, "]")
		if effect == "" || strings.HasPrefix(effect, "error") {
			continue
		}
		add(effect)
	}
	for _, req := range fn.Requires {
		switch strings.TrimSpace(req) {
		case "x86_64.segment.write":
			add("Unsafe.SegmentMutation")
		}
	}
	switch easmDeclaredFinalFSOwner(fn) {
	case "guest":
		add("Segment.Guest")
	case "host":
		add("Segment.Host")
	}
	return out
}

func easmDeclaredFinalFSOwner(fn *easm.Function) string {
	if fn == nil {
		return ""
	}
	owner := ""
	for _, inst := range easm.EmittedBody(fn) {
		if !inst.Pseudo || strings.ToLower(strings.TrimSpace(inst.Op)) != "state" {
			continue
		}
		text := strings.TrimSpace(inst.Text)
		lower := strings.ToLower(text)
		if !strings.HasPrefix(lower, "fs:") {
			continue
		}
		value := strings.TrimSpace(text[len("fs:"):])
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "guest", "host":
			owner = strings.ToLower(fields[0])
		}
	}
	return owner
}

func (g *llvmGenerator) easmSemanticType(name string) (semantic.Type, error) {
	name = strings.TrimSpace(name)
	switch name {
	case "void", "bool", "char", "int", "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "usize", "uintptr", "f32", "f64":
		if t, ok := g.result.NamedTypes[name]; ok {
			return t, nil
		}
		return &semantic.BuiltinType{Name: name}, nil
	default:
		if strings.HasSuffix(name, "&?") || strings.HasSuffix(name, "&") {
			return g.result.NamedTypes["void"], fmt.Errorf("EASM v1 only accepts primitive value types in signatures, got %s", name)
		}
		if t, ok := g.easmAddressSpaceCarrierType(name); ok {
			return t, nil
		}
		if t, ok := g.result.NamedTypes[name]; ok {
			return t, nil
		}
		return nil, fmt.Errorf("unknown EASM type %s", name)
	}
}

func (g *llvmGenerator) easmAddressSpaceCarrierType(name string) (semantic.Type, bool) {
	name = strings.TrimSpace(name)
	for _, spec := range []struct {
		prefix string
		space  string
	}{
		{prefix: "GuestVAddr[", space: "guest"},
		{prefix: "HostPtr[", space: "host"},
		{prefix: "NativeMappedGuestPtr[", space: "native_mapped_guest"},
	} {
		if !strings.HasPrefix(name, spec.prefix) || !strings.HasSuffix(name, "]") {
			continue
		}
		elemName := strings.TrimSpace(name[len(spec.prefix) : len(name)-1])
		elem, ok := g.result.NamedTypes[elemName]
		if !ok {
			return nil, false
		}
		return &semantic.AddressSpaceType{Space: spec.space, Elem: elem, Storage: g.result.NamedTypes["uintptr"]}, true
	}
	return nil, false
}

func easmCallConv(abi string) string {
	switch strings.ToLower(strings.TrimSpace(abi)) {
	case "", "c", "sysv", "sysv_x86_64", "ps4_sysv":
		return "c"
	default:
		return abi
	}
}

func easmInlineAsmText(fn *easm.Function) string {
	body := easm.EmittedBody(fn)
	lines := make([]string, 0, len(body))
	for _, inst := range body {
		lines = append(lines, inst.Text)
	}
	return strings.Join(lines, "\n")
}

func easmInlineAsmConstraints(fn *easm.Function) string {
	var parts []string
	boundRegisters := map[string]bool{}
	if strings.TrimSpace(fn.ReturnType) == "void" {
		parts = append(parts, "=r")
	} else {
		constraint, reg := easmOutputConstraint(fn)
		parts = append(parts, constraint)
		if reg != "" {
			boundRegisters[reg] = true
		}
	}
	for _, input := range fn.Inputs {
		constraint, reg := easmInputConstraint(input)
		parts = append(parts, constraint)
		if reg != "" {
			boundRegisters[reg] = true
		}
	}
	if len(fn.Params) == 0 {
		parts = append(parts, "r")
	}
	for len(parts) < outputConstraintCount(fn)+len(fn.Params) {
		parts = append(parts, "r")
	}
	for _, clobber := range fn.Clobbers {
		for _, item := range strings.FieldsFunc(clobber, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
			item = strings.ToLower(strings.TrimSpace(item))
			if item == "" {
				continue
			}
			if item == "memory" || item == "cc" {
				parts = append(parts, "~{"+item+"}")
				continue
			}
			if item == "flags" {
				// The EASM validator accepts 'flags' as a synonym for the 'cc'
				// condition-code clobber, but LLVM only recognises ~{cc} for EFLAGS.
				// Emitting ~{flags} would leave the flags effectively unclobbered.
				parts = append(parts, "~{cc}")
				continue
			}
			item = strings.TrimPrefix(item, "%")
			if boundRegisters[item] {
				continue
			}
			parts = append(parts, "~{"+item+"}")
		}
	}
	return strings.Join(parts, ",")
}

func outputConstraintCount(fn *easm.Function) int {
	return 1
}

func easmOutputConstraint(fn *easm.Function) (string, string) {
	for _, output := range fn.Outputs {
		if reg := registerAfterEquals(output); reg != "" {
			return "={" + reg + "}", reg
		}
	}
	return "=r", ""
}

func easmInputConstraint(input string) (string, string) {
	if reg := registerAfterEquals(input); reg != "" {
		return "{" + reg + "}", reg
	}
	fields := strings.Fields(input)
	if len(fields) > 0 {
		last := strings.TrimPrefix(strings.ToLower(fields[len(fields)-1]), "%")
		if isRegisterName(last) {
			return "{" + last + "}", last
		}
	}
	return "r", ""
}

func registerAfterEquals(value string) string {
	if i := strings.LastIndex(value, "="); i >= 0 {
		reg := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value[i+1:])), "%")
		if isRegisterName(reg) {
			return reg
		}
	}
	return ""
}

func isRegisterName(value string) bool {
	switch value {
	case "rax", "rbx", "rcx", "rdx", "rsi", "rdi", "rsp", "rbp", "r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15",
		"eax", "ebx", "ecx", "edx", "esi", "edi", "esp", "ebp":
		return true
	default:
		return false
	}
}

func easmHasControl(fn *easm.Function, control string) bool {
	for _, item := range fn.Control {
		for _, field := range strings.FieldsFunc(item, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
			if strings.EqualFold(field, control) {
				return true
			}
		}
	}
	return false
}

func easmStackAlignFlag(fn *easm.Function) C.LLVMBool {
	for _, item := range fn.Stack {
		if strings.Contains(strings.ToLower(item), "aligned") {
			return 1
		}
	}
	return 0
}
