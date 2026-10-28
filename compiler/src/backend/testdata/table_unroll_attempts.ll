target triple = "arm64-apple-darwin27.2.0"
target datalayout = "e-m:o-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-n32:64-S128-Fn32"
@table = internal constant [48 x i64] [i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11, i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11, i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11, i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11]
define i64 @reject_0() noinline optnone  {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %acc = phi i64 [ 0, %entry ], [ %sum4, %loop ]
  %ix0 = add i64 %i, 0
  %p0 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix0
  %v0 = load i64, ptr %p0, align 8
  %sum1 = add i64 %acc, %v0
  %ix1 = add i64 %i, 12
  %p1 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix1
  %v1 = load i64, ptr %p1, align 8
  %sum2 = add i64 %sum1, %v1
  %ix2 = add i64 %i, 24
  %p2 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix2
  %v2 = load i64, ptr %p2, align 8
  %sum3 = add i64 %sum2, %v2
  %ix3 = add i64 %i, 36
  %p3 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix3
  %v3 = load i64, ptr %p3, align 8
  %sum4 = add i64 %sum3, %v3
  %next = add nuw nsw i64 %i, 1
  %done = icmp eq i64 %next, 12
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
define i64 @reject_1() noinline optnone  {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %acc = phi i64 [ 0, %entry ], [ %sum4, %loop ]
  %ix0 = add i64 %i, 0
  %p0 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix0
  %v0 = load i64, ptr %p0, align 8
  %sum1 = add i64 %acc, %v0
  %ix1 = add i64 %i, 12
  %p1 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix1
  %v1 = load i64, ptr %p1, align 8
  %sum2 = add i64 %sum1, %v1
  %ix2 = add i64 %i, 24
  %p2 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix2
  %v2 = load i64, ptr %p2, align 8
  %sum3 = add i64 %sum2, %v2
  %ix3 = add i64 %i, 36
  %p3 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix3
  %v3 = load i64, ptr %p3, align 8
  %sum4 = add i64 %sum3, %v3
  %next = add nuw nsw i64 %i, 1
  %done = icmp eq i64 %next, 12
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
define i64 @reject_2() noinline optnone  {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %acc = phi i64 [ 0, %entry ], [ %sum4, %loop ]
  %ix0 = add i64 %i, 0
  %p0 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix0
  %v0 = load i64, ptr %p0, align 8
  %sum1 = add i64 %acc, %v0
  %ix1 = add i64 %i, 12
  %p1 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix1
  %v1 = load i64, ptr %p1, align 8
  %sum2 = add i64 %sum1, %v1
  %ix2 = add i64 %i, 24
  %p2 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix2
  %v2 = load i64, ptr %p2, align 8
  %sum3 = add i64 %sum2, %v2
  %ix3 = add i64 %i, 36
  %p3 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix3
  %v3 = load i64, ptr %p3, align 8
  %sum4 = add i64 %sum3, %v3
  %next = add nuw nsw i64 %i, 1
  %done = icmp eq i64 %next, 12
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
define i64 @reject_3() noinline optnone  {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %acc = phi i64 [ 0, %entry ], [ %sum4, %loop ]
  %ix0 = add i64 %i, 0
  %p0 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix0
  %v0 = load i64, ptr %p0, align 8
  %sum1 = add i64 %acc, %v0
  %ix1 = add i64 %i, 12
  %p1 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix1
  %v1 = load i64, ptr %p1, align 8
  %sum2 = add i64 %sum1, %v1
  %ix2 = add i64 %i, 24
  %p2 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix2
  %v2 = load i64, ptr %p2, align 8
  %sum3 = add i64 %sum2, %v2
  %ix3 = add i64 %i, 36
  %p3 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix3
  %v3 = load i64, ptr %p3, align 8
  %sum4 = add i64 %sum3, %v3
  %next = add nuw nsw i64 %i, 1
  %done = icmp eq i64 %next, 12
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
define i64 @reject_4() noinline optnone  {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %acc = phi i64 [ 0, %entry ], [ %sum4, %loop ]
  %ix0 = add i64 %i, 0
  %p0 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix0
  %v0 = load i64, ptr %p0, align 8
  %sum1 = add i64 %acc, %v0
  %ix1 = add i64 %i, 12
  %p1 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix1
  %v1 = load i64, ptr %p1, align 8
  %sum2 = add i64 %sum1, %v1
  %ix2 = add i64 %i, 24
  %p2 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix2
  %v2 = load i64, ptr %p2, align 8
  %sum3 = add i64 %sum2, %v2
  %ix3 = add i64 %i, 36
  %p3 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix3
  %v3 = load i64, ptr %p3, align 8
  %sum4 = add i64 %sum3, %v3
  %next = add nuw nsw i64 %i, 1
  %done = icmp eq i64 %next, 12
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
define i64 @reject_5() noinline optnone  {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %acc = phi i64 [ 0, %entry ], [ %sum4, %loop ]
  %ix0 = add i64 %i, 0
  %p0 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix0
  %v0 = load i64, ptr %p0, align 8
  %sum1 = add i64 %acc, %v0
  %ix1 = add i64 %i, 12
  %p1 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix1
  %v1 = load i64, ptr %p1, align 8
  %sum2 = add i64 %sum1, %v1
  %ix2 = add i64 %i, 24
  %p2 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix2
  %v2 = load i64, ptr %p2, align 8
  %sum3 = add i64 %sum2, %v2
  %ix3 = add i64 %i, 36
  %p3 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix3
  %v3 = load i64, ptr %p3, align 8
  %sum4 = add i64 %sum3, %v3
  %next = add nuw nsw i64 %i, 1
  %done = icmp eq i64 %next, 12
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
define i64 @reject_6() noinline optnone  {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %acc = phi i64 [ 0, %entry ], [ %sum4, %loop ]
  %ix0 = add i64 %i, 0
  %p0 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix0
  %v0 = load i64, ptr %p0, align 8
  %sum1 = add i64 %acc, %v0
  %ix1 = add i64 %i, 12
  %p1 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix1
  %v1 = load i64, ptr %p1, align 8
  %sum2 = add i64 %sum1, %v1
  %ix2 = add i64 %i, 24
  %p2 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix2
  %v2 = load i64, ptr %p2, align 8
  %sum3 = add i64 %sum2, %v2
  %ix3 = add i64 %i, 36
  %p3 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix3
  %v3 = load i64, ptr %p3, align 8
  %sum4 = add i64 %sum3, %v3
  %next = add nuw nsw i64 %i, 1
  %done = icmp eq i64 %next, 12
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
define i64 @reject_7() noinline optnone  {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %acc = phi i64 [ 0, %entry ], [ %sum4, %loop ]
  %ix0 = add i64 %i, 0
  %p0 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix0
  %v0 = load i64, ptr %p0, align 8
  %sum1 = add i64 %acc, %v0
  %ix1 = add i64 %i, 12
  %p1 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix1
  %v1 = load i64, ptr %p1, align 8
  %sum2 = add i64 %sum1, %v1
  %ix2 = add i64 %i, 24
  %p2 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix2
  %v2 = load i64, ptr %p2, align 8
  %sum3 = add i64 %sum2, %v2
  %ix3 = add i64 %i, 36
  %p3 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix3
  %v3 = load i64, ptr %p3, align 8
  %sum4 = add i64 %sum3, %v3
  %next = add nuw nsw i64 %i, 1
  %done = icmp eq i64 %next, 12
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
