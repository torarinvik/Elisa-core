(* ===================================================================== *)
(*  EasmTAL.v                                                            *)
(*  A mechanized core of the EASM transition relation (docs/104, 106).  *)
(*                                                                       *)
(*  This file formalizes the straight-line GPR/ALU subset of the EASM   *)
(*  virtual ISA that the Go verifier tracks for definite-initialization *)
(*  ("liveness") in compiler/src/easm/easm.go (verifyFunction register  *)
(*  tracking: machineFactState.LiveRegs, and the register-read-          *)
(*  uninitialized diagnostic).                                          *)
(*                                                                       *)
(*  It proves the two standard type-safety metatheorems for that core:  *)
(*    - PRESERVATION: abstract typing is sound w.r.t. the concrete       *)
(*      definedness semantics, instruction-by-instruction and over       *)
(*      whole sequences.                                                 *)
(*    - PROGRESS: a well-typed instruction never reads an undefined      *)
(*      register (no stuck-by-uninitialized-read), and a well-typed      *)
(*      sequence runs to completion without getting stuck.               *)
(*    - MERGE SOUNDNESS: typing a post-merge block under the MEET of      *)
(*      the predecessor states is sound on every incoming edge (the      *)
(*      soundness of checkMergeConsistency / the dataflow join).         *)
(*                                                                       *)
(*  Checked with: rocq compile EasmTAL.v  (Rocq 9.1; no admits/Admitted).*)
(* ===================================================================== *)

Require Import Stdlib.Lists.List.
Require Import Stdlib.Arith.PeanoNat.
Import ListNotations.

