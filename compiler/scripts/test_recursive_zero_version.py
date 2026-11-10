#!/usr/bin/env python3
"""Test zero-argument recursive call versioning, including observable effects."""
import ctypes,os,random,shlex,subprocess,tempfile
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
CONFIG=os.environ.get('LLVM_CONFIG','/opt/homebrew/opt/llvm@23/bin/llvm-config' if os.uname().sysname=='Darwin' else '/usr/lib/llvm-23/bin/llvm-config')
TIMEOUT='gtimeout' if os.uname().sysname=='Darwin' else 'timeout'
TRIPLE='arm64-apple-darwin' if os.uname().sysname=='Darwin' else 'aarch64-unknown-linux-gnu'
def run(cmd):
 p=subprocess.run([TIMEOUT,'-k','1s','28s',*map(str,cmd)],capture_output=True,text=True)
 if p.returncode:raise RuntimeError(f'{cmd}: {p.stderr}')
 return p.stdout.strip()
def flags(*args):return shlex.split(run([CONFIG,*args]))
DRIVER=r'''
#include <llvm/IRReader/IRReader.h>
#include <llvm/Support/SourceMgr.h>
#include <llvm/IR/Verifier.h>
#include "llvm_recursive_inline.cpp"
int main(int argc,char**argv){llvm::LLVMContext c;llvm::SMDiagnostic d;auto m=llvm::parseIRFile(argv[1],d,c);if(!m){d.print(argv[0],llvm::errs());return 2;}auto a=versionRecursiveZeroCall(*m);auto b=versionRecursiveZeroCall(*m);if(llvm::verifyModule(*m,&llvm::errs()))return 3;std::error_code e;llvm::raw_fd_ostream out(argv[2],e);m->print(out,nullptr);llvm::outs()<<a<<" "<<b;return e?4:0;}
'''
def fixture(attrs='',caller_attrs='',variant=False):
 arg='%i' if variant else '%n'
 return f'''target triple = "{TRIPLE}"
define private i1 @helper(i64 %n, ptr %p) {attrs} "elisa.literal-specialized" {{
entry:
  %old = load i64, ptr %p, align 8
  %new = add i64 %old, 1
  store i64 %new, ptr %p, align 8
  %zero = icmp eq i64 %n, 0
  br i1 %zero, label %done, label %recurse
recurse:
  %next = sub i64 %n, 1
  %r = call i1 @helper(i64 %next, ptr %p)
  br label %done
done:
  %result = phi i1 [ false, %entry ], [ %r, %recurse ]
  ret i1 %result
}}
define i64 @entry(i64 %n, i64 %iterations) {caller_attrs} {{
entry:
  %state = alloca i64, align 8
  store i64 0, ptr %state, align 8
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inc, %loop ]
  %r = call i1 @helper(i64 {arg}, ptr %state)
  %inc = add i64 %i, 1
  %done = icmp uge i64 %inc, %iterations
  br i1 %done, label %exit, label %loop
exit:
  %out = load i64, ptr %state, align 8
  ret i64 %out
}}
'''
with tempfile.TemporaryDirectory(prefix='elisa-recursive-zero-') as tmp:
 d=Path(tmp);bindir=Path(run([CONFIG,'--bindir']));libdir=run([CONFIG,'--libdir']);(d/'driver.cpp').write_text(DRIVER)
 run([bindir/'clang++',*flags('--cxxflags'),'-I'+str(ROOT/'src/backend/llvmnative/llvm_wrappers'),'-I'+str(ROOT/'src/backend/llvmnative'),d/'driver.cpp',*flags('--ldflags','--libs','--system-libs'),'-Wl,-rpath,'+libdir,'-o',d/'driver'])
 cases={'eligible':(fixture(),'1 0'),'volatile effects':(fixture().replace('load i64, ptr %p','load volatile i64, ptr %p').replace('store i64 %new, ptr %p','store volatile i64 %new, ptr %p'),'1 0'),'variant argument':(fixture(variant=True),'0 0'),'callee noinline':(fixture(attrs='noinline'),'0 0'),'callee asan':(fixture(attrs='sanitize_address'),'0 0'),'caller asan':(fixture(caller_attrs='sanitize_address'),'0 0'),'callee returns_twice':(fixture(attrs='returns_twice'),'0 0')}
 for name,(ir,want) in cases.items():
  inp=d/(name.replace(' ','_')+'.ll');out=d/(name.replace(' ','_')+'.out.ll');inp.write_text(ir);got=run([d/'driver',inp,out]);assert got==want,(name,got,want);print('PASS',name,flush=True)
 funcs=[]
 for i,ir in enumerate([d/'eligible.ll',d/'eligible.out.ll']):
  lib=d/f'fixture{i}.so';run([bindir/'clang','-O2','-shared','-fPIC',ir,'-o',lib]);h=ctypes.CDLL(str(lib));f=h.entry;f.argtypes=[ctypes.c_uint64,ctypes.c_uint64];f.restype=ctypes.c_uint64;funcs.append((h,f))
 for n in range(8):
  for iterations in range(1,16):
   actual=[f(n,iterations) for _,f in funcs];assert actual==[(n+1)*iterations]*2,(n,iterations,actual)
 print('PASS zero and nonzero paths preserve observable writes',flush=True)
