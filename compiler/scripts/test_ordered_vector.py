#!/usr/bin/env python3
"""Check ordered-conversion and paired-counter eligibility and LLVM validity."""
import os, shlex, subprocess, tempfile
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
CONFIG=os.environ.get('LLVM_CONFIG','/opt/homebrew/opt/llvm@23/bin/llvm-config')
def run(args):
 return subprocess.run(['timeout' if os.uname().sysname=='Linux' else 'gtimeout','-k','1s','28s',*map(str,args)],capture_output=True,text=True,check=True)
def flags(*a):return shlex.split(run([CONFIG,*a]).stdout)
DRIVER=r'''#include <llvm-c/Analysis.h>
#include <llvm/IRReader/IRReader.h>
#include <llvm/Support/SourceMgr.h>
#include "llvm_ordered_vector.cpp"
int main(int argc,char**argv){
 llvm::LLVMContext ctx;llvm::SMDiagnostic diag;auto M=llvm::parseIRFile(argv[1],diag,ctx);if(!M)return 2;
 llvm::PassBuilder PB;llvm::LoopAnalysisManager LAM;llvm::FunctionAnalysisManager FAM;llvm::CGSCCAnalysisManager CGAM;llvm::ModuleAnalysisManager MAM;
 PB.registerModuleAnalyses(MAM);PB.registerCGSCCAnalyses(CGAM);PB.registerFunctionAnalyses(FAM);PB.registerLoopAnalyses(LAM);PB.crossRegisterProxies(LAM,FAM,CGAM,MAM);
 unsigned changed=0;for(auto &F:*M)if(!F.isDeclaration()) {auto result=std::string(argv[2])=="casts"?ElisaOrderedCasts().run(F,FAM):ElisaPairCounters().run(F,FAM);changed+=!result.areAllPreserved();FAM.invalidate(F,result);}
 char *err=nullptr;int bad=LLVMVerifyModule(llvm::wrap(M.get()),LLVMReturnStatusAction,&err);if(bad){llvm::errs()<<err;LLVMDisposeMessage(err);return 3;}llvm::outs()<<changed<<"\n";return 0;
}
'''
def make_sum():
 s='target triple = "arm64-apple-darwin"\ndefine double @sum8(ptr %p) {\nentry:\n'
 for i in range(8):
  s+=f'  %p{i} = getelementptr i64, ptr %p, i64 {i}\n  %a{i} = load i64, ptr %p{i}, align 8\n  %x{i} = sitofp i64 %a{i} to double\n'
  if i:s+=f'  %s{i} = fadd double {"%x0" if i==1 else "%s"+str(i-1)}, %x{i}\n'
 return s+'  ret double %s7\n}\n'
base=make_sum()
LOOP='''target triple = "arm64-apple-darwin"
define i64 @count(i64 %n, i64 %stride) {
entry: br label %loop
loop:
 %a = phi i64 [ 0, %entry ], [ %an, %latch ]
 %b = phi i64 [ 0, %entry ], [ %bn, %latch ]
 %sum = add i64 %a, %b
 br label %latch
latch:
 %an = add i64 %a, 1
 %bn = add i64 %b, %stride
 %cond = icmp ult i64 %an, %n
 br i1 %cond, label %loop, label %exit
exit: ret i64 %sum
}
'''
cases={'eligible':('casts',base,1),'volatile':('casts',base.replace('%a1 = load i64','%a1 = load volatile i64'),0),'strict':('casts',base.replace('define double @sum8(ptr %p) {','define double @sum8(ptr %p) strictfp {'),0),'store':('casts',base.replace('  %x3 = sitofp','  store i64 4, ptr %p0, align 8\n  %x3 = sitofp'),0),'flags':('casts',base.replace('%s3 = fadd double','%s3 = fadd contract double'),0),'nonadjacent':('casts',base.replace('%p1 = getelementptr i64, ptr %p, i64 1','%p1 = getelementptr i64, ptr %p, i64 12'),0),'pair':('counters',LOOP,1),'nonzero':('counters',LOOP.replace('%b = phi i64 [ 0, %entry ]','%b = phi i64 [ 1, %entry ]'),0),'variant_stride':('counters',LOOP.replace('%bn = add i64 %b, %stride','%bn = add i64 %b, %sum'),0)}
with tempfile.TemporaryDirectory(prefix='ordered-vector-') as tmp:
 d=Path(tmp);(d/'driver.cpp').write_text(DRIVER);bindir=Path(run([CONFIG,'--bindir']).stdout.strip());libdir=run([CONFIG,'--libdir']).stdout.strip();run([bindir/'clang++',*flags('--cxxflags'),'-I'+str(ROOT/'src/backend/llvmnative/llvm_wrappers'),'-I'+str(ROOT/'src/backend/llvmnative'),d/'driver.cpp',*flags('--ldflags','--libs','--system-libs'),'-Wl,-rpath,'+libdir,'-o',d/'driver'])
 for name,(mode,ir,expected) in cases.items():
  (d/'input.ll').write_text(ir);actual=int(run([d/'driver',d/'input.ll',mode]).stdout);assert actual==expected,(name,actual,expected);print('PASS',name)
