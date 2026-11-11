//go:build cgo

package backend

import (
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
)

// Frame-block backing for a default reserve_commit stack with a proven element bound
// (semantic/analyzer_region_stack_block.go). The bound is evaluated at region entry with
// overflow-FLAGGING (never trapping) arithmetic; when it is non-negative and the stack's worst
// footprint fits the frame block, the arena is pointed at that block (arena_init_stack_block).
// Otherwise the stack keeps the lazy default reservation already set by
// emitDefaultReserveCommitStackInit, so the original program's behavior is unchanged.
const (
	stackBlockPayloadSlots = 1024 // 8 KiB of payload on 64-bit
	stackBlockHeaderSlots  = 16   // room for the Region header (8 words today)
)

// stackBlockMaxElems is the largest bound N whose worst footprint fits the block. The
// growable's capacity never exceeds max(8, 2N) elements (doubling from 8, see
// emitReserveCommitStackInit), so 2N+16 elements plus slot rounding is a safe ceiling.
func stackBlockMaxElems(elemBytes uint64) int64 {
	if elemBytes == 0 {
		elemBytes = 1
	}
	usable := uint64(stackBlockPayloadSlots-2) * 8
	if usable/elemBytes <= 16 {
		return 0
	}
	return int64((usable/elemBytes - 16) / 2)
}

func (s *functionState) emitStackBlockInit(arenaPtr llvmIRValueRef, arenaType semantic.Type, bound ast.Expr, elemType semantic.Type) error {
	if bound == nil || elemType == nil {
		return nil
	}
	elemBytes, err := s.sizeOfType(elemType)
	if err != nil {
		return err
	}
	i64 := llvmInt64TypeInContext(s.g.context)
	i1 := llvmInt1TypeInContext(s.g.context)
	// A compile-time-constant bound (e.g. a resize capped by a guard) gets a block sized to its
	// exact worst footprint, installed unconditionally; it may exceed the default 8 KiB block.
	if n, ok := stackBlockConstBound(bound); ok {
		if slots, fits := stackBlockConstPayloadSlots(n, elemBytes); fits {
			return s.emitStackBlockInstall(arenaPtr, arenaType, s.createStackBlockAllocaSlots(i64, stackBlockHeaderSlots+slots), slots)
		}
		return nil
	}
	maxN := stackBlockMaxElems(elemBytes)
	if maxN <= 0 {
		return nil
	}
	value, overflow, ok, err := s.emitStackBlockBoundValue(bound, i64, i1)
	if err != nil || !ok {
		return err
	}
	fits := llvmBuildICmp(s.builder, llvmIntULE, value, llvmConstInt(i64, llvmIRUnsignedLongLong(maxN), 0), cStringFree("stack.block.fits"))
	noOverflow := llvmBuildNot(s.builder, overflow, cStringFree("stack.block.no.overflow"))
	use := llvmBuildAnd(s.builder, fits, noOverflow, cStringFree("stack.block.use"))

	block := s.createStackBlockAlloca(i64)

	useBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("stack.block.init"))
	contBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("stack.block.cont"))
	llvmBuildCondBr(s.builder, use, useBB, contBB)
	llvmPositionBuilderAtEnd(s.builder, useBB)

	if err := s.emitStackBlockInstall(arenaPtr, arenaType, block, stackBlockPayloadSlots); err != nil {
		return err
	}
	llvmBuildBr(s.builder, contBB)
	llvmPositionBuilderAtEnd(s.builder, contBB)
	return nil
}

