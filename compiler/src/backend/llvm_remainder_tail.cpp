// go:build cgo

#include <llvm/Analysis/ValueTracking.h>
#include <llvm/Config/llvm-config.h>
#include <llvm/IR/CFG.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/Instructions.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/Verifier.h>
#include <llvm/Support/CBindingWrapping.h>
#include <llvm/Support/FileSystem.h>
#include <llvm/Support/SourceMgr.h>
#include <llvm/Support/raw_ostream.h>
#include <llvm/TargetParser/Triple.h>
using namespace llvm;
static bool onlyUsers(Value *v, std::initializer_list<Value *> allowed) {
  for (User *u : v->users()) {
    bool found = false;
    for (Value *a : allowed)
      if (u == a)
        found = true;
    if (!found)
      return false;
  }
  return true;
}
// When the quotient is below the divisor, the next reduction contributes the
// quotient and its quotient is zero. Bypass that otherwise redundant visit.
// Check every carried value and user before altering the CFG: the eliminated
// visit must have no observable work or independent uses. Wrapping adds and a
// non-exact unsigned divide preserve the arithmetic and poison behavior.
static bool matchAndRewrite(BasicBlock &h) {
  auto *br = dyn_cast<BranchInst>(h.getTerminator());
  if (!br || !br->isConditional())
    return false;
  auto *cmp = dyn_cast<ICmpInst>(br->getCondition());
  if (!cmp || cmp->getPredicate() != ICmpInst::ICMP_ULT || !cmp->hasOneUse())
    return false;
  auto *n = dyn_cast<PHINode>(cmp->getOperand(0));
  auto *d = dyn_cast<ConstantInt>(cmp->getOperand(1));
  if (!n || n->getParent() != &h || !d || d->isZero() ||
      !n->getType()->isIntegerTy(64))
    return false;
  BasicBlock *out = br->getSuccessor(0), *latch = br->getSuccessor(1);
  if (out == latch || out->getSinglePredecessor() != &h)
    return false;
  auto *outBr = dyn_cast<BranchInst>(out->getTerminator());
  auto *latchBr = dyn_cast<BranchInst>(latch->getTerminator());
  if (!outBr || !outBr->isConditional() || !latchBr ||
      !latchBr->isUnconditional() || latchBr->getSuccessor(0) != &h)
    return false;
  if (outBr->getSuccessor(0) != latch && outBr->getSuccessor(1) != latch)
    return false;
  if (!out->phis().empty() || pred_size(latch) != 2 ||
      !is_contained(predecessors(latch), out) ||
      !is_contained(predecessors(latch), &h))
    return false;
  BinaryOperator *q = nullptr, *prod = nullptr, *rem = nullptr, *sum = nullptr;
  PHINode *acc = nullptr;
  for (Instruction &i : h)
    if (auto *x = dyn_cast<BinaryOperator>(&i)) {
      if (x->getOpcode() == Instruction::UDiv && x->getOperand(0) == n &&
          x->getOperand(1) == d && !x->isExact())
        q = x;
    }
  if (!q)
    return false;
  for (Instruction &i : h)
    if (auto *x = dyn_cast<BinaryOperator>(&i))
      if (x->getOpcode() == Instruction::Mul && x->getOperand(0) == q) {
        auto *c = dyn_cast<ConstantInt>(x->getOperand(1));
        if (c && c->getValue() == -d->getValue())
          prod = x;
      }
  if (!prod)
    return false;
  for (Instruction &i : h)
    if (auto *x = dyn_cast<BinaryOperator>(&i))
      if (x->getOpcode() == Instruction::Add && !x->hasNoSignedWrap() &&
          !x->hasNoUnsignedWrap() && x->getOperand(0) == n &&
          x->getOperand(1) == prod)
        rem = x;
  if (!rem)
    return false;
  for (Instruction &i : h)
    if (auto *x = dyn_cast<BinaryOperator>(&i))
      if (x->getOpcode() == Instruction::Add && !x->hasNoSignedWrap() &&
          !x->hasNoUnsignedWrap() && x->getOperand(0) == rem) {
        acc = dyn_cast<PHINode>(x->getOperand(1));
        if (acc && acc->getParent() == &h)
          sum = x;
      }
  if (!sum || !onlyUsers(n, {q, rem, cmp}) || !onlyUsers(prod, {rem}) ||
      !onlyUsers(rem, {sum}))
    return false;
  if (h.size() != 8 || q->getParent() != &h || prod->getParent() != &h)
    return false;
  PHINode *lq = nullptr, *ls = nullptr;
  unsigned phis = 0;
  for (PHINode &p : latch->phis()) {
    phis++;
    auto *v = p.getIncomingValueForBlock(&h);
    if (v == q)
      lq = &p;
    else if (v == sum)
      ls = &p;
    else
      return false;
  }
  if (phis != 2 || !lq || !ls || !onlyUsers(q, {prod, lq}) ||
      latch->size() != 3)
    return false;
  if (n->getBasicBlockIndex(latch) < 0 || acc->getBasicBlockIndex(latch) < 0 ||
      n->getIncomingValueForBlock(latch) != lq ||
      acc->getIncomingValueForBlock(latch) != ls)
    return false;
  if (!onlyUsers(acc, {sum}) || !onlyUsers(lq, {n}) || !onlyUsers(ls, {acc}))
    return false;
  // Every cycle must carry exactly the proved values, and external entries
  // must not introduce undef choices that could differ between uses.
  if (lq->getIncomingValueForBlock(out) != sum ||
      !isa<ConstantInt>(ls->getIncomingValueForBlock(out)) ||
      !cast<ConstantInt>(ls->getIncomingValueForBlock(out))->isZero())
    return false;
  for (PHINode *p : {n, acc})
    for (unsigned i = 0; i < p->getNumIncomingValues(); ++i)
      if (p->getIncomingBlock(i) != latch &&
          !isGuaranteedNotToBeUndefOrPoison(p->getIncomingValue(i)))
        return false;
  // Only uses of sum in the latch or in out's dominated region are allowed.
  for (User *u : sum->users()) {
    auto *i = dyn_cast<Instruction>(u);
    if (!i)
      return false;
    if (i == ls)
      continue;
    if (i->getParent() == out)
      continue;
    auto *p = dyn_cast<PHINode>(i);
    if (!p)
      return false;
    for (unsigned j = 0; j < p->getNumIncomingValues(); j++)
      if (p->getIncomingValue(j) == sum && p->getIncomingBlock(j) != out)
        return false;
  }
  Function *f = h.getParent();
  BasicBlock *tail = BasicBlock::Create(f->getContext(), "elisa.tail", f, out);
  IRBuilder<> b(tail);
  Value *combined = b.CreateAdd(sum, q, "elisa.tail.sum");
  b.CreateBr(out);
  for (Use &use : make_early_inc_range(sum->uses())) {
    auto *i = cast<Instruction>(use.getUser());
    if (i == combined || i == ls)
      continue;
    use.set(combined);
  }
  out->replacePhiUsesWith(&h, tail);
  br->setSuccessor(0, tail);
  cmp->setOperand(0, q);
#if LLVM_VERSION_MAJOR >= 20
  cmp->setSameSign(false);
#endif
  return true;
}
extern "C" unsigned elisacorePeelRemainderTail(LLVMModuleRef ref) {
  if (!ref)
    return 0;
  Module *m = unwrap(ref);
  if (!Triple(m->getTargetTriple()).isAArch64())
    return 0;
  unsigned changes = 0;
  for (Function &f : *m) {
    if (f.hasFnAttribute(Attribute::OptimizeNone))
      continue;
    for (auto it = f.begin(), end = f.end(); it != end;) {
      BasicBlock &b = *it++;
      changes += matchAndRewrite(b);
    }
  }
  return changes;
}
