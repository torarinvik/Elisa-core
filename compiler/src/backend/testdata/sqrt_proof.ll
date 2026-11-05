; ModuleID = 'dispersion.elisa'
source_filename = "dispersion.elisa"
target datalayout = "e-m:o-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-n32:64-S128-Fn32"
target triple = "arm64-apple-darwin27.2.0"

; Function Attrs: mustprogress nocallback nofree nosync nounwind willreturn memory(errnomem: write)
declare double @sqrt(double) local_unnamed_addr #0

; Function Attrs: nofree norecurse nosync nounwind memory(argmem: readwrite, errnomem: write)
define range(i64 -2, 17) i64 @dispersion(ptr nofree readonly captures(address_is_null) %0, i64 %1, ptr nofree writeonly captures(address_is_null) %2, i64 %3) local_unnamed_addr #1 {
entry:
  %.fr = freeze i64 %1
  %ptrcmptmp.i = icmp eq ptr %0, null
  %4 = add i64 %.fr, -129
  %or.cond.i = icmp ult i64 %4, -128
  %or.cond82.i = or i1 %ptrcmptmp.i, %or.cond.i
  br i1 %or.cond82.i, label %dispersion_impl.exit, label %for.body.i.preheader

for.body.i.preheader:                             ; preds = %entry
  %idx.i.peel = load i64, ptr %0, align 8
  %5 = add i64 %idx.i.peel, -10001
  %or.cond83.i.peel = icmp ult i64 %5, -20001
  br i1 %or.cond83.i.peel, label %dispersion_impl.exit, label %for.cond.i.peel

for.cond.i.peel:                                  ; preds = %for.body.i.preheader
  %exitcond.not.i.peel = icmp eq i64 %.fr, 1
  br i1 %exitcond.not.i.peel, label %for.end.i, label %for.body.i.peel.next

for.body.i.peel.next:                             ; preds = %for.cond.i.peel
  %6 = add nuw nsw i64 %.fr, 3
  %7 = add nsw i64 %.fr, -2
  %xtraiter = and i64 %6, 3
  %lcmp.mod.not = icmp eq i64 %xtraiter, 0
  br i1 %lcmp.mod.not, label %for.body.i.prol.loopexit, label %for.body.i.prol.preheader

for.body.i.prol.preheader:                        ; preds = %for.body.i.peel.next
  %idx.ptr.i.prol = getelementptr i8, ptr %0, i64 8
  %idx.i.prol = load i64, ptr %idx.ptr.i.prol, align 8
  %8 = add i64 %idx.i.prol, -10001
  %or.cond83.i.prol = icmp ult i64 %8, -20001
  br i1 %or.cond83.i.prol, label %dispersion_impl.exit, label %for.cond.i.prol

for.cond.i.prol:                                  ; preds = %for.body.i.prol.preheader
  %prol.iter.cmp.not = icmp eq i64 %xtraiter, 1
  br i1 %prol.iter.cmp.not, label %for.body.i.prol.loopexit, label %for.body.i.prol.1

for.body.i.prol.1:                                ; preds = %for.cond.i.prol
  %idx.ptr.i.prol.1 = getelementptr i8, ptr %0, i64 16
  %idx.i.prol.1 = load i64, ptr %idx.ptr.i.prol.1, align 8
  %9 = add i64 %idx.i.prol.1, -10001
  %or.cond83.i.prol.1 = icmp ult i64 %9, -20001
  br i1 %or.cond83.i.prol.1, label %dispersion_impl.exit, label %for.cond.i.prol.1

for.cond.i.prol.1:                                ; preds = %for.body.i.prol.1
  %prol.iter.cmp.1.not = icmp eq i64 %xtraiter, 2
  br i1 %prol.iter.cmp.1.not, label %for.body.i.prol.loopexit, label %for.body.i.prol.2

for.body.i.prol.2:                                ; preds = %for.cond.i.prol.1
  %idx.ptr.i.prol.2 = getelementptr i8, ptr %0, i64 24
  %idx.i.prol.2 = load i64, ptr %idx.ptr.i.prol.2, align 8
  %10 = add i64 %idx.i.prol.2, -10001
  %or.cond83.i.prol.2 = icmp ult i64 %10, -20001
  br i1 %or.cond83.i.prol.2, label %dispersion_impl.exit, label %for.body.i.prol.loopexit

for.body.i.prol.loopexit:                         ; preds = %for.cond.i.prol, %for.cond.i.prol.1, %for.body.i.prol.2, %for.body.i.peel.next
  %i.for.cur.0107.i.unr = phi i64 [ 1, %for.body.i.peel.next ], [ 2, %for.cond.i.prol ], [ 3, %for.cond.i.prol.1 ], [ 4, %for.body.i.prol.2 ]
  %11 = icmp ult i64 %7, 3
  br i1 %11, label %for.end.i, label %for.body.i

