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
(*  All of add/sub/and/xor clobber flags in the real ISA; flags are    *)
(*  not part of the definite-init lattice we formalize, so they are    *)
(*  elided (see README "What is not covered").                         *)
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
(*  Abstract state Gamma : reg -> definedness                          *)
(*                                                                     *)
(*  Matches machineFactState.LiveRegs (a finite map reg -> bool, true  *)
(*  meaning "established/Defined"). We represent it as a total          *)
(*  function reg -> bool; absence in the Go map = Undefined = false.    *)
(* ------------------------------------------------------------------ *)

Definition absstate := reg -> bool.

Definition aempty : absstate := fun _ => false.

Definition aset (g : absstate) (r : reg) (b : bool) : absstate :=
  fun r' => if reg_eqb r r' then b else g r'.

Definition adefine (g : absstate) (r : reg) : absstate := aset g r true.

Lemma aset_same : forall g r b, aset g r b r = b.
Proof. intros. unfold aset. rewrite reg_eqb_refl. reflexivity. Qed.

Lemma aset_other : forall g r b r', r <> r' -> aset g r b r' = g r'.
Proof.
  intros g r b r' H. unfold aset.
  destruct (reg_eqb r r') eqn:E.
  - apply reg_eqb_true in E. contradiction.
  - reflexivity.
Qed.

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
  | OReg r => g r
  | OImm _ => true
  end.

Inductive has_type : absstate -> instr -> absstate -> Prop :=
  | T_mov : forall g d s,
      op_ok g s = true ->
      has_type g (Imov d s) (adefine g d)
  | T_add : forall g d s,
      g d = true -> op_ok g s = true ->
      has_type g (Iadd d s) (adefine g d)
  | T_sub : forall g d s,
      g d = true -> op_ok g s = true ->
      has_type g (Isub d s) (adefine g d)
  | T_and : forall g d s,
      g d = true -> op_ok g s = true ->
      has_type g (Iand d s) (adefine g d)
  | T_xor : forall g d s,
      g d = true -> op_ok g s = true ->
      has_type g (Ixor d s) (adefine g d)
  | T_inc : forall g d,
      g d = true ->
      has_type g (Iinc d) (adefine g d)
  | T_dec : forall g d,
      g d = true ->
      has_type g (Idec d) (adefine g d).

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

Definition rfile := reg -> option nat.

Definition rfset (rho : rfile) (r : reg) (v : nat) : rfile :=
  fun r' => if reg_eqb r r' then Some v else rho r'.

Lemma rfset_same : forall rho r v, rfset rho r v r = Some v.
Proof. intros. unfold rfset. rewrite reg_eqb_refl. reflexivity. Qed.

Lemma rfset_other : forall rho r v r', r <> r' -> rfset rho r v r' = rho r'.
Proof.
  intros rho r v r' H. unfold rfset.
  destruct (reg_eqb r r') eqn:E.
  - apply reg_eqb_true in E. contradiction.
  - reflexivity.
Qed.

(* Value of an operand in a concrete machine: None if it reads an
   undefined register (a STUCK read). *)
Definition oval (rho : rfile) (o : operand) : option nat :=
  match o with
  | OReg r => rho r
  | OImm n => Some n
  end.

(* Big-step evaluation: step rho i = Some rho' on success, None if the
   instruction reads an undefined register (gets stuck). This is the
   concrete counterpart of "ill-typed = stuck". *)
Definition alu (f : nat -> nat -> nat) (rho : rfile) (d : reg) (o : operand)
  : option rfile :=
  match rho d, oval rho o with
  | Some a, Some b => Some (rfset rho d (f a b))
  | _, _ => None
  end.

Definition step (rho : rfile) (i : instr) : option rfile :=
  match i with
  | Imov d s =>
      match oval rho s with
      | Some v => Some (rfset rho d v)
      | None => None
      end
  | Iadd d s => alu Nat.add rho d s
  | Isub d s => alu Nat.sub rho d s
  | Iand d s => alu (fun a b => a) rho d s   (* concrete bit-op shape irrelevant to safety *)
  | Ixor d s => alu (fun a b => a) rho d s
  | Iinc d   => match rho d with Some a => Some (rfset rho d (S a)) | None => None end
  | Idec d   => match rho d with Some a => Some (rfset rho d (Nat.pred a)) | None => None end
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

Definition models (rho : rfile) (g : absstate) : Prop :=
  forall r, g r = true -> exists v, rho r = Some v.

(* operand soundness: if op_ok abstractly, it has a concrete value *)
Lemma op_ok_models : forall rho g o,
  models rho g -> op_ok g o = true -> exists v, oval rho o = Some v.
Proof.
  intros rho g o Hm Hok. destruct o as [r | n]; simpl in *.
  - apply Hm. exact Hok.
  - exists n. reflexivity.
Qed.

(* defining a register preserves the models relation *)
Lemma models_define : forall rho g d v,
  models rho g -> models (rfset rho d v) (adefine g d).
Proof.
  intros rho g d v Hm r Hr. unfold adefine, aset in Hr.
  destruct (reg_eqb d r) eqn:E.
  - apply reg_eqb_true in E. subst. exists v. apply rfset_same.
  - apply reg_eqb_false in E.
    destruct (Hm r Hr) as [w Hw]. exists w. rewrite rfset_other; auto.
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
    destruct (Hm d H) as [a Ha]. rewrite Ha.
    destruct (op_ok_models rho g s Hm H0) as [b Hb]. rewrite Hb. eauto.
  - (* sub *) unfold alu.
    destruct (Hm d H) as [a Ha]. rewrite Ha.
    destruct (op_ok_models rho g s Hm H0) as [b Hb]. rewrite Hb. eauto.
  - (* and *) unfold alu.
    destruct (Hm d H) as [a Ha]. rewrite Ha.
    destruct (op_ok_models rho g s Hm H0) as [b Hb]. rewrite Hb. eauto.
  - (* xor *) unfold alu.
    destruct (Hm d H) as [a Ha]. rewrite Ha.
    destruct (op_ok_models rho g s Hm H0) as [b Hb]. rewrite Hb. eauto.
  - (* inc *) destruct (Hm d H) as [a Ha]. rewrite Ha. eauto.
  - (* dec *) destruct (Hm d H) as [a Ha]. rewrite Ha. eauto.
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
    destruct (rho d) as [a|] eqn:Ea; [|discriminate].
    destruct (oval rho s) as [b|] eqn:Eb; [|discriminate].
    inversion Hstep; subst. apply models_define; auto.
  - (* sub *) unfold alu in Hstep.
    destruct (rho d) as [a|] eqn:Ea; [|discriminate].
    destruct (oval rho s) as [b|] eqn:Eb; [|discriminate].
    inversion Hstep; subst. apply models_define; auto.
  - (* and *) unfold alu in Hstep.
    destruct (rho d) as [a|] eqn:Ea; [|discriminate].
    destruct (oval rho s) as [b|] eqn:Eb; [|discriminate].
    inversion Hstep; subst. apply models_define; auto.
  - (* xor *) unfold alu in Hstep.
    destruct (rho d) as [a|] eqn:Ea; [|discriminate].
    destruct (oval rho s) as [b|] eqn:Eb; [|discriminate].
    inversion Hstep; subst. apply models_define; auto.
  - (* inc *)
    destruct (rho d) as [a|] eqn:Ea; [|discriminate].
    inversion Hstep; subst. apply models_define; auto.
  - (* dec *)
    destruct (rho d) as [a|] eqn:Ea; [|discriminate].
    inversion Hstep; subst. apply models_define; auto.
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
  run (fun _ => None) is <> None.
Proof.
  intros is g' Hseq.
  apply (no_stuck is aempty g').
  - exact Hseq.
  - intros r Hr. unfold aempty in Hr. discriminate.
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

(* The meet of two abstract states: a register is Defined only if Defined on BOTH. *)
Definition ameet (g1 g2 : absstate) : absstate := fun r => andb (g1 r) (g2 r).

(* The meet is a lower bound of each predecessor (Defined-in-meet => Defined-in-pred). *)
Lemma ameet_lb_l : forall g1 g2 r, ameet g1 g2 r = true -> g1 r = true.
Proof. intros g1 g2 r H. unfold ameet in H. destruct (g1 r); simpl in H; [reflexivity | discriminate]. Qed.

Lemma ameet_lb_r : forall g1 g2 r, ameet g1 g2 r = true -> g2 r = true.
Proof. intros g1 g2 r H. unfold ameet in H. destruct (g1 r); simpl in H; [exact H | discriminate]. Qed.

(* checkMergeConsistency's core fact: a register the post-merge code DEMANDS (reads, hence must be
   Defined in the meet) is established on EVERY predecessor edge -- not just the linear fall-through. *)
Corollary meet_demanded_on_all_preds : forall g1 g2 r,
  ameet g1 g2 r = true -> g1 r = true /\ g2 r = true.
Proof. intros g1 g2 r H. split; [apply (ameet_lb_l g1 g2 r H) | apply (ameet_lb_r g1 g2 r H)]. Qed.

(* The meet is the GREATEST lower bound: any state below both predecessors is below the meet. So the
   verifier loses no information it could soundly keep -- the join is as precise as soundness allows. *)
Lemma ameet_glb : forall g g1 g2,
  (forall r, g r = true -> g1 r = true) ->
  (forall r, g r = true -> g2 r = true) ->
  (forall r, g r = true -> ameet g1 g2 r = true).
Proof.
  intros g g1 g2 H1 H2 r Hr. unfold ameet.
  rewrite (H1 r Hr). rewrite (H2 r Hr). reflexivity.
Qed.

(* A concrete state modeling either predecessor models the meet (fewer obligations). *)
Lemma models_meet_l : forall rho g1 g2, models rho g1 -> models rho (ameet g1 g2).
Proof. intros rho g1 g2 H r Hr. apply H. apply (ameet_lb_l g1 g2 r Hr). Qed.

Lemma models_meet_r : forall rho g1 g2, models rho g2 -> models rho (ameet g1 g2).
Proof. intros rho g1 g2 H r Hr. apply H. apply (ameet_lb_r g1 g2 r Hr). Qed.

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
