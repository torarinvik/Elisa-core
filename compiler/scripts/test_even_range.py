#!/usr/bin/env python3
"""Verify the guarded even-range rewrite and its declined cases."""
import ctypes
import os
import random
import shutil
import shlex
import subprocess
import tempfile
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
if os.uname().machine not in ('arm64','aarch64'):
    print('SKIP: the pass targets AArch64');raise SystemExit(0)
CONFIG=os.environ.get('LLVM_CONFIG','/opt/homebrew/opt/llvm@23/bin/llvm-config' if os.uname().sysname=='Darwin' else '/usr/lib/llvm-23/bin/llvm-config')
TIMEOUT='gtimeout' if os.uname().sysname=='Darwin' else 'timeout'
def run(args):
    p=subprocess.run([TIMEOUT,'-k','1s','28s',*map(str,args)],capture_output=True,text=True)
    if p.returncode:raise RuntimeError(f'{args}: {p.stderr}')
    return p.stdout.strip()
def flags(*args):return shlex.split(run([CONFIG,*args]))
TRIPLE='arm64-apple-darwin' if os.uname().sysname=='Darwin' else 'aarch64-unknown-linux-gnu'
def fixture(lower=2,span=6):
    upper=lower+span
    return f'''target triple = "{TRIPLE}"
define i1 @outside(i64 %x, i64 %y) {{
entry:
  %offset = add i64 %x, {-upper-1}
  %range = icmp ult i64 %offset, {-span-1}
  %odd = trunc i64 %x to i1
  %bad = or i1 %range, %odd
  ret i1 %bad
}}
'''
DRIVER=r'''
#include <llvm-c/Core.h>
#include <llvm/IR/LLVMContext.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/Verifier.h>
#include <llvm/IRReader/IRReader.h>
#include <llvm/Support/SourceMgr.h>
#include <llvm/Support/raw_ostream.h>
extern "C" unsigned elisacoreCompactEvenRanges(LLVMModuleRef);
int main(int argc,char **argv) {
 llvm::LLVMContext c;llvm::SMDiagnostic d;auto m=llvm::parseIRFile(argv[1],d,c);
 if(!m){d.print(argv[0],llvm::errs());return 2;}
 auto a=elisacoreCompactEvenRanges(llvm::wrap(m.get()));auto b=elisacoreCompactEvenRanges(llvm::wrap(m.get()));
 if(llvm::verifyModule(*m,&llvm::errs()))return 3;
 std::error_code e;llvm::raw_fd_ostream out(argv[2],e);m->print(out,nullptr);llvm::outs()<<a<<" "<<b;return e?4:0;
}
'''
with tempfile.TemporaryDirectory(prefix='elisa-even-range-') as tmp:
    d=Path(tmp);bindir=Path(run([CONFIG,'--bindir']));libdir=run([CONFIG,'--libdir']);(d/'driver.cpp').write_text(DRIVER)
    run([bindir/'clang++',*flags('--cxxflags'),d/'driver.cpp',ROOT/'src/backend/llvmnative/llvm_wrappers/llvm_even_range.cpp',*flags('--ldflags','--libs','--system-libs'),'-Wl,-rpath,'+libdir,'-o',d/'driver'])
    src=fixture();cases={
      'eligible':(src,'1 0'),
      'different input':(src.replace('%odd = trunc i64 %x','%odd = trunc i64 %y'),'0 0'),
      'signed wrap':(src.replace('%offset = add i64','%offset = add nsw i64'),'0 0'),
      'unsigned wrap':(src.replace('%offset = add i64','%offset = add nuw i64'),'0 0'),
      'wrong parity':(src.replace('%odd = trunc i64 %x to i1','%odd0 = trunc i64 %x to i1\n  %odd = xor i1 %odd0, true'),'0 0'),
      'wrong span':(fixture(2,8),'0 0'),
      'odd lower':(fixture(3,6),'0 0'),
      'optsize':(src.replace(') {',') optsize {'),'0 0'),
    }
    for name,(ir,want) in cases.items():
        n=name.replace(' ','_');inp=d/(n+'.ll');out=d/(n+'.out.ll');inp.write_text(ir);got=run([d/'driver',inp,out]);assert got==want,(name,got,want);print('PASS',name,flush=True)
    rng=random.Random(612042)
    for lower,span in [(0,2),(2,6),(10,14),(100,30),(1024,62),(2**40,126)]:
        inp=d/'numeric.ll';out=d/'numeric.out.ll';inp.write_text(fixture(lower,span));assert run([d/'driver',inp,out])=='1 0'
        fs=[]
        for i,path in enumerate([inp,out]):
            lib=d/f'numeric-{lower}-{span}-{i}.so';run([bindir/'clang','-O3','-shared','-fPIC',path,'-o',lib]);h=ctypes.CDLL(str(lib));f=h.outside;f.argtypes=[ctypes.c_int64,ctypes.c_int64];f.restype=ctypes.c_bool;fs.append((h,f))
        values=[-(2**63),2**63-1,-1,0,1,lower-1,lower,lower+1,lower+span,lower+span+1]+[rng.randrange(-(2**63),2**63) for _ in range(10000)]
        for x in values:
            expected=not(lower<=x<=lower+span and x%2==0)
            assert [f(x,0) for _,f in fs]==[expected,expected],(lower,span,x)
    print('PASS boundaries and randomized values for six ranges',flush=True)
    if shutil.which('z3'):
        assert run(['z3',ROOT/'scripts/even_range_equivalence.smt2']) == 'unsat'
        print('PASS general 64-bit SMT equivalence',flush=True)
