; ModuleID = '/tmp/doitgen-initial-mac/raw-O0.ll'
source_filename = "doitgen.elisa"
target datalayout = "e-m:o-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-n32:64-S128-Fn32"
target triple = "arm64-apple-darwin27.2.0"

%PackedStoreIndexAllocResult = type { ptr, i32 }

declare ptr @ctx_packed_store_state_new_variant_sparse_with_side_words(ptr, i64, i64)

declare void @ctx_packed_store_record_side_words_at(ptr, ptr, ptr, i32)

declare i64 @ctx_packed_store_read_side_word(ptr, i32, i64)

declare ptr @arena_alloc(ptr, i64)

declare i64 @ctx_streq(ptr, ptr)

declare i64 @ctx_string_views_eq({ ptr, i64 }, { ptr, i64 })

declare i64 @ctx_string_view_index({ ptr, i64 }, i64)

declare ptr @ctx_packed_store_state_new_variant_sparse(ptr, i64)

declare %PackedStoreIndexAllocResult @ctx_packed_store_alloc_fixed_tagged_variant_sparse_result(ptr, ptr, i32)

declare i32 @ctx_packed_store_read_variant_sparse_tag(ptr, i32)

declare i64 @ctx_packed_store_read_variant_sparse_word(i32, ptr, i64)

declare ptr @new_region_backend(i64, i64)

declare ptr @arena_realloc(ptr, ptr, i64, i64)

declare void @arena_free(ptr)

; Function Attrs: nounwind
define double @doitgen_impl(ptr %0, i64 %1) #0 {
entry:
  %sum = alloca [16 x double], align 8
  %c4 = alloca [256 x double], align 8
  %a = alloca [4096 x double], align 8
  %nullcmp.ptr = icmp eq ptr %0, null
  %cmp = icmp ne i64 %1, 3
  %spec.select = select i1 %nullcmp.ptr, i1 true, i1 %cmp
  br i1 %spec.select, label %common.ret, label %if.else

common.ret.loopexit:                              ; preds = %while.head225
  %checksum.0.lcssa = phi double [ %checksum.0, %while.head225 ]
  br label %common.ret

common.ret:                                       ; preds = %common.ret.loopexit, %sc.rhs29, %sc.rhs15, %if.else, %sc.rhs22, %sc.rhs36, %entry
  %common.ret.op = phi double [ -1.000000e+300, %sc.rhs36 ], [ -1.000000e+300, %entry ], [ -1.000000e+300, %sc.rhs29 ], [ -1.000000e+300, %sc.rhs22 ], [ -1.000000e+300, %if.else ], [ -1.000000e+300, %sc.rhs15 ], [ %checksum.0.lcssa, %common.ret.loopexit ]
  ret double %common.ret.op

if.else:                                          ; preds = %entry
  %scalar.ref.elem = load i64, ptr %0, align 8
  %cmp7 = icmp slt i64 %scalar.ref.elem, 1
  %cmp13 = icmp sgt i64 %scalar.ref.elem, 16
  %spec.select2 = select i1 %cmp7, i1 true, i1 %cmp13
  br i1 %spec.select2, label %common.ret, label %sc.rhs15

sc.rhs15:                                         ; preds = %if.else
  %scalar.ref.elem.ptr18 = getelementptr i8, ptr %0, i64 8
  %scalar.ref.elem19 = load i64, ptr %scalar.ref.elem.ptr18, align 8
  %cmp20 = icmp slt i64 %scalar.ref.elem19, 1
  br i1 %cmp20, label %common.ret, label %sc.rhs22

sc.rhs22:                                         ; preds = %sc.rhs15
  %scalar.ref.elem.ptr25 = getelementptr i8, ptr %0, i64 8
  %scalar.ref.elem26 = load i64, ptr %scalar.ref.elem.ptr25, align 8
  %cmp27 = icmp sgt i64 %scalar.ref.elem26, 16
  br i1 %cmp27, label %common.ret, label %sc.rhs29

