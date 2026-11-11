//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"os"
	"strings"
	"unsafe"
)

// wasmImportModule reads the target-specific WIT namespace from the extern
// declaration. It is deliberately kept separate from LinkName: LLVM uses the
// latter for the core symbol and this string attribute for the component
// interface/package namespace.
func (g *llvmGenerator) wasmImportModule(name string) string {
	if g == nil || g.result == nil || !g.hasGlobalScope() {
		return ""
	}
	sym, ok := g.lookupGlobal(name)
	if !ok || sym == nil {
		return ""
	}
	decl, ok := sym.Node.(*ast.ExternFuncDecl)
	if !ok || decl == nil {
		return ""
	}
	for _, annotation := range decl.Annotations {
		if annotation.Name == "wasm_import_module" && len(annotation.Args) == 1 {
			return strings.TrimSpace(annotation.Args[0])
		}
	}
	return ""
}

func (g *llvmGenerator) llvmCallConvForFunc(fn *semantic.FuncType) (llvmIRUnsigned, bool) {
	if fn == nil {
		return 0, false
	}
	switch strings.ToLower(strings.TrimSpace(fn.CallConv)) {
	case "", "c":
		return llvmCCallConv, fn.CallConv != ""
	case "fast":
		return llvmFastCallConv, true
	case "cold":
		return llvmColdCallConv, true
	case "stdcall":
		return llvmX86StdcallCallConv, true
	case "winapi":
		triple := ""
		if g != nil {
			triple = strings.ToLower(g.requestedTargetTriple)
		}
		if strings.Contains(triple, "i386") || strings.Contains(triple, "i686") {
			return llvmX86StdcallCallConv, true
		}
		return llvmCCallConv, true
	default:
		return 0, false
	}
}
func (g *llvmGenerator) isDirectlyExportedFunction(name string) bool {
	if g == nil || g.result == nil {
		return false
	}
	for _, exported := range g.result.ExportedFuncs {
		if exported == nil {
			continue
		}
		if exported.PublicName == name && exported.TargetName == name && !g.sameNameExportNeedsWrapper(exported) {
			return true
		}
	}
	return false
}
func (g *llvmGenerator) setDefinedFunctionLinkage(name string, value llvmIRValueRef, fnType *semantic.FuncType) {
	if value == nil {
		return
	}
	linkage := llvmIRLinkage(llvmExternalLinkage)
	if g.preferPrivateLinkage && !g.isDirectlyExportedFunction(name) {
		linkage = llvmIRLinkage(llvmPrivateLinkage)
	}
	// `main` is the program entry point: the C runtime (crt0) calls it from outside the module,
	// so it must stay externally visible. Without this it gets private linkage at -O>0 and the
	// optimizer's GlobalDCE strips it (no in-module callers), breaking native exe links with
	// "undefined symbol _main".
	if name == "main" || g.isDirectlyExportedFunction(name) || g.isDefaultNativeRuntimeSupportExport(name) {
		linkage = llvmIRLinkage(llvmExternalLinkage)
	}
	llvmSetLinkage(value, linkage)
	// Every Elisa-defined function is `nounwind`: the language has no exceptions and the backend
	// emits no `invoke`/landingpad/personality — panics lower to `trap`/`unreachable` (noreturn) and
	// all cleanup (defer/drop) runs on straight-line normal-exit paths, so no body can unwind. The
	// attribute lets callers assume a `call` here cannot unwind, which removes EH bookkeeping and
	// unblocks optimizations (e.g. eliminating a call whose result is unused) — most valuable at the
	// call barriers LLVM can't see through (exported, indirect, separately compiled). (A foreign C++
	// callee that threw across the boundary would be UB, but Elisa has no landing pads to catch one
	// regardless — exceptions through Elisa frames already terminate — so this matches the model.)
	g.addNoUnwindAttribute(value)
	explicitInlineMode := false
	if fnType != nil {
		if fnType.HasInlineMode {
			explicitInlineMode = true
			switch fnType.InlineMode {
			case semantic.FuncInlineModeAlways:
				g.addAlwaysInlineAttribute(value)
			case semantic.FuncInlineModeNever:
				g.addNoInlineAttribute(value)
			}
		}
		g.applyFunctionNoRecurseAttributes(value, fnType)
		g.applyFunctionTemperatureAttributes(value, fnType)
		g.applyProfileTemperatureAttributes(name, value, fnType)
		g.applyFunctionSegmentSafetyAttributes(value, fnType)
	}
	if explicitInlineMode {
		return
	}
	if linkage == llvmIRLinkage(llvmPrivateLinkage) {
		if g.shouldNeverInlineDefinedFunction(name) {
			g.addNoInlineAttribute(value)
		}
	}
}

