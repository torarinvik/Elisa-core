//go:build cgo

// Preserve use-list order only for the optimized-module cache. LLVM's C
// writer does not expose this flag; losing the order can change textual IR on
// replay even when the underlying instructions are unchanged.
#include <llvm-c/Core.h>
#include <llvm/ADT/SmallVector.h>
#include <llvm/Bitcode/BitcodeWriter.h>
#include <llvm/IR/Module.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Support/MemoryBuffer.h>
#include <llvm/Support/raw_ostream.h>

extern "C" LLVMMemoryBufferRef elisacoreWriteOptimizedBitcodePreservingUses(LLVMModuleRef module) {
    if (!module) return nullptr;
    llvm::SmallVector<char, 0> bytes;
    llvm::raw_svector_ostream stream(bytes);
    llvm::WriteBitcodeToFile(*llvm::unwrap(module), stream, true);
    auto buffer = llvm::MemoryBuffer::getMemBufferCopy(llvm::StringRef(bytes.data(), bytes.size()), "optimized-cache");
    return llvm::wrap(buffer.release());
}
