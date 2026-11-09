package main

import (
	"elisacore/src/semantic"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type cOpaqueRuntimeBinding struct {
	ElisaName string
	CHeader   string
	CType     string
}

func collectCOpaqueRuntimeBindings(result *semantic.Result) []cOpaqueRuntimeBinding {
	if result == nil {
		return nil
	}
	names := make([]string, 0, len(result.NamedTypes))
	for name := range result.NamedTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	bindings := make([]cOpaqueRuntimeBinding, 0)
	seen := map[string]bool{}
	for _, name := range names {
		opaque, ok := result.NamedTypes[name].(*semantic.OpaqueType)
		if !ok || strings.TrimSpace(opaque.CHeader) == "" || strings.TrimSpace(opaque.CType) == "" {
			continue
		}
		key := name + "\x00" + opaque.CHeader + "\x00" + opaque.CType
		if seen[key] {
			continue
		}
		seen[key] = true
		bindings = append(bindings, cOpaqueRuntimeBinding{
			ElisaName: name,
			CHeader:   strings.TrimSpace(opaque.CHeader),
			CType:     strings.TrimSpace(opaque.CType),
		})
	}
	return bindings
}

func generatedCOpaqueRuntimeShimSource(result *semantic.Result) string {
	bindings := collectCOpaqueRuntimeBindings(result)
	if len(bindings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("#include <stddef.h>\n#include <stdlib.h>\n#include <string.h>\n")
	included := map[string]bool{}
	for _, binding := range bindings {
		if included[binding.CHeader] {
			continue
		}
		included[binding.CHeader] = true
		b.WriteString("#include ")
		b.WriteString(formatCInclude(binding.CHeader))
		b.WriteByte('\n')
	}
	b.WriteString("\nstatic size_t elisa_c_opaque_size_for(const char *name) {\n")
	b.WriteString("  if (name == NULL) { return 0; }\n")
	for _, binding := range bindings {
		fmt.Fprintf(&b, "  if (strcmp(name, %q) == 0 || strcmp(name, %q) == 0) { return sizeof(%s); }\n", binding.ElisaName, binding.CType, binding.CType)
	}
	b.WriteString("  return 0;\n}\n\n")
	b.WriteString("size_t elisa_c_opaque_size(const char *name) {\n")
	b.WriteString("  return elisa_c_opaque_size_for(name);\n")
	b.WriteString("}\n\n")
	b.WriteString("size_t elisa_c_opaque_align(const char *name) {\n")
	b.WriteString("  if (name == NULL) { return 0; }\n")
	for _, binding := range bindings {
		fmt.Fprintf(&b, "  if (strcmp(name, %q) == 0 || strcmp(name, %q) == 0) { return _Alignof(%s); }\n", binding.ElisaName, binding.CType, binding.CType)
	}
	b.WriteString("  return 0;\n}\n\n")
	b.WriteString("void *elisa_c_opaque_alloc(const char *name) {\n")
	b.WriteString("  size_t size = elisa_c_opaque_size_for(name);\n")
	b.WriteString("  if (size == 0) { return NULL; }\n")
	b.WriteString("  return calloc(1, size);\n")
	b.WriteString("}\n\n")
	b.WriteString("void elisa_c_opaque_free(void *ptr) {\n")
	b.WriteString("  free(ptr);\n")
	b.WriteString("}\n")
	return b.String()
}

type nativeCallbackRuntimeBinding struct {
	ElisaName string
	CName     string
	Return    string
	Param     string
	CallConv  string
}

func collectNativeCallbackRuntimeBindings(result *semantic.Result) []nativeCallbackRuntimeBinding {
	if result == nil || result.GlobalScope == nil {
		return nil
	}
	names := make([]string, 0, len(result.GlobalScope.Symbols))
	for name := range result.GlobalScope.Symbols {
		names = append(names, name)
	}
	sort.Strings(names)
	bindings := make([]nativeCallbackRuntimeBinding, 0)
	seen := map[string]bool{}
	for _, name := range names {
		sym := result.GlobalScope.Symbols[name]
		if sym == nil || sym.Kind != semantic.SymbolFunc {
			continue
		}
		fnType, ok := sym.Type.(*semantic.FuncType)
		if !ok || strings.TrimSpace(fnType.CallConv) == "" || len(fnType.Params) != 1 {
			continue
		}
		returnType, ok := nativeCallbackCScalarType(fnType.Return)
		if !ok {
			continue
		}
		paramType, ok := nativeCallbackCScalarType(fnType.Params[0])
		if !ok {
			continue
		}
		cName := strings.TrimSpace(sym.LinkName)
		if cName == "" {
			cName = sym.Name
		}
		if !isSimpleCIdentifier(cName) {
			continue
		}
		key := name + "\x00" + cName
		if seen[key] {
			continue
		}
		seen[key] = true
		bindings = append(bindings, nativeCallbackRuntimeBinding{
			ElisaName: name,
			CName:     cName,
			Return:    returnType,
			Param:     paramType,
			CallConv:  strings.ToLower(strings.TrimSpace(fnType.CallConv)),
		})
	}
	return bindings
}

func nativeCallbackCScalarType(t semantic.Type) (string, bool) {
	switch tt := t.(type) {
	case *semantic.BuiltinType:
		switch tt.Name {
		case "void":
			return "void", true
		case "u32":
			return "uint32_t", true
		case "i32":
			return "int32_t", true
		case "usize":
			return "size_t", true
		case "isize":
			return "ptrdiff_t", true
		}
	case *semantic.RefType:
		if builtin, ok := tt.Elem.(*semantic.BuiltinType); ok && builtin.Name == "void" {
			return "void *", true
		}
	}
	return "", false
}

func isSimpleCIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' {
				continue
			}
			return false
		}
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func nativeCallbackCallConvMacro(callConv string) string {
	switch strings.ToLower(strings.TrimSpace(callConv)) {
	case "stdcall", "winapi":
		return "ELISA_NATIVE_CALLBACK_STDCALL"
	default:
		return "ELISA_NATIVE_CALLBACK_CDECL"
	}
}

