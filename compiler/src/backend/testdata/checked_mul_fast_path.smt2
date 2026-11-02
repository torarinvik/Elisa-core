(set-logic QF_BV)
(set-option :timeout 5000)
(declare-fun a () (_ BitVec 64))
(declare-fun b () (_ BitVec 64))
(assert (bvule b #x0000000000000001))
; An overflowing signed product would differ from the sign extension of its low half.
(assert (not (= (bvmul ((_ sign_extend 64) a) ((_ sign_extend 64) b))
                ((_ sign_extend 64) (bvmul a b)))))
(check-sat)
