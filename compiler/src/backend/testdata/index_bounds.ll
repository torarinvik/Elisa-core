target triple = "aarch64-unknown-linux-gnu"
declare void @observable()
define i64 @validate(i64 %a, i64 %b, i64 noundef %raw) {
entry:
 %n = and i64 %raw, 63
 %an = icmp slt i64 %a, 0
 %bn = icmp slt i64 %b, 0
 %negative = select i1 %an, i1 true, i1 %bn
 br i1 %negative, label %invalid, label %upper
upper:
 %au = icmp sge i64 %a, %n
 %bu = icmp sge i64 %b, %n
 %outside = select i1 %au, i1 true, i1 %bu
 br i1 %outside, label %invalid, label %valid
valid:
 ret i64 7
invalid:
 %result = phi i64 [ -1, %entry ], [ -1, %upper ]
 ret i64 %result
}
