target datalayout = "e-m:o-p270:32:32-p271:32:32-p272:64:64-i64:64-n32:64-S128"
target triple = "arm64-apple-darwin27.2.0"

declare void @llvm.memset.p0.i64(ptr nocapture writeonly, i8, i64, i1 immarg)
declare void @capture(ptr)
declare void @unknown()
@leaked_ptr = global ptr null
@leaked_integer = global i64 0

define i64 @good() {
  %a = alloca [32 x i8], align 8
  %start = getelementptr inbounds i8, ptr %a, i64 8
  call void @llvm.memset.p0.i64(ptr %start, i8 0, i64 16, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @good_disjoint_store() {
  %a = alloca [32 x i8], align 8
  %start = getelementptr inbounds i8, ptr %a, i64 8
  call void @llvm.memset.p0.i64(ptr %start, i8 0, i64 16, i1 false)
  store i64 42, ptr %a, align 8
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @marker_vector(i64 %n) {
entry:
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %vec = insertelement <2 x i64> poison, i64 %v, i64 0
  %next = add i64 %i, 1
  %more = icmp ult i64 %next, %n
  br i1 %more, label %loop, label %exit, !llvm.loop !8
exit:
  ret i64 %v
}

define i64 @marker_scalar(i64 %n) {
entry:
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %next = add i64 %i, 1
  %more = icmp ult i64 %next, %n
  br i1 %more, label %loop, label %exit, !llvm.loop !10
exit:
  ret i64 %v
}

define i64 @overlap_store() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 16
  store i8 3, ptr %p, align 1
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @unknown_store(ptr %external) {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  store i64 1, ptr %external, align 8
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @escaped() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  call void @capture(ptr %a)
  ret i64 %v
}

define i64 @escaped_to_global() {
  %a = alloca [32 x i8], align 8
  store ptr %a, ptr @leaked_ptr
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @escaped_as_integer() {
  %a = alloca [32 x i8], align 8
  %address = ptrtoint ptr %a to i64
  store i64 %address, ptr @leaked_integer
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @unknown_call() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  call void @unknown()
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @volatile_load() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load volatile i64, ptr %p, align 8
  ret i64 %v
}

define i64 @atomic_load() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load atomic i64, ptr %p monotonic, align 8
  ret i64 %v
}

define i64 @volatile_store() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  store volatile i64 42, ptr %a, align 8
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @fence_case() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  fence seq_cst
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @volatile_memset() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 true)
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @nonzero_memset() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 1, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @partial_memset() {
  %a = alloca [32 x i8], align 8
  %p = getelementptr inbounds i8, ptr %a, i64 16
  call void @llvm.memset.p0.i64(ptr %p, i8 0, i64 4, i1 false)
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @out_of_bounds() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 32
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @dynamic_offset(i64 %offset) {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 %offset
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @not_inbounds() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @poison_intermediate_gep() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %negative = getelementptr inbounds i8, ptr %a, i64 -1
  %p = getelementptr inbounds i8, ptr %negative, i64 17
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @dynamic_memset_size(i64 %length) {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 %length, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @dynamic_alloca(i64 %length) {
  %a = alloca i8, i64 %length, align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @bad_alignment() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 8
  %v = load i64, ptr %p, align 16
  ret i64 %v
}

define i64 @range_metadata() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8, !range !0
  ret i64 %v
}

define i64 @other_block() {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  br label %next
next:
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8
  ret i64 %v
}

define i64 @debug_function() !dbg !4 {
  %a = alloca [32 x i8], align 8
  call void @llvm.memset.p0.i64(ptr %a, i8 0, i64 32, i1 false)
  %p = getelementptr inbounds i8, ptr %a, i64 16
  %v = load i64, ptr %p, align 8, !dbg !7
  ret i64 %v
}

!llvm.dbg.cu = !{!1}
!llvm.module.flags = !{!3}
!0 = !{i64 1, i64 10}
!1 = distinct !DICompileUnit(language: DW_LANG_C, file: !2, producer: "prototype", isOptimized: true, runtimeVersion: 0, emissionKind: FullDebug)
!2 = !DIFile(filename: "prototype.c", directory: "/tmp")
!3 = !{i32 2, !"Debug Info Version", i32 3}
!4 = distinct !DISubprogram(name: "debug_function", scope: !2, file: !2, line: 1, type: !5, scopeLine: 1, spFlags: DISPFlagDefinition, unit: !1)
!5 = !DISubroutineType(types: !6)
!6 = !{}
!7 = !DILocation(line: 1, column: 1, scope: !4)
!8 = distinct !{!8, !9, !12}
!9 = !{!"elisa.autovec.expected", !"fixture:1", !"loop"}
!10 = distinct !{!10, !11, !12}
!11 = !{!"elisa.autovec.expected", !"fixture:2", !"loop"}
!12 = !{!"llvm.loop.isvectorized", i32 1}