sc.rhs29:                                         ; preds = %sc.rhs22
  %scalar.ref.elem.ptr32 = getelementptr i8, ptr %0, i64 16
  %scalar.ref.elem33 = load i64, ptr %scalar.ref.elem.ptr32, align 8
  %cmp34 = icmp slt i64 %scalar.ref.elem33, 1
  br i1 %cmp34, label %common.ret, label %sc.rhs36

sc.rhs36:                                         ; preds = %sc.rhs29
  %scalar.ref.elem.ptr39 = getelementptr i8, ptr %0, i64 16
  %scalar.ref.elem40 = load i64, ptr %scalar.ref.elem.ptr39, align 8
  %cmp41 = icmp sgt i64 %scalar.ref.elem40, 16
  br i1 %cmp41, label %common.ret, label %if.else44

if.else44:                                        ; preds = %sc.rhs36
  %scalar.ref.elem.ptr50 = getelementptr i8, ptr %0, i64 8
  %scalar.ref.elem51 = load i64, ptr %scalar.ref.elem.ptr50, align 8
  %scalar.ref.elem.ptr53 = getelementptr i8, ptr %0, i64 16
  %scalar.ref.elem54 = load i64, ptr %scalar.ref.elem.ptr53, align 8
  call void @llvm.memset.p0.i64(ptr noundef nonnull align 8 dereferenceable(32768) %a, i8 0, i64 32768, i1 false)
  call void @llvm.memset.p0.i64(ptr noundef nonnull align 8 dereferenceable(2048) %c4, i8 0, i64 2048, i1 false)
  call void @llvm.memset.p0.i64(ptr noundef nonnull align 8 dereferenceable(128) %sum, i8 0, i64 128, i1 false)
  br label %while.head

while.head:                                       ; preds = %while.exit60, %if.else44
  %r.0 = phi i64 [ 0, %if.else44 ], [ %add90, %while.exit60 ]
  %cmp57 = icmp ult i64 %r.0, %scalar.ref.elem
  br i1 %cmp57, label %while.head58.preheader, label %while.head91.preheader

while.head58.preheader:                           ; preds = %while.head
  br label %while.head58

while.head91.preheader:                           ; preds = %while.head
  br label %while.head91

while.head58:                                     ; preds = %while.head58.preheader, %while.exit66
  %q.0 = phi i64 [ %add88, %while.exit66 ], [ 0, %while.head58.preheader ]
  %cmp63 = icmp ult i64 %q.0, %scalar.ref.elem51
  br i1 %cmp63, label %while.head64.preheader, label %while.exit60

while.head64.preheader:                           ; preds = %while.head58
  br label %while.head64

while.exit60:                                     ; preds = %while.head58
  %add90 = add i64 %r.0, 1
  br label %while.head

while.head64:                                     ; preds = %while.head64.preheader, %wd.ok
  %p.0 = phi i64 [ %add86, %wd.ok ], [ 0, %while.head64.preheader ]
  %cmp69 = icmp ult i64 %p.0, %scalar.ref.elem54
  br i1 %cmp69, label %while.body65, label %while.exit66

while.body65:                                     ; preds = %while.head64
  %mul = mul i64 %r.0, %scalar.ref.elem51
  %add = add i64 %mul, %q.0
  %mul74 = mul i64 %add, %scalar.ref.elem54
  %add76 = add i64 %mul74, %p.0
  %wd.in_bounds.unsigned = icmp ult i64 %add76, 4096
  br i1 %wd.in_bounds.unsigned, label %wd.ok, label %wd.fail

while.exit66:                                     ; preds = %while.head64
  %add88 = add i64 %q.0, 1
  br label %while.head58

wd.ok:                                            ; preds = %while.body65
  %elem.ptr = getelementptr [8 x i8], ptr %a, i64 %add76
  %mul79 = mul i64 %r.0, %q.0
  %add81 = add i64 %mul79, %p.0
  %rem = urem i64 %add81, %scalar.ref.elem54
  %uitofp = uitofp i64 %rem to double
  %uitofp84 = uitofp i64 %scalar.ref.elem54 to double
  %fdiv = fdiv arcp contract double %uitofp, %uitofp84
  store double %fdiv, ptr %elem.ptr, align 8
  %add86 = add i64 %p.0, 1
  br label %while.head64