func nativeCallbackReturnSuffix(cType string) (string, bool) {
	switch strings.TrimSpace(cType) {
	case "uint32_t":
		return "u32", true
	case "int32_t":
		return "i32", true
	case "size_t":
		return "usize", true
	case "ptrdiff_t":
		return "isize", true
	default:
		return "", false
	}
}

func generatedNativeCallbackRuntimeShimSource(result *semantic.Result) string {
	bindings := collectNativeCallbackRuntimeBindings(result)
	// The runtime module declares and emits calls to this bridge even when the
	// current source has no callconv callbacks. Always provide the bridge symbols;
	// an empty binding set naturally keeps lookup fail-closed (nil pointer/fallback
	// result) while allowing unrelated native test programs to link.
	var b strings.Builder
	b.WriteString("#include <stddef.h>\n#include <stdint.h>\n#include <stdlib.h>\n#include <string.h>\n\n")
	b.WriteString("#if defined(_WIN32)\n")
	b.WriteString("#define WIN32_LEAN_AND_MEAN\n")
	b.WriteString("#include <process.h>\n")
	b.WriteString("#include <windows.h>\n")
	b.WriteString("#else\n")
	b.WriteString("#include <pthread.h>\n")
	b.WriteString("#endif\n\n")
	b.WriteString("#if defined(_WIN32) && !defined(_WIN64)\n")
	b.WriteString("#define ELISA_NATIVE_CALLBACK_STDCALL __stdcall\n")
	b.WriteString("#else\n")
	b.WriteString("#define ELISA_NATIVE_CALLBACK_STDCALL\n")
	b.WriteString("#endif\n")
	b.WriteString("#define ELISA_NATIVE_CALLBACK_CDECL\n\n")
	for _, binding := range bindings {
		fmt.Fprintf(&b, "extern %s %s %s(%s);\n", binding.Return, nativeCallbackCallConvMacro(binding.CallConv), binding.CName, binding.Param)
	}
	b.WriteString("\ntypedef uint32_t (*elisa_native_u32_voidp_fn)(void *);\n")
	b.WriteString("typedef struct elisa_native_u32_thread_start {\n")
	b.WriteString("  elisa_native_u32_voidp_fn fn;\n")
	b.WriteString("  void *arg;\n")
	b.WriteString("  uint32_t result;\n")
	b.WriteString("} elisa_native_u32_thread_start;\n\n")
	b.WriteString("typedef struct elisa_native_u32_callback_context {\n")
	b.WriteString("  const char *name;\n")
	b.WriteString("  void *arg;\n")
	b.WriteString("  uint32_t fallback;\n")
	b.WriteString("  uint32_t result;\n")
	b.WriteString("} elisa_native_u32_callback_context;\n\n")
	b.WriteString("uint32_t elisa_native_callback_call_u32_voidp(const char *name, void *arg, uint32_t fallback);\n\n")
	b.WriteString("#if defined(_WIN32)\n")
	b.WriteString("static unsigned __stdcall elisa_native_u32_thread_main(void *raw) {\n")
	b.WriteString("  elisa_native_u32_thread_start *start = (elisa_native_u32_thread_start *)raw;\n")
	b.WriteString("  start->result = start->fn(start->arg);\n")
	b.WriteString("  return 0u;\n")
	b.WriteString("}\n")
	b.WriteString("#else\n")
	b.WriteString("static void *elisa_native_u32_thread_main(void *raw) {\n")
	b.WriteString("  elisa_native_u32_thread_start *start = (elisa_native_u32_thread_start *)raw;\n")
	b.WriteString("  start->result = start->fn(start->arg);\n")
	b.WriteString("  return NULL;\n")
	b.WriteString("}\n")
	b.WriteString("#endif\n")
	b.WriteString("static void *elisa_native_u32_context_ctx_thread_entry(void *raw) {\n")
	b.WriteString("  elisa_native_u32_callback_context *ctx = (elisa_native_u32_callback_context *)raw;\n")
	b.WriteString("  if (ctx != NULL) { ctx->result = elisa_native_callback_call_u32_voidp(ctx->name, ctx->arg, ctx->fallback); }\n")
	b.WriteString("  return NULL;\n")
	b.WriteString("}\n")
	b.WriteString("#if defined(_WIN32)\n")
	b.WriteString("static unsigned __stdcall elisa_native_u32_context_thread_main(void *raw) {\n")
	b.WriteString("  elisa_native_u32_callback_context *ctx = (elisa_native_u32_callback_context *)raw;\n")
	b.WriteString("  ctx->result = elisa_native_callback_call_u32_voidp(ctx->name, ctx->arg, ctx->fallback);\n")
	b.WriteString("  return 0u;\n")
	b.WriteString("}\n")
	b.WriteString("#else\n")
	b.WriteString("static void *elisa_native_u32_context_thread_main(void *raw) {\n")
	b.WriteString("  elisa_native_u32_callback_context *ctx = (elisa_native_u32_callback_context *)raw;\n")
	b.WriteString("  ctx->result = elisa_native_callback_call_u32_voidp(ctx->name, ctx->arg, ctx->fallback);\n")
	b.WriteString("  return NULL;\n")
	b.WriteString("}\n")
	b.WriteString("#endif\n")
	b.WriteString("\nvoid *elisa_native_callback_ptr(const char *name) {\n")
	b.WriteString("  if (name == NULL) { return NULL; }\n")
	for _, binding := range bindings {
		fmt.Fprintf(&b, "  if (strcmp(name, %q) == 0 || strcmp(name, %q) == 0) { return (void *)&%s; }\n", binding.ElisaName, binding.CName, binding.CName)
	}
	b.WriteString("  return NULL;\n")
	b.WriteString("}\n")
	b.WriteString("\nuint32_t elisa_native_callback_call_u32_voidp(const char *name, void *arg, uint32_t fallback) {\n")
	b.WriteString("  if (name == NULL) { return fallback; }\n")
	for _, binding := range bindings {
		if binding.Return != "uint32_t" || binding.Param != "void *" {
			continue
		}
		fmt.Fprintf(&b, "  if (strcmp(name, %q) == 0 || strcmp(name, %q) == 0) { return %s(arg); }\n", binding.ElisaName, binding.CName, binding.CName)
	}
	b.WriteString("  return fallback;\n")
	b.WriteString("}\n")
	for _, ret := range []string{"int32_t", "size_t", "ptrdiff_t"} {
		suffix, _ := nativeCallbackReturnSuffix(ret)
		fmt.Fprintf(&b, "\n%s elisa_native_callback_call_%s_voidp(const char *name, void *arg, %s fallback) {\n", ret, suffix, ret)
		b.WriteString("  if (name == NULL) { return fallback; }\n")
		for _, binding := range bindings {
			if binding.Return != ret || binding.Param != "void *" {
				continue
			}
			fmt.Fprintf(&b, "  if (strcmp(name, %q) == 0 || strcmp(name, %q) == 0) { return %s(arg); }\n", binding.ElisaName, binding.CName, binding.CName)
		}
		b.WriteString("  return fallback;\n")
		b.WriteString("}\n")
	}
	b.WriteString("\nuint32_t elisa_native_callback_spawn_join_u32_voidp(const char *name, void *arg, uint32_t fallback) {\n")
	b.WriteString("  elisa_native_u32_thread_start start;\n")
	b.WriteString("  if (name == NULL) { return fallback; }\n")
	b.WriteString("  start.fn = NULL;\n")
	b.WriteString("  start.arg = arg;\n")
	b.WriteString("  start.result = fallback;\n")
	for _, binding := range bindings {
		if binding.Return != "uint32_t" || binding.Param != "void *" {
			continue
		}
		fmt.Fprintf(&b, "  if (strcmp(name, %q) == 0 || strcmp(name, %q) == 0) { start.fn = (elisa_native_u32_voidp_fn)&%s; }\n", binding.ElisaName, binding.CName, binding.CName)
	}
	b.WriteString("  if (start.fn == NULL) { return fallback; }\n")
	b.WriteString("#if defined(_WIN32)\n")
	b.WriteString("  uintptr_t handle = _beginthreadex(NULL, 0u, elisa_native_u32_thread_main, &start, 0u, NULL);\n")
	b.WriteString("  if (handle == 0u) { return fallback; }\n")
	b.WriteString("  if (WaitForSingleObject((HANDLE)handle, INFINITE) != WAIT_OBJECT_0) { CloseHandle((HANDLE)handle); return fallback; }\n")
	b.WriteString("  CloseHandle((HANDLE)handle);\n")
	b.WriteString("#else\n")
	b.WriteString("  pthread_t thread;\n")
	b.WriteString("  if (pthread_create(&thread, NULL, elisa_native_u32_thread_main, &start) != 0) { return fallback; }\n")
	b.WriteString("  if (pthread_join(thread, NULL) != 0) { return fallback; }\n")
	b.WriteString("#endif\n")
	b.WriteString("  return start.result;\n")
	b.WriteString("}\n")
	b.WriteString("\nvoid *elisa_native_callback_context_new_u32_voidp(const char *name, void *arg, uint32_t fallback) {\n")
	b.WriteString("  if (name == NULL || elisa_native_callback_ptr(name) == NULL) { return NULL; }\n")
	b.WriteString("  elisa_native_u32_callback_context *ctx = (elisa_native_u32_callback_context *)calloc(1, sizeof(*ctx));\n")
	b.WriteString("  if (ctx == NULL) { return NULL; }\n")
	b.WriteString("  ctx->name = name;\n")
	b.WriteString("  ctx->arg = arg;\n")
	b.WriteString("  ctx->fallback = fallback;\n")
	b.WriteString("  ctx->result = fallback;\n")
	b.WriteString("  return ctx;\n")
	b.WriteString("}\n")
	b.WriteString("\nvoid *elisa_native_callback_context_entry_u32_voidp(void) {\n")
	b.WriteString("  return (void *)&elisa_native_u32_context_ctx_thread_entry;\n")
	b.WriteString("}\n")
	b.WriteString("\nint elisa_native_callback_context_start_u32_voidp(void *raw, uintptr_t *out) {\n")
	b.WriteString("  if (raw == NULL || out == NULL) { return -1; }\n")
	b.WriteString("#if defined(_WIN32)\n")
	b.WriteString("  uintptr_t handle = _beginthreadex(NULL, 0u, elisa_native_u32_context_thread_main, raw, 0u, NULL);\n")
	b.WriteString("  if (handle == 0u) { return -1; }\n")
	b.WriteString("  *out = handle;\n")
	b.WriteString("  return 0;\n")
	b.WriteString("#else\n")
	b.WriteString("  pthread_t thread;\n")
	b.WriteString("  int status = pthread_create(&thread, NULL, elisa_native_u32_context_thread_main, raw);\n")
	b.WriteString("  if (status != 0) { return status; }\n")
	b.WriteString("  *out = (uintptr_t)thread;\n")
	b.WriteString("  return 0;\n")
	b.WriteString("#endif\n")
	b.WriteString("}\n")
	b.WriteString("\nuint32_t elisa_native_callback_context_join_u32_voidp(uintptr_t handle, void *raw, uint32_t fallback) {\n")
	b.WriteString("  elisa_native_u32_callback_context *ctx = (elisa_native_u32_callback_context *)raw;\n")
	b.WriteString("  if (handle == 0u || ctx == NULL) { return fallback; }\n")
	b.WriteString("#if defined(_WIN32)\n")
	b.WriteString("  if (WaitForSingleObject((HANDLE)handle, INFINITE) != WAIT_OBJECT_0) { CloseHandle((HANDLE)handle); return fallback; }\n")
	b.WriteString("  CloseHandle((HANDLE)handle);\n")
	b.WriteString("#else\n")
	b.WriteString("  if (pthread_join((pthread_t)handle, NULL) != 0) { return fallback; }\n")
	b.WriteString("#endif\n")
	b.WriteString("  return ctx->result;\n")
	b.WriteString("}\n")
	b.WriteString("\nuint32_t elisa_native_callback_context_spawn_join_u32_voidp(void *raw, uint32_t fallback) {\n")
	b.WriteString("  elisa_native_u32_callback_context *ctx = (elisa_native_u32_callback_context *)raw;\n")
	b.WriteString("  if (ctx == NULL) { return fallback; }\n")
	b.WriteString("#if defined(_WIN32)\n")
	b.WriteString("  uintptr_t handle = _beginthreadex(NULL, 0u, elisa_native_u32_context_thread_main, ctx, 0u, NULL);\n")
	b.WriteString("  if (handle == 0u) { return fallback; }\n")
	b.WriteString("  if (WaitForSingleObject((HANDLE)handle, INFINITE) != WAIT_OBJECT_0) { CloseHandle((HANDLE)handle); return fallback; }\n")
	b.WriteString("  CloseHandle((HANDLE)handle);\n")
	b.WriteString("#else\n")
	b.WriteString("  pthread_t thread;\n")
	b.WriteString("  if (pthread_create(&thread, NULL, elisa_native_u32_context_thread_main, ctx) != 0) { return fallback; }\n")
	b.WriteString("  if (pthread_join(thread, NULL) != 0) { return fallback; }\n")
	b.WriteString("#endif\n")
	b.WriteString("  return ctx->result;\n")
	b.WriteString("}\n")
	b.WriteString("\nuint32_t elisa_native_callback_context_result_u32(void *raw, uint32_t fallback) {\n")
	b.WriteString("  elisa_native_u32_callback_context *ctx = (elisa_native_u32_callback_context *)raw;\n")
	b.WriteString("  return ctx == NULL ? fallback : ctx->result;\n")
	b.WriteString("}\n")
	b.WriteString("\nvoid elisa_native_callback_context_free(void *raw) {\n")
	b.WriteString("  free(raw);\n")
	b.WriteString("}\n")
	return b.String()
}

