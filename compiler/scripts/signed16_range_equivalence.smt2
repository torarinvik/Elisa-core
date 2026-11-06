(set-logic QF_BV)
(declare-fun x () (_ BitVec 64))
(assert (not (= (bvult (bvadd x (_ bv18446744073709518848 64)) (_ bv18446744073709486080 64)) (bvugt (bvadd x (_ bv32768 64)) (_ bv65535 64)))))
(check-sat)