wd.fail:                                          ; preds = %while.body65
  call void @llvm.trap()
  unreachable

while.head91:                                     ; preds = %while.head91.preheader, %while.exit99
  %i.0 = phi i64 [ %add124, %while.exit99 ], [ 0, %while.head91.preheader ]
  %cmp96 = icmp ult i64 %i.0, %scalar.ref.elem54
  br i1 %cmp96, label %while.head97.preheader, label %while.head125.preheader

while.head97.preheader:                           ; preds = %while.head91
  br label %while.head97

while.head125.preheader:                          ; preds = %while.head91
  br label %while.head125

while.head97:                                     ; preds = %while.head97.preheader, %wd.ok109
  %j.0 = phi i64 [ %add122, %wd.ok109 ], [ 0, %while.head97.preheader ]
  %cmp102 = icmp ult i64 %j.0, %scalar.ref.elem54
  br i1 %cmp102, label %while.body98, label %while.exit99

while.body98:                                     ; preds = %while.head97
  %mul105 = mul i64 %i.0, %scalar.ref.elem54
  %add107 = add i64 %mul105, %j.0
  %wd.in_bounds.unsigned108 = icmp ult i64 %add107, 256
  br i1 %wd.in_bounds.unsigned108, label %wd.ok109, label %wd.fail110

while.exit99:                                     ; preds = %while.head97
  %add124 = add i64 %i.0, 1
  br label %while.head91

wd.ok109:                                         ; preds = %while.body98
  %elem.ptr111 = getelementptr [8 x i8], ptr %c4, i64 %add107
  %mul114 = mul i64 %i.0, %j.0
  %rem116 = urem i64 %mul114, %scalar.ref.elem54
  %uitofp117 = uitofp i64 %rem116 to double
  %uitofp119 = uitofp i64 %scalar.ref.elem54 to double
  %fdiv120 = fdiv arcp contract double %uitofp117, %uitofp119
  store double %fdiv120, ptr %elem.ptr111, align 8
  %add122 = add i64 %j.0, 1
  br label %while.head97

wd.fail110:                                       ; preds = %while.body98
  call void @llvm.trap()
  unreachable

while.head125:                                    ; preds = %while.head125.preheader, %while.exit134
  %r.1 = phi i64 [ %add224, %while.exit134 ], [ 0, %while.head125.preheader ]
  %cmp130 = icmp ult i64 %r.1, %scalar.ref.elem
  br i1 %cmp130, label %while.head132.preheader, label %while.head225.preheader

while.head132.preheader:                          ; preds = %while.head125
  br label %while.head132

while.head225.preheader:                          ; preds = %while.head125
  br label %while.head225

while.head132:                                    ; preds = %while.head132.preheader, %while.exit196
  %q131.0 = phi i64 [ %add222, %while.exit196 ], [ 0, %while.head132.preheader ]
  %cmp137 = icmp ult i64 %q131.0, %scalar.ref.elem51
  br i1 %cmp137, label %while.head139.preheader, label %while.exit134

while.head139.preheader:                          ; preds = %while.head132
  br label %while.head139

while.exit134:                                    ; preds = %while.head132
  %add224 = add i64 %r.1, 1
  br label %while.head125

while.head139:                                    ; preds = %while.head139.preheader, %while.exit152
  %p138.0 = phi i64 [ %add193, %while.exit152 ], [ 0, %while.head139.preheader ]
  %cmp144 = icmp ult i64 %p138.0, %scalar.ref.elem54
  br i1 %cmp144, label %while.body140, label %while.head194.preheader

while.head194.preheader:                          ; preds = %while.head139
  br label %while.head194

while.body140:                                    ; preds = %while.head139
  %wd.in_bounds.unsigned146 = icmp samesign ult i64 %p138.0, 16
  br i1 %wd.in_bounds.unsigned146, label %wd.ok147, label %wd.fail148

