target triple = "aarch64-unknown-linux-gnu"
define i64 @validated_sum(ptr %p, i64 %length) {
entry:
 %shape = icmp eq i64 %length, 4
 br i1 %shape, label %load0, label %invalid
load0:
 %p0 = getelementptr i64, ptr %p, i64 0
 %x0 = load i64, ptr %p0, align 8
 %bad0 = icmp ugt i64 %x0, 10
 br i1 %bad0, label %invalid, label %load1
load1:
 %p1 = getelementptr i64, ptr %p, i64 1
 %x1 = load i64, ptr %p1, align 8
 %bad1 = icmp ugt i64 %x1, 10
 br i1 %bad1, label %invalid, label %load2
load2:
 %p2 = getelementptr i64, ptr %p, i64 2
 %x2 = load i64, ptr %p2, align 8
 %bad2 = icmp ugt i64 %x2, 10
 br i1 %bad2, label %invalid, label %load3
load3:
 %p3 = getelementptr i64, ptr %p, i64 3
 %x3 = load i64, ptr %p3, align 8
 %bad3 = icmp ugt i64 %x3, 10
 br i1 %bad3, label %invalid, label %success
success:
 %s0 = add i64 %x0, %x1
 %s1 = add i64 %x2, %x3
 %sum = add i64 %s0, %s1
 br label %invalid
invalid:
 %result = phi i64 [ -1, %entry ], [ -1, %load0 ], [ -1, %load1 ], [ -1, %load2 ], [ -1, %load3 ], [ %sum, %success ]
 ret i64 %result
}
