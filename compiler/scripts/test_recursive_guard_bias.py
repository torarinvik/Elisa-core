#!/usr/bin/env python3
"""Check guarded recursive call branch weighting and profile preservation."""
import ctypes,os,shlex,subprocess,tempfile
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
CONFIG=os.environ.get('LLVM_CONFIG','/opt/homebrew/opt/llvm@23/bin/llvm-config' if os.uname().sysname=='Darwin' else '/usr/lib/llvm-23/bin/llvm-config')
TIMEOUT='gtimeout' if os.uname().sysname=='Darwin' else 'timeout'
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
int main(int argc,char**argv){llvm::LLVMContext c;llvm::SMDiagnostic d;auto m=llvm::parseIRFile(argv[1],d,c);if(!m){d.print(argv[0],llvm::errs());return 2;}auto a=biasGuardedRecursiveCalls(*m);auto b=biasGuardedRecursiveCalls(*m);if(llvm::verifyModule(*m,&llvm::errs()))return 3;std::error_code e;llvm::raw_fd_ostream out(argv[2],e);m->print(out,nullptr);llvm::outs()<<a<<" "<<b;return e?4:0;}
'''
def fixture(triple='arm64-apple-darwin',attrs='',prof=False,callee_attr=True):
 marker='"elisa.zero-versioned"' if callee_attr else ''
 meta=', !prof !0' if prof else ''
 return f'''target triple = "{triple}"
define private i64 @helper(i64 %x) {marker} {{
entry:
  %r = add i64 %x, 5
  ret i64 %r
}}
define i64 @entry(i64 %target, i64 %n) {attrs} {{
entry:
  br label %loop
loop:
  %i = phi i64 [0, %entry], [%next, %join]
  %sum = phi i64 [0, %entry], [%v, %join]
  %equal = icmp eq i64 %i, %target
  br i1 %equal, label %match, label %join{meta}
match:
  %called = call i64 @helper(i64 %i)
  br label %join
join:
  %v = phi i64 [%sum, %loop], [%called, %match]
  %next = add i64 %i, 1
  %done = icmp uge i64 %next, %n
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %v
}}
''' + ('!0 = !{!"branch_weights", i32 9, i32 7}\n' if prof else '')
with tempfile.TemporaryDirectory(prefix='elisa-recursive-bias-') as tmp:
 d=Path(tmp);bindir=Path(run([CONFIG,'--bindir']));libdir=run([CONFIG,'--libdir']);(d/'driver.cpp').write_text(DRIVER)
 run([bindir/'clang++',*flags('--cxxflags'),'-I'+str(ROOT/'src/backend/llvmnative/llvm_wrappers'),'-I'+str(ROOT/'src/backend/llvmnative'),d/'driver.cpp',*flags('--ldflags','--libs','--system-libs'),'-Wl,-rpath,'+libdir,'-o',d/'driver'])
 cases={'eligible':(fixture(),'1 0'),'existing profile':(fixture(prof=True),'0 0'),'unmarked callee':(fixture(callee_attr=False),'0 0'),'caller asan':(fixture(attrs='sanitize_address'),'0 0'),'linux target':(fixture(triple='aarch64-unknown-linux-gnu'),'0 0')}
 for name,(ir,want) in cases.items():
  inp=d/(name.replace(' ','_')+'.ll');out=d/(name.replace(' ','_')+'.out.ll');inp.write_text(ir);got=run([d/'driver',inp,out]);assert got==want,(name,got,want);print('PASS',name,flush=True)
 assert 'i32 9, i32 7' in (d/'existing_profile.out.ll').read_text()
 # Target the native platform for execution; the metadata changes no semantics.
 if os.uname().machine in ('arm64','aarch64'):
  funcs=[];native=TRIPLE='arm64-apple-darwin' if os.uname().sysname=='Darwin' else 'aarch64-unknown-linux-gnu'
  for i,ir in enumerate([d/'eligible.ll',d/'eligible.out.ll']):
   native_ir=d/f'native{i}.ll';native_ir.write_text(ir.read_text().replace('arm64-apple-darwin',native));lib=d/f'fixture{i}.so';run([bindir/'clang','-O2','-shared','-fPIC',native_ir,'-o',lib]);h=ctypes.CDLL(str(lib));f=h.entry;f.argtypes=[ctypes.c_uint64,ctypes.c_uint64];f.restype=ctypes.c_uint64;funcs.append((h,f))
  for n in range(1,20):
   for target in range(25):
    expect=target+5 if target<n else 0;assert [f(target,n) for _,f in funcs]==[expect,expect]
  print('PASS runtime branch behavior',flush=True)
