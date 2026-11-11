; A guarded power search leaves an unnecessary zero-exponent select unless
; late loop cleanup propagates the facts established by its entry branch.

; Function Attrs: nofree norecurse nosync nounwind memory(none)
define i64 @count_set_bits(i64 %0) local_unnamed_addr {
entry:
  %spec.select.i = icmp ugt i64 %0, 67108863
  br i1 %spec.select.i, label %count_set_bits_impl.exit, label %if.end.i

if.end.i:                                         ; preds = %entry
  %cmptmp30.i.i = icmp eq i64 %0, 0
  br i1 %cmptmp30.i.i, label %count_set_bits_impl.exit, label %if.end.i.i

if.end.i.i:                                       ; preds = %if.end.i, %highest_power_position.exit.thread.i.i
  %.tr32.i.i = phi i64 [ %subtmp.i.i, %highest_power_position.exit.thread.i.i ], [ %0, %if.end.i ]
  %accumulator.tr31.i.i = phi i64 [ %addtmp20.i.i, %highest_power_position.exit.thread.i.i ], [ 0, %if.end.i ]
  %divtmp.i.i.i = sdiv i64 %.tr32.i.i, 2
  %cmptmp.not6.i.i.i = icmp slt i64 %.tr32.i.i, 2
  br i1 %cmptmp.not6.i.i.i, label %highest_power_position.exit.thread.i.i, label %while.body.i.i.i

while.body.i.i.i:                                 ; preds = %if.end.i.i, %while.body.i.i.i
  %power.08.i.i.i = phi i64 [ %multmp.i.i.i, %while.body.i.i.i ], [ 1, %if.end.i.i ]
  %position.07.i.i.i = phi i32 [ %addtmp.i.i.i, %while.body.i.i.i ], [ 0, %if.end.i.i ]
  %multmp.i.i.i = shl i64 %power.08.i.i.i, 1
  %addtmp.i.i.i = add i32 %position.07.i.i.i, 1
  %cmptmp.not.i.i.i = icmp sgt i64 %multmp.i.i.i, %divtmp.i.i.i
  br i1 %cmptmp.not.i.i.i, label %highest_power_position.exit.i.i, label %while.body.i.i.i

highest_power_position.exit.i.i:                  ; preds = %while.body.i.i.i
  %shltmp.i.i = shl nuw i32 1, %addtmp.i.i.i
  %sext.i.i = sext i32 %shltmp.i.i to i64
  %cmptmp7.i.i = icmp ult i32 %position.07.i.i.i, 2147483647
  %sext9.i.i = sext i32 %addtmp.i.i.i to i64
  %shrtmp.i.i = ashr i64 %sext.i.i, 1
  %multmp.i.i = mul nsw i64 %shrtmp.i.i, %sext9.i.i
  %spec.select.i.i = select i1 %cmptmp7.i.i, i64 %multmp.i.i, i64 0
  br label %highest_power_position.exit.thread.i.i

highest_power_position.exit.thread.i.i:           ; preds = %highest_power_position.exit.i.i, %if.end.i.i
  %sext29.i.i = phi i64 [ 1, %if.end.i.i ], [ %sext.i.i, %highest_power_position.exit.i.i ]
  %1 = phi i64 [ 0, %if.end.i.i ], [ %spec.select.i.i, %highest_power_position.exit.i.i ]
  %subtmp.i.i = sub i64 %.tr32.i.i, %sext29.i.i
  %addtmp.i.i = add i64 %accumulator.tr31.i.i, 1
  %addtmp15.i.i = add i64 %addtmp.i.i, %subtmp.i.i
  %addtmp20.i.i = add i64 %addtmp15.i.i, %1
  %cmptmp.i.i = icmp eq i64 %subtmp.i.i, 0
  br i1 %cmptmp.i.i, label %count_set_bits_impl.exit, label %if.end.i.i

count_set_bits_impl.exit:                         ; preds = %highest_power_position.exit.thread.i.i, %entry, %if.end.i
  %common.ret.op.i = phi i64 [ -1, %entry ], [ 0, %if.end.i ], [ %addtmp20.i.i, %highest_power_position.exit.thread.i.i ]
  ret i64 %common.ret.op.i
}

