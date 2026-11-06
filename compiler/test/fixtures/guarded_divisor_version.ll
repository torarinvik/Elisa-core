; The input check and store must remain before the specialization dispatch.
define i64 @work(ptr %input, ptr %count, i1 %skip) {
entry:
  %null = icmp eq ptr %input, null
  br i1 %null, label %invalid, label %prefix
prefix:
  %prior = load i64, ptr %count
  %next = add i64 %prior, 1
  store i64 %next, ptr %count
  br i1 %skip, label %early, label %load
load:
  %raw = load i64, ptr %input
  %div = freeze i64 %raw
  %zero = icmp eq i64 %div, 0
  br i1 %zero, label %invalid, label %preheader
preheader:
  br label %loop
loop:
  %i = phi i64 [0, %preheader], [%inc, %loop]
  %sum = phi i64 [0, %preheader], [%sum2, %loop]
  %r1 = urem i64 %i, %div
  %i1 = add i64 %i, 1
  %r2 = urem i64 %i1, %div
  %sum1 = add i64 %sum, %r1
  %sum2 = add i64 %sum1, %r2
  %inc = add i64 %i, 1
  %done = icmp eq i64 %inc, 10
  br i1 %done, label %exit, label %loop, !llvm.loop !0
exit:
  br label %result
invalid:
  br label %result
early:
  br label %result
result:
  %ret = phi i64 [%sum2, %exit], [-9, %invalid], [-8, %early]
  ret i64 %ret
}
!0 = distinct !{!0, !1}
!1 = !{!"llvm.loop.unroll.disable"}
