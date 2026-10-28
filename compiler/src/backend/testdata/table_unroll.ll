target triple = "arm64-apple-darwin27.2.0"
target datalayout = "e-m:o-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-n32:64-S128-Fn32"
@table = internal constant [48 x i64] [i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11, i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11, i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11, i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11]
@mutable_table = internal global [48 x i64] [i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11, i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11, i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11, i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11]
@table64 = internal constant [64 x i64] [i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11, i64 12, i64 13, i64 14, i64 15, i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11, i64 12, i64 13, i64 14, i64 15, i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11, i64 12, i64 13, i64 14, i64 15, i64 0, i64 1, i64 2, i64 3, i64 4, i64 5, i64 6, i64 7, i64 8, i64 9, i64 10, i64 11, i64 12, i64 13, i64 14, i64 15]
define i64 @positive()  {
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
define i64 @trip2()  {
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
  %done = icmp eq i64 %next, 2
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
define i64 @trip16()  {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %acc = phi i64 [ 0, %entry ], [ %sum4, %loop ]
  %ix0 = add i64 %i, 0
  %p0 = getelementptr [64 x i64], ptr @table64, i64 0, i64 %ix0
  %v0 = load i64, ptr %p0, align 8
  %sum1 = add i64 %acc, %v0
  %ix1 = add i64 %i, 16
  %p1 = getelementptr [64 x i64], ptr @table64, i64 0, i64 %ix1
  %v1 = load i64, ptr %p1, align 8
  %sum2 = add i64 %sum1, %v1
  %ix2 = add i64 %i, 32
  %p2 = getelementptr [64 x i64], ptr @table64, i64 0, i64 %ix2
  %v2 = load i64, ptr %p2, align 8
  %sum3 = add i64 %sum2, %v2
  %ix3 = add i64 %i, 48
  %p3 = getelementptr [64 x i64], ptr @table64, i64 0, i64 %ix3
  %v3 = load i64, ptr %p3, align 8
  %sum4 = add i64 %sum3, %v3
  %next = add nuw nsw i64 %i, 1
  %done = icmp eq i64 %next, 16
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
define i64 @dynamic(i64 %limit)  {
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
  %done = icmp eq i64 %next, %limit
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
define i64 @counter_independent()  {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %acc = phi i64 [ 0, %entry ], [ %sum4, %loop ]
  %ix0 = add i64 %i, 0
  %p0 = getelementptr [48 x i64], ptr @table, i64 0, i64 0
  %v0 = load i64, ptr %p0, align 8
  %sum1 = add i64 %acc, %v0
  %ix1 = add i64 %i, 12
  %p1 = getelementptr [48 x i64], ptr @table, i64 0, i64 12
  %v1 = load i64, ptr %p1, align 8
  %sum2 = add i64 %sum1, %v1
  %ix2 = add i64 %i, 24
  %p2 = getelementptr [48 x i64], ptr @table, i64 0, i64 24
  %v2 = load i64, ptr %p2, align 8
  %sum3 = add i64 %sum2, %v2
  %ix3 = add i64 %i, 36
  %p3 = getelementptr [48 x i64], ptr @table, i64 0, i64 36
  %v3 = load i64, ptr %p3, align 8
  %sum4 = add i64 %sum3, %v3
  %next = add nuw nsw i64 %i, 1
  %done = icmp eq i64 %next, 12
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
define i64 @volatile()  {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %acc = phi i64 [ 0, %entry ], [ %sum4, %loop ]
  %ix0 = add i64 %i, 0
  %p0 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix0
  %v0 = load volatile i64, ptr %p0, align 8
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
define i64 @atomic()  {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %acc = phi i64 [ 0, %entry ], [ %sum4, %loop ]
  %ix0 = add i64 %i, 0
  %p0 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix0
  %v0 = load atomic i64, ptr %p0 monotonic, align 8
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
declare void @side_effect_fn()
define i64 @extra_volatile()  {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %acc = phi i64 [ 0, %entry ], [ %sum4, %loop ]
  %ix0 = add i64 %i, 0
  %p0 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix0
  %v0 = load i64, ptr %p0, align 8
  %volatile_extra = load volatile i64, ptr %p0, align 8
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
define i64 @extra_atomic()  {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %acc = phi i64 [ 0, %entry ], [ %sum4, %loop ]
  %ix0 = add i64 %i, 0
  %p0 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix0
  %v0 = load i64, ptr %p0, align 8
  %atomic_extra = load atomic i64, ptr %p0 monotonic, align 8
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
define i64 @side_effect()  {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %acc = phi i64 [ 0, %entry ], [ %sum4, %loop ]
  %ix0 = add i64 %i, 0
  %p0 = getelementptr [48 x i64], ptr @table, i64 0, i64 %ix0
  %v0 = load i64, ptr %p0, align 8
  call void @side_effect_fn()
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
define i64 @poison_bound()  {
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
  %done = icmp eq i64 %next, poison
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
define i64 @optnone() noinline optnone  {
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
define i64 @multiple_loops()  {
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
  br label %loop2
loop2:
  %i2 = phi i64 [ 0, %exit ], [ %next2, %loop2 ]
  %acc2 = phi i64 [ %sum4, %exit ], [ %sum8, %loop2 ]
  %j0 = getelementptr [48 x i64], ptr @table, i64 0, i64 %i2
  %w0 = load i64, ptr %j0, align 8
  %j1 = getelementptr [48 x i64], ptr @table, i64 0, i64 %i2
  %w1 = load i64, ptr %j1, align 8
  %j2 = getelementptr [48 x i64], ptr @table, i64 0, i64 %i2
  %w2 = load i64, ptr %j2, align 8
  %j3 = getelementptr [48 x i64], ptr @table, i64 0, i64 %i2
  %w3 = load i64, ptr %j3, align 8
  %sum5 = add i64 %acc2, %w0
  %sum6 = add i64 %sum5, %w1
  %sum7 = add i64 %sum6, %w2
  %sum8 = add i64 %sum7, %w3
  %next2 = add nuw nsw i64 %i2, 1
  %done2 = icmp eq i64 %next2, 17
  br i1 %done2, label %exit2, label %loop2
exit2:
  ret i64 %sum8
}
define i64 @trip17()  {
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
  %done = icmp eq i64 %next, 17
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
define i64 @small_trip()  {
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
  %done = icmp eq i64 %next, 1
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
define i64 @mutable()  {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %next, %loop ]
  %acc = phi i64 [ 0, %entry ], [ %sum4, %loop ]
  %ix0 = add i64 %i, 0
  %p0 = getelementptr [48 x i64], ptr @mutable_table, i64 0, i64 %ix0
  %v0 = load i64, ptr %p0, align 8
  %sum1 = add i64 %acc, %v0
  %ix1 = add i64 %i, 12
  %p1 = getelementptr [48 x i64], ptr @mutable_table, i64 0, i64 %ix1
  %v1 = load i64, ptr %p1, align 8
  %sum2 = add i64 %sum1, %v1
  %ix2 = add i64 %i, 24
  %p2 = getelementptr [48 x i64], ptr @mutable_table, i64 0, i64 %ix2
  %v2 = load i64, ptr %p2, align 8
  %sum3 = add i64 %sum2, %v2
  %ix3 = add i64 %i, 36
  %p3 = getelementptr [48 x i64], ptr @mutable_table, i64 0, i64 %ix3
  %v3 = load i64, ptr %p3, align 8
  %sum4 = add i64 %sum3, %v3
  %next = add nuw nsw i64 %i, 1
  %done = icmp eq i64 %next, 12
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
define i64 @loop_metadata()  {
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
  br i1 %done, label %exit, label %loop, !llvm.loop !0
exit:
  ret i64 %sum4
}
define i64 @large()  {
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
  %dummy0 = add i64 %i, 0
  %dummy1 = add i64 %i, 1
  %dummy2 = add i64 %i, 2
  %dummy3 = add i64 %i, 3
  %dummy4 = add i64 %i, 4
  %dummy5 = add i64 %i, 5
  %dummy6 = add i64 %i, 6
  %dummy7 = add i64 %i, 7
  %dummy8 = add i64 %i, 8
  %dummy9 = add i64 %i, 9
  %dummy10 = add i64 %i, 10
  %dummy11 = add i64 %i, 11
  %dummy12 = add i64 %i, 12
  %dummy13 = add i64 %i, 13
  %dummy14 = add i64 %i, 14
  %dummy15 = add i64 %i, 15
  %dummy16 = add i64 %i, 16
  %dummy17 = add i64 %i, 17
  %dummy18 = add i64 %i, 18
  %dummy19 = add i64 %i, 19
  %dummy20 = add i64 %i, 20
  %dummy21 = add i64 %i, 21
  %dummy22 = add i64 %i, 22
  %dummy23 = add i64 %i, 23
  %dummy24 = add i64 %i, 24
  %dummy25 = add i64 %i, 25
  %dummy26 = add i64 %i, 26
  %dummy27 = add i64 %i, 27
  %dummy28 = add i64 %i, 28
  %dummy29 = add i64 %i, 29
  %dummy30 = add i64 %i, 30
  %dummy31 = add i64 %i, 31
  %dummy32 = add i64 %i, 32
  %dummy33 = add i64 %i, 33
  %dummy34 = add i64 %i, 34
  %dummy35 = add i64 %i, 35
  %dummy36 = add i64 %i, 36
  %dummy37 = add i64 %i, 37
  %dummy38 = add i64 %i, 38
  %dummy39 = add i64 %i, 39
  %dummy40 = add i64 %i, 40
  %dummy41 = add i64 %i, 41
  %dummy42 = add i64 %i, 42
  %dummy43 = add i64 %i, 43
  %dummy44 = add i64 %i, 44
  %dummy45 = add i64 %i, 45
  %dummy46 = add i64 %i, 46
  %dummy47 = add i64 %i, 47
  %dummy48 = add i64 %i, 48
  %dummy49 = add i64 %i, 49
  %dummy50 = add i64 %i, 50
  %dummy51 = add i64 %i, 51
  %dummy52 = add i64 %i, 52
  %dummy53 = add i64 %i, 53
  %dummy54 = add i64 %i, 54
  %dummy55 = add i64 %i, 55
  %dummy56 = add i64 %i, 56
  %dummy57 = add i64 %i, 57
  %dummy58 = add i64 %i, 58
  %dummy59 = add i64 %i, 59
  %dummy60 = add i64 %i, 60
  %dummy61 = add i64 %i, 61
  %dummy62 = add i64 %i, 62
  %dummy63 = add i64 %i, 63
  %dummy64 = add i64 %i, 64
  %dummy65 = add i64 %i, 65
  %dummy66 = add i64 %i, 66
  %dummy67 = add i64 %i, 67
  %dummy68 = add i64 %i, 68
  %dummy69 = add i64 %i, 69
  %dummy70 = add i64 %i, 70
  %dummy71 = add i64 %i, 71
  %dummy72 = add i64 %i, 72
  %dummy73 = add i64 %i, 73
  %dummy74 = add i64 %i, 74
  %dummy75 = add i64 %i, 75
  %dummy76 = add i64 %i, 76
  %dummy77 = add i64 %i, 77
  %dummy78 = add i64 %i, 78
  %dummy79 = add i64 %i, 79
  %dummy80 = add i64 %i, 80
  %dummy81 = add i64 %i, 81
  %dummy82 = add i64 %i, 82
  %dummy83 = add i64 %i, 83
  %dummy84 = add i64 %i, 84
  %dummy85 = add i64 %i, 85
  %dummy86 = add i64 %i, 86
  %dummy87 = add i64 %i, 87
  %dummy88 = add i64 %i, 88
  %dummy89 = add i64 %i, 89
  %dummy90 = add i64 %i, 90
  %dummy91 = add i64 %i, 91
  %dummy92 = add i64 %i, 92
  %dummy93 = add i64 %i, 93
  %dummy94 = add i64 %i, 94
  %dummy95 = add i64 %i, 95
  %dummy96 = add i64 %i, 96
  %dummy97 = add i64 %i, 97
  %dummy98 = add i64 %i, 98
  %dummy99 = add i64 %i, 99
  %dummy100 = add i64 %i, 100
  %dummy101 = add i64 %i, 101
  %dummy102 = add i64 %i, 102
  %dummy103 = add i64 %i, 103
  %dummy104 = add i64 %i, 104
  %dummy105 = add i64 %i, 105
  %dummy106 = add i64 %i, 106
  %dummy107 = add i64 %i, 107
  %dummy108 = add i64 %i, 108
  %dummy109 = add i64 %i, 109
  %dummy110 = add i64 %i, 110
  %dummy111 = add i64 %i, 111
  %dummy112 = add i64 %i, 112
  %dummy113 = add i64 %i, 113
  %dummy114 = add i64 %i, 114
  %dummy115 = add i64 %i, 115
  %dummy116 = add i64 %i, 116
  %dummy117 = add i64 %i, 117
  %dummy118 = add i64 %i, 118
  %dummy119 = add i64 %i, 119
  %dummy120 = add i64 %i, 120
  %dummy121 = add i64 %i, 121
  %dummy122 = add i64 %i, 122
  %dummy123 = add i64 %i, 123
  %dummy124 = add i64 %i, 124
  %dummy125 = add i64 %i, 125
  %dummy126 = add i64 %i, 126
  %dummy127 = add i64 %i, 127
  %dummy128 = add i64 %i, 128
  %dummy129 = add i64 %i, 129
  %dummy130 = add i64 %i, 130
  %dummy131 = add i64 %i, 131
  %dummy132 = add i64 %i, 132
  %dummy133 = add i64 %i, 133
  %dummy134 = add i64 %i, 134
  %dummy135 = add i64 %i, 135
  %dummy136 = add i64 %i, 136
  %dummy137 = add i64 %i, 137
  %dummy138 = add i64 %i, 138
  %dummy139 = add i64 %i, 139
  %dummy140 = add i64 %i, 140
  %dummy141 = add i64 %i, 141
  %dummy142 = add i64 %i, 142
  %dummy143 = add i64 %i, 143
  %dummy144 = add i64 %i, 144
  %dummy145 = add i64 %i, 145
  %dummy146 = add i64 %i, 146
  %dummy147 = add i64 %i, 147
  %dummy148 = add i64 %i, 148
  %dummy149 = add i64 %i, 149
  %dummy150 = add i64 %i, 150
  %dummy151 = add i64 %i, 151
  %dummy152 = add i64 %i, 152
  %dummy153 = add i64 %i, 153
  %dummy154 = add i64 %i, 154
  %dummy155 = add i64 %i, 155
  %dummy156 = add i64 %i, 156
  %dummy157 = add i64 %i, 157
  %dummy158 = add i64 %i, 158
  %dummy159 = add i64 %i, 159
  %dummy160 = add i64 %i, 160
  %dummy161 = add i64 %i, 161
  %dummy162 = add i64 %i, 162
  %dummy163 = add i64 %i, 163
  %dummy164 = add i64 %i, 164
  %dummy165 = add i64 %i, 165
  %dummy166 = add i64 %i, 166
  %dummy167 = add i64 %i, 167
  %dummy168 = add i64 %i, 168
  %dummy169 = add i64 %i, 169
  %dummy170 = add i64 %i, 170
  %dummy171 = add i64 %i, 171
  %dummy172 = add i64 %i, 172
  %dummy173 = add i64 %i, 173
  %dummy174 = add i64 %i, 174
  %dummy175 = add i64 %i, 175
  %dummy176 = add i64 %i, 176
  %dummy177 = add i64 %i, 177
  %dummy178 = add i64 %i, 178
  %dummy179 = add i64 %i, 179
  %dummy180 = add i64 %i, 180
  %dummy181 = add i64 %i, 181
  %dummy182 = add i64 %i, 182
  %dummy183 = add i64 %i, 183
  %dummy184 = add i64 %i, 184
  %dummy185 = add i64 %i, 185
  %dummy186 = add i64 %i, 186
  %dummy187 = add i64 %i, 187
  %dummy188 = add i64 %i, 188
  %dummy189 = add i64 %i, 189
  %dummy190 = add i64 %i, 190
  %dummy191 = add i64 %i, 191
  %dummy192 = add i64 %i, 192
  %dummy193 = add i64 %i, 193
  %dummy194 = add i64 %i, 194
  %dummy195 = add i64 %i, 195
  %dummy196 = add i64 %i, 196
  %dummy197 = add i64 %i, 197
  %dummy198 = add i64 %i, 198
  %dummy199 = add i64 %i, 199
  %dummy200 = add i64 %i, 200
  %dummy201 = add i64 %i, 201
  %dummy202 = add i64 %i, 202
  %dummy203 = add i64 %i, 203
  %dummy204 = add i64 %i, 204
  %dummy205 = add i64 %i, 205
  %dummy206 = add i64 %i, 206
  %dummy207 = add i64 %i, 207
  %dummy208 = add i64 %i, 208
  %dummy209 = add i64 %i, 209
  %dummy210 = add i64 %i, 210
  %dummy211 = add i64 %i, 211
  %dummy212 = add i64 %i, 212
  %dummy213 = add i64 %i, 213
  %dummy214 = add i64 %i, 214
  %dummy215 = add i64 %i, 215
  %dummy216 = add i64 %i, 216
  %dummy217 = add i64 %i, 217
  %dummy218 = add i64 %i, 218
  %dummy219 = add i64 %i, 219
  %dummy220 = add i64 %i, 220
  %dummy221 = add i64 %i, 221
  %dummy222 = add i64 %i, 222
  %dummy223 = add i64 %i, 223
  %dummy224 = add i64 %i, 224
  %dummy225 = add i64 %i, 225
  %dummy226 = add i64 %i, 226
  %dummy227 = add i64 %i, 227
  %dummy228 = add i64 %i, 228
  %dummy229 = add i64 %i, 229
  %dummy230 = add i64 %i, 230
  %dummy231 = add i64 %i, 231
  %dummy232 = add i64 %i, 232
  %dummy233 = add i64 %i, 233
  %dummy234 = add i64 %i, 234
  %dummy235 = add i64 %i, 235
  %dummy236 = add i64 %i, 236
  %dummy237 = add i64 %i, 237
  %dummy238 = add i64 %i, 238
  %dummy239 = add i64 %i, 239
  %dummy240 = add i64 %i, 240
  %dummy241 = add i64 %i, 241
  %dummy242 = add i64 %i, 242
  %dummy243 = add i64 %i, 243
  %dummy244 = add i64 %i, 244
  %dummy245 = add i64 %i, 245
  %dummy246 = add i64 %i, 246
  %dummy247 = add i64 %i, 247
  %dummy248 = add i64 %i, 248
  %dummy249 = add i64 %i, 249
  %dummy250 = add i64 %i, 250
  %dummy251 = add i64 %i, 251
  %dummy252 = add i64 %i, 252
  %dummy253 = add i64 %i, 253
  %dummy254 = add i64 %i, 254
  %dummy255 = add i64 %i, 255
  %dummy256 = add i64 %i, 256
  %dummy257 = add i64 %i, 257
  %dummy258 = add i64 %i, 258
  %dummy259 = add i64 %i, 259
  %dummy260 = add i64 %i, 260
  %dummy261 = add i64 %i, 261
  %dummy262 = add i64 %i, 262
  %dummy263 = add i64 %i, 263
  %dummy264 = add i64 %i, 264
  %dummy265 = add i64 %i, 265
  %dummy266 = add i64 %i, 266
  %dummy267 = add i64 %i, 267
  %dummy268 = add i64 %i, 268
  %dummy269 = add i64 %i, 269
  %dummy270 = add i64 %i, 270
  %dummy271 = add i64 %i, 271
  %dummy272 = add i64 %i, 272
  %dummy273 = add i64 %i, 273
  %dummy274 = add i64 %i, 274
  %dummy275 = add i64 %i, 275
  %dummy276 = add i64 %i, 276
  %dummy277 = add i64 %i, 277
  %dummy278 = add i64 %i, 278
  %dummy279 = add i64 %i, 279
  %dummy280 = add i64 %i, 280
  %dummy281 = add i64 %i, 281
  %dummy282 = add i64 %i, 282
  %dummy283 = add i64 %i, 283
  %dummy284 = add i64 %i, 284
  %dummy285 = add i64 %i, 285
  %dummy286 = add i64 %i, 286
  %dummy287 = add i64 %i, 287
  %dummy288 = add i64 %i, 288
  %dummy289 = add i64 %i, 289
  %dummy290 = add i64 %i, 290
  %dummy291 = add i64 %i, 291
  %dummy292 = add i64 %i, 292
  %dummy293 = add i64 %i, 293
  %dummy294 = add i64 %i, 294
  %dummy295 = add i64 %i, 295
  %dummy296 = add i64 %i, 296
  %dummy297 = add i64 %i, 297
  %dummy298 = add i64 %i, 298
  %dummy299 = add i64 %i, 299
  %dummy300 = add i64 %i, 300
  %dummy301 = add i64 %i, 301
  %dummy302 = add i64 %i, 302
  %dummy303 = add i64 %i, 303
  %dummy304 = add i64 %i, 304
  %dummy305 = add i64 %i, 305
  %dummy306 = add i64 %i, 306
  %dummy307 = add i64 %i, 307
  %dummy308 = add i64 %i, 308
  %dummy309 = add i64 %i, 309
  %dummy310 = add i64 %i, 310
  %dummy311 = add i64 %i, 311
  %dummy312 = add i64 %i, 312
  %dummy313 = add i64 %i, 313
  %dummy314 = add i64 %i, 314
  %dummy315 = add i64 %i, 315
  %dummy316 = add i64 %i, 316
  %dummy317 = add i64 %i, 317
  %dummy318 = add i64 %i, 318
  %dummy319 = add i64 %i, 319
  %dummy320 = add i64 %i, 320
  %dummy321 = add i64 %i, 321
  %dummy322 = add i64 %i, 322
  %dummy323 = add i64 %i, 323
  %dummy324 = add i64 %i, 324
  %dummy325 = add i64 %i, 325
  %dummy326 = add i64 %i, 326
  %dummy327 = add i64 %i, 327
  %dummy328 = add i64 %i, 328
  %dummy329 = add i64 %i, 329
  %dummy330 = add i64 %i, 330
  %dummy331 = add i64 %i, 331
  %dummy332 = add i64 %i, 332
  %dummy333 = add i64 %i, 333
  %dummy334 = add i64 %i, 334
  %dummy335 = add i64 %i, 335
  %dummy336 = add i64 %i, 336
  %dummy337 = add i64 %i, 337
  %dummy338 = add i64 %i, 338
  %dummy339 = add i64 %i, 339
  %dummy340 = add i64 %i, 340
  %dummy341 = add i64 %i, 341
  %dummy342 = add i64 %i, 342
  %dummy343 = add i64 %i, 343
  %dummy344 = add i64 %i, 344
  %dummy345 = add i64 %i, 345
  %dummy346 = add i64 %i, 346
  %dummy347 = add i64 %i, 347
  %dummy348 = add i64 %i, 348
  %dummy349 = add i64 %i, 349
  %dummy350 = add i64 %i, 350
  %dummy351 = add i64 %i, 351
  %dummy352 = add i64 %i, 352
  %dummy353 = add i64 %i, 353
  %dummy354 = add i64 %i, 354
  %dummy355 = add i64 %i, 355
  %dummy356 = add i64 %i, 356
  %dummy357 = add i64 %i, 357
  %dummy358 = add i64 %i, 358
  %dummy359 = add i64 %i, 359
  %dummy360 = add i64 %i, 360
  %dummy361 = add i64 %i, 361
  %dummy362 = add i64 %i, 362
  %dummy363 = add i64 %i, 363
  %dummy364 = add i64 %i, 364
  %dummy365 = add i64 %i, 365
  %dummy366 = add i64 %i, 366
  %dummy367 = add i64 %i, 367
  %dummy368 = add i64 %i, 368
  %dummy369 = add i64 %i, 369
  %dummy370 = add i64 %i, 370
  %dummy371 = add i64 %i, 371
  %dummy372 = add i64 %i, 372
  %dummy373 = add i64 %i, 373
  %dummy374 = add i64 %i, 374
  %dummy375 = add i64 %i, 375
  %dummy376 = add i64 %i, 376
  %dummy377 = add i64 %i, 377
  %dummy378 = add i64 %i, 378
  %dummy379 = add i64 %i, 379
  %dummy380 = add i64 %i, 380
  %dummy381 = add i64 %i, 381
  %dummy382 = add i64 %i, 382
  %dummy383 = add i64 %i, 383
  %dummy384 = add i64 %i, 384
  %dummy385 = add i64 %i, 385
  %dummy386 = add i64 %i, 386
  %dummy387 = add i64 %i, 387
  %dummy388 = add i64 %i, 388
  %dummy389 = add i64 %i, 389
  %dummy390 = add i64 %i, 390
  %dummy391 = add i64 %i, 391
  %dummy392 = add i64 %i, 392
  %dummy393 = add i64 %i, 393
  %dummy394 = add i64 %i, 394
  %dummy395 = add i64 %i, 395
  %dummy396 = add i64 %i, 396
  %dummy397 = add i64 %i, 397
  %dummy398 = add i64 %i, 398
  %dummy399 = add i64 %i, 399
  %next = add nuw nsw i64 %i, 1
  %done = icmp eq i64 %next, 12
  br i1 %done, label %exit, label %loop
exit:
  ret i64 %sum4
}
define i64 @debug() !dbg !11 {
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

!0 = distinct !{!0, !1}
!1 = !{!"llvm.loop.unroll.disable"}
!llvm.dbg.cu = !{!10}
!llvm.module.flags = !{!16}
!10 = distinct !DICompileUnit(language: DW_LANG_C, file: !12, producer: "test", isOptimized: true, runtimeVersion: 0, emissionKind: FullDebug)
!11 = distinct !DISubprogram(name: "debug", scope: !12, file: !12, line: 1, type: !13, scopeLine: 1, spFlags: DISPFlagDefinition, unit: !10)
!12 = !DIFile(filename: "test.c", directory: "/")
!13 = !DISubroutineType(types: !14)
!14 = !{null}
!16 = !{i32 2, !"Debug Info Version", i32 3}
