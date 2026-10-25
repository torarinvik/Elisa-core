import unittest
import io
import json
import tempfile
from pathlib import Path
from ir_bijection import verify_streams, load_manifest, verify_checkpoint, checkpoint_digest
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



class StreamingBijectionTests(unittest.TestCase):
    def streams(self, left, right, entries=None):
        return verify_streams(io.BytesIO(left), io.BytesIO(right), entries)

    def test_stream_matches_legacy_on_fixture(self):
        right = BASE.replace(b"%__temp_1", b"%__temp_91")
        entry = Rename("local", 0, b"__temp_1", b"__temp_91", "synthetic test producer")
        self.assertEqual(self.streams(BASE, right, [entry]), {k:v for k,v in strict_verify(BASE, right, [entry]).items() if k[2] != v})

    def test_stack_suffix_preserved_and_producer_required(self):
        left = b'define i32 @f() {\nentry:\n  %"__auto_12#2" = add i32 1, 2\n  ret i32 %"__auto_12#2"\n}\n'
        right = left.replace(b"__auto_12", b"__auto_23")
        entry = Rename("local", 0, b"__auto_12#2", b"__auto_23#2", "synthetic region-extra-stack fixture", "region-extra-stack")
        self.streams(left, right, [entry])
        with self.assertRaises(UnsupportedIR):
            self.streams(left, right, [Rename("local", 0, entry.left, entry.right, "no producer")])
        with self.assertRaises(NotEquivalent):
            changed = right.replace(b"#2", b"#3")
            self.streams(left, changed, [Rename("local", 0, entry.left, b"__auto_23#3", "changed stack fixture", "region-extra-stack")])

    def test_unchanged_collision_and_changed_reference(self):
        left = b'define i32 @f() {\nentry:\n  %__auto_1 = add i32 1, 2\n  %__auto_2 = add i32 %__auto_1, 3\n  ret i32 %__auto_2\n}\n'
        right = left.replace(b"__auto_1", b"__auto_2")
        with self.assertRaises(NotEquivalent):
            self.streams(left, right, [Rename("local", 0, b"__auto_1", b"__auto_2", "collision fixture", "auto-region")])

    def test_scopes_include_nameless_functions_and_declarations(self):
        prefix = b'declare void @external()\ndefine void @empty() {\nentry:\n  ret void\n}\n'
        left = prefix + b'define i32 @f() {\nentry:\n  %__quantifier_12 = add i32 1, 2\n  ret i32 %__quantifier_12\n}\n'
        right = left.replace(b"__quantifier_12", b"__quantifier_23")
        self.streams(left, right, [Rename("local", 2, b"__quantifier_12", b"__quantifier_23", "quantifier fixture", "quantifier")])
        with self.assertRaises(NotEquivalent):
            self.streams(left, right, [Rename("local", 1, b"__quantifier_12", b"__quantifier_23", "wrong owner", "quantifier")])

    def test_module_binding_collision_across_functions(self):
        left = b'@__global_1 = internal global i32 0\n@__global_2 = internal global i32 0\ndefine i32 @f() {\nentry:\n  %a = load i32, ptr @__global_1\n  ret i32 %a\n}\ndefine ptr @g() {\nentry:\n  ret ptr @__global_2\n}\n'
        right = left.replace(b"__global_1", b"__global_2")
        with self.assertRaises(NotEquivalent):
            self.streams(left, right, [Rename("global", -1, b"__global_1", b"__global_2", "collision fixture")])

    def test_external_abi_and_nonidentifier_bytes_exact(self):
        with self.assertRaises(UnsupportedIR):
            self.streams(b"@__global_1 = global i32 0\n", b"@__global_2 = global i32 0\n", [Rename("global", -1, b"__global_1", b"__global_2", "external fixture")])
        for right in [BASE.replace(b"i32 3", b"i32 4"), BASE.replace(b"in a comment", b"in a changed comment"), BASE.replace(b"  ret", b" ret")]:
            with self.assertRaises(NotEquivalent): self.streams(BASE, right)

    def test_forward_named_types_and_unsupported_syntax(self):
        module = b'define %Packet @f(%Packet %value) {\nentry:\n  ret %Packet %value\n}\n%Packet = type { i32 }\n'
        self.streams(module, module)
        for bad in [b'@x = global ptr blockaddress(@f, %entry)\n', b'define void @f() {\n', b'\x00']:
            with self.assertRaises(UnsupportedIR): self.streams(bad, bad)

    def test_version2_manifest_roundtrip_and_unused(self):
        row = {"namespace":"local", "scope":0, "left":"__auto_12#2", "right":"__auto_23#2", "evidence":"synthetic reviewed stack site", "producer":"region-extra-stack"}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)/"manifest.json"
            path.write_text(json.dumps({"version":2, "renames":[row]}))
            entries = load_manifest(path)
        with self.assertRaises(UnsupportedIR): self.streams(BASE, BASE, entries)

    def test_version2_manifest_rejects_empty_producer(self):
        row = {"namespace":"local", "scope":0, "left":"__auto_12", "right":"__auto_23", "evidence":"reviewed site", "producer":""}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)/"manifest.json"
            for producer in ("", " ", None, 42):
                row["producer"] = producer
                path.write_text(json.dumps({"version":2, "renames":[row]}))
                with self.subTest(producer=producer), self.assertRaises(UnsupportedIR):
                    load_manifest(path)

    def test_llvm_escaped_backslash_is_exact(self):
        from ir_bijection import decode_name
        literal = b'@x = private constant [2 x i8] c"\\\\\\00"\n'
        self.streams(literal, literal)
        with self.assertRaises(NotEquivalent):
            self.streams(literal, literal.replace(b'\\\\', b'\\5C', 1))
        self.assertEqual(decode_name(b'"slash\\\\name"'), b'slash\\name')
        self.assertEqual(decode_name(b'"slash\\5Cname"'), b'slash\\name')

    def test_never_reads_whole_stream(self):
        class Bounded(io.BytesIO):
            def read(self, *args): raise AssertionError("whole-stream read")
            def readlines(self, *args): raise AssertionError("whole-stream readlines")
        verify_streams(Bounded(BASE), Bounded(BASE))