wd.ok147:                                         ; preds = %while.body140
  %elem.ptr149 = getelementptr [8 x i8], ptr %sum, i64 %p138.0
  store double 0.000000e+00, ptr %elem.ptr149, align 8
  br label %while.head150

wd.fail148:                                       ; preds = %while.body140
  call void @llvm.trap()
  unreachable

while.head150:                                    ; preds = %wd.ok186, %wd.ok147
  %elem = phi double [ 0.000000e+00, %wd.ok147 ], [ %fadd, %wd.ok186 ]
  %s.0 = phi i64 [ 0, %wd.ok147 ], [ %add191, %wd.ok186 ]
  %cmp155 = icmp ult i64 %s.0, %scalar.ref.elem54
  br i1 %cmp155, label %wd.ok163, label %while.exit152

while.exit152:                                    ; preds = %while.head150
  %add193 = add nuw nsw i64 %p138.0, 1
  br label %while.head139

wd.ok163:                                         ; preds = %while.head150
  %mul168 = mul i64 %r.1, %scalar.ref.elem51
  %add170 = add i64 %mul168, %q131.0
  %mul172 = mul i64 %add170, %scalar.ref.elem54
  %add174 = add i64 %mul172, %s.0
  %wd.in_bounds.unsigned175 = icmp ult i64 %add174, 4096
  br i1 %wd.in_bounds.unsigned175, label %wd.ok176, label %wd.fail177

wd.ok176:                                         ; preds = %wd.ok163
  %mul182 = mul i64 %s.0, %scalar.ref.elem54
  %add184 = add i64 %mul182, %p138.0
  %wd.in_bounds.unsigned185 = icmp ult i64 %add184, 256
  br i1 %wd.in_bounds.unsigned185, label %wd.ok186, label %wd.fail187

wd.fail177:                                       ; preds = %wd.ok163
  call void @llvm.trap()
  unreachable

wd.ok186:                                         ; preds = %wd.ok176
  %elem.ptr178 = getelementptr [8 x i8], ptr %a, i64 %add174
  %elem179 = load double, ptr %elem.ptr178, align 8
  %elem.ptr188 = getelementptr [8 x i8], ptr %c4, i64 %add184
  %elem189 = load double, ptr %elem.ptr188, align 8
  %fmul = fmul contract double %elem179, %elem189
  %fadd = fadd contract double %elem, %fmul
  store double %fadd, ptr %elem.ptr149, align 8
  %add191 = add i64 %s.0, 1
  br label %while.head150

wd.fail187:                                       ; preds = %wd.ok176
  call void @llvm.trap()
  unreachable

while.head194:                                    ; preds = %while.head194.preheader, %wd.ok215
  %p138.1 = phi i64 [ %add220, %wd.ok215 ], [ 0, %while.head194.preheader ]
  %cmp199 = icmp ult i64 %p138.1, %scalar.ref.elem54
  br i1 %cmp199, label %while.body195, label %while.exit196

while.body195:                                    ; preds = %while.head194
  %mul202 = mul i64 %r.1, %scalar.ref.elem51
  %add204 = add i64 %mul202, %q131.0
  %mul206 = mul i64 %add204, %scalar.ref.elem54
  %add208 = add i64 %mul206, %p138.1
  %wd.in_bounds.unsigned209 = icmp ult i64 %add208, 4096
  br i1 %wd.in_bounds.unsigned209, label %wd.ok210, label %wd.fail211

while.exit196:                                    ; preds = %while.head194
  %add222 = add i64 %q131.0, 1
  br label %while.head132

wd.ok210:                                         ; preds = %while.body195
  %wd.in_bounds.unsigned214 = icmp samesign ult i64 %p138.1, 16
  br i1 %wd.in_bounds.unsigned214, label %wd.ok215, label %wd.fail216

wd.fail211:                                       ; preds = %while.body195
  call void @llvm.trap()
  unreachable

