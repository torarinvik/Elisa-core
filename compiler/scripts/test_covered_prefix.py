#!/usr/bin/env python3
"""Verifier-backed regression checks for private initialized-prefix proofs."""
import os, shlex, subprocess, tempfile
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
CONFIG=os.environ.get('LLVM_CONFIG','/opt/homebrew/opt/llvm@23/bin/llvm-config')
def run(cmd):return subprocess.run(cmd,capture_output=True,text=True,check=True,timeout=28)
def flags(*args):return shlex.split(run([CONFIG,*args]).stdout)
DRIVER='''#include <llvm/TargetParser/Triple.h>
#include "llvm_covered_prefix.cpp"
int main(int argc,char**argv){LLVMContext C;SMDiagnostic E;auto M=parseIRFile(argv[1],E,C);if(!M){E.print(argv[0],errs());return 2;}PassBuilder PB;LoopAnalysisManager LAM;FunctionAnalysisManager FAM;CGSCCAnalysisManager CGAM;ModuleAnalysisManager MAM;PB.registerModuleAnalyses(MAM);PB.registerCGSCCAnalyses(CGAM);PB.registerFunctionAnalyses(FAM);PB.registerLoopAnalyses(LAM);PB.crossRegisterProxies(LAM,FAM,CGAM,MAM);outs()<<(argc>2?prepareCoveredPrefixZeros(*M,FAM,PB):removeCoveredPrefixZeros(*M,FAM))<<"\\n";if(verifyModule(*M,&errs()))return 3;M->print(outs(),nullptr);}
'''
BASE='''target triple = "arm64-apple-darwin"
declare void @llvm.memset.p0.i64(ptr, i8, i64, i1 immarg)
declare void @unknown(ptr)
define i64 @f(i64 %n, i64 %index) {
entry:
 %a = alloca [16 x i64], align 8
 call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 128, i1 false)
 %bytes = shl nuw i64 %n, 3
 call void @llvm.memset.p0.i64(ptr %a, i8 1, i64 %bytes, i1 false)
 %ok = icmp ult i64 %index, %n
 br i1 %ok, label %read, label %exit
read:
 %p = getelementptr [8 x i8], ptr %a, i64 %index
 %v = load i64, ptr %p, align 8
 ret i64 %v
exit:
 ret i64 0
}
'''
COVER=' call void @llvm.memset.p0.i64(ptr %a, i8 1, i64 %bytes, i1 false)'
BASE=BASE.replace("arm64-apple-darwin",os.environ.get("ELISA_TEST_TRIPLE","arm64-apple-darwin"))
cases={
 'covered':(BASE,1),
 'escape':(BASE.replace(COVER,COVER+'\n call void @unknown(ptr %a)'),0),
 'early_read':(BASE.replace(COVER,' %early = load i64, ptr %a\n'+COVER),0),
 'no_overwrite':(BASE.replace(COVER,''),0),
 'wrapping_length':(BASE.replace('shl nuw','shl'),0),
 'volatile':(BASE.replace('load i64, ptr %p','load volatile i64, ptr %p'),0),
 'unguarded':(BASE.replace('br i1 %ok','br i1 true'),0),
 'outside_prefix':(BASE.replace('ptr %a, i64 %index','ptr %a, i64 %n'),0),
 'partial_initial':(BASE.replace('i64 128, i1 false','i64 120, i1 false'),0),
 'sanitizer':(BASE.replace('@f(i64 %n, i64 %index) {','@f(i64 %n, i64 %index) sanitize_address {'),0),
}
with tempfile.TemporaryDirectory(prefix='covered-prefix-') as tmp:
 d=Path(tmp);(d/'driver.cpp').write_text(DRIVER)
 clang=Path(run([CONFIG,'--bindir']).stdout.strip())/'clang++'
 run([str(clang),*flags('--cxxflags'),'-I'+str(ROOT/'src/backend'),str(d/'driver.cpp'),*flags('--ldflags','--libs','core','irreader','passes','analysis','--system-libs'),'-Wl,-rpath,'+run([CONFIG,'--libdir']).stdout.strip(),'-o',str(d/'driver')])
 for name,(ir,expected) in cases.items():
  p=d/(name+'.ll');p.write_text(ir);output=run([str(d/'driver'),str(p)]).stdout
  assert int(output.splitlines()[0])==expected,(name,output)
  print('PASS',name)

 # Eligibility for libc normalization must use a trusted declaration.
 lib=BASE.replace('declare void @unknown(ptr)', 'declare void @unknown(ptr)\ndeclare ptr @memset(ptr, i32, i64)').replace(' call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 128, i1 false)', ' %zero = call ptr @memset(ptr %a, i32 0, i64 128)')
 lib_cases={
  'trusted_memset':(lib,True),
  'defined_memset':(lib.replace('declare ptr @memset(ptr, i32, i64)', 'define ptr @memset(ptr %p, i32 %v, i64 %n) { ret ptr %p }'),False),
  'nobuiltin_memset':(lib.replace('call ptr @memset(ptr %a, i32 0, i64 128)', 'call ptr @memset(ptr %a, i32 0, i64 128) nobuiltin'),False),
 }
 for name,(ir,canonicalized) in lib_cases.items():
  p=d/(name+'.ll');p.write_text(ir)
  output=run([str(d/'driver'),str(p),'prepare']).stdout
  assert ('call ptr @memset' not in output)==canonicalized,(name,output)
  print('PASS',name)