for.cond.i:                                       ; preds = %for.body.i
  %idx.ptr.i.1 = getelementptr i8, ptr %idx.ptr.i, i64 8
  %idx.i.1 = load i64, ptr %idx.ptr.i.1, align 8
  %12 = add i64 %idx.i.1, -10001
  %or.cond83.i.1 = icmp ult i64 %12, -20001
  br i1 %or.cond83.i.1, label %dispersion_impl.exit, label %for.cond.i.1

for.cond.i.1:                                     ; preds = %for.cond.i
  %idx.ptr.i.2 = getelementptr i8, ptr %idx.ptr.i, i64 16
  %idx.i.2 = load i64, ptr %idx.ptr.i.2, align 8
  %13 = add i64 %idx.i.2, -10001
  %or.cond83.i.2 = icmp ult i64 %13, -20001
  br i1 %or.cond83.i.2, label %dispersion_impl.exit, label %for.cond.i.2

for.cond.i.2:                                     ; preds = %for.cond.i.1
  %idx.ptr.i.3 = getelementptr i8, ptr %idx.ptr.i, i64 24
  %idx.i.3 = load i64, ptr %idx.ptr.i.3, align 8
  %14 = add i64 %idx.i.3, -10001
  %or.cond83.i.3 = icmp ult i64 %14, -20001
  br i1 %or.cond83.i.3, label %dispersion_impl.exit, label %for.cond.i.3

for.cond.i.3:                                     ; preds = %for.cond.i.2
  %for.next.asc.i.3 = add nuw nsw i64 %i.for.cur.0107.i, 4
  %exitcond.not.i.3 = icmp eq i64 %for.next.asc.i.3, %.fr
  br i1 %exitcond.not.i.3, label %for.end.i, label %for.body.i, !llvm.loop !0

for.body.i:                                       ; preds = %for.body.i.prol.loopexit, %for.cond.i.3
  %i.for.cur.0107.i = phi i64 [ %for.next.asc.i.3, %for.cond.i.3 ], [ %i.for.cur.0107.i.unr, %for.body.i.prol.loopexit ]
  %idx.ptr.i = getelementptr [8 x i8], ptr %0, i64 %i.for.cur.0107.i
  %idx.i = load i64, ptr %idx.ptr.i, align 8
  %15 = add i64 %idx.i, -10001
  %or.cond83.i = icmp ult i64 %15, -20001
  br i1 %or.cond83.i, label %dispersion_impl.exit, label %for.cond.i

for.end.i:                                        ; preds = %for.body.i.prol.loopexit, %for.cond.i.3, %for.cond.i.peel
  %ptrcmptmp24.i = icmp eq ptr %2, null
  %cmptmp26.i = icmp ult i64 %3, 16
  %or.cond80.i = select i1 %ptrcmptmp24.i, i1 true, i1 %cmptmp26.i
  br i1 %or.cond80.i, label %dispersion_impl.exit, label %for.body31.i.preheader

for.body31.i.preheader:                           ; preds = %for.end.i
  %min.iters.check = icmp ult i64 %.fr, 8
  br i1 %min.iters.check, label %for.body31.i.preheader24, label %vector.ph

vector.ph:                                        ; preds = %for.body31.i.preheader
  %n.vec = and i64 %.fr, 248
  br label %vector.body

vector.body:                                      ; preds = %vector.body, %vector.ph
  %index = phi i64 [ 0, %vector.ph ], [ %index.next, %vector.body ]
  %vec.phi = phi double [ 0.000000e+00, %vector.ph ], [ %27, %vector.body ]
  %16 = getelementptr [8 x i8], ptr %0, i64 %index
  %17 = getelementptr i8, ptr %16, i64 16
  %18 = getelementptr i8, ptr %16, i64 32
  %19 = getelementptr i8, ptr %16, i64 48
  %wide.load = load <2 x i64>, ptr %16, align 8
  %wide.load3 = load <2 x i64>, ptr %17, align 8
  %wide.load4 = load <2 x i64>, ptr %18, align 8
  %wide.load5 = load <2 x i64>, ptr %19, align 8
  %20 = sitofp <2 x i64> %wide.load to <2 x double>
  %21 = sitofp <2 x i64> %wide.load3 to <2 x double>
  %22 = sitofp <2 x i64> %wide.load4 to <2 x double>
  %23 = sitofp <2 x i64> %wide.load5 to <2 x double>
  %24 = tail call contract double @llvm.vector.reduce.fadd.v2f64(double %vec.phi, <2 x double> %20)
  %25 = tail call contract double @llvm.vector.reduce.fadd.v2f64(double %24, <2 x double> %21)
  %26 = tail call contract double @llvm.vector.reduce.fadd.v2f64(double %25, <2 x double> %22)
  %27 = tail call contract double @llvm.vector.reduce.fadd.v2f64(double %26, <2 x double> %23)
  %index.next = add nuw i64 %index, 8
  %28 = icmp eq i64 %index.next, %n.vec
  br i1 %28, label %middle.block, label %vector.body, !llvm.loop !2

