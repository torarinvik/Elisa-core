#!/usr/bin/env python3
"""Check conservative worklist initialization proofs with executable LLVM fixtures."""
import os,random,shlex,subprocess,tempfile
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
CONFIG=os.environ.get('LLVM_CONFIG','/opt/homebrew/opt/llvm@23/bin/llvm-config')
def run(cmd):return subprocess.run([str(x) for x in cmd],capture_output=True,text=True,check=True,timeout=28)
def flags(*args):return shlex.split(run([CONFIG,*args]).stdout)
DRIVER='''#include <llvm/IRReader/IRReader.h>\n#include <llvm/Support/SourceMgr.h>\n#include <llvm/IR/Verifier.h>\n#include <llvm/IR/InstIterator.h>\n#include <llvm/IR/IntrinsicInst.h>\n#include <llvm/Analysis/ValueTracking.h>\n#include "llvm_stack_prefix.h"\nint main(int argc,char**argv){llvm::LLVMContext C;llvm::SMDiagnostic E;auto M=llvm::parseIRFile(argv[1],E,C);if(!M)return 2;llvm::errs()<<removeStackPrefixZeros(*M)<<"\\n";if(llvm::verifyModule(*M,&llvm::errs()))return 3;M->print(llvm::outs(),nullptr);}\n'''
HEAD='''target triple = "TRIPLE"
target datalayout = "e-m:o-i64:64-i128:128-n32:64-S128"
declare void @llvm.memset.p0.i64(ptr, i8, i64, i1 immarg)
declare void @escape(ptr)
define i64 @f(i64 %i) {
entry:
 %a = alloca [8 x i64]
 call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 64, i1 false)
'''.replace('TRIPLE',os.environ.get('ELISA_TEST_TRIPLE','arm64-apple-darwin'))
CASES={
 'push_pop':(' store i64 9, ptr %a\n %v = load i64, ptr %a',1),
 'read_before_write':(' %v = load i64, ptr %a',0),
 'gap':(' %p = getelementptr i64, ptr %a, i64 1\n store i64 9, ptr %p\n %v = load i64, ptr %a',0),
 'wrong_slot':(' store i64 9, ptr %a\n %p = getelementptr i64, ptr %a, i64 1\n %v = load i64, ptr %p',0),
 'escape':(' call void @escape(ptr %a)\n store i64 9, ptr %a\n %v = load i64, ptr %a',0),
 'volatile':(' store volatile i64 9, ptr %a\n %v = load i64, ptr %a',0),
 'dynamic_unproven':(' store i64 9, ptr %a\n %p = getelementptr i64, ptr %a, i64 %i\n %v = load i64, ptr %p',0),
 'conditional_write':(' %c = icmp eq i64 %i, 0\n br i1 %c, label %write, label %join\nwrite:\n store i64 9, ptr %a\n br label %join\njoin:\n %v = load i64, ptr %a',0),
 'both_branches_write':(' %c = icmp eq i64 %i, 0\n br i1 %c, label %left, label %right\nleft:\n store i64 9, ptr %a\n br label %join\nright:\n store i64 8, ptr %a\n br label %join\njoin:\n %v = load i64, ptr %a',1),
 'loop_maybe_empty':(' br label %head\nhead:\n %n = phi i64 [0, %entry], [%n1, %body]\n %c = icmp ult i64 %n, %i\n br i1 %c, label %body, label %exit\nbody:\n store i64 7, ptr %a\n %n1 = add i64 %n, 1\n br label %head\nexit:\n %v = load i64, ptr %a',0),
}
with tempfile.TemporaryDirectory(prefix='stack-prefix-') as tmp:
 d=Path(tmp);(d/'driver.cpp').write_text(DRIVER);bindir=Path(run([CONFIG,'--bindir']).stdout.strip())
 run([bindir/'clang++',*flags('--cxxflags'),'-I'+str(ROOT/'src/backend'),d/'driver.cpp',*flags('--ldflags','--libs','core','irreader','passes','--system-libs'),'-Wl,-rpath,'+run([CONFIG,'--libdir']).stdout.strip(),'-o',d/'driver'])
 for name,(body,want) in CASES.items():
  (d/'input.ll').write_text(HEAD+body+'\n ret i64 %v\n}\n');r=run([d/'driver',d/'input.ll']);got=int(r.stderr.strip());assert got==want,(name,got);print('PASS',name)
