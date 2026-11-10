#!/usr/bin/env python3
"""Check recursive literal specialization, fallback recursion and exclusions."""
import ctypes,os,random,shlex,subprocess,tempfile
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
CONFIG=os.environ.get('LLVM_CONFIG','/opt/homebrew/opt/llvm@23/bin/llvm-config' if os.uname().sysname=='Darwin' else '/usr/lib/llvm-23/bin/llvm-config')
TIMEOUT='gtimeout' if os.uname().sysname=='Darwin' else 'timeout'
def run(cmd):
 p=subprocess.run([TIMEOUT,'-k','1s','28s',*map(str,cmd)],capture_output=True,text=True)
 if p.returncode:raise RuntimeError(f'{cmd}: {p.stderr}')
 return p.stdout.strip()
def flags(*args):return shlex.split(run([CONFIG,*args]))
TRIPLE='arm64-apple-darwin' if os.uname().sysname=='Darwin' else 'aarch64-unknown-linux-gnu'
DRIVER=r'''
#include <llvm/IRReader/IRReader.h>
#include <llvm/Support/SourceMgr.h>
#include <llvm/IR/Verifier.h>
#include "llvm_recursive_inline.cpp"
int main(int argc,char**argv){llvm::LLVMContext c;llvm::SMDiagnostic d;auto m=llvm::parseIRFile(argv[1],d,c);if(!m){d.print(argv[0],llvm::errs());return 2;}auto a=specializeRecursiveLiteralCalls(*m);auto b=specializeRecursiveLiteralCalls(*m);if(llvm::verifyModule(*m,&llvm::errs()))return 3;std::error_code e;llvm::raw_fd_ostream out(argv[2],e);m->print(out,nullptr);llvm::outs()<<a<<" "<<b;return e?4:0;}
'''
def fixture(attrs='',other_literal=False,external=False):
 linkage='' if external else 'private '
 body=[f'target triple = "{TRIPLE}"',f'define {linkage}i64 @tree(i64 %n, i64 %mode) {attrs} {{','entry:','  %end = icmp eq i64 %n, 0','  br i1 %end, label %base, label %step','base:','  ret i64 %mode','step:','  %next = sub i64 %n, 1','  %nextmode = add i64 %mode, 1','  %a = call i64 @tree(i64 %next, i64 %mode)','  %b = call i64 @tree(i64 %next, i64 %nextmode)']
 prev='%a'
 for i in range(40):
  name=f'%x{i}';body.append(f'  {name} = add i64 {prev}, %b');prev=name
 body += [f'  ret i64 {prev}','}','define i64 @entry_one(i64 %n) { %r = call i64 @tree(i64 %n, i64 1) ret i64 %r }','define i64 @entry_dynamic(i64 %n, i64 %x) { %r = call i64 @tree(i64 %n, i64 %x) ret i64 %r }']
 if other_literal:body.append('define i64 @entry_two(i64 %n) { %r = call i64 @tree(i64 %n, i64 2) ret i64 %r }')
 return '\n'.join(body)+'\n'
with tempfile.TemporaryDirectory(prefix='elisa-recursive-literal-') as tmp:
 d=Path(tmp);bindir=Path(run([CONFIG,'--bindir']));libdir=run([CONFIG,'--libdir']);(d/'driver.cpp').write_text(DRIVER)
 run([bindir/'clang++',*flags('--cxxflags'),'-I'+str(ROOT/'src/backend/llvm_wrappers'),'-I'+str(ROOT/'src/backend'),d/'driver.cpp',*flags('--ldflags','--libs','--system-libs'),'-Wl,-rpath,'+libdir,'-o',d/'driver'])
 cases={'eligible':(fixture(),'1 0'),'different literals':(fixture(other_literal=True),'0 0'),'noinline':(fixture(attrs='noinline'),'0 0'),'optsize':(fixture(attrs='optsize'),'0 0'),'asan':(fixture(attrs='sanitize_address'),'0 0'),'noduplicate':(fixture(attrs='noduplicate'),'0 0'),'returns_twice':(fixture(attrs='returns_twice'),'0 0'),'external':(fixture(external=True),'0 0')}
 for name,(ir,want) in cases.items():
  inp=d/(name.replace(' ','_')+'.ll');out=d/(name.replace(' ','_')+'.out.ll');inp.write_text(ir);got=run([d/'driver',inp,out]);assert got==want,(name,got,want);print('PASS',name,flush=True)
 libs=[]
 for i,ir in enumerate([d/'eligible.ll',d/'eligible.out.ll']):
  lib=d/f'fixture{i}.so';run([bindir/'clang','-O2','-shared','-fPIC',ir,'-o',lib]);h=ctypes.CDLL(str(lib));one=h.entry_one;one.argtypes=[ctypes.c_uint64];one.restype=ctypes.c_uint64;dynamic=h.entry_dynamic;dynamic.argtypes=[ctypes.c_uint64,ctypes.c_uint64];dynamic.restype=ctypes.c_uint64;libs.append((h,one,dynamic))
 rng=random.Random(821);values=[0,1,2,2**64-1]+[rng.randrange(2**64) for _ in range(1000)]
 for i,v in enumerate(values):
  depth=i%7;assert libs[0][1](depth)==libs[1][1](depth);assert libs[0][2](depth,v)==libs[1][2](depth,v),(depth,v)
 print('PASS mixed recursion and 1004 runtime inputs',flush=True)
