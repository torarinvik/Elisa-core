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
#include <llvm/Transforms/IPO/StripDeadPrototypes.h>
#include <cstdint>
#include <cstdlib>
#include <iterator>

// Incremental shards use symbol identity rather than body weight. Editing one
// body must not migrate unrelated definitions across shard boundaries. Ordinary
// one-shot parallel emission retains its instruction-balanced assignment.
extern "C" long long *elisacoreStableObjectOwners(
    LLVMModuleRef module, int jobs, size_t *count) {
  auto &source = *llvm::unwrap(module);
  *count = std::distance(source.begin(), source.end());
  auto *owners = static_cast<long long *>(std::malloc((*count ? *count : 1) * sizeof(long long)));
  if (!owners) return nullptr;
  size_t index = 0;
  for (const auto &function : source) {
    bool owned = !function.isDeclaration() &&
        (function.hasExternalLinkage() || function.hasLocalLinkage());
    uint64_t hash = 14695981039346656037ULL;
    for (unsigned char byte : function.getName()) {
      hash ^= byte;
      hash *= 1099511628211ULL;
    }
    // Avalanche before reducing to a small power-of-two shard count.
    hash ^= hash >> 33; hash *= 0xff51afd7ed558ccdULL;
    hash ^= hash >> 33; hash *= 0xc4ceb9fe1a85ec53ULL;
    hash ^= hash >> 33;
    owners[index++] = owned ? static_cast<long long>(hash % jobs) : -1;
  }
  return owners;
}

// Foreign declarations with no uses cannot affect emitted code. Removing them
// makes unrelated declaration insertion/removal invisible to a shard's exact
// bitcode key. Referenced prototypes and their ABI/attributes remain inputs.
extern "C" void elisacorePruneObjectDeclarations(LLVMModuleRef module) {
  auto &shard = *llvm::unwrap(module);
  // Globally unnamed local constants are duplicated rather than assigned an
  // external owner. Once other function bodies are removed, an unused copy
  // cannot affect this shard's native object. Keep referenced initializers,
  // metadata roots, address-significant constants and externally owned globals.
  // A single pass is deliberately conservative for dead initializer chains.
  for (auto it = shard.global_begin(); it != shard.global_end();) {
    llvm::GlobalVariable &global = *it++;
    if (!global.hasLocalLinkage() || !global.isConstant() ||
        global.getUnnamedAddr() != llvm::GlobalValue::UnnamedAddr::Global ||
        global.isUsedByMetadata() || global.hasSection() || global.hasComdat() ||
        global.isExternallyInitialized()) continue;
    if (global.use_empty()) global.eraseFromParent();
  }
  llvm::ModuleAnalysisManager analyses;
  llvm::StripDeadPrototypesPass().run(shard, analyses);
}

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