// emitStackBlockInstall calls arena_init_stack_block(arena, block, payloadSlots) at the builder.
func (s *functionState) emitStackBlockInstall(arenaPtr llvmIRValueRef, arenaType semantic.Type, block llvmIRValueRef, payloadSlots uint64) error {
	i64 := llvmInt64TypeInContext(s.g.context)
	usizeType := s.g.namedType("usize")
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRef := s.lambdaVoidRefType()
	helperType := &semantic.FuncType{Name: "arena_init_stack_block", Params: []semantic.Type{arenaRefType, voidRef, usizeType}, Return: s.g.namedType("void")}
	callee, err := s.g.ensureFunctionDeclared("arena_init_stack_block", helperType)
	if err != nil {
		return err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return err
	}
	s.buildCall(llvmFnType, callee, []llvmIRValueRef{arenaPtr, block, llvmConstInt(i64, llvmIRUnsignedLongLong(payloadSlots), 0)}, "")
	return nil
}

// stackBlockConstMaxBytes caps an exactly-sized constant-bound frame block.
const stackBlockConstMaxBytes = 64 * 1024

// stackBlockConstBound folds a bound built only from integer literals, `+`, `*`, and parens.
func stackBlockConstBound(e ast.Expr) (int64, bool) {
	switch b := e.(type) {
	case *ast.IntLit:
		v, err := strconv.ParseInt(b.Value, 0, 64)
		return v, err == nil && v >= 0
	case *ast.ParenExpr:
		return stackBlockConstBound(b.Inner)
	case *ast.BinaryExpr:
		l, ok := stackBlockConstBound(b.Left)
		if !ok {
			return 0, false
		}
		r, ok := stackBlockConstBound(b.Right)
		if !ok || l > 1<<30 || r > 1<<30 {
			return 0, false
		}
		switch b.Op {
		case lexer.TOKEN_PLUS:
			return l + r, true
		case lexer.TOKEN_STAR:
			return l * r, true
		}
	}
	return 0, false
}

// stackBlockConstPayloadSlots is the payload (in 8-byte slots) covering the worst footprint of
// a growable bounded by n elements — capacity <= max(8, 2n), plus slack — and whether the
// whole block stays within stackBlockConstMaxBytes.
func stackBlockConstPayloadSlots(n int64, elemBytes uint64) (uint64, bool) {
	if elemBytes == 0 {
		elemBytes = 1
	}
	if n < 0 || n > 1<<24 {
		return 0, false
	}
	bytes := (uint64(2*n) + 16) * elemBytes
	slots := (bytes+7)/8 + 2
	if (slots+stackBlockHeaderSlots)*8 > stackBlockConstMaxBytes {
		return 0, false
	}
	return slots, true
}

// createStackBlockAlloca places the frame block in the entry block so it is one fixed frame
// slot (no dynamic stack growth when the region sits inside a loop).
func (s *functionState) createStackBlockAlloca(i64 llvmIRTypeRef) llvmIRValueRef {
	return s.createStackBlockAllocaSlots(i64, stackBlockHeaderSlots+stackBlockPayloadSlots)
}

func (s *functionState) createStackBlockAllocaSlots(i64 llvmIRTypeRef, totalSlots uint64) llvmIRValueRef {
	builder := llvmCreateBuilderInContext(s.g.context)
	defer llvmDisposeBuilder(builder)
	entry := llvmGetEntryBasicBlock(s.fnValue)
	if first := llvmGetFirstInstruction(entry); first != nil {
		llvmPositionBuilderBefore(builder, first)
	} else {
		llvmPositionBuilderAtEnd(builder, entry)
	}
	arrayType := llvmArrayType2(i64, llvmIRUint64(totalSlots))
	alloca := llvmBuildAlloca(builder, arrayType, cStringFree("stack.block"))
	llvmSetAlignment(alloca, 16)
	return alloca
}

