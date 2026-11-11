//go:build cgo

package backend

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

type lambdaCaptureBinding struct {
	name    string
	binding valueBinding
}

func (g *llvmGenerator) ensureLambdaClosureType() (llvmIRTypeRef, error) {
	const name = "ElisaCoreLambdaClosure"
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	ptrType := llvmPointerTypeInContext(g.context, 0)
	fields := []llvmIRTypeRef{ptrType, ptrType}
	llvmStructSetBody(ty, llvmTypeSlicePtr(fields), llvmIRUnsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func (s *functionState) lambdaVoidRefType() *semantic.RefType {
	if s == nil || s.g == nil {
		return &semantic.RefType{}
	}
	if s.g.cachedVoidRefType == nil {
		s.g.cachedVoidRefType = &semantic.RefType{
			Elem:            s.g.namedType("void"),
			State:           semantic.RefStateNonNull,
			Storage:         semantic.RefStorageAny,
			ExplicitStorage: true,
		}
	}
	return s.g.cachedVoidRefType
}

func (s *functionState) lambdaClosureFunctionType(base *semantic.FuncType) *semantic.FuncType {
	if base == nil {
		return nil
	}
	params := make([]semantic.Type, 0, len(base.Params)+1)
	params = append(params, s.lambdaVoidRefType())
	params = append(params, base.Params...)
	return &semantic.FuncType{
		Name:               base.Name + "__lambda_closure",
		Params:             params,
		Return:             base.Return,
		Variadic:           base.Variadic,
		ExplicitParamCount: len(params),
	}
}

func (s *functionState) lambdaCaptureBindings(expr *ast.LambdaExpr) ([]lambdaCaptureBinding, error) {
	if s == nil || s.g == nil || s.g.result == nil || expr == nil {
		return nil, fmt.Errorf("missing lambda metadata")
	}
	info, ok := s.g.semanticReadView().LookupLambdas(expr)
	if !ok || info == nil || len(info.Captures) == 0 {
		return nil, nil
	}
	captures := make([]lambdaCaptureBinding, 0, len(info.Captures))
	for _, name := range info.Captures {
		binding, ok := s.lookupBinding(name)
		if !ok || binding.ptr == nil || binding.typ == nil {
			return nil, fmt.Errorf("missing lambda capture binding %q during LLVM lowering", name)
		}
		captures = append(captures, lambdaCaptureBinding{name: name, binding: binding})
	}
	return captures, nil
}

func (s *functionState) emitMallocBytes(byteCount uint64, name string) (llvmIRValueRef, error) {
	usizeType := s.g.namedType("usize")
	llvmSizeType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	mallocType := s.g.cachedRuntimeHelperType("malloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "malloc", Params: []semantic.Type{usizeType}, Return: s.lambdaVoidRefType()}
	})
	callee, err := s.g.ensureFunctionDeclared("malloc", mallocType)
	if err != nil {
		return nil, err
	}
	llvmMallocType, err := s.g.lowerFunctionType(mallocType)
	if err != nil {
		return nil, err
	}
	sizeValue := llvmConstInt(llvmSizeType, llvmIRUnsignedLongLong(byteCount), 0)
	return s.buildCall(llvmMallocType, callee, []llvmIRValueRef{sizeValue}, name), nil
}

func (s *functionState) emitLambdaEnvironment(captures []lambdaCaptureBinding, name string) (llvmIRValueRef, llvmIRTypeRef, error) {
	if len(captures) == 0 {
		return nil, nil, nil
	}
	fieldTypes := make([]llvmIRTypeRef, 0, len(captures))
	for _, capture := range captures {
		fieldType, err := s.g.lowerType(capture.binding.typ)
		if err != nil {
			return nil, nil, err
		}
		fieldTypes = append(fieldTypes, fieldType)
	}
	envType := llvmStructTypeInContext(s.g.context, llvmTypeSlicePtr(fieldTypes), llvmIRUnsigned(len(fieldTypes)), 0)
	envBytes, err := s.g.abiSizeOfLLVMType(envType)
	if err != nil {
		return nil, nil, err
	}
	envPtr, err := s.emitMallocBytes(envBytes, name+".env")
	if err != nil {
		return nil, nil, err
	}
	for i, capture := range captures {
		captureValue, err := s.loadValue(capture.binding.ptr, capture.binding.typ, name+"."+capture.name)
		if err != nil {
			return nil, nil, err
		}
		fieldPtr := llvmBuildStructGEP2(s.builder, envType, envPtr, llvmIRUnsigned(i), cStringFree(name+"."+capture.name+".ptr"))
		llvmBuildStore(s.builder, captureValue, fieldPtr)
	}
	return envPtr, envType, nil
}

func (s *functionState) tagLambdaClosurePointer(ptr llvmIRValueRef, name string) (llvmIRValueRef, error) {
	usizeType := s.g.namedType("usize")
	llvmSizeType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	ptrType := llvmPointerTypeInContext(s.g.context, 0)
	ptrInt := llvmBuildPtrToInt(s.builder, ptr, llvmSizeType, cStringFree(name+".int"))
	taggedInt := llvmBuildOr(s.builder, ptrInt, llvmConstInt(llvmSizeType, 1, 0), cStringFree(name+".tagged"))
	return llvmBuildIntToPtr(s.builder, taggedInt, ptrType, cStringFree(name+".value")), nil
}

func (s *functionState) emitLambdaHelper(expr *ast.LambdaExpr, funcType *semantic.FuncType, captures []lambdaCaptureBinding, envType llvmIRTypeRef, helperName string) (llvmIRValueRef, error) {
	if expr == nil || funcType == nil {
		return nil, fmt.Errorf("missing lambda expression metadata")
	}
	helperType := funcType
	hasEnv := len(captures) != 0
	if hasEnv {
		helperType = s.lambdaClosureFunctionType(funcType)
	}
	fnValue, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, err
	}
	s.g.setDefinedFunctionLinkage(helperName, fnValue, helperType)
	// The helper is used only inside its defining partition. Keeping it local
	// also prevents unrelated linked objects from defining the same name.
	if s.g.partSynthNS != "" {
		llvmSetLinkage(fnValue, llvmPrivateLinkage)
		if s.g.partLocalHelpers == nil {
			s.g.partLocalHelpers = make(map[string]bool)
		}
		s.g.partLocalHelpers[llvmValueName(fnValue)] = true
	}
	if llvmCountBasicBlocks(fnValue) != 0 {
		return fnValue, nil
	}

	builder := llvmCreateBuilderInContext(s.g.context)
	defer llvmDisposeBuilder(builder)

	entry := llvmAppendBasicBlockInContext(s.g.context, fnValue, cStringFree("entry"))
	llvmPositionBuilderAtEnd(builder, entry)

	decl := &ast.FuncDecl{Name: helperName, Params: append([]ast.ParamDecl(nil), expr.Params...), Body: expr.Body}
	state := &functionState{
		g:       s.g,
		decl:    decl,
		fnValue: fnValue,
		fnType:  helperType,
		builder: builder,
	}

	paramOffset := 0
	if _, ok := nonVoidErrorUnion(helperType.Return); ok {
		state.resultSlot = llvmGetParam(fnValue, 0)
		paramOffset = 1
	}
	if hasEnv {
		envParam := llvmGetParam(fnValue, llvmIRUnsigned(paramOffset))
		for i, capture := range captures {
			fieldPtr := llvmBuildStructGEP2(builder, envType, envParam, llvmIRUnsigned(i), cStringFree(helperName+"."+capture.name+".env.ptr"))
			captureLLVMType, err := state.g.lowerType(capture.binding.typ)
			if err != nil {
				return nil, err
			}
			captureValue := llvmBuildLoad2(builder, captureLLVMType, fieldPtr, cStringFree(helperName+"."+capture.name+".env"))
			alloca, err := state.createEntryAlloca(capture.name, capture.binding.typ)
			if err != nil {
				return nil, err
			}
			llvmBuildStore(builder, captureValue, alloca)
			state.defineBinding(capture.name, valueBinding{ptr: alloca, typ: capture.binding.typ, mutable: capture.binding.mutable})
			state.bindPackedStoreValue(capture.binding.typ, captureValue)
		}
		paramOffset++
	}
	for i, param := range expr.Params {
		if i >= len(funcType.Params) {
			break
		}
		alloca, err := state.createEntryAlloca(param.Name, funcType.Params[i])
		if err != nil {
			return nil, err
		}
		paramValue := llvmGetParam(fnValue, llvmIRUnsigned(i+paramOffset))
		llvmBuildStore(builder, paramValue, alloca)
		state.defineBinding(param.Name, valueBinding{ptr: alloca, typ: funcType.Params[i], mutable: param.Mutable})
		state.bindPackedStoreValue(funcType.Params[i], paramValue)
	}
	if expr.BodyExpr != nil {
		value, actualType, err := state.emitExpr(expr.BodyExpr, funcType.Return)
		if err != nil {
			return nil, err
		}
		if err := state.emitFunctionReturn(value, actualType); err != nil {
			return nil, err
		}
		return fnValue, nil
	}
	if err := state.emitBlock(expr.Body, false); err != nil {
		return nil, err
	}
	if !state.currentBlockTerminated() {
		if err := state.emitActiveScopedCleanup(); err != nil {
			return nil, err
		}
		if state.currentBlockTerminated() {
			return fnValue, nil
		}
		if err := state.emitRegionCleanup(); err != nil {
			return nil, err
		}
		if isVoidType(helperType.Return) {
			llvmBuildRetVoid(builder)
		} else if retUnion, ok := helperType.Return.(*semantic.ErrorUnionType); ok && isVoidType(retUnion.Value) {
			zeroCode, err := state.errorCodeConstant(0)
			if err != nil {
				return nil, err
			}
			successValue, err := state.wrapVoidErrorUnionCode(retUnion.Errors, zeroCode)
			if err != nil {
				return nil, err
			}
			llvmBuildRet(builder, successValue)
		} else {
			return nil, fmt.Errorf("lambda helper %s may fall through without returning a value", helperName)
		}
	}
	return fnValue, nil
}