func (g *llvmGenerator) isDefaultNativeRuntimeSupportExport(name string) bool {
	if g == nil || g.result == nil || g.result.File == nil {
		return false
	}
	if !strings.HasSuffix(g.result.File.Filename, "native_runtime_support.elisa") {
		return false
	}
	// The packed-store helper families are declared directly by the backend (region-backed
	// packed enums, docs/74) in programs that do not include the std runtime themselves.
	// They must stay external here or those programs' declarations null-bind under
	// -undefined,dynamic_lookup and segfault on the first constructor/match.
	if strings.HasPrefix(name, "ctx_packed_store_") || strings.HasPrefix(name, "ctx_aos_store_") {
		return true
	}
	switch name {
	case "new_region",
		"new_region_backend",
		"free_region",
		"free_region_backend",
		"arena_alloc",
		"arena_realloc",
		"arena_reclaim_bytes",
		"arena_memcpy",
		"arena_memeq",
		"arena_strdup",
		"arena_memdup",
		"arena_snapshot",
		"arena_reset",
		"arena_rewind",
		"arena_free",
		"arena_adopt",
		"arena_trim",
		"arena_init_stack_block",
		"arena_profile_region_layout",
		"arena_profile_allocation_event",
		"alloc_perm",
		"alloc_scratch",
		"reset_scratch",
		"ctx_hash_cstr",
		"ctx_hash_u64",
		"ctx_strlen",
		"ctx_streq",
		"ctx_string_slice_eq",
		"ctx_string_slices_eq",
		"ctx_string_index",
		"ctx_string_slice",
		"ctx_string_view",
		"ctx_string_view_len",
		"ctx_string_view_slice",
		"ctx_string_view_prefix",
		"ctx_string_view_suffix",
		"ctx_string_view_index",
		"ctx_string_view_eq",
		"ctx_string_views_eq",
		"ctx_string_from_view",
		"ctx_fstr_alloc",
		"ctx_fstr_append",
		"ctx_llvm_codegen_fatal",
		"elisa_trace_record",
		"elisa_trace_record_value",
		"elisa_trace_count",
		"elisa_trace_dump",
		"elisa_trace_install_fault_handler":
		return true
	default:
		return false
	}
}
func (g *llvmGenerator) applyFunctionNoRecurseAttributes(fn llvmIRValueRef, fnType *semantic.FuncType) {
	if g == nil || fn == nil || fnType == nil || !fnType.HasNoRecurse {
		return
	}
	g.addNoRecurseAttribute(fn)
}
func (g *llvmGenerator) applyFunctionTemperatureAttributes(fn llvmIRValueRef, fnType *semantic.FuncType) {
	if g == nil || fn == nil || fnType == nil || !fnType.HasTemperatureMode {
		return
	}
	switch fnType.TemperatureMode {
	case semantic.FuncTemperatureModeHot:
		g.addHotAttribute(fn)
	case semantic.FuncTemperatureModeCold:
		g.addColdAttribute(fn)
	}
}

func profileHotFunctionsFromEnvironment() map[string]bool {
	result := make(map[string]bool)
	for _, name := range strings.Split(os.Getenv("ELISACORE_PGO_HOT_FUNCTIONS"), "\n") {
		name = strings.TrimSpace(name)
		if name != "" {
			result[name] = true
		}
	}
	return result
}

