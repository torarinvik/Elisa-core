"""Identity shortcut regression tests; differential reference uses full units.

No baseline checker is vendored: disable only the prepared identity shortcut
to exercise the existing verify_unit implementation through the same APIs.
"""
from contextlib import contextmanager
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import ir_bijection as checker


IDENTICAL = b'define i32 @f(i32 %x) {\nentry:\n  ret i32 %x\n}\n'


@contextmanager
def full_units():
    prepare = checker._prepare_identity_policy

    def disabled(*args):
        policy = prepare(*args)
        return checker._IdentityPolicy(False, policy.permitted_scopes)

    with patch.object(checker, '_prepare_identity_policy', side_effect=disabled):
        yield


def outcome(a, b, rows=()):
    entries = [checker.Rename(*row) for row in rows]
    try:
        return ('ok', checker.verify_streams(io.BytesIO(a), io.BytesIO(b), entries))
    except checker.NotEquivalent as error:
        return (type(error).__name__, str(error))


class IdentityUnitTests(unittest.TestCase):
    def assertDifferential(self, a, b, rows=()):
        optimized = outcome(a, b, rows)
        with full_units():
            reference = outcome(a, b, rows)
        self.assertEqual(reference, optimized)
        return optimized

    def test_identical_function_classifies_once(self):
        with patch.object(checker, 'classify', wraps=checker.classify) as classify:
            self.assertEqual({}, checker.verify_streams(io.BytesIO(IDENTICAL), io.BytesIO(IDENTICAL)))
            self.assertEqual(1, classify.call_count)
        self.assertDifferential(IDENTICAL, IDENTICAL)

    def test_direct_verify_unit_retains_full_path(self):
        with patch.object(checker, 'classify', wraps=checker.classify) as classify:
            self.assertEqual((1, 5), checker.verify_unit(IDENTICAL, IDENTICAL,
                set(), set(), set(), set(), 0, 1, {}, set(), {}, {}, {}))
            self.assertEqual(2, classify.call_count)

    def test_policy_prepared_once_for_many_functions(self):
        data = b''.join(IDENTICAL.replace(b'@f(', ('@f%d(' % i).encode()) for i in range(20))
        with patch.object(checker, '_prepare_identity_policy', wraps=checker._prepare_identity_policy) as prepare:
            self.assertEqual({}, checker.verify_streams(io.BytesIO(data), io.BytesIO(data)))
            self.assertEqual(1, prepare.call_count)
        self.assertDifferential(data, data)

    def test_permission_scope_does_not_skip_unused_entry(self):
        rows = [('local', 0, b'__auto_1', b'__auto_2', 'fixture', 'auto-region')]
        self.assertEqual('UnsupportedIR', self.assertDifferential(IDENTICAL, IDENTICAL, rows)[0])

    def test_changed_scope_target_collision(self):
        a = b'define i32 @f() {\n  %__auto_1 = add i32 1, 2\n  %__auto_2 = add i32 3, 4\n  ret i32 %__auto_1\n}\n'
        b = a.replace(b'__auto_1', b'__auto_2')
        rows = [('local', 0, b'__auto_1', b'__auto_2', 'fixture', 'auto-region')]
        # Deliberately invalid SSA fixture checks fail-closed API behavior.
        self.assertEqual('NotEquivalent', self.assertDifferential(a, b, rows)[0])

    def test_declared_function_consumes_ordinal(self):
        a = b'declare void @external()\n' + IDENTICAL
        a += b'define i32 @g() {\n  %__quantifier_1 = add i32 1, 2\n  ret i32 %__quantifier_1\n}\n'
        b = a.replace(b'__quantifier_1', b'__quantifier_2')
        rows = [('local', 2, b'__quantifier_1', b'__quantifier_2', 'fixture', 'quantifier')]
        self.assertEqual('ok', self.assertDifferential(a, b, rows)[0])

    def test_quoted_names_type_aliases_and_header_ambiguity(self):
        fixtures = [
            b'define i32 @"f\\20name"(i32 %"x\\20name") {\n  ret i32 %"x\\20name"\n}\n',
            b'%T = type { i32 }\ndefine %T @f(%T %x) {\n  ret %T %x\n}\n',
            b'%T = type { i32 }\ndefine i32 @f(i32 %T) {\n  ret i32 %T\n}\n',
            b'%T = type { i32 }\ndefine i32 @f() prefix %T zeroinitializer {\n  ret i32 0\n}\n',
            b'define void @f() {\n  %x = call ptr @g(ptr blockaddress(@f, %entry))\nentry:\n  ret void\n}\n',
            b'define i32 @f(i32 %"bad\\q") {\n  ret i32 0\n}\n',
        ]
        for data in fixtures:
            with self.subTest(data=data): self.assertDifferential(data, data)

    def test_global_alias_and_module_bytes_still_checked(self):
        a = b'@a = internal global i32 0\n@b = internal alias i32, ptr @a\n' + IDENTICAL
        self.assertEqual('ok', self.assertDifferential(a, a)[0])
        self.assertEqual('NotEquivalent', self.assertDifferential(a, a.replace(b'ptr @a', b'ptr @b'))[0])
        self.assertEqual('NotEquivalent', self.assertDifferential(a, a.replace(b'i32 0', b'i32 1'))[0])

    def test_type_index_difference_disables_identity(self):
        self.assertFalse(checker._prepare_identity_policy({}, {b'A'}, {b'B'}, {}, {}).enabled)

    def test_nonlocal_authority_forces_fallback(self):
        authorized = {('global', -1, b'a', b'b'): object()}
        policy = checker._prepare_identity_policy(authorized, set(), set(), {}, {})
        self.assertFalse(policy.enabled)
        with patch.object(checker, 'classify', wraps=checker.classify) as classify:
            checker._verify_unit_prepared(IDENTICAL, IDENTICAL, set(), set(),
                set(), set(), 0, 1, authorized, set(), {}, {}, {}, policy)
            self.assertEqual(2, classify.call_count)

    def test_preparation_rejects_nonidentity_nonlocal_state(self):
        f = {('global', -1, b'a'): b'b'}
        r = {('global', -1, b'b'): b'a'}
        with self.assertRaises(checker.UnsupportedIR):
            checker._prepare_identity_policy({}, set(), set(), f, r)

    def test_actual_classified_permission_scope_falls_back(self):
        authorized = {('local', 1, b'__auto_1', b'__auto_2'): object()}
        policy = checker._prepare_identity_policy(authorized, set(), set(), {}, {})
        token = checker.Token(b'%x', 1, 'local', b'x', False, 1)
        with patch.object(checker, 'classify', return_value=[token]), patch.object(checker, 'verify_unit', return_value=(2, 5)) as full:
            self.assertEqual((2, 5), checker._verify_unit_prepared(IDENTICAL,
                IDENTICAL, set(), set(), set(), set(), 0, 1, authorized,
                set(), {}, {}, {}, policy))
            full.assert_called_once()

    def test_nonlocal_occurrence_collision_check(self):
        f, r = {}, {}
        policy = checker._prepare_identity_policy({}, set(), set(), f, r)
        f[('global', -1, b'f')] = b'other'
        with self.assertRaises(checker.NotEquivalent):
            checker._verify_unit_prepared(IDENTICAL, IDENTICAL, set(), set(),
                set(), set(), 0, 1, {}, set(), {}, f, r, policy)

    def test_checkpoint_resume_preserves_exact_maps_and_progress(self):
        data = b'@v = internal global i32 0\ndeclare void @external()\n' + IDENTICAL
        data += b'define ptr @g() {\n  ret ptr @v\n}\n'
        states = []
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp); left = p/'left.ll'; right = p/'right.ll'
            left.write_bytes(data); right.write_bytes(data)
            for disabled in (False, True):
                cp = p/('full.json' if disabled else 'identity.json')
                def run():
                    for _ in range(20):
                        result = checker.verify_checkpoint(left, right, cp, [], max_units=1)
                        if result is not None: return result
                    self.fail('checkpoint failed to reach EOF')
                if disabled:
                    with full_units(): self.assertEqual({}, run())
                else: self.assertEqual({}, run())
                states.append(json.loads(cp.read_text())['payload'])
            self.assertEqual(states[0], states[1])

    def test_checkpoint_corruption_and_manifest_identity(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp); left=p/'left'; right=p/'right'; cp=p/'checkpoint'
            left.write_bytes(IDENTICAL); right.write_bytes(IDENTICAL)
            checker.verify_checkpoint(left, right, cp, [], max_units=1)
            saved = cp.read_bytes(); envelope = json.loads(saved)
            envelope['payload']['line'] += 1
            cp.write_text(json.dumps(envelope))
            with self.assertRaises(checker.UnsupportedIR): checker.verify_checkpoint(left, right, cp, [])
            cp.write_bytes(saved)
            entry = checker.Rename('local', 1, b'__auto_1', b'__auto_2', 'fixture', 'auto-region')
            with self.assertRaises(checker.UnsupportedIR): checker.verify_checkpoint(left, right, cp, [entry])

    def test_scope_advancement_ignores_text_tokens(self):
        policy = checker._prepare_identity_policy({}, set(), set(), {}, {})
        # A future classifier must not turn ordinary text into a scope source:
        # the original paired loop skips text before advancing next_scope.
        tokens = [checker.Token(b'word', 1, 'text', scope=99)]
        with patch.object(checker, 'classify', return_value=tokens):
            self.assertEqual((1, 5), checker._verify_unit_prepared(IDENTICAL,
                IDENTICAL, set(), set(), set(), set(), 0, 1, {}, set(), {},
                {}, {}, policy))


if __name__ == '__main__': unittest.main()
