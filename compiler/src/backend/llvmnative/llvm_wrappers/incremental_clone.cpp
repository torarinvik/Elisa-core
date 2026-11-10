//go:build cgo

// The C API only exposes whole-module cloning. Selective definition cloning keeps
// partition preparation proportional to retained bodies rather than partitions
// times whole-program instructions. Final ownership/linkage is still applied by
// parallel_emit.c so cached and ordinary parallel objects use the same ABI.
#include <llvm-c/Core.h>
#include <llvm/ADT/DenseMap.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/IR/Function.h>
#include <llvm/IR/GlobalVariable.h>
#include <llvm/IR/InstrTypes.h>
#include <llvm/IR/Module.h>
#include <llvm/Bitcode/BitcodeReader.h>
#include <llvm/Support/Error.h>
#include <llvm/Support/MemoryBuffer.h>
#include <cstring>
#include <algorithm>
#include <vector>
#include <llvm/Transforms/Utils/Cloning.h>
#include <llvm/Transforms/IPO/StripDeadPrototypes.h>
#include <cstdint>
#include <cstdlib>
#include <iterator>
#include <utility>

// Incremental shards use symbol identity rather than body weight. Editing one
// body must not migrate unrelated definitions across shard boundaries. Ordinary
// one-shot parallel emission retains its instruction-balanced assignment.
static long long *stableObjectOwners(
    LLVMModuleRef module, int jobs, size_t *count, bool weak) {
  auto &source = *llvm::unwrap(module);
  *count = std::distance(source.begin(), source.end());
  auto *owners = static_cast<long long *>(std::malloc((*count ? *count : 1) * sizeof(long long)));
  if (!owners) return nullptr;
  size_t index = 0;
  for (const auto &function : source) {
    bool owned = !function.isDeclaration() &&
        (function.hasExternalLinkage() || function.hasLocalLinkage() ||
         (weak && (function.hasWeakODRLinkage() || function.hasLinkOnceODRLinkage())));
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

extern "C" long long *elisacoreStableObjectOwners(LLVMModuleRef m, int jobs, size_t *count) {
  return stableObjectOwners(m, jobs, count, false);
}
extern "C" int elisacoreSelectiveCloneSupported(LLVMModuleRef);
extern "C" long long *elisacoreNativeFragmentOwners(LLVMModuleRef m, int jobs, size_t *count) {
  if (!elisacoreSelectiveCloneSupported(m)) return nullptr;
  auto &source = *llvm::unwrap(m);
  // COMDAT selection and appending registrations are indivisible groups.
  for (const auto &f : source) if (f.hasComdat()) return nullptr;
  for (const auto &g : source.globals())
    if (g.hasComdat() || g.hasAppendingLinkage()) return nullptr;
  return stableObjectOwners(m, jobs, count, true);
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

// Aliases, ifuncs, module asm, address-taken blocks and inline asm carry
// definition dependencies outside ordinary symbol references; such a module
// keeps serial emission. The scan walks every instruction, so a caller cloning
// many partitions of one module runs it once and uses the prechecked entry.
extern "C" int elisacoreSelectiveCloneSupported(LLVMModuleRef module) {
  using namespace llvm;
  Module &source = *unwrap(module);
  if (!source.alias_empty() || !source.ifunc_empty() ||
      !source.getModuleInlineAsm().empty()) return 0;
  for (const Function &function : source)
    for (const BasicBlock &block : function) {
      if (block.hasAddressTaken()) return 0;
      for (const Instruction &instruction : block)
        if (const auto *call = dyn_cast<CallBase>(&instruction))
          if (call->isInlineAsm()) return 0;
    }
  return 1;
}

extern "C" LLVMModuleRef elisacoreCloneSelectedDefinitionsPrechecked(
    LLVMModuleRef module, const long long *owners, size_t count, int part,
    const char *unit_namespace);

extern "C" LLVMModuleRef elisacoreCloneSelectedDefinitions(
    LLVMModuleRef module, const long long *owners, size_t count, int part,
    const char *unit_namespace) {
  if (!elisacoreSelectiveCloneSupported(module)) return nullptr;
  return elisacoreCloneSelectedDefinitionsPrechecked(module, owners, count, part, unit_namespace);
}

// The caller has established elisacoreSelectiveCloneSupported for this module.
extern "C" LLVMModuleRef elisacoreCloneSelectedDefinitionsPrechecked(
    LLVMModuleRef module, const long long *owners, size_t count, int part,
    const char *unit_namespace) {
  using namespace llvm;
  Module &source = *unwrap(module);
  DenseMap<const GlobalValue *, bool> keep;
  size_t index = 0;
  for (const Function &function : source) {
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

// Loads one partition of a module from its complete bitcode into `context`,
// materializing only the bodies the partition defines. The result matches what
// elisacoreCloneSelectedDefinitions produces from the in-memory module: foreign
// definitions become declarations that keep their original linkage, and local
// symbols carry the unit namespace, so the shared C restriction step applies the
// same ownership and final linkage. Each call needs its own context; calls on
// distinct contexts may run concurrently. The bitcode must outlive the module.
extern "C" LLVMModuleRef elisacoreLoadSelectedDefinitions(
    LLVMContextRef context, const char *data, size_t size, const long long *owners,
    size_t count, int part, const char *unit_namespace, const unsigned char *imports,
    char **error_out) {
  using namespace llvm;
  *error_out = nullptr;
  auto buffer = MemoryBuffer::getMemBuffer(StringRef(data, size), "partition", false);
  auto loaded = getOwningLazyBitcodeModule(std::move(buffer), *unwrap(context));
  if (!loaded) {
    *error_out = strdup(toString(loaded.takeError()).c_str());
    return nullptr;
  }
  std::unique_ptr<Module> module = std::move(*loaded);
  // Materializing debug metadata can inspect omitted functions. Keep their
  // temporary declarations valid until materialization finishes, then restore
  // original linkage for the shared namespace/externalization step.
  SmallVector<std::pair<Function *, GlobalValue::LinkageTypes>, 16> omitted;

  size_t index = 0;
  for (Function &function : *module) {
    if (index >= count) return nullptr;
    long long owner = owners[index++];
    bool imported = imports != nullptr && imports[index - 1] != 0;
    if (owner >= 0 && owner != part && !imported && !function.isDeclaration()) {
      GlobalValue::LinkageTypes linkage = function.getLinkage();
      function.deleteBody();
      omitted.emplace_back(&function, linkage);
    }
  }
  if (index != count) return nullptr;
  if (Error err = module->materializeAll()) {
    *error_out = strdup(toString(std::move(err)).c_str());
    return nullptr;
  }
  for (auto &entry : omitted) entry.first->setLinkage(entry.second);

  for (Function &function : *module)
    if (function.hasLocalLinkage())
      function.setName(Twine(unit_namespace) + "." + function.getName());
  for (GlobalVariable &global : module->globals())
    if (global.hasLocalLinkage() && (!global.isConstant() ||
        global.getUnnamedAddr() != GlobalValue::UnnamedAddr::Global))
      global.setName(Twine(unit_namespace) + "." + global.getName());
  return wrap(module.release());
}

// For every partition, the foreign definitions it imports: partitioned functions of
// at most `weight` instructions reachable from the partition's own functions through
// other imported functions. Row `part` of the jobs*count result marks them. They are
// loaded as available_externally copies, so the optimizer can inline across
// partitions while each function keeps exactly one emitted definition. NULL when the
// owner map does not match the module.
extern "C" unsigned char *elisacoreObjectImportSets(
    LLVMModuleRef m, const long long *owners, size_t count, int jobs, long long weight) {
  using namespace llvm;
  Module &module = *unwrap(m);
  std::vector<Function *> functions;
  DenseMap<const Function *, size_t> index;
  for (Function &function : module) {
    index[&function] = functions.size();
    functions.push_back(&function);
  }
  if (functions.size() != count || jobs < 1 || weight <= 0) return nullptr;
  std::vector<std::vector<size_t>> callees(count);
  std::vector<unsigned char> importable(count, 0);
  for (size_t i = 0; i < count; i++) {
    Function &function = *functions[i];
    if (owners[i] < 0 || function.isDeclaration()) continue;
    long long instructions = 0;
    std::vector<size_t> &targets = callees[i];
    for (BasicBlock &block : function) {
      for (Instruction &instruction : block) {
        instructions++;
        for (Value *operand : instruction.operands()) {
          if (auto *target = dyn_cast<Function>(operand->stripPointerCasts())) {
            auto found = index.find(target);
            if (found != index.end()) targets.push_back(found->second);
          }
        }
      }
    }
    std::sort(targets.begin(), targets.end());
    targets.erase(std::unique(targets.begin(), targets.end()), targets.end());
    importable[i] = instructions <= weight && !function.hasPersonalityFn();
  }
  unsigned char *sets = static_cast<unsigned char *>(calloc((size_t)jobs * count ? (size_t)jobs * count : 1, 1));
  if (sets == nullptr) return nullptr;
  std::vector<size_t> work;
  for (int part = 0; part < jobs; part++) {
    unsigned char *row = sets + (size_t)part * count;
    work.clear();
    for (size_t i = 0; i < count; i++)
      if (owners[i] == part) work.insert(work.end(), callees[i].begin(), callees[i].end());
    while (!work.empty()) {
      size_t candidate = work.back();
      work.pop_back();
      if (owners[candidate] < 0 || owners[candidate] == part || !importable[candidate] || row[candidate]) continue;
      row[candidate] = 1;
      work.insert(work.end(), callees[candidate].begin(), callees[candidate].end());
    }
  }
  return sets;
}