func resultNeedsLLVMCAPILinkage(result *semantic.Result) bool {
	if result == nil || result.GlobalScope == nil {
		return false
	}
	for _, sym := range result.GlobalScope.Symbols {
		if sym == nil || (sym.Kind != semantic.SymbolExternFunc && sym.Kind != semantic.SymbolExternVar) {
			continue
		}
		linkName := strings.TrimSpace(sym.LinkName)
		if strings.HasPrefix(linkName, "LLVM") || strings.HasPrefix(sym.Name, "llvm_") {
			return true
		}
	}
	return false
}

func llvmNativeLinkFlags(existing []string) []string {
	flags := make([]string, 0, 8)
	switch runtime.GOOS {
	case "darwin":
		switch runtime.GOARCH {
		case "arm64":
			flags = appendExistingLibraryDirFlag(flags, "/opt/homebrew/opt/llvm/lib", existing)
		case "amd64":
			flags = appendExistingLibraryDirFlag(flags, "/usr/local/opt/llvm/lib", existing)
		}
	case "linux":
		for _, dir := range []string{
			"/usr/lib/llvm-21/lib",
			"/usr/lib/llvm-20/lib",
			"/usr/lib/llvm-19/lib",
			"/usr/lib/llvm-18/lib",
			"/usr/lib/llvm-17/lib",
			"/usr/lib/llvm-16/lib",
			"/usr/lib/llvm-15/lib",
		} {
			flags = appendExistingLibraryDirFlag(flags, dir, existing)
		}
	}
	flags = append(flags, "-lLLVM-C", "-lLLVM")
	return flags
}