wd.ok215:                                         ; preds = %wd.ok210
  %elem.ptr212 = getelementptr [8 x i8], ptr %a, i64 %add208
  %elem.ptr217 = getelementptr [8 x i8], ptr %sum, i64 %p138.1
  %elem218 = load double, ptr %elem.ptr217, align 8
  store double %elem218, ptr %elem.ptr212, align 8
  %add220 = add nuw nsw i64 %p138.1, 1
  br label %while.head194

wd.fail216:                                       ; preds = %wd.ok210
  call void @llvm.trap()
  unreachable

while.head225:                                    ; preds = %while.head225.preheader, %while.exit234
  %r.2 = phi i64 [ %add266, %while.exit234 ], [ 0, %while.head225.preheader ]
  %checksum.0 = phi double [ %checksum.1.lcssa, %while.exit234 ], [ 0.000000e+00, %while.head225.preheader ]
  %cmp230 = icmp ult i64 %r.2, %scalar.ref.elem
  br i1 %cmp230, label %while.head232.preheader, label %common.ret.loopexit

while.head232.preheader:                          ; preds = %while.head225
  br label %while.head232

while.head232:                                    ; preds = %while.head232.preheader, %while.exit241
  %checksum.1 = phi double [ %checksum.2.lcssa, %while.exit241 ], [ %checksum.0, %while.head232.preheader ]
  %q231.0 = phi i64 [ %add264, %while.exit241 ], [ 0, %while.head232.preheader ]
  %cmp237 = icmp ult i64 %q231.0, %scalar.ref.elem51
  br i1 %cmp237, label %while.head239.preheader, label %while.exit234

while.head239.preheader:                          ; preds = %while.head232
  br label %while.head239

while.exit234:                                    ; preds = %while.head232
  %checksum.1.lcssa = phi double [ %checksum.1, %while.head232 ]
  %add266 = add i64 %r.2, 1
  br label %while.head225

while.head239:                                    ; preds = %while.head239.preheader, %wd.ok256
  %checksum.2 = phi double [ %fadd260, %wd.ok256 ], [ %checksum.1, %while.head239.preheader ]
  %p238.0 = phi i64 [ %add262, %wd.ok256 ], [ 0, %while.head239.preheader ]
  %cmp244 = icmp ult i64 %p238.0, %scalar.ref.elem54
  br i1 %cmp244, label %while.body240, label %while.exit241

while.body240:                                    ; preds = %while.head239
  %mul248 = mul i64 %r.2, %scalar.ref.elem51
  %add250 = add i64 %mul248, %q231.0
  %mul252 = mul i64 %add250, %scalar.ref.elem54
  %add254 = add i64 %mul252, %p238.0
  %wd.in_bounds.unsigned255 = icmp ult i64 %add254, 4096
  br i1 %wd.in_bounds.unsigned255, label %wd.ok256, label %wd.fail257

while.exit241:                                    ; preds = %while.head239
  %checksum.2.lcssa = phi double [ %checksum.2, %while.head239 ]
  %add264 = add i64 %q231.0, 1
  br label %while.head232

wd.ok256:                                         ; preds = %while.body240
  %elem.ptr258 = getelementptr [8 x i8], ptr %a, i64 %add254
  %elem259 = load double, ptr %elem.ptr258, align 8
  %fadd260 = fadd contract double %checksum.2, %elem259
  %add262 = add i64 %p238.0, 1
  br label %while.head239

wd.fail257:                                       ; preds = %while.body240
  call void @llvm.trap()
  unreachable
}

; Function Attrs: nounwind
define double @doitgen(ptr %0, i64 %1) #0 {
entry:
  %export.call = call double @doitgen_impl(ptr %0, i64 %1)
  ret double %export.call
}

; Function Attrs: nocallback nofree nosync nounwind willreturn memory(argmem: write)
declare void @llvm.memset.p0.i64(ptr writeonly captures(none), i8, i64, i1 immarg) #1

; Function Attrs: cold noreturn nounwind memory(inaccessiblemem: write)
declare void @llvm.trap() #2

attributes #0 = { nounwind }
attributes #1 = { nocallback nofree nosync nounwind willreturn memory(argmem: write) }
attributes #2 = { cold noreturn nounwind memory(inaccessiblemem: write) }