// emitStackBlockBoundValue evaluates a bound accepted by semantic.stackBlockBoundEvaluable as a
// signed i64 plus an overflow flag. A negative value is reported as overflow so the unsigned
// fits-check rejects it. Integer conversions are evaluated as the identity: a narrowing
// conversion can only make the real count smaller than this value, never larger.
func (s *functionState) emitStackBlockBoundValue(e ast.Expr, i64, i1 llvmIRTypeRef) (llvmIRValueRef, llvmIRValueRef, bool, error) {
	noOverflow := llvmConstInt(i1, 0, 0)
	switch b := e.(type) {
	case *ast.IntLit:
		v, err := strconv.ParseInt(b.Value, 0, 64)
		if err != nil || v < 0 {
			return nil, nil, false, nil
		}
		return llvmConstInt(i64, llvmIRUnsignedLongLong(v), 0), noOverflow, true, nil
	case *ast.Ident:
		binding, ok := s.lookupBinding(b.Name)
		if !ok || binding.ptr == nil {
			return nil, nil, false, nil
		}
		raw, err := s.loadValue(binding.ptr, binding.typ, b.Name)
		if err != nil {
			return nil, nil, false, err
		}
		rawType := llvmTypeOf(raw)
		if llvmGetTypeKind(rawType) != llvmIntegerTypeKind {
			return nil, nil, false, nil
		}
		width := llvmGetIntTypeWidth(rawType)
		signed := isSignedIntegerType(binding.typ)
		switch {
		case width > 64:
			return nil, nil, false, nil
		case width == 64:
			neg := llvmBuildICmp(s.builder, llvmIntSLT, raw, llvmConstInt(i64, 0, 0), cStringFree("stack.block.neg"))
			return raw, neg, true, nil
		case signed:
			v := llvmBuildSExt(s.builder, raw, i64, cStringFree("stack.block.sext"))
			neg := llvmBuildICmp(s.builder, llvmIntSLT, v, llvmConstInt(i64, 0, 0), cStringFree("stack.block.neg"))
			return v, neg, true, nil
		default:
			return llvmBuildZExt(s.builder, raw, i64, cStringFree("stack.block.zext")), noOverflow, true, nil
		}
	case *ast.ParenExpr:
		return s.emitStackBlockBoundValue(b.Inner, i64, i1)
	case *ast.CastExpr:
		if semantic.StackBlockIntegerTypeName(b.Target) == "" {
			return nil, nil, false, nil
		}
		return s.emitStackBlockBoundValue(b.Operand, i64, i1)
	case *ast.CallExpr:
		field, ok := b.Func.(*ast.FieldExpr)
		if !ok || len(b.Args) != 0 {
			return nil, nil, false, nil
		}
		return s.emitStackBlockBoundValue(field.Object, i64, i1)
	case *ast.BinaryExpr:
		intrinsic := "llvm.sadd.with.overflow"
		switch b.Op {
		case lexer.TOKEN_PLUS:
		case lexer.TOKEN_STAR:
			intrinsic = "llvm.smul.with.overflow"
		default:
			return nil, nil, false, nil
		}
		l, lo, ok, err := s.emitStackBlockBoundValue(b.Left, i64, i1)
		if err != nil || !ok {
			return nil, nil, ok, err
		}
		r, ro, ok, err := s.emitStackBlockBoundValue(b.Right, i64, i1)
		if err != nil || !ok {
			return nil, nil, ok, err
		}
		fn, err := s.overflowIntrinsic(intrinsic, i64)
		if err != nil {
			return nil, nil, false, err
		}
		call := llvmBuildCall2(s.builder, llvmGlobalGetValueType(fn), fn, llvmValueSlicePtr([]llvmIRValueRef{l, r}), 2, cStringFree("stack.block.op"))
		v := llvmBuildExtractValue(s.builder, call, 0, cStringFree("stack.block.op.value"))
		o := llvmBuildExtractValue(s.builder, call, 1, cStringFree("stack.block.op.overflow"))
		o = llvmBuildOr(s.builder, o, llvmBuildOr(s.builder, lo, ro, cStringFree("stack.block.ovf.in")), cStringFree("stack.block.ovf"))
		neg := llvmBuildICmp(s.builder, llvmIntSLT, v, llvmConstInt(i64, 0, 0), cStringFree("stack.block.neg"))
		return v, llvmBuildOr(s.builder, o, neg, cStringFree("stack.block.bad")), true, nil
	}
	return nil, nil, false, nil
}