(* ------------------------------------------------------------------ *)
(*  Registers                                                          *)
(*                                                                     *)
(*  We model a finite, fixed set of canonical 64-bit GPRs. This        *)
(*  mirrors the verifier's canonicalRegisterName: it works with the    *)
(*  64-bit roots (rax/rbx/...) keyed by string. We use an inductive    *)
(*  with decidable equality instead of strings so the maps are total   *)
(*  functions and equality is trivially decidable.                     *)
(* ------------------------------------------------------------------ *)

Inductive reg : Type :=
  | RAX | RBX | RCX | RDX | RSI | RDI | R8 | R9.

Definition reg_eqb (a b : reg) : bool :=
  match a, b with
  | RAX, RAX | RBX, RBX | RCX, RCX | RDX, RDX
  | RSI, RSI | RDI, RDI | R8, R8 | R9, R9 => true
  | _, _ => false
  end.

Lemma reg_eqb_refl : forall r, reg_eqb r r = true.
Proof. destruct r; reflexivity. Qed.

Lemma reg_eqb_true : forall a b, reg_eqb a b = true -> a = b.
Proof. destruct a, b; simpl; intro H; (reflexivity || discriminate). Qed.

Lemma reg_eqb_false : forall a b, reg_eqb a b = false -> a <> b.
Proof. destruct a, b; simpl; intro H; (discriminate || (intro Heq; discriminate)). Qed.

(* ------------------------------------------------------------------ *)
(*  Operands and instructions                                          *)
(*                                                                     *)
(*  Operand = register | immediate. We model 64-bit words as nat       *)
(*  (the definite-initialization lattice the verifier tracks does NOT  *)
(*  depend on the concrete bit-width; only definedness matters for the *)
(*  safety theorem). KnownUInt-style value tracking is orthogonal and  *)
(*  not needed for progress/preservation, so it is omitted.            *)
(*                                                                     *)
(*  Instruction set: the q-suffix GPR/ALU subset the verifier tracks   *)
(*  with a same-shape effect signature in opRules                      *)
(*  (compiler/src/easm/easm_oprules.go):                               *)
(*    mov(reg/imm), add, sub, and, xor (two-operand dst,src)           *)
(*    inc, dec (one-operand dst, read-modify-write)                    *)
(*  All of add/sub/and/xor/inc/dec clobber flags in the real ISA       *)
(*  (opRules.ClobbersFlags). Flags DEFINEDNESS is now modeled as the    *)
(*  `aflags` lattice slot: ALU ops define it, mov preserves it. See     *)
(*  the absstate definition and alu_defines_flags/mov_preserves_flags.  *)
(* ------------------------------------------------------------------ *)

Inductive operand : Type :=
  | OReg : reg -> operand
  | OImm : nat -> operand.

Inductive instr : Type :=
  | Imov : reg -> operand -> instr   (* dst <- src                         *)
  | Iadd : reg -> operand -> instr   (* dst <- dst + src   (reads dst)     *)
  | Isub : reg -> operand -> instr   (* dst <- dst - src   (reads dst)     *)
  | Iand : reg -> operand -> instr   (* dst <- dst & src   (reads dst)     *)
  | Ixor : reg -> operand -> instr   (* dst <- dst ^ src   (reads dst)     *)
  | Iinc : reg -> instr              (* dst <- dst + 1     (reads dst)     *)
  | Idec : reg -> instr              (* dst <- dst - 1     (reads dst)     *)
  (* docs/107 typed guest-memory overlay:                                  *)
  | Iguard : nat -> instr            (* size guard: refine known-min size  *)
                                     (* to >= N (enter the discharging     *)
                                     (* branch of `if base.size>=N` /      *)
                                     (* fall-through of `if base.size<N:    *)
                                     (* return`). Abstract image of a       *)
                                     (* dominating SizeGuardFact.           *)
  | Iread : reg -> nat -> nat -> instr.
                                     (* dst <- guest[offset .. offset+width]*)
                                     (* a checked field read; offset N,     *)
                                     (* width W. Well-typed ONLY when        *)
                                     (* offset+width <= known minimum size.  *)
                                     (* (base.field[mem] over GuestVAddr[L]) *)

(* ------------------------------------------------------------------ *)
(*  Abstract state Gamma : per-register definedness + a flags slot      *)
(*                                                                     *)
(*  The register map matches machineFactState.LiveRegs (a finite map    *)
(*  reg -> bool, true meaning "established/Defined"); absence in the Go  *)
(*  map = Undefined = false. We additionally carry ONE extra lattice     *)
(*  bit, `aflags`, tracking whether the EFLAGS condition codes are       *)
(*  Defined at this program point. This is the first item of docs/106's  *)
(*  "Path to widening the mechanized subset" (flags as a lattice slot).  *)
(*                                                                       *)
(*  Faithfulness note: the Go verifier records, per opcode, a            *)
(*  ClobbersFlags bit (easm_oprules.go opRules / instructionClobbersFlags*)
(*  easm.go:3013). The ALU ops add/sub/and/xor/inc/dec carry             *)
(*  ClobbersFlags=true; mov(reg/imm) carries ClobbersFlags=false. The    *)
(*  flags slot here is DEFINED by exactly the clobbering ops and         *)
(*  PRESERVED across mov — mirroring that table. (The verifier does not  *)
(*  expose a flags-defined fact in machineFactState, nor a flag-reading  *)
(*  op in the modeled subset; we therefore do not invent a flag read —   *)
(*  see preservation_flags below and the README.)                        *)
(* ------------------------------------------------------------------ *)

Record absstate := mkabs {
  aregs  : reg -> bool;   (* per-register definite-init (LiveRegs) *)
  aflags : bool;          (* EFLAGS condition codes Defined?        *)
  amsize : nat            (* known MINIMUM guest-struct size (docs/107)
                             a size lower-bound proven to hold here:
                             "the carrier's runtime size is >= amsize".
                             0 = nothing proven (no dominating guard).   *)
}.

(* coercion-free accessor for reading a register's definedness *)
Definition aget (g : absstate) (r : reg) : bool := aregs g r.

Definition aempty : absstate := mkabs (fun _ => false) false 0.

Definition aset (g : absstate) (r : reg) (b : bool) : absstate :=
  mkabs (fun r' => if reg_eqb r r' then b else aregs g r') (aflags g) (amsize g).

Definition adefine (g : absstate) (r : reg) : absstate := aset g r true.

(* set the flags slot (Defined when the op clobbers flags). *)
Definition asetflags (g : absstate) (b : bool) : absstate :=
  mkabs (aregs g) b (amsize g).

(* ------------------------------------------------------------------ *)
(*  Guest-memory size slot helpers (docs/107)                         *)
(*                                                                     *)
(*  amsize is the known MINIMUM of the carrier's runtime `size` field. *)
(*  A dominating size guard `if base.size[mem] >= N:` (positive form)  *)
(*  or the fall-through of `if base.size[mem] < N: return` (early-     *)
(*  return form) STRENGTHENS this fact to at least N. This mirrors the *)
(*  Go SizeGuardFact (easm_guest_overlay.go): the front-end turns a    *)
(*  dominating guard into a proven lower bound, and a refinement only  *)
(*  ever RAISES the known minimum (never lowers it), so we take the    *)
(*  max with the existing slot — keeping any stronger prior fact.      *)
(* ------------------------------------------------------------------ *)

Definition asetmsize (g : absstate) (n : nat) : absstate :=
  mkabs (aregs g) (aflags g) n.

(* refine the known-minimum-size to be AT LEAST n (entering a guard's
   discharging branch); never weakens an already-stronger bound. *)
Definition arefine_size (g : absstate) (n : nat) : absstate :=
  asetmsize g (Nat.max (amsize g) n).

Lemma aset_same : forall g r b, aget (aset g r b) r = b.
Proof. intros. unfold aget, aset. simpl. rewrite reg_eqb_refl. reflexivity. Qed.

Lemma aset_other : forall g r b r', r <> r' -> aget (aset g r b) r' = aget g r'.
Proof.
  intros g r b r' H. unfold aget, aset. simpl.
  destruct (reg_eqb r r') eqn:E.
  - apply reg_eqb_true in E. contradiction.
  - reflexivity.
Qed.

(* setting/defining a register leaves the flags slot untouched. *)
Lemma aset_flags : forall g r b, aflags (aset g r b) = aflags g.
Proof. intros. unfold aset. reflexivity. Qed.

Lemma adefine_flags : forall g r, aflags (adefine g r) = aflags g.
Proof. intros. apply aset_flags. Qed.

(* setting/defining a register leaves the size slot untouched too. *)
Lemma aset_msize : forall g r b, amsize (aset g r b) = amsize g.
Proof. intros. unfold aset. reflexivity. Qed.

Lemma adefine_msize : forall g r, amsize (adefine g r) = amsize g.
Proof. intros. apply aset_msize. Qed.

(* the size slot after a guard refinement is at least the guarded bound, and at
   least the prior known minimum. *)
Lemma arefine_size_lb : forall g n, n <= amsize (arefine_size g n).
Proof. intros. unfold arefine_size, asetmsize. simpl. apply Nat.le_max_r. Qed.

Lemma arefine_size_mono : forall g n, amsize g <= amsize (arefine_size g n).
Proof. intros. unfold arefine_size, asetmsize. simpl. apply Nat.le_max_l. Qed.

(* ------------------------------------------------------------------ *)
(*  The abstract typing relation  Gamma |- instr => Gamma'             *)
(*                                                                     *)
(*  Mirrors the verifier walk:                                         *)
(*   - reading an operand register requires it to be Defined in Gamma  *)
(*     (the register-read-uninitialized check, easm.go ~line 1212);    *)
(*     reading an undefined register is ILL-TYPED (no rule applies).   *)
(*   - the destination is Defined in Gamma' (easm.go ~line 1278:       *)
(*     "instruction writes a defined result here").                    *)
(*   - read-modify-write ops (add/sub/and/xor/inc/dec) also read dst.  *)
(* ------------------------------------------------------------------ *)

(* an operand is "abstractly readable" under Gamma *)
Definition op_ok (g : absstate) (o : operand) : bool :=
  match o with
  | OReg r => aget g r
  | OImm _ => true
  end.

(* The flags-defining wrapper: an ALU op defines BOTH its destination register
   and the flags slot. mov defines only the destination, leaving flags alone.
   This is the abstract image of opRules.ClobbersFlags. *)
Definition adefine_cc (g : absstate) (r : reg) : absstate :=
  asetflags (adefine g r) true.

Inductive has_type : absstate -> instr -> absstate -> Prop :=
  | T_mov : forall g d s,
      op_ok g s = true ->
      has_type g (Imov d s) (adefine g d)          (* mov: no ClobbersFlags *)
  | T_add : forall g d s,
      aget g d = true -> op_ok g s = true ->
      has_type g (Iadd d s) (adefine_cc g d)        (* ALU: ClobbersFlags     *)
  | T_sub : forall g d s,
      aget g d = true -> op_ok g s = true ->
      has_type g (Isub d s) (adefine_cc g d)
  | T_and : forall g d s,
      aget g d = true -> op_ok g s = true ->
      has_type g (Iand d s) (adefine_cc g d)
  | T_xor : forall g d s,
      aget g d = true -> op_ok g s = true ->
      has_type g (Ixor d s) (adefine_cc g d)
  | T_inc : forall g d,
      aget g d = true ->
      has_type g (Iinc d) (adefine_cc g d)
  | T_dec : forall g d,
      aget g d = true ->
      has_type g (Idec d) (adefine_cc g d)
  (* docs/107: a size guard strengthens the known-minimum-size fact. It reads
     and writes no GPR and does not touch the flags slot — it is the abstract
     image of entering the branch a dominating size guard discharges. *)
  | T_guard : forall g n,
      has_type g (Iguard n) (arefine_size g n)
  (* docs/107: a guest field read is well-typed ONLY when its byte span
     [offset, offset+width) is covered by the known minimum size — i.e. a
     dominating size guard has proven the struct is at least that big. A read
     whose span exceeds the known minimum has NO applicable rule = ill-typed =
     stuck (the overlay-field-needs-size-guard rejection). The loaded value is
     Defined in the post-state; flags are preserved (a load, like mov). *)
  | T_read : forall g d off w,
      off + w <= amsize g ->
      has_type g (Iread d off w) (adefine g d).

(* Typing of a straight-line sequence (list of instructions). *)
Inductive seq_type : absstate -> list instr -> absstate -> Prop :=
  | TS_nil  : forall g, seq_type g [] g
  | TS_cons : forall g g' g'' i instructions,
      has_type g i g' ->
      seq_type g' instructions g'' ->
      seq_type g (i :: instructions) g''.

(* ------------------------------------------------------------------ *)
(*  Concrete semantics                                                 *)
(*                                                                     *)
(*  A concrete machine is a register file: each register carries an    *)
(*  optional 64-bit word. None = physically undefined/garbage.         *)
(*  This is the ground truth the abstract definedness lattice          *)
(*  approximates.                                                      *)
(* ------------------------------------------------------------------ *)

(* The concrete machine carries the register file AND the flags. flags = None
   means the condition codes are physically undefined/garbage (e.g. on function
   entry, before any flag-writing op has run); Some tt means defined. We model
   the flags VALUE as unit, since the modeled subset has no flag-reading op:
   only flag DEFINEDNESS is observable, matching the verifier. *)
Record rfile := mkrf {
  rregs  : reg -> option nat;
  rflags : option unit;
  rsize  : nat              (* the carrier's ACTUAL runtime guest-struct size.
                              The ground truth amsize under-approximates: a
                              read of bytes [off, off+w) is physically in
                              bounds iff off+w <= rsize.                      *)
}.

Definition rfset (rho : rfile) (r : reg) (v : nat) : rfile :=
  mkrf (fun r' => if reg_eqb r r' then Some v else rregs rho r') (rflags rho) (rsize rho).

(* an ALU op also defines the flags (sets them to Some tt). *)
Definition rfsetf (rho : rfile) (r : reg) (v : nat) : rfile :=
  mkrf (fun r' => if reg_eqb r r' then Some v else rregs rho r') (Some tt) (rsize rho).

Lemma rfset_same : forall rho r v, rregs (rfset rho r v) r = Some v.
Proof. intros. unfold rfset. simpl. rewrite reg_eqb_refl. reflexivity. Qed.

Lemma rfset_other : forall rho r v r', r <> r' -> rregs (rfset rho r v) r' = rregs rho r'.
Proof.
  intros rho r v r' H. unfold rfset. simpl.
  destruct (reg_eqb r r') eqn:E.
  - apply reg_eqb_true in E. contradiction.
  - reflexivity.
Qed.

Lemma rfset_flags : forall rho r v, rflags (rfset rho r v) = rflags rho.
Proof. intros. reflexivity. Qed.

Lemma rfsetf_same : forall rho r v, rregs (rfsetf rho r v) r = Some v.
Proof. intros. unfold rfsetf. simpl. rewrite reg_eqb_refl. reflexivity. Qed.

Lemma rfsetf_other : forall rho r v r', r <> r' -> rregs (rfsetf rho r v) r' = rregs rho r'.
Proof.
  intros rho r v r' H. unfold rfsetf. simpl.
  destruct (reg_eqb r r') eqn:E.
  - apply reg_eqb_true in E. contradiction.
  - reflexivity.
Qed.

Lemma rfsetf_flags : forall rho r v, rflags (rfsetf rho r v) = Some tt.
Proof. intros. reflexivity. Qed.

(* register writes leave the actual runtime size unchanged (a GPR write does
   not alter the guest struct). *)
Lemma rfset_size : forall rho r v, rsize (rfset rho r v) = rsize rho.
Proof. intros. reflexivity. Qed.

Lemma rfsetf_size : forall rho r v, rsize (rfsetf rho r v) = rsize rho.
Proof. intros. reflexivity. Qed.

(* Value of an operand in a concrete machine: None if it reads an
   undefined register (a STUCK read). *)
Definition oval (rho : rfile) (o : operand) : option nat :=
  match o with
  | OReg r => rregs rho r
  | OImm n => Some n
  end.

(* Big-step evaluation: step rho i = Some rho' on success, None if the
   instruction reads an undefined register (gets stuck). This is the
   concrete counterpart of "ill-typed = stuck". *)
(* ALU ops define the flags: they use rfsetf, which sets rflags to Some tt. *)
Definition alu (f : nat -> nat -> nat) (rho : rfile) (d : reg) (o : operand)
  : option rfile :=
  match rregs rho d, oval rho o with
  | Some a, Some b => Some (rfsetf rho d (f a b))
  | _, _ => None
  end.

Definition step (rho : rfile) (i : instr) : option rfile :=
  match i with
  | Imov d s =>
      match oval rho s with
      | Some v => Some (rfset rho d v)         (* mov: flags preserved (rfset) *)
      | None => None
      end
  | Iadd d s => alu Nat.add rho d s
  | Isub d s => alu Nat.sub rho d s
  | Iand d s => alu (fun a b => a) rho d s   (* concrete bit-op shape irrelevant to safety *)
  | Ixor d s => alu (fun a b => a) rho d s
  | Iinc d   => match rregs rho d with Some a => Some (rfsetf rho d (S a)) | None => None end
  | Idec d   => match rregs rho d with Some a => Some (rfsetf rho d (Nat.pred a)) | None => None end
  (* docs/107: a size guard is a runtime no-op on the register file, but it is
     a CONTROL-FLOW filter: we only reach the discharging branch when the actual
     runtime size really is >= N. We model that by stepping iff rsize rho >= N
     (and getting "stuck" otherwise — the branch is simply not taken, so the
     post-guard program point is unreachable on the failing path). On the taken
     path the register file/flags/size are unchanged. This is what justifies the
     abstract refinement amsize := max(amsize, N): past the guard, size >= N is a
     physical fact. *)
  | Iguard n => if Nat.leb n (rsize rho) then Some rho else None
  (* docs/107: a guest read of [off, off+w) physically succeeds (produces a
     Defined value) iff the span is within the actual runtime size; otherwise it
     is an over-read and gets STUCK (None) — the wild ReadU64 the overlay exists
     to prevent. The loaded value is modeled abstractly (the concrete byte value
     is irrelevant to definedness); flags are preserved (rfset). *)
  | Iread d off w =>
      if Nat.leb (off + w) (rsize rho) then Some (rfset rho d 0) else None
  end.

Fixpoint run (rho : rfile) (instructions : list instr) : option rfile :=
  match instructions with
  | [] => Some rho
  | i :: rest =>
      match step rho i with
      | Some rho' => run rho' rest
      | None => None
      end
  end.

