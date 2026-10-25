import unittest
from ir_bijection import verify as strict_verify, classify, Rename, NotEquivalent, UnsupportedIR


def verify(left, right):
    # Synthetic fixtures explicitly authorize their paired symbol occurrences.
    # This helper is test-only, never a provenance policy for real compiler IR.
    a, b = classify(left), classify(right)
    entries = {(x.kind, x.scope, x.name, y.name) for x, y in zip(a, b)
               if x.kind != "text" and x.raw != y.raw}
    return strict_verify(left, right, [Rename(*entry, "synthetic fixture binding") for entry in entries])

BASE = rb'''%Packet = type { i32 }
@__global_1 = internal global i32 3
; @__global_1 in a comment is immutable text
@literal = private constant [11 x i8] c"__global_1\00"
define i32 @main(i32 %__arg_1) {
__block_1:
  %__temp_1 = add i32 %__arg_1, 1
  %__temp_2 = add i32 %__temp_1, 2
  br label %__block_2
__block_2:
  ret i32 %__temp_2
}
'''

class BijectionTests(unittest.TestCase):
    def assertDifferent(self, a, b):
        with self.assertRaises(NotEquivalent):
            verify(a, b)

    def test_renames_deny_by_default(self):
        with self.assertRaises(NotEquivalent):
            strict_verify(BASE, BASE.replace(b"%__temp_1", b"%__temp_9"))

    def test_manifest_cannot_authorize_external_abi_rename(self):
        left = b"@__global_1 = global i32 0\n"
        right = b"@__global_9 = global i32 0\n"
        with self.assertRaises(UnsupportedIR):
            verify(left, right)

    def test_unused_manifest_entry_rejected(self):
        with self.assertRaises(UnsupportedIR):
            strict_verify(BASE, BASE, [Rename("local", 0, b"__temp_1", b"__temp_9", "fixture")])

    def test_identical(self):
        verify(BASE, BASE)

    def test_consistent_scoped_renaming(self):
        right = BASE.replace(b"%__temp_1", b"%__temp_91").replace(b"%__temp_2", b"%__temp_92").replace(b"__block_1:", b"__block_81:").replace(b"__block_2:", b"__block_82:").replace(b"%__block_2", b"%__block_82")
        verify(BASE, right)

    def test_collapse_rejected(self):
        self.assertDifferent(BASE, BASE.replace(b"%__temp_2", b"%__temp_1"))

    def test_binding_change_rejected(self):
        self.assertDifferent(BASE, BASE.replace(b"ret i32 %__temp_2", b"ret i32 %__temp_1"))

    def test_unchanged_identifiers_participate_in_bijection(self):
        self.assertDifferent(BASE, BASE.replace(b"%__temp_1", b"%__temp_2"))

    def test_literal_and_comment_are_exact(self):
        self.assertDifferent(BASE, BASE.replace(b"; @__global_1", b"; @__global_2"))
        self.assertDifferent(BASE, BASE.replace(b'c"__global_1', b'c"__global_2'))

    def test_other_text_exact(self):
        self.assertDifferent(BASE, BASE.replace(b"i32 3", b"i32 4"))
        self.assertDifferent(BASE, BASE.replace(b" = add", b"  = add"))
        self.assertDifferent(BASE, BASE.replace(b"@main", b"@other"))
        self.assertDifferent(BASE, BASE.replace(b"%__temp_1", b"%__other_91"))

    def test_global_and_type_namespaces_are_distinct(self):
        left = b"%__type_1 = type { i32 }\n@__type_1 = internal global %__type_1 zeroinitializer\n"
        right = b"%__type_8 = type { i32 }\n@__type_9 = internal global %__type_8 zeroinitializer\n"
        verify(left, right)

    def test_local_namespaces_are_per_function(self):
        left = b"define i32 @a(i32 %__arg_1) {\n  ret i32 %__arg_1\n}\ndefine i32 @b(i32 %__arg_1) {\n  ret i32 %__arg_1\n}\n"
        right = left.replace(b"%__arg_1", b"%__arg_8", 2).replace(b"%__arg_1", b"%__arg_9")
        verify(left, right)

    def test_globals_are_not_per_function(self):
        left = b"@__global_1 = internal global i32 0\ndefine ptr @a() {\n  ret ptr @__global_1\n}\ndefine ptr @b() {\n  ret ptr @__global_1\n}\n"
        self.assertDifferent(left, left.replace(b"@__global_1", b"@__global_8", 2))

    def test_quoted_symbols_and_escapes(self):
        left = b'define internal i32 @"__fn_1"(i32 %"__arg_1") {\n"__block_1":\n  ret i32 %"__arg_1"\n}\n'
        right = left.replace(b"__fn_1", b"__fn_8").replace(b"__arg_1", b"__arg_9").replace(b"__block_1", b"__block_7")
        verify(left, right)
        # LLVM hex escapes decode to the same symbol identity for binding checks.
        escaped = right.replace(b'@"__fn_8"', b'@"__fn_\\38"')
        verify(left, escaped)
        self.assertDifferent(left, right.replace(b'%"__arg_9"\n', b'%"__arg_8"\n'))

    def test_location_name_family(self):
        verify(b"@__L1_C2 = internal global i32 0\n", b"@__L10_C20 = internal global i32 0\n")
        self.assertDifferent(b"@prefix__L1_C2 = global i32 0\n", b"@prefix__L10_C20 = global i32 0\n")

    def test_different_function_binding_rejected(self):
        left = b"define i32 @__fn_1() {\n  ret i32 1\n}\ndefine i32 @__fn_2() {\n  %__temp_1 = call i32 @__fn_1()\n  ret i32 %__temp_1\n}\n"
        right = left.replace(b"call i32 @__fn_1", b"call i32 @__fn_2")
        self.assertDifferent(left, right)

    def test_supported_named_type_in_body(self):
        left = b"%__type_1 = type { i32 }\ndefine i32 @a(ptr %__arg_1) {\n  %__temp_1 = getelementptr %__type_1, ptr %__arg_1, i32 0, i32 0\n  ret i32 0\n}\n"
        verify(left, left.replace(b"%__type_1", b"%__type_2"))

    def test_fail_closed_syntax(self):
        examples = [b"define i32 @f(\ni32 %a) {\nret i32 %a\n}\n",
                    b"@x = global ptr blockaddress(@f, %bb)\n",
                    b'%T = type { i32 }\ndefine void @f(%T %T) {\n ret void\n}\n',
                    b'@x = global [2 x i8] c"\\q"\n',
                    b'define i32 @f() {\n ret i32 %__missing_1\n}\n']
        for data in examples:
            with self.subTest(data=data), self.assertRaises(UnsupportedIR):
                verify(data, data)

if __name__ == "__main__":
    unittest.main()