func appendExistingLibraryDirFlag(flags []string, dir string, existing []string) []string {
	if dir == "" || linkFlagsContainLibraryDir(existing, dir) {
		return flags
	}
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return append(flags, "-L"+dir)
	}
	return flags
}

func appendMissingLinkFlags(base []string, candidates ...string) []string {
	result := append([]string(nil), base...)
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || linkFlagsContain(result, candidate) {
			continue
		}
		result = append(result, candidate)
	}
	return result
}

func linkFlagsContain(flags []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for index, flag := range flags {
		trimmed := strings.TrimSpace(flag)
		if trimmed == target {
			return true
		}
		if strings.HasPrefix(target, "-l") && trimmed == "-l" && index+1 < len(flags) && strings.TrimSpace(flags[index+1]) == strings.TrimPrefix(target, "-l") {
			return true
		}
	}
	return false
}

func linkFlagsContainLibraryDir(flags []string, dir string) bool {
	dir = filepath.Clean(strings.TrimSpace(dir))
	for index, flag := range flags {
		trimmed := strings.TrimSpace(flag)
		if strings.HasPrefix(trimmed, "-L") && filepath.Clean(strings.TrimSpace(strings.TrimPrefix(trimmed, "-L"))) == dir {
			return true
		}
		if trimmed == "-L" && index+1 < len(flags) && filepath.Clean(strings.TrimSpace(flags[index+1])) == dir {
			return true
		}
	}
	return false
}
