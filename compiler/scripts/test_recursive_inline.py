#!/usr/bin/env python3
"""Exercise bounded recursive inlining eligibility and verifier correctness."""
import os,shlex,subprocess,tempfile,re
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
CONFIG=os.environ.get('LLVM_CONFIG','/opt/homebrew/opt/llvm@23/bin/llvm-config')
def run(cmd):return subprocess.run(cmd,capture_output=True,text=True,check=True,timeout=28)
def flags(*args):return shlex.split(run([CONFIG,*args]).stdout)
DRIVER=r'''
#include <llvm/IRReader/IRReader.h>
#include <llvm/Support/SourceMgr.h>
#include <llvm/IR/Verifier.h>
#include <llvm/Support/TargetSelect.h>
#include <llvm/MC/TargetRegistry.h>
#include "llvm_recursive_inline.cpp"
int main(int argc,char**argv){llvm::LLVMContext C;llvm::SMDiagnostic E;auto M=llvm::parseIRFile(argv[1],E,C);if(!M){E.print(argv[0],llvm::errs());return 2;}if(argc==2){llvm::outs()<<recursiveInlineCandidate(*M->getFunction("tree"))<<"\n";return 0;}llvm::InitializeNativeTarget();llvm::InitializeNativeTargetAsmPrinter();std::string Error;auto *T=llvm::TargetRegistry::lookupTarget(M->getTargetTriple(),Error);if(!T)return 2;llvm::TargetOptions O;std::unique_ptr<llvm::TargetMachine> TM(T->createTargetMachine(M->getTargetTriple(),"generic","",O,llvm::Reloc::PIC_));auto N=elisacoreInlineSmallRecursiveFunctions(wrap(M.get()),reinterpret_cast<LLVMTargetMachineRef>(TM.get()));if(llvm::verifyModule(*M,&llvm::errs()))return 3;llvm::outs()<<N<<"\n";M->print(llvm::outs(),nullptr);}
'''
BASE='''target triple = "arm64-apple-darwin"
define private fastcc i64 @tree(i64 %n) nounwind {
entry:
 %empty = icmp eq i64 %n, 0
 br i1 %empty, label %base, label %step
base:
 ret i64 1
step:
 %next = sub i64 %n, 1
 %a = call fastcc i64 @tree(i64 %next)
 %b = call fastcc i64 @tree(i64 %next)
 %c = add i64 %a, %b
 %d = add i64 %c, %a
 %e = xor i64 %d, %b
 %f = add i64 %e, %a
 %g = xor i64 %f, %b
 %h = add i64 %g, %a
 %i = xor i64 %h, %b
 %j = add i64 %i, %a
 %k = xor i64 %j, %b
 ret i64 %k
}
'''
BASE+='\ndefine i64 @entry(i64 %n) { %r = call fastcc i64 @tree(i64 %n) ret i64 %r }\n'
BASE=BASE.replace("arm64-apple-darwin",os.environ.get("ELISA_TEST_TRIPLE","arm64-apple-darwin"))
cases={
 'small_binary':(BASE,1),
 'noinline':(BASE.replace('nounwind {','nounwind noinline {'),0),
 'optsize':(BASE.replace('nounwind {','nounwind optsize {'),0),
 'sanitizer':(BASE.replace('nounwind {','nounwind sanitize_address {'),0),
 'convergent':(BASE.replace('nounwind {','nounwind convergent {'),0),
 'externally_visible':(BASE.replace('define private fastcc','define fastcc'),0),
 'address_taken':(BASE+'\n@address = global ptr @tree\n',0),
 'alloca':(BASE.replace('step:\n','step:\n %slot = alloca i64\n'),0),
 'external_call':(BASE.replace('step:\n','step:\n call void @other()\n')+'\ndeclare void @other()\n',0),
 'one_recursive_call':(BASE.replace('%b = call fastcc i64 @tree(i64 %next)','%b = add i64 %next, 1'),0),
 'too_large':(BASE.replace(' ret i64 %k','\n'.join(f' %pad{x} = add i64 %n, {x}' for x in range(150))+'\n ret i64 %k'),0),
}
with tempfile.TemporaryDirectory(prefix='recursive-inline-') as tmp:
 d=Path(tmp);(d/'driver.cpp').write_text(DRIVER);clang=Path(run([CONFIG,'--bindir']).stdout.strip())/'clang++'
 run([str(clang),*flags('--cxxflags'),'-I'+str(ROOT/'src/backend/llvm_wrappers'),'-I'+str(ROOT/'src/backend'),str(d/'driver.cpp'),*flags('--ldflags','--libs','core','irreader','passes','native','--system-libs'),'-Wl,-rpath,'+run([CONFIG,'--libdir']).stdout.strip(),'-o',str(d/'driver')])
 for name,(ir,expected) in cases.items():
  p=d/(name+'.ll');p.write_text(ir);output=run([str(d/'driver'),str(p)]).stdout;assert int(output.strip())==expected,(name,output);print('PASS',name)
 p=d/'positive.ll';p.write_text(BASE)
 output=run([str(d/'driver'),str(p),'transform']).stdout
 assert output.startswith('1\n') and 'elisa.recursive-expanded' in output
 assert not re.search(r'^define[^\n]*@[^\n(]*recursive_snapshot',output,re.M)
 print('PASS transform_and_verify')
