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
  | Idec : reg -> instr.             (* dst <- dst - 1     (reads dst)     *)

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
  aflags : bool           (* EFLAGS condition codes Defined?        *)
}.

(* coercion-free accessor for reading a register's definedness *)
Definition aget (g : absstate) (r : reg) : bool := aregs g r.

Definition aempty : absstate := mkabs (fun _ => false) false.

Definition aset (g : absstate) (r : reg) (b : bool) : absstate :=
  mkabs (fun r' => if reg_eqb r r' then b else aregs g r') (aflags g).

Definition adefine (g : absstate) (r : reg) : absstate := aset g r true.

(* set the flags slot (Defined when the op clobbers flags). *)
Definition asetflags (g : absstate) (b : bool) : absstate :=
  mkabs (aregs g) b.

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
      has_type g (Idec d) (adefine_cc g d).

(* Typing of a straight-line sequence (list of instructions). *)
Inductive seq_type : absstate -> list instr -> absstate -> Prop :=
  | TS_nil  : forall g, seq_type g [] g
  | TS_cons : forall g g' g'' i is,
      has_type g i g' ->
      seq_type g' is g'' ->
      seq_type g (i :: is) g''.

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
  rflags : option unit
}.

Definition rfset (rho : rfile) (r : reg) (v : nat) : rfile :=
  mkrf (fun r' => if reg_eqb r r' then Some v else rregs rho r') (rflags rho).

(* an ALU op also defines the flags (sets them to Some tt). *)
Definition rfsetf (rho : rfile) (r : reg) (v : nat) : rfile :=
  mkrf (fun r' => if reg_eqb r r' then Some v else rregs rho r') (Some tt).

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
  end.

Fixpoint run (rho : rfile) (is : list instr) : option rfile :=
  match is with
  | [] => Some rho
  | i :: rest =>
      match step rho i with
      | Some rho' => run rho' rest
      | None => None
      end
  end.

(* ------------------------------------------------------------------ *)
(*  The models relation  rho |= Gamma                                  *)
(*                                                                     *)
(*  Every register that Gamma calls Defined is physically defined in   *)
(*  rho. (Gamma is a sound under-approximation of physical             *)
(*  definedness.)                                                      *)
(* ------------------------------------------------------------------ *)

(* models has TWO conjuncts now: the register conjunct (every Defined-in-g reg
   is physically defined) AND the flags conjunct (if g says flags are Defined,
   they are physically defined in rho). *)
Definition models (rho : rfile) (g : absstate) : Prop :=
  (forall r, aget g r = true -> exists v, rregs rho r = Some v)
  /\ (aflags g = true -> exists u, rflags rho = Some u).

(* projections for convenience *)
Lemma models_regs : forall rho g r,
  models rho g -> aget g r = true -> exists v, rregs rho r = Some v.
Proof. intros rho g r [Hr _] H. apply Hr. exact H. Qed.

Lemma models_flags : forall rho g,
  models rho g -> aflags g = true -> exists u, rflags rho = Some u.
Proof. intros rho g [_ Hf] H. apply Hf. exact H. Qed.

(* operand soundness: if op_ok abstractly, it has a concrete value *)
Lemma op_ok_models : forall rho g o,
  models rho g -> op_ok g o = true -> exists v, oval rho o = Some v.
Proof.
  intros rho g o Hm Hok. destruct o as [r | n]; simpl in *.
  - apply (models_regs rho g r Hm). exact Hok.
  - exists n. reflexivity.
Qed.

(* defining a register via mov (rfset / adefine) preserves the models relation.
   Both sides leave the flags slot UNCHANGED, so the flags conjunct is carried
   verbatim. *)
Lemma models_define : forall rho g d v,
  models rho g -> models (rfset rho d v) (adefine g d).
Proof.
  intros rho g d v [Hmr Hmf]. split.
  - intros r Hr. unfold adefine, aset, aget in Hr. simpl in Hr.
    destruct (reg_eqb d r) eqn:E.
    + apply reg_eqb_true in E. subst. exists v. apply rfset_same.
    + apply reg_eqb_false in E.
      assert (Hr' : aget g r = true) by exact Hr.
      destruct (Hmr r Hr') as [w Hw]. exists w. rewrite rfset_other; auto.
  - rewrite adefine_flags. rewrite rfset_flags. exact Hmf.
Qed.

(* defining a register via an ALU op (rfsetf / adefine_cc) preserves models AND
   establishes the flags conjunct: the post-state's flags slot is Defined (true)
   and the concrete machine's flags are physically Some tt. *)
Lemma models_define_cc : forall rho g d v,
  models rho g -> models (rfsetf rho d v) (adefine_cc g d).
Proof.
  intros rho g d v [Hmr _]. split.
  - intros r Hr. unfold adefine_cc, asetflags, adefine, aset, aget in Hr. simpl in Hr.
    destruct (reg_eqb d r) eqn:E.
    + apply reg_eqb_true in E. subst. exists v. apply rfsetf_same.
    + apply reg_eqb_false in E.
      assert (Hr' : aget g r = true) by exact Hr.
      destruct (Hmr r Hr') as [w Hw]. exists w. rewrite rfsetf_other; auto.
  - intros _. exists tt. apply rfsetf_flags.
Qed.

(* ================================================================== *)
(*  PROGRESS                                                           *)
(*                                                                     *)
(*  A well-typed instruction, in a state that models its abstract     *)
(*  precondition, never gets stuck: step succeeds.                    *)
(* ================================================================== *)

Theorem progress : forall g i g' rho,
  has_type g i g' ->
  models rho g ->
  exists rho', step rho i = Some rho'.
Proof.
  intros g i g' rho Ht Hm.
  inversion Ht; subst; simpl.
  - (* mov *) destruct (op_ok_models rho g s Hm H) as [v Hv].
    rewrite Hv. eauto.
  - (* add *) unfold alu.
    destruct (models_regs rho g d Hm H) as [a Ha]. rewrite Ha.
    destruct (op_ok_models rho g s Hm H0) as [b Hb]. rewrite Hb. eauto.
  - (* sub *) unfold alu.
    destruct (models_regs rho g d Hm H) as [a Ha]. rewrite Ha.
    destruct (op_ok_models rho g s Hm H0) as [b Hb]. rewrite Hb. eauto.
  - (* and *) unfold alu.
    destruct (models_regs rho g d Hm H) as [a Ha]. rewrite Ha.
    destruct (op_ok_models rho g s Hm H0) as [b Hb]. rewrite Hb. eauto.
  - (* xor *) unfold alu.
    destruct (models_regs rho g d Hm H) as [a Ha]. rewrite Ha.
    destruct (op_ok_models rho g s Hm H0) as [b Hb]. rewrite Hb. eauto.
  - (* inc *) destruct (models_regs rho g d Hm H) as [a Ha]. rewrite Ha. eauto.
  - (* dec *) destruct (models_regs rho g d Hm H) as [a Ha]. rewrite Ha. eauto.
Qed.

(* ================================================================== *)
(*  PRESERVATION                                                       *)
(*                                                                     *)
(*  If Gamma |- i => Gamma', rho |= Gamma, and i steps rho to rho',    *)
(*  then rho' |= Gamma'.                                               *)
(* ================================================================== *)

Theorem preservation : forall g i g' rho rho',
  has_type g i g' ->
  models rho g ->
  step rho i = Some rho' ->
  models rho' g'.
Proof.
  intros g i g' rho rho' Ht Hm Hstep.
  inversion Ht; subst; simpl in Hstep.
  - (* mov *)
    destruct (oval rho s) as [v|] eqn:Ev; [|discriminate].
    inversion Hstep; subst. apply models_define; auto.
  - (* add *) unfold alu in Hstep.
    destruct (rregs rho d) as [a|] eqn:Ea; [|discriminate].
    destruct (oval rho s) as [b|] eqn:Eb; [|discriminate].
    inversion Hstep; subst. apply models_define_cc; auto.
  - (* sub *) unfold alu in Hstep.
    destruct (rregs rho d) as [a|] eqn:Ea; [|discriminate].
    destruct (oval rho s) as [b|] eqn:Eb; [|discriminate].
    inversion Hstep; subst. apply models_define_cc; auto.
  - (* and *) unfold alu in Hstep.
    destruct (rregs rho d) as [a|] eqn:Ea; [|discriminate].
    destruct (oval rho s) as [b|] eqn:Eb; [|discriminate].
    inversion Hstep; subst. apply models_define_cc; auto.
  - (* xor *) unfold alu in Hstep.
    destruct (rregs rho d) as [a|] eqn:Ea; [|discriminate].
    destruct (oval rho s) as [b|] eqn:Eb; [|discriminate].
    inversion Hstep; subst. apply models_define_cc; auto.
  - (* inc *)
    destruct (rregs rho d) as [a|] eqn:Ea; [|discriminate].
    inversion Hstep; subst. apply models_define_cc; auto.
  - (* dec *)
    destruct (rregs rho d) as [a|] eqn:Ea; [|discriminate].
    inversion Hstep; subst. apply models_define_cc; auto.
Qed.

(* ================================================================== *)
(*  FLAGS TRACKING IS FAITHFUL TO opRules.ClobbersFlags               *)
(*                                                                    *)
(*  These two corollaries pin down the flags slot's behavior, which   *)
(*  is exactly what the Go opRules table declares per opcode:         *)
(*   - an ALU op (ClobbersFlags=true) DEFINES the flags slot in the   *)
(*     post-state (aflags g' = true), regardless of the pre-state;    *)
(*   - mov (ClobbersFlags=false) PRESERVES the flags slot (aflags g'  *)
(*     = aflags g): it neither establishes nor clears it.             *)
(*  preservation (above) already shows these abstract facts are sound *)
(*  w.r.t. the concrete flags (rflags): when the post-state claims    *)
(*  flags Defined, the machine's flags are physically Some.           *)
(* ================================================================== *)

(* ALU ops define the flags slot in the abstract post-state. *)
Theorem alu_defines_flags : forall g i g',
  has_type g i g' ->
  (forall d s, i <> Imov d s) ->
  aflags g' = true.
Proof.
  intros g i g' Ht Hnmov.
  inversion Ht; subst; try reflexivity.
  - exfalso. eapply Hnmov. reflexivity.
Qed.

(* mov preserves the flags slot (does not clobber it). *)
Theorem mov_preserves_flags : forall g d s g',
  has_type g (Imov d s) g' ->
  aflags g' = aflags g.
Proof.
  intros g d s g' Ht. inversion Ht; subst. apply adefine_flags.
Qed.

(* ================================================================== *)
(*  SEQUENCE-LEVEL SAFETY  (type soundness for straight-line blocks)  *)
(*                                                                     *)
(*  A well-typed sequence run from a modeling state never gets stuck   *)
(*  and ends in a state modeling the final abstract context.          *)
(* ================================================================== *)

Theorem seq_safety : forall is g g' rho,
  seq_type g is g' ->
  models rho g ->
  exists rho', run rho is = Some rho' /\ models rho' g'.
Proof.
  induction is as [| i rest IH]; intros g g' rho Hseq Hm.
  - (* nil *) inversion Hseq; subst. exists rho. split; [reflexivity | exact Hm].
  - (* cons *)
    inversion Hseq as [| ga gmid gb ii iis Hhead Htail Eqg Eqi Eqg2]; subst.
    (* one step makes progress... *)
    destruct (progress g i gmid rho Hhead Hm) as [rho1 Hstep].
    (* ...and preserves the models relation *)
    pose proof (preservation g i gmid rho rho1 Hhead Hm Hstep) as Hm1.
    (* recurse on the tail *)
    destruct (IH gmid g' rho1 Htail Hm1) as [rhoF [Hrun HmF]].
    exists rhoF. split.
    + simpl. rewrite Hstep. exact Hrun.
    + exact HmF.
Qed.

(* Corollary, stated as the headline "well-typed => can't get stuck": a
   well-typed sequence from a modeling state always produces SOME final
   state (run is never None). *)
Corollary no_stuck : forall is g g' rho,
  seq_type g is g' ->
  models rho g ->
  run rho is <> None.
Proof.
  intros is g g' rho Hseq Hm.
  destruct (seq_safety is g g' rho Hseq Hm) as [rho' [Hrun _]].
  rewrite Hrun. discriminate.
Qed.

(* A convenient entry point: a sequence well-typed from the EMPTY abstract
   context (no inputs declared) cannot read an uninitialized register,
   matching a verified EASM block whose only established registers are the
   ones it writes itself. *)
Corollary no_stuck_from_empty : forall is g',
  seq_type aempty is g' ->
  run (mkrf (fun _ => None) None) is <> None.
Proof.
  intros is g' Hseq.
  apply (no_stuck is aempty g').
  - exact Hseq.
  - split.
    + intros r Hr. unfold aempty, aget in Hr. simpl in Hr. discriminate.
    + intros Hf. unfold aempty in Hf. simpl in Hf. discriminate.
Qed.

(* ================================================================== *)
(*  DATAFLOW JOIN AT CONTROL-FLOW MERGES                              *)
(*                                                                    *)
(*  Mechanizes the soundness of checkMergeConsistency                 *)
(*  (compiler/src/easm/easm_oprules.go): at a label reached from      *)
(*  several predecessors, the verifier types the continuation under   *)
(*  the MEET (pointwise intersection) of the predecessor abstract     *)
(*  states. A register the continuation reads must therefore be       *)
(*  Defined in the meet -- i.e. established on EVERY incoming edge     *)
(*  (the "merge-state-unsound" diagnostic fires otherwise). The       *)
(*  theorem below shows this is sound: whichever predecessor the      *)
(*  concrete machine actually arrived from, the continuation runs     *)
(*  without getting stuck.                                            *)
(* ================================================================== *)

(* The meet of two abstract states. The meet extends POINTWISE to BOTH lattice
   slots: a register is Defined only if Defined on BOTH predecessors, and the
   flags slot is Defined only if Defined on BOTH. This mirrors the verifier's
   per-fact meet at merges (a fact survives only if established on every edge). *)
Definition ameet (g1 g2 : absstate) : absstate :=
  mkabs (fun r => andb (aregs g1 r) (aregs g2 r)) (andb (aflags g1) (aflags g2)).

(* The register meet is a lower bound of each predecessor. *)
Lemma ameet_lb_l : forall g1 g2 r, aget (ameet g1 g2) r = true -> aget g1 r = true.
Proof. intros g1 g2 r H. unfold ameet, aget in *. simpl in H. destruct (aregs g1 r); simpl in H; [reflexivity | discriminate]. Qed.

Lemma ameet_lb_r : forall g1 g2 r, aget (ameet g1 g2) r = true -> aget g2 r = true.
Proof. intros g1 g2 r H. unfold ameet, aget in *. simpl in H. destruct (aregs g1 r); simpl in H; [exact H | discriminate]. Qed.

(* The flags meet is a lower bound of each predecessor's flags slot too. *)
Lemma ameet_flags_lb_l : forall g1 g2, aflags (ameet g1 g2) = true -> aflags g1 = true.
Proof. intros g1 g2 H. unfold ameet in H. simpl in H. destruct (aflags g1); simpl in H; [reflexivity | discriminate]. Qed.

Lemma ameet_flags_lb_r : forall g1 g2, aflags (ameet g1 g2) = true -> aflags g2 = true.
Proof. intros g1 g2 H. unfold ameet in H. simpl in H. destruct (aflags g1); simpl in H; [exact H | discriminate]. Qed.

(* checkMergeConsistency's core fact: a register the post-merge code DEMANDS (reads, hence must be
   Defined in the meet) is established on EVERY predecessor edge -- not just the linear fall-through. *)
Corollary meet_demanded_on_all_preds : forall g1 g2 r,
  aget (ameet g1 g2) r = true -> aget g1 r = true /\ aget g2 r = true.
Proof. intros g1 g2 r H. split; [apply (ameet_lb_l g1 g2 r H) | apply (ameet_lb_r g1 g2 r H)]. Qed.

(* The same fact for flags: flags demanded after a merge are established on every edge. *)
Corollary meet_flags_demanded_on_all_preds : forall g1 g2,
  aflags (ameet g1 g2) = true -> aflags g1 = true /\ aflags g2 = true.
Proof. intros g1 g2 H. split; [apply (ameet_flags_lb_l g1 g2 H) | apply (ameet_flags_lb_r g1 g2 H)]. Qed.

(* The meet is the GREATEST lower bound (registers AND flags): any state below both predecessors is
   below the meet. So the verifier loses no information it could soundly keep. *)
Lemma ameet_glb : forall g g1 g2,
  (forall r, aget g r = true -> aget g1 r = true) ->
  (forall r, aget g r = true -> aget g2 r = true) ->
  (forall r, aget g r = true -> aget (ameet g1 g2) r = true).
Proof.
  intros g g1 g2 H1 H2 r Hr. unfold ameet, aget in *. simpl.
  rewrite (H1 r Hr). rewrite (H2 r Hr). reflexivity.
Qed.

Lemma ameet_flags_glb : forall g g1 g2,
  (aflags g = true -> aflags g1 = true) ->
  (aflags g = true -> aflags g2 = true) ->
  (aflags g = true -> aflags (ameet g1 g2) = true).
Proof.
  intros g g1 g2 H1 H2 Hr. unfold ameet. simpl.
  rewrite (H1 Hr). rewrite (H2 Hr). reflexivity.
Qed.

(* A concrete state modeling either predecessor models the meet (fewer obligations on BOTH slots). *)
Lemma models_meet_l : forall rho g1 g2, models rho g1 -> models rho (ameet g1 g2).
Proof.
  intros rho g1 g2 [Hr Hf]. split.
  - intros r H. apply Hr. apply (ameet_lb_l g1 g2 r H).
  - intros H. apply Hf. apply (ameet_flags_lb_l g1 g2 H).
Qed.

Lemma models_meet_r : forall rho g1 g2, models rho g2 -> models rho (ameet g1 g2).
Proof.
  intros rho g1 g2 [Hr Hf]. split.
  - intros r H. apply Hr. apply (ameet_lb_r g1 g2 r H).
  - intros H. apply Hf. apply (ameet_flags_lb_r g1 g2 H).
Qed.

(* MERGE SOUNDNESS: if the post-merge continuation is well-typed under the meet of the predecessor
   states, then from a concrete machine that arrived via EITHER predecessor it runs to completion
   without getting stuck and ends in a state modeling the final context. This is exactly why typing
   the continuation against the meet (and rejecting reads of registers not in the meet) is sound. *)
Theorem merge_soundness : forall is g1 g2 g' rho,
  seq_type (ameet g1 g2) is g' ->
  (models rho g1 \/ models rho g2) ->
  exists rho', run rho is = Some rho' /\ models rho' g'.
Proof.
  intros is g1 g2 g' rho Hseq [H1 | H2].
  - apply (seq_safety is (ameet g1 g2) g' rho Hseq). apply models_meet_l. exact H1.
  - apply (seq_safety is (ameet g1 g2) g' rho Hseq). apply models_meet_r. exact H2.
Qed.