func (g *llvmGenerator) applyProfileTemperatureAttributes(name string, fn llvmIRValueRef, fnType *semantic.FuncType) {
	if g == nil || fn == nil || fnType == nil || fnType.HasTemperatureMode {
		return
	}
	if g.profileHotFunctions[name] {
		g.addHotAttribute(fn)
		return
	}
	// Trace records use the source-level name while generic LLVM functions carry a
	// specialization suffix such as `map__i64`. Let one profile entry guide all concrete
	// specializations without making the profile depend on mangling.
	if separator := strings.Index(name, "__"); separator > 0 && g.profileHotFunctions[name[:separator]] {
		g.addHotAttribute(fn)
	}
}
func (g *llvmGenerator) applyFunctionSegmentSafetyAttributes(fn llvmIRValueRef, fnType *semantic.FuncType) {
	if g == nil || fn == nil || fnType == nil {
		return
	}
	if fnType.HasSegmentAgnostic {
		g.addFunctionStringAttribute(fn, "elisacore.segment_agnostic", "true")
	}
	if fnType.HasAsyncEntry {
		g.addFunctionStringAttribute(fn, "elisacore.async_entry", "true")
	}
	if fnType.HasSegmentEstablishing {
		g.addFunctionStringAttribute(fn, "elisacore.segment_establishing", "true")
	}
}
func (g *llvmGenerator) addAlwaysInlineAttribute(fn llvmIRValueRef) {
	if g == nil || g.context == nil || fn == nil {
		return
	}
	g.addFunctionEnumAttribute(fn, "alwaysinline")
}
func (g *llvmGenerator) addNoInlineAttribute(fn llvmIRValueRef) {
	if g == nil || g.context == nil || fn == nil {
		return
	}
	g.addFunctionEnumAttribute(fn, "noinline")
}
func (g *llvmGenerator) addNoRecurseAttribute(fn llvmIRValueRef) {
	if g == nil || g.context == nil || fn == nil {
		return
	}
	g.addFunctionEnumAttribute(fn, "norecurse")
}
func (g *llvmGenerator) addNoUnwindAttribute(fn llvmIRValueRef) {
	if g == nil || g.context == nil || fn == nil {
		return
	}
	g.addFunctionEnumAttribute(fn, "nounwind")
	// Profiling builds: keep a walkable frame chain so `sample` attributes callers.
	if os.Getenv("ELISA_KEEP_FRAME_POINTER") != "" {
		g.addFunctionStringAttribute(fn, "frame-pointer", "all")
	}
}
func (g *llvmGenerator) addHotAttribute(fn llvmIRValueRef) {
	if g == nil || g.context == nil || fn == nil {
		return
	}
	g.addFunctionEnumAttribute(fn, "hot")
}
func (g *llvmGenerator) addColdAttribute(fn llvmIRValueRef) {
	if g == nil || g.context == nil || fn == nil {
		return
	}
	g.addFunctionEnumAttribute(fn, "cold")
}
func (g *llvmGenerator) addFunctionEnumAttribute(fn llvmIRValueRef, name string) {
	if g == nil || g.context == nil || fn == nil || name == "" {
		return
	}
	nameC := cString(name)
	defer llvmFree(unsafe.Pointer(nameC))
	llvmAddAlwaysInlineAttr(g.context, fn, nameC, llvmIRSize(len(name)))
}
func (g *llvmGenerator) addFunctionStringAttribute(fn llvmIRValueRef, name string, value string) {
	if g == nil || g.context == nil || fn == nil || name == "" {
		return
	}
	nameC := cString(name)
	defer llvmFree(unsafe.Pointer(nameC))
	valueC := cString(value)
	defer llvmFree(unsafe.Pointer(valueC))
	llvmAddFunctionStringAttr(g.context, fn, nameC, llvmIRSize(len(name)), valueC, llvmIRSize(len(value)))
}
func (g *llvmGenerator) shouldNeverInlineDefinedFunction(name string) bool {
	switch name {
	case "ctx_packed_store_reserve":
		return true
	case "ctx_packed_store_alloc_result", "ctx_packed_store_alloc_fixed_result", "ctx_packed_store_alloc_index_result", "ctx_packed_store_alloc_fixed_index_result":
		return true
	case "ctx_packed_store_alloc_fixed_tagged_result", "ctx_packed_store_alloc_fixed_tagged_index_result":
		return true
	case "ctx_packed_store_alloc_fixed_tagged_variant_sparse_result", "ctx_packed_store_alloc_tagged_variant_sparse_result":
		return true
	case "ctx_aos_store_new", "ctx_aos_store_alloc":
		return true
	default:
		return false
	}
}
func (g *llvmGenerator) lookupIntrinsic(name string, fn *semantic.FuncType) (llvmIRUnsigned, []llvmIRTypeRef, bool, error) {
	if !strings.HasPrefix(strings.TrimSpace(name), "llvm.") {
		return 0, nil, false, nil
	}
	nameC := cString(name)
	defer llvmFree(unsafe.Pointer(nameC))
	intrinsicID := llvmLookupIntrinsicID(nameC, llvmIRSize(len(name)))
	if intrinsicID == 0 {
		return 0, nil, false, nil
	}
	// LLVMGetIntrinsicDeclaration only accepts overload types for intrinsics
	// that are actually overloaded. Passing the source function's ordinary
	// parameter types to a fully mangled intrinsic such as
	// llvm.wasm.memory.grow.i32 makes LLVM append another `.i32` and emit the
	// malformed declaration `llvm.wasm.memory.grow.i32.i32`. This matters for
	// freestanding WASM component runtimes, whose memory intrinsics are already
	// fully specialized in their @intrinsic spelling.
	overloadParams := []semantic.Type(nil)
	if llvmIntrinsicIsOverloaded(intrinsicID) != 0 {
		if resolved, ok, err := g.resolveIntrinsicOverloadTypes(intrinsicID, fn); err != nil {
			return 0, nil, false, err
		} else if ok {
			return intrinsicID, resolved, true, nil
		}
		overloadParams = intrinsicOverloadParams(name, fn)
	}
	paramTypes := make([]llvmIRTypeRef, 0, len(overloadParams))
	for _, param := range overloadParams {
		paramType, err := g.lowerType(param)
		if err != nil {
			return 0, nil, false, err
		}
		paramTypes = append(paramTypes, paramType)
	}
	return intrinsicID, paramTypes, true, nil
}
func intrinsicOverloadParams(name string, fn *semantic.FuncType) []semantic.Type {
	if fn == nil {
		return nil
	}
	switch name {
	case "llvm.wasm.memory.grow", "llvm.wasm.memory.grow.i32", "llvm.wasm.memory.size", "llvm.wasm.memory.size.i32":
		// The WASM memory intrinsics are overloaded by their result type, not
		// by every ordinary call argument. Supplying the function parameters
		// would append both `i32` arguments to an already-specialized name.
		return []semantic.Type{fn.Return}
	case "memset", "llvm.memset":
		if len(fn.Params) >= 3 {
			return []semantic.Type{fn.Params[0], fn.Params[2]}
		}
	}
	return fn.Params
}
func (g *llvmGenerator) addGlobal(name string, t semantic.Type, external bool) (llvmIRValueRef, error) {
	globalType, err := g.lowerType(t)
	if err != nil {
		return nil, err
	}
	nameC := cString(g.llvmSymbolName(name))
	defer llvmFree(unsafe.Pointer(nameC))
	value := llvmAddGlobal(g.module, globalType, nameC)
	llvmSetLinkage(value, llvmExternalLinkage)
	g.applyTypeAlignment(value, t)
	_ = external
	return value, nil
}
