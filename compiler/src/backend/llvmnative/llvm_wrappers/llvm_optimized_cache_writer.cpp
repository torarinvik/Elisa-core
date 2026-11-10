//go:build cgo

// Preserve use-list order only for the optimized-module cache. LLVM's C
// writer does not expose this flag; losing the order can change textual IR on
// replay even when the underlying instructions are unchanged.
#include <llvm-c/Core.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/Bitcode/BitcodeWriter.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/Attributes.h>
#include <llvm/IR/Function.h>
#include <llvm/IR/Metadata.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Support/MemoryBuffer.h>
#include <llvm/Support/raw_ostream.h>

extern "C" LLVMMemoryBufferRef elisacoreWriteOptimizedBitcodePreservingUses(LLVMModuleRef module) {
    if (!module) return nullptr;
    llvm::Module &m = *llvm::unwrap(module);
    constexpr const char *key = "elisacore.optimized_cache.mustprogress.v1";
    if (m.getNamedMetadata(key)) return nullptr;
    auto *marker = m.getOrInsertNamedMetadata(key);
    for (const llvm::Function &f : m) {
        if (f.isIntrinsic() && f.hasFnAttribute(llvm::Attribute::MustProgress))
            marker->addOperand(llvm::MDNode::get(m.getContext(), llvm::MDString::get(m.getContext(), f.getName())));
    }
    llvm::SmallVector<char, 0> bytes;
    llvm::raw_svector_ostream stream(bytes);
    llvm::WriteBitcodeToFile(m, stream, true);
    marker->eraseFromParent();
    auto buffer = llvm::MemoryBuffer::getMemBufferCopy(llvm::StringRef(bytes.data(), bytes.size()), "optimized-cache");
    return llvm::wrap(buffer.release());
}

// Restore only attributes explicitly recorded by the producer. The caller
// still compares the entire restored IR with the authenticated original.
extern "C" int elisacoreRestoreOptimizedIntrinsicProgress(LLVMModuleRef module) {
    if (!module) return 0;
    llvm::Module &m = *llvm::unwrap(module);
    auto *marker = m.getNamedMetadata("elisacore.optimized_cache.mustprogress.v1");
    if (!marker) return 0;
    for (const llvm::MDNode *row : marker->operands()) {
        if (!row || row->getNumOperands() != 1) return 0;
        auto *name = llvm::dyn_cast_or_null<llvm::MDString>(row->getOperand(0));
        if (!name) return 0;
        auto *f = m.getFunction(name->getString());
        if (!f || !f->isIntrinsic()) return 0;
        f->addFnAttr(llvm::Attribute::MustProgress);
    }
    marker->eraseFromParent();
    return 1;
}
