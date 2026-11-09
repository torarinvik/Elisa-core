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