class HeaderTypeRoleTests(unittest.TestCase):
    def same(self, text):
        strict_verify(text, text)
        verify_streams(io.BytesIO(text), io.BytesIO(text))

    def test_actual_stage1_sret_header_shape(self):
        self.same(b'%Parser.Parser = type { ptr }\ndefine void @constructor(ptr sret(%Parser.Parser) %0, ptr %1) #1 {\nentry:\n  ret void\n}\nattributes #1 = { nounwind }\n')

    def test_aggregate_return_and_typed_attributes(self):
        self.same(b'%StringView = type { ptr, i64 }\ndefine { %StringView, i32 } @f(ptr byval(%StringView) align 8 %arg) {\nentry:\n  ret { %StringView, i32 } zeroinitializer\n}\n')
        self.same(b'%Packet = type { i32 }\ndeclare void @f(%Packet, ptr sret(%Packet), ptr byref(%Packet), ...)\n')

    def test_complete_module_type_index_can_be_read_only(self):
        module = b"%Packet = type { i32 }\ndeclare void @f(%Packet)\n"
        index = frozenset({b"Packet"})
        self.assertEqual(classify(module, index), classify(module))
        self.assertEqual(index, frozenset({b"Packet"}))
        with self.assertRaises(UnsupportedIR):
            classify(module, frozenset())

    def test_unnamed_named_type_formal_is_a_type_not_a_local(self):
        tokens = classify(b'%Packet = type { i32 }\ndeclare void @f(%Packet)\n')
        self.assertEqual([t.kind for t in tokens if t.name == b'Packet'], ['type', 'type'])

    def test_real_local_type_name_collision_stays_unsupported(self):
        text = b'%Packet = type { i32 }\ndeclare void @f(%Packet %Packet)\n'
        with self.assertRaises(UnsupportedIR): strict_verify(text, text)
        with self.assertRaises(UnsupportedIR): verify_streams(io.BytesIO(text), io.BytesIO(text))

    def test_unknown_percent_attribute_context_stays_unsupported(self):
        text = b'%Packet = type { i32 }\ndeclare void @f(ptr unknown(%Packet) %arg)\n'
        with self.assertRaises(UnsupportedIR): strict_verify(text, text)

    def test_return_type_and_sret_types_remain_exact(self):
        left = b'%Packet = type { i32 }\n%Other = type { i64 }\ndeclare void @f(ptr sret(%Packet))\n'
        right = left.replace(b'sret(%Packet)', b'sret(%Other)')
        with self.assertRaises(NotEquivalent): strict_verify(left, right)
        with self.assertRaises(NotEquivalent): verify_streams(io.BytesIO(left), io.BytesIO(right))

    def test_generated_parameter_binding_still_needs_bijection(self):
        left = b'%Packet = type { i32 }\ndefine void @f(ptr sret(%Packet) %__arg_1, i32 %__arg_2) {\nentry:\n  %v = add i32 %__arg_2, 0\n  ret void\n}\n'
        right = left.replace(b'%__arg_1', b'%__arg_2')
        entry = Rename('local',0,b'__arg_1',b'__arg_2','synthetic collision fixture')
        with self.assertRaises(NotEquivalent): verify_streams(io.BytesIO(left), io.BytesIO(right),[entry])



