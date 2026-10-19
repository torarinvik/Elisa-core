//go:build cgo

package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise IR constructs the source frontend does not currently expose. Native
// ctor/dtor registration arrays must not be duplicated across object partitions.
func TestIncrementalObjectPartitionDefinitionGuards(t *testing.T) {
	config := os.Getenv("LLVM_CONFIG")
	if config == "" {
		config, _ = exec.LookPath("llvm-config")
	}
	if config == "" {
		config = "/opt/homebrew/opt/llvm/bin/llvm-config"
	}
	if resolved, err := exec.LookPath(config); err == nil {
		config = resolved
	}
	if _, err := os.Stat(config); err != nil {
		t.Skip("LLVM development toolchain unavailable for C++ boundary fixture")
	}
	flags := func(options ...string) []string {
		t.Helper()
		out, err := exec.Command(config, options...).Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.Fields(string(out))
	}
	bin := flags("--bindir")
	if len(bin) != 1 {
		t.Fatal("invalid LLVM bindir")
	}
	clang := filepath.Join(bin[0], "clang++")
	dir := t.TempDir()
	source := filepath.Join(dir, "guards.cpp")
	binary := filepath.Join(dir, "guards")
	fixture := `#include <llvm-c/Core.h>
#include <llvm/AsmParser/Parser.h>
#include <llvm/IR/LLVMContext.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/Verifier.h>
#include <llvm/Support/SourceMgr.h>
#include <string>
extern "C" LLVMModuleRef elisacoreCloneSelectedDefinitions(LLVMModuleRef, const long long *, size_t, int, const char *);
extern "C" void elisacorePruneObjectDeclarations(LLVMModuleRef);
int main() {
 const std::string base = R"(
@count = private global i32 0
@shared = private unnamed_addr constant i32 42
define private void @init() {
entry:
 br label %body
body:
 %v = load i32, ptr @count
 %next = add i32 %v, 1
 store i32 %next, ptr @count
 ret void
}
define i32 @main() {
 %v = load i32, ptr @count
 ret i32 %v
}
)";
 const char *extras[] = {
 "", // positive control must actually clone
 "@llvm.global_ctors = appending global [1 x { i32, ptr, ptr }] [{ i32, ptr, ptr } { i32 65535, ptr @init, ptr null }]\n",
 "@llvm.global_dtors = appending global [1 x { i32, ptr, ptr }] [{ i32, ptr, ptr } { i32 65535, ptr @init, ptr null }]\n",
 "@alias = alias void (), ptr @init\n",
 "module asm \".text\"\n",
 "@address = global ptr blockaddress(@init, %body)\n",
 "" // inline assembly injected into an existing function below
 };
 for (unsigned i=0; i<sizeof(extras)/sizeof(extras[0]); ++i) {
  llvm::LLVMContext context;
  llvm::SMDiagnostic error;
  auto input = base + extras[i];
  if (i == 6) input.insert(input.find(" store i32"), R"( call void asm sideeffect "", ""()
)");
  auto module = llvm::parseAssemblyString(input, error, context);
  if (!module || llvm::verifyModule(*module)) return 10+i;
  const long long owners[] = {0, 1};
  LLVMModuleRef copy = elisacoreCloneSelectedDefinitions(llvm::wrap(module.get()), owners, 2, 0, "guard-unit");
  if ((copy != nullptr) != (i == 0)) return 30+i;
  if (copy) {
   if (!LLVMGetNamedFunction(copy, "guard-unit.init") || !LLVMGetNamedGlobal(copy, "guard-unit.count")) return 70;
   if (LLVMGetNamedFunction(copy, "init") || LLVMGetNamedGlobal(copy, "count")) return 71;
   LLVMValueRef shared = LLVMGetNamedGlobal(copy, "shared");
   if (!shared || LLVMIsDeclaration(shared)) return 72;
   LLVMModuleRef other = elisacoreCloneSelectedDefinitions(llvm::wrap(module.get()), owners, 2, 1, "guard-unit");
   if (!other) return 73;
   shared = LLVMGetNamedGlobal(other, "shared");
   if (!shared || LLVMIsDeclaration(shared)) return 74;
   LLVMDisposeModule(other);
   LLVMDisposeModule(copy);
  }
  if (llvm::verifyModule(*module)) return 50+i;
 }
 // Pruning is narrower than GlobalDCE: preserve owned/address-significant
 // definitions, initializer closure, and globals referenced only by metadata.
 llvm::LLVMContext pruneContext;
 llvm::SMDiagnostic pruneError;
 auto pruneModule = llvm::parseAssemblyString(R"(
@unused = private unnamed_addr constant i32 41
@address_significant = private constant i32 42
@public = constant i32 43
@leaf = private unnamed_addr constant i32 44
@root = global ptr @leaf
@metadata_only = private unnamed_addr constant i32 45
@section_root = private unnamed_addr constant i32 46, section "special"
@externally_initialized = private unnamed_addr externally_initialized constant i32 47
!keep = !{!0}
!0 = !{ptr @metadata_only}
)", pruneError, pruneContext);
 if (!pruneModule || llvm::verifyModule(*pruneModule)) return 80;
 elisacorePruneObjectDeclarations(llvm::wrap(pruneModule.get()));
 if (pruneModule->getNamedGlobal("unused")) return 81;
 for (const char *name : {"address_significant", "public", "leaf", "root", "metadata_only", "section_root", "externally_initialized"})
   if (!pruneModule->getNamedGlobal(name)) return 82;
 if (llvm::verifyModule(*pruneModule)) return 83;
 return 0;
}
`
	if err := os.WriteFile(source, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate partition guard test source")
	}
	helper := filepath.Join(filepath.Dir(testFile), "incremental_clone.cpp")
	args := append(flags("--cxxflags"), source, helper, "-o", binary)
	args = append(args, flags("--ldflags", "--libs", "core", "asmparser", "transformutils", "ipo", "--system-libs")...)
	if out, err := exec.Command(clang, args...).CombinedOutput(); err != nil {
		t.Fatalf("build partition boundary fixture: %v\n%s", err, out)
	}
	if out, err := exec.Command(binary).CombinedOutput(); err != nil {
		t.Fatalf("partition boundary fixture: %v\n%s", err, out)
	}
}
