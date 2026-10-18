//go:build cgo

// The C API only exposes whole-module cloning. Selective definition cloning keeps
// partition preparation proportional to retained bodies rather than partitions
// times whole-program instructions. Final ownership/linkage is still applied by
// parallel_emit.c so cached and ordinary parallel objects use the same ABI.
#include <llvm-c/Core.h>
#include <llvm/ADT/DenseMap.h>
#include <llvm/IR/Function.h>
#include <llvm/IR/GlobalVariable.h>
#include <llvm/IR/InstrTypes.h>
#include <llvm/IR/Module.h>
#include <llvm/Transforms/Utils/Cloning.h>

extern "C" LLVMModuleRef elisacoreCloneSelectedDefinitions(
    LLVMModuleRef module, const long long *owners, size_t count, int part,
    const char *unit_namespace) {
  using namespace llvm;
  Module &source = *unwrap(module);
  // These carry definition dependencies outside ordinary symbol references.
  // Decline before cloning/removing blocks; the caller keeps serial emission.
  if (!source.alias_empty() || !source.ifunc_empty() ||
      !source.getModuleInlineAsm().empty()) return nullptr;
  DenseMap<const GlobalValue *, bool> keep;
  size_t index = 0;
  for (const Function &function : source) {
    for (const BasicBlock &block : function) {
      if (block.hasAddressTaken()) return nullptr;
      for (const Instruction &instruction : block)
        if (const auto *call = dyn_cast<CallBase>(&instruction))
          if (call->isInlineAsm()) return nullptr;
    }
    if (index >= count) return nullptr;
    keep[&function] = owners[index] < 0 || owners[index] == part;
    ++index;
  }
  if (index != count) return nullptr;
  for (const GlobalVariable &global : source.globals()) {
    // Appending arrays include global constructors/destructors. Duplicating
    // those registrations in each native object would execute them repeatedly.
    // Their ownership requires a dedicated split rule, so keep serial emission.
    if (global.hasAppendingLinkage()) return nullptr;
    bool owned = !global.isDeclaration() &&
        (global.hasExternalLinkage() ||
         (global.hasLocalLinkage() && (!global.isConstant() ||
             global.getUnnamedAddr() != GlobalValue::UnnamedAddr::Global)));
    keep[&global] = !owned || part == 0;
  }
  ValueToValueMapTy values;
  auto clone = CloneModule(source, values, [&](const GlobalValue *value) {
    auto found = keep.find(value);
    return found == keep.end() || found->second;
  });
  // CloneModule makes omitted definitions external. Restore original linkage
  // temporarily so the shared C externalization step gives declarations and
  // definitions identical hidden symbol names, including formerly private ones.
  for (const Function &function : source) {
    if (auto *copy = dyn_cast_or_null<GlobalValue>(values.lookup(&function))) {
      copy->setLinkage(function.getLinkage());
      if (function.hasLocalLinkage())
        copy->setName(Twine(unit_namespace) + "." + function.getName());
    }
  }
  for (const GlobalVariable &global : source.globals()) {
    if (auto *copy = dyn_cast_or_null<GlobalValue>(values.lookup(&global))) {
      copy->setLinkage(global.getLinkage());
      if (global.hasLocalLinkage() && (!global.isConstant() ||
          global.getUnnamedAddr() != GlobalValue::UnnamedAddr::Global))
        copy->setName(Twine(unit_namespace) + "." + global.getName());
    }
  }
  return wrap(clone.release());
}