class CheckpointBijectionTests(unittest.TestCase):
    def fixture(self, directory, left=BASE, right=BASE):
        a,b,c = (Path(directory)/name for name in ("left.ll","right.ll","proof.json"))
        a.write_bytes(left);b.write_bytes(right)
        return a,b,c

    def rewrite(self, checkpoint, edit):
        outer=json.loads(checkpoint.read_text())
        edit(outer["payload"])
        outer["sha256"]=checkpoint_digest(outer["payload"])
        checkpoint.write_text(json.dumps(outer))

    def test_resume_complete_units_matches_uninterrupted(self):
        right=BASE.replace(b"%__temp_1",b"%__temp_91")
        entries=[Rename("local",0,b"__temp_1",b"__temp_91","checkpoint fixture")]
        expected=verify_streams(io.BytesIO(BASE),io.BytesIO(right),entries)
        with tempfile.TemporaryDirectory() as directory:
            a,b,c=self.fixture(directory,BASE,right)
            incomplete=0
            for attempt in range(20):
                got=verify_checkpoint(a,b,c,entries,max_units=1)
                state=json.loads(c.read_text())["payload"]
                if got is not None:
                    self.assertTrue(state["complete"])
                    self.assertEqual((state["left_offset"],state["right_offset"]),(len(BASE),len(right)))
                    self.assertEqual(got,expected)
                    self.assertEqual(verify_checkpoint(a,b,c,entries,max_units=1),expected)
                    break
                incomplete+=1
                self.assertFalse(state["complete"])
            else:self.fail("resume never reached checked EOF")
            self.assertGreater(incomplete,1)

    def test_module_binding_collision_survives_resume(self):
        left=b'@__global_1 = internal global i32 1\n@__global_2 = internal global i32 2\ndefine i32 @f() {\nentry:\n  %v = load i32, ptr @__global_1\n  ret i32 %v\n}\n'
        right=left.replace(b'ptr @__global_1',b'ptr @__global_2')
        entries=[Rename("global",-1,b"__global_1",b"__global_2","reference redirection fixture")]
        with tempfile.TemporaryDirectory() as directory:
            a,b,c=self.fixture(directory,left,right)
            self.assertIsNone(verify_checkpoint(a,b,c,entries,max_units=2))
            with self.assertRaises(NotEquivalent):verify_checkpoint(a,b,c,entries,max_units=1)

    def test_corrupt_checkpoint_and_changed_input_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            a,b,c=self.fixture(directory)
            self.assertIsNone(verify_checkpoint(a,b,c,max_units=1))
            saved=c.read_bytes()
            outer=json.loads(saved);outer["sha256"]="0"*64;c.write_text(json.dumps(outer))
            with self.assertRaises(UnsupportedIR):verify_checkpoint(a,b,c,max_units=1)
            c.write_bytes(saved)
            b.write_bytes(BASE.replace(b'i32 3',b'i32 4'))
            with self.assertRaises(UnsupportedIR):verify_checkpoint(a,b,c,max_units=1)

    def test_rekeyed_manifest_and_checker_identity_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            a,b,c=self.fixture(directory)
            self.assertIsNone(verify_checkpoint(a,b,c,max_units=1))
            saved=c.read_bytes()
            self.rewrite(c,lambda p:p["identity"].__setitem__("checker_sha256","0"*64))
            with self.assertRaises(UnsupportedIR):verify_checkpoint(a,b,c,max_units=1)
            c.write_bytes(saved)
            extra=[Rename("local",0,b"__temp_1",b"__temp_99","different manifest")]
            with self.assertRaises(UnsupportedIR):verify_checkpoint(a,b,c,extra,max_units=1)

    def test_midfunction_cursor_or_binding_table_tampering_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            a,b,c=self.fixture(directory)
            self.assertIsNone(verify_checkpoint(a,b,c,max_units=1))
            saved=c.read_bytes()
            offset=BASE.index(b'  %__temp_1')
            def mid(p):
                p["left_offset"]=offset;p["right_offset"]=offset;p["scope"]=1;p["line"]=BASE[:offset].count(b'\n')+1
            self.rewrite(c,mid)
            with self.assertRaises(UnsupportedIR):verify_checkpoint(a,b,c,max_units=1)
            c.write_bytes(saved)
            self.rewrite(c,lambda p:p["forward"].append(["global",-1,b"extra".hex(),b"other".hex()]))
            with self.assertRaises(UnsupportedIR):verify_checkpoint(a,b,c,max_units=1)

    def test_unused_permission_checked_only_at_complete_eof(self):
        entries=[Rename("local",99,b"__temp_1",b"__temp_2","unused fixture")]
        with tempfile.TemporaryDirectory() as directory:
            a,b,c=self.fixture(directory)
            self.assertIsNone(verify_checkpoint(a,b,c,entries,max_units=1))
            with self.assertRaises(UnsupportedIR):
                for attempt in range(20):
                    result=verify_checkpoint(a,b,c,entries,max_units=1)
                    self.assertIsNone(result)
            self.assertFalse(json.loads(c.read_text())["payload"]["complete"])

    def test_work_budget_excludes_mandatory_startup(self):
        from unittest import mock
        import ir_bijection as checker
        clock = [0.0]
        index, unit = checker.module_index, checker.verify_unit
        def slow_index(stream):
            clock[0] += 10
            return index(stream)
        def work(*args):
            clock[0] += 0.1
            return unit(*args)
        with tempfile.TemporaryDirectory() as directory:
            a, b, c = self.fixture(directory)
            with mock.patch.object(checker.time, "monotonic", side_effect=lambda: clock[0]), mock.patch.object(checker, "module_index", side_effect=slow_index), mock.patch.object(checker, "verify_unit", side_effect=work):
                self.assertIsNone(verify_checkpoint(a, b, c, budget_seconds=1, max_units=2))
            self.assertEqual(json.loads(c.read_text())["payload"]["units"], 2)

    def test_retargeted_input_symlink_is_rejected(self):
        from unittest import mock
        import ir_bijection as checker
        with tempfile.TemporaryDirectory() as directory:
            a, b, c = self.fixture(directory)
            old, new = b.with_name("original.ll"), b.with_name("replacement.ll")
            b.rename(old)
            new.write_bytes(BASE)
            try:
                b.symlink_to(old)
            except OSError:
                self.skipTest("symlinks unavailable")
            unit = checker.verify_unit
            def retarget(*args):
                result = unit(*args)
                b.unlink()
                b.symlink_to(new)
                return result
            with mock.patch.object(checker, "verify_unit", side_effect=retarget), self.assertRaises(UnsupportedIR):
                verify_checkpoint(a, b, c, max_units=1)
            self.assertFalse(c.exists())

    def test_identical_unsupported_unit_is_not_skipped_on_resume(self):
        invalid=b'; validated first unit\n@p = global ptr blockaddress(@f, %entry)\n'
        with tempfile.TemporaryDirectory() as directory:
            a,b,c=self.fixture(directory,invalid,invalid)
            self.assertIsNone(verify_checkpoint(a,b,c,max_units=1))
            with self.assertRaises(UnsupportedIR):verify_checkpoint(a,b,c,max_units=1)

if __name__ == "__main__":
    unittest.main()