func (s *functionState) emitLambdaExpr(expr *ast.LambdaExpr) (llvmIRValueRef, semantic.Type, error) {
	funcType, ok := s.exprType(expr).(*semantic.FuncType)
	if !ok || funcType == nil {
		return nil, nil, fmt.Errorf("lambda expression is missing a function type")
	}
	captures, err := s.lambdaCaptureBindings(expr)
	if err != nil {
		return nil, nil, err
	}
	helperName := s.g.nextSyntheticHelperName("lambda_")
	if len(captures) == 0 {
		helperValue, err := s.emitLambdaHelper(expr, funcType, nil, nil, helperName)
		if err != nil {
			return nil, nil, err
		}
		return helperValue, funcType, nil
	}
	envPtr, envType, err := s.emitLambdaEnvironment(captures, helperName)
	if err != nil {
		return nil, nil, err
	}
	helperValue, err := s.emitLambdaHelper(expr, funcType, captures, envType, helperName)
	if err != nil {
		return nil, nil, err
	}
	closureType, err := s.g.ensureLambdaClosureType()
	if err != nil {
		return nil, nil, err
	}
	closureBytes, err := s.g.abiSizeOfLLVMType(closureType)
	if err != nil {
		return nil, nil, err
	}
	closurePtr, err := s.emitMallocBytes(closureBytes, helperName+".closure")
	if err != nil {
		return nil, nil, err
	}
	codeFieldPtr := llvmBuildStructGEP2(s.builder, closureType, closurePtr, 0, cStringFree(helperName+".closure.code.ptr"))
	envFieldPtr := llvmBuildStructGEP2(s.builder, closureType, closurePtr, 1, cStringFree(helperName+".closure.env.ptr"))
	llvmBuildStore(s.builder, helperValue, codeFieldPtr)
	llvmBuildStore(s.builder, envPtr, envFieldPtr)
	closureValue, err := s.tagLambdaClosurePointer(closurePtr, helperName+".closure")
	if err != nil {
		return nil, nil, err
	}
	return closureValue, funcType, nil
}