middle.block:                                     ; preds = %vector.body
  %cmp.n = icmp eq i64 %.fr, %n.vec
  br i1 %cmp.n, label %for.body52.preheader.i, label %for.body31.i.preheader24

for.body31.i.preheader24:                         ; preds = %for.body31.i.preheader, %middle.block
  %sum.0110.i.ph = phi double [ 0.000000e+00, %for.body31.i.preheader ], [ %27, %middle.block ]
  %i.for.cur28.0109.i.ph = phi i64 [ 0, %for.body31.i.preheader ], [ %n.vec, %middle.block ]
  br label %for.body31.i

for.body31.i:                                     ; preds = %for.body31.i.preheader24, %for.body31.i
  %sum.0110.i = phi double [ %addtmp.i, %for.body31.i ], [ %sum.0110.i.ph, %for.body31.i.preheader24 ]
  %i.for.cur28.0109.i = phi i64 [ %for.next.asc43.i, %for.body31.i ], [ %i.for.cur28.0109.i.ph, %for.body31.i.preheader24 ]
  %idx.ptr41.i = getelementptr [8 x i8], ptr %0, i64 %i.for.cur28.0109.i
  %idx42.i = load i64, ptr %idx.ptr41.i, align 8
  %sitofp.i = sitofp i64 %idx42.i to double
  %addtmp.i = fadd contract double %sum.0110.i, %sitofp.i
  %for.next.asc43.i = add nuw nsw i64 %i.for.cur28.0109.i, 1
  %exitcond115.not.i = icmp eq i64 %for.next.asc43.i, %.fr
  br i1 %exitcond115.not.i, label %for.body52.preheader.i, label %for.body31.i, !llvm.loop !5

for.body52.preheader.i:                           ; preds = %for.body31.i, %middle.block
  %addtmp.i.lcssa = phi double [ %27, %middle.block ], [ %addtmp.i, %for.body31.i ]
  %uitofp.i = uitofp i64 %.fr to double
  %divtmp.i = fdiv arcp contract double %addtmp.i.lcssa, %uitofp.i
  br i1 %min.iters.check, label %for.body52.i.preheader, label %vector.ph8

vector.ph8:                                       ; preds = %for.body52.preheader.i
  %n.vec10 = and i64 %.fr, 248
  %broadcast.splatinsert = insertelement <2 x double> poison, double %divtmp.i, i64 0
  %broadcast.splat = shufflevector <2 x double> %broadcast.splatinsert, <2 x double> poison, <2 x i32> zeroinitializer
  br label %vector.body11

vector.body11:                                    ; preds = %vector.body11, %vector.ph8
  %index12 = phi i64 [ 0, %vector.ph8 ], [ %index.next18, %vector.body11 ]
  %vec.phi13 = phi double [ 0.000000e+00, %vector.ph8 ], [ %48, %vector.body11 ]
  %29 = getelementptr [8 x i8], ptr %0, i64 %index12
  %30 = getelementptr i8, ptr %29, i64 16
  %31 = getelementptr i8, ptr %29, i64 32
  %32 = getelementptr i8, ptr %29, i64 48
  %wide.load14 = load <2 x i64>, ptr %29, align 8
  %wide.load15 = load <2 x i64>, ptr %30, align 8
  %wide.load16 = load <2 x i64>, ptr %31, align 8
  %wide.load17 = load <2 x i64>, ptr %32, align 8
  %33 = sitofp <2 x i64> %wide.load14 to <2 x double>
  %34 = sitofp <2 x i64> %wide.load15 to <2 x double>
  %35 = sitofp <2 x i64> %wide.load16 to <2 x double>
  %36 = sitofp <2 x i64> %wide.load17 to <2 x double>
  %37 = fsub contract <2 x double> %33, %broadcast.splat
  %38 = fsub contract <2 x double> %34, %broadcast.splat
  %39 = fsub contract <2 x double> %35, %broadcast.splat
  %40 = fsub contract <2 x double> %36, %broadcast.splat
  %41 = fmul contract <2 x double> %37, %37
  %42 = fmul contract <2 x double> %38, %38
  %43 = fmul contract <2 x double> %39, %39
  %44 = fmul contract <2 x double> %40, %40
  %45 = tail call contract double @llvm.vector.reduce.fadd.v2f64(double %vec.phi13, <2 x double> %41)
  %46 = tail call contract double @llvm.vector.reduce.fadd.v2f64(double %45, <2 x double> %42)
  %47 = tail call contract double @llvm.vector.reduce.fadd.v2f64(double %46, <2 x double> %43)
  %48 = tail call contract double @llvm.vector.reduce.fadd.v2f64(double %47, <2 x double> %44)
  %index.next18 = add nuw i64 %index12, 8
  %49 = icmp eq i64 %index.next18, %n.vec10
  br i1 %49, label %middle.block19, label %vector.body11, !llvm.loop !6