func (s *functionState) emitFunctionValueCall(calleeValue llvmIRValueRef, funcType *semantic.FuncType, callArgs []llvmIRValueRef, callName string) (llvmIRValueRef, error) {
	if funcType == nil {
		return nil, fmt.Errorf("missing function type for dynamic call")
	}
	rawLLVMType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, err
	}
	closureLLVMType, err := s.g.lowerFunctionType(s.lambdaClosureFunctionType(funcType))
	if err != nil {
		return nil, err
	}
	closureType, err := s.g.ensureLambdaClosureType()
	if err != nil {
		return nil, err
	}
	usizeType := s.g.namedType("usize")
	llvmSizeType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	llvmReturnType, err := s.g.lowerFunctionReturnType(funcType.Return)
	if err != nil {
		return nil, err
	}
	ptrType := llvmPointerTypeInContext(s.g.context, 0)
	tagMask := llvmConstInt(llvmSizeType, 1, 0)
	zero := llvmConstInt(llvmSizeType, 0, 0)
	allOnes := llvmConstAllOnes(llvmSizeType)
	clearMask := llvmConstSub(allOnes, tagMask)

	calleeInt := llvmBuildPtrToInt(s.builder, calleeValue, llvmSizeType, cStringFree(callName+".callee.int"))
	calleeTag := llvmBuildAnd(s.builder, calleeInt, tagMask, cStringFree(callName+".callee.tag"))
	isClosure := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntNE), calleeTag, zero, cStringFree(callName+".callee.is_closure"))

	rawBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(callName+".raw"))
	closureBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(callName+".closure"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(callName+".merge"))
	llvmBuildCondBr(s.builder, isClosure, closureBB, rawBB)

	incomingValues := make([]llvmIRValueRef, 0, 2)
	incomingBlocks := make([]llvmIRBlockRef, 0, 2)
	returnsVoid := isVoidType(funcType.Return)
	rawCallName := callName + ".raw"
	closureCallName := callName + ".closure"
	if returnsVoid {
		rawCallName = ""
		closureCallName = ""
	}

	llvmPositionBuilderAtEnd(s.builder, rawBB)
	rawCall := s.buildTypedCall(rawLLVMType, calleeValue, callArgs, rawCallName, funcType)
	if !s.currentBlockTerminated() {
		rawEnd := llvmGetInsertBlock(s.builder)
		llvmBuildBr(s.builder, mergeBB)
		if !returnsVoid {
			incomingValues = append(incomingValues, rawCall)
			incomingBlocks = append(incomingBlocks, rawEnd)
		}
	}

	llvmPositionBuilderAtEnd(s.builder, closureBB)
	closureInt := llvmBuildAnd(s.builder, calleeInt, clearMask, cStringFree(callName+".closure.int"))
	closurePtr := llvmBuildIntToPtr(s.builder, closureInt, ptrType, cStringFree(callName+".closure.ptr"))
	codePtrPtr := llvmBuildStructGEP2(s.builder, closureType, closurePtr, 0, cStringFree(callName+".closure.code.ptr"))
	envPtrPtr := llvmBuildStructGEP2(s.builder, closureType, closurePtr, 1, cStringFree(callName+".closure.env.ptr"))
	codePtr := llvmBuildLoad2(s.builder, ptrType, codePtrPtr, cStringFree(callName+".closure.code"))
	envPtr := llvmBuildLoad2(s.builder, ptrType, envPtrPtr, cStringFree(callName+".closure.env"))
	closureArgs := make([]llvmIRValueRef, 0, len(callArgs)+1)
	if _, ok := nonVoidErrorUnion(funcType.Return); ok {
		closureArgs = append(closureArgs, callArgs[0], envPtr)
		closureArgs = append(closureArgs, callArgs[1:]...)
	} else {
		closureArgs = append(closureArgs, envPtr)
		closureArgs = append(closureArgs, callArgs...)
	}
	closureCall := s.buildTypedCall(closureLLVMType, codePtr, closureArgs, closureCallName, funcType)
	if !s.currentBlockTerminated() {
		closureEnd := llvmGetInsertBlock(s.builder)
		llvmBuildBr(s.builder, mergeBB)
		if !returnsVoid {
			incomingValues = append(incomingValues, closureCall)
			incomingBlocks = append(incomingBlocks, closureEnd)
		}
	}

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	if returnsVoid {
		return nil, nil
	}
	if len(incomingValues) == 1 {
		return incomingValues[0], nil
	}
	phi := llvmBuildPhi(s.builder, llvmReturnType, cStringFree(callName))
	llvmAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), llvmIRUnsigned(len(incomingBlocks)))
	return phi, nil
}