middle.block19:                                   ; preds = %vector.body11
  %cmp.n20 = icmp eq i64 %.fr, %n.vec10
  br i1 %cmp.n20, label %for.end54.i, label %for.body52.i.preheader

for.body52.i.preheader:                           ; preds = %for.body52.preheader.i, %middle.block19
  %i.for.cur49.0113.i.ph = phi i64 [ 0, %for.body52.preheader.i ], [ %n.vec10, %middle.block19 ]
  %squared_deviations.0112.i.ph = phi double [ 0.000000e+00, %for.body52.preheader.i ], [ %48, %middle.block19 ]
  br label %for.body52.i

for.body52.i:                                     ; preds = %for.body52.i.preheader, %for.body52.i
  %i.for.cur49.0113.i = phi i64 [ %for.next.asc69.i, %for.body52.i ], [ %i.for.cur49.0113.i.ph, %for.body52.i.preheader ]
  %squared_deviations.0112.i = phi double [ %addtmp68.i, %for.body52.i ], [ %squared_deviations.0112.i.ph, %for.body52.i.preheader ]
  %idx.ptr61.i = getelementptr [8 x i8], ptr %0, i64 %i.for.cur49.0113.i
  %idx62.i = load i64, ptr %idx.ptr61.i, align 8
  %sitofp63.i = sitofp i64 %idx62.i to double
  %subtmp.i = fsub contract double %sitofp63.i, %divtmp.i
  %multmp.i = fmul contract double %subtmp.i, %subtmp.i
  %addtmp68.i = fadd contract double %squared_deviations.0112.i, %multmp.i
  %for.next.asc69.i = add nuw nsw i64 %i.for.cur49.0113.i, 1
  %exitcond116.not.i = icmp eq i64 %for.next.asc69.i, %.fr
  br i1 %exitcond116.not.i, label %for.end54.i, label %for.body52.i, !llvm.loop !7

for.end54.i:                                      ; preds = %for.body52.i, %middle.block19
  %addtmp68.i.lcssa = phi double [ %48, %middle.block19 ], [ %addtmp68.i, %for.body52.i ]
  %divtmp75.i = fdiv arcp contract double %addtmp68.i.lcssa, %uitofp.i
  store double %divtmp75.i, ptr %2, align 1
  %calltmp.i = tail call double @sqrt(double %divtmp75.i)
  %50 = getelementptr i8, ptr %2, i64 8
  store double %calltmp.i, ptr %50, align 1
  br label %dispersion_impl.exit

dispersion_impl.exit:                             ; preds = %for.body.i.preheader, %for.body.i.prol.preheader, %for.body.i.prol.1, %for.body.i.prol.2, %for.body.i, %for.cond.i, %for.cond.i.1, %for.cond.i.2, %entry, %for.end.i, %for.end54.i
  %common.ret.op.i = phi i64 [ 16, %for.end54.i ], [ -2, %for.end.i ], [ -1, %entry ], [ -1, %for.cond.i.2 ], [ -1, %for.cond.i.1 ], [ -1, %for.cond.i ], [ -1, %for.body.i ], [ -1, %for.body.i.prol.2 ], [ -1, %for.body.i.prol.1 ], [ -1, %for.body.i.prol.preheader ], [ -1, %for.body.i.preheader ]
  ret i64 %common.ret.op.i
}

; Function Attrs: nocallback nocreateundeforpoison nofree nosync nounwind speculatable willreturn memory(none)
declare double @llvm.vector.reduce.fadd.v2f64(double, <2 x double>) #2

attributes #0 = { mustprogress nocallback nofree nosync nounwind willreturn memory(errnomem: write) }
attributes #1 = { nofree norecurse nosync nounwind memory(argmem: readwrite, errnomem: write) }
attributes #2 = { nocallback nocreateundeforpoison nofree nosync nounwind speculatable willreturn memory(none) }

!0 = distinct !{!0, !1}
!1 = !{!"llvm.loop.peeled.count", i32 1}
!2 = distinct !{!2, !3, !4}
!3 = !{!"llvm.loop.isvectorized", i32 1}
!4 = !{!"llvm.loop.unroll.runtime.disable"}
!5 = distinct !{!5, !4, !3}
!6 = distinct !{!6, !3, !4}
!7 = distinct !{!7, !4, !3}
