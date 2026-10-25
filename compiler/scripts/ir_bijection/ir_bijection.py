#!/usr/bin/env python3
"""Strict textual LLVM IR equivalence modulo a scoped generated-name bijection.

Not an LLVM verifier. Inputs must already be valid LLVM textual IR. Unsupported
scope syntax fails closed; every non-identifier byte remains significant.
"""
from __future__ import annotations
import argparse
import json
import hashlib
import os
import tempfile
import time
from itertools import zip_longest
from dataclasses import dataclass
from pathlib import Path
import re
import sys


class NotEquivalent(ValueError):
    pass


class UnsupportedIR(NotEquivalent):
    pass


@dataclass(frozen=True)
class Token:
    raw: bytes
    line: int
    kind: str = "text"
    name: bytes = b""
    quoted: bool = False
    scope: int = -1


# Deliberately narrower than Claude's substring substitution: a generated
# identifier is a COMPLETE symbol, with a stable stem and decimal counter.
GENERATED = re.compile(rb"(__[a-z_]+_)[0-9]+\Z")
LOCATION = re.compile(rb"__L[0-9]+_C[0-9]+\Z")
NAME = re.compile(rb"[-a-zA-Z$._0-9]+")
HEX = frozenset(b"0123456789abcdefABCDEF")


def generated_family(name: bytes) -> bytes | None:
    m = GENERATED.fullmatch(name)
    if m:
        return m[1] + b"#"
    # Extra region stacks preserve their explicit ordinal; only the source
    # offset can vary. This is the backend's emitRegionExtraStacks spelling.
    m = re.fullmatch(rb"__auto_[0-9]+#([1-9][0-9]*)", name)
    if m:
        return b"__auto_#stack=" + m[1]
    if LOCATION.fullmatch(name):
        return b"__L#_C#"
    return None


def quoted_end(data: bytes, start: int) -> int:
    i = start + 1
    while i < len(data):
        c = data[i]
        if c == 34:
            return i + 1
        if c in (10, 13):
            raise UnsupportedIR("newline in quoted LLVM token")
        if c == 92:
            if i + 1 < len(data) and data[i+1] == 92:
                i += 2
                continue
            if i + 2 >= len(data) or data[i+1] not in HEX or data[i+2] not in HEX:
                raise UnsupportedIR("only LLVM hex and escaped-backslash forms are supported")
            i += 3
        else:
            i += 1
    raise UnsupportedIR("unterminated quoted LLVM token")


def decode_name(raw: bytes) -> bytes:
    if not raw.startswith(b'"'):
        return raw
    return re.sub(rb"\\(\\|[0-9A-Fa-f]{2})", lambda m: b"\\" if m[1] == b"\\" else bytes([int(m[1], 16)]), raw[1:-1])


def lex(data: bytes) -> list[Token]:
    tokens = []
    i = 0
    line = 1
    while i < len(data):
        start = i
        c = data[i]
        if c == 59:  # comment, including identifier-looking bytes
            end = data.find(b"\n", i)
            i = len(data) if end < 0 else end
            tokens.append(Token(data[start:i], line, "comment"))
        elif c in (64, 37):
            i += 1
            if i < len(data) and data[i] == 34:
                i = quoted_end(data, i)
            else:
                m = NAME.match(data, i)
                if not m:
                    raise UnsupportedIR(f"invalid sigil identifier at line {line}")
                i = m.end()
            spelling = data[start+1:i]
            tokens.append(Token(data[start:i], line, chr(c), decode_name(spelling), spelling.startswith(b'"')))
        elif c == 34:
            i = quoted_end(data, i)
            tokens.append(Token(data[start:i], line, "string"))
        elif chr(c).isspace():
            while i < len(data) and chr(data[i]).isspace():
                i += 1
            raw = data[start:i]
            tokens.append(Token(raw, line))
            line += raw.count(b"\n")
        else:
            m = NAME.match(data, i)
            i = m.end() if m else i + 1
            tokens.append(Token(data[start:i], line, "word"))
    return tokens


TYPE_WORDS = {b"void", b"half", b"bfloat", b"float", b"double", b"x86_fp80", b"fp128", b"ppc_fp128", b"x86_mmx", b"x86_amx", b"ptr", b"label", b"metadata", b"token"}
TYPED_PARAM_ATTRIBUTES = {b"sret", b"byval", b"byref", b"inalloca", b"preallocated", b"elementtype"}
OPEN_CLOSE = {b"(": b")", b"[": b"]", b"{": b"}", b"<": b">"}


def header_local_names(ts: list[Token], named_types: set[bytes]) -> set[bytes]:
    """Assign percent-token roles in LLVM-printer single-line headers.

    For independently valid IR, percent identifiers in a type production are
    named types; a formal's optional final identifier is its local binding.
    Unknown contexts fail closed instead of guessing from the following comma.
    """
    roles = {}

    def type_percent(index):
        token = ts[index]
        if token.name not in named_types:
            raise UnsupportedIR("unresolved named type in function header")
        roles[index] = "type"

    def group(start, limit, type_context):
        stack = [OPEN_CLOSE[ts[start].raw]]
        i = start + 1
        while i < limit:
            raw = ts[i].raw
            if raw in OPEN_CLOSE:
                stack.append(OPEN_CLOSE[raw])
            elif raw in OPEN_CLOSE.values():
                if not stack or raw != stack.pop():
                    raise UnsupportedIR("unbalanced function header group")
                if not stack:
                    return i + 1
            elif ts[i].kind == "%":
                if not type_context:
                    raise UnsupportedIR("percent identifier in unsupported header attribute")
                type_percent(i)
            i += 1
        raise UnsupportedIR("unterminated function header group")

    def type_end(start, limit):
        if start >= limit:
            raise UnsupportedIR("missing header type")
        token = ts[start]
        if token.kind == "%":
            type_percent(start)
            end = start + 1
        elif token.raw in (b"[", b"{", b"<"):
            end = group(start, limit, True)
        elif token.raw in TYPE_WORDS or re.fullmatch(rb"i[0-9]+", token.raw):
            end = start + 1
        else:
            raise UnsupportedIR("unsupported header type production")
        # Typed pointers/function types are accepted only as a complete type
        # production. Opaque pointer addrspace qualifiers carry no identifiers.
        while end < limit:
            if ts[end].raw == b"*":
                end += 1
            elif ts[end].raw == b"addrspace" and end + 1 < limit and ts[end+1].raw == b"(":
                end = group(end + 1, limit, False)
            elif ts[end].raw == b"(":
                end = group(end, limit, True)
            else:
                break
        return end

    function = next((i for i, token in enumerate(ts) if token.kind == "@"), None)
    if function is None or function + 1 >= len(ts) or ts[function+1].raw != b"(":
        raise UnsupportedIR("unsupported function parameter-list boundary")
    # Find a complete return type immediately before the function symbol;
    # linkage/calling-convention/return attributes precede it and have no locals.
    candidates = []
    for start in range(1, function):
        saved = dict(roles)
        try:
            if type_end(start, function) == function:
                candidates.append((start, dict(roles)))
        except UnsupportedIR:
            pass
        roles.clear()
        roles.update(saved)
    if len(candidates) != 1:
        raise UnsupportedIR("unsupported or ambiguous return type production")
    return_start, return_roles = candidates[0]
    roles.update(return_roles)
    if any(t.kind == "%" for t in ts[1:return_start]):
        raise UnsupportedIR("percent identifier before return type")

    opening = function + 1
    # Split parameters only at the outer list's commas. Nested aggregate types
    # and typed attributes have their own delimiters.
    segments, start, i = [], opening + 1, opening + 1
    while i < len(ts):
        raw = ts[i].raw
        if raw == b")":
            if start < i:
                segments.append((start, i))
            closing = i
            break
        if raw == b",":
            segments.append((start, i))
            start, i = i + 1, i + 1
            continue
        if raw in OPEN_CLOSE:
            # Determine bounds without assigning roles; formal parsing below
            # assigns each group's actual type/attribute context.
            stack = [OPEN_CLOSE[raw]]
            i += 1
            while i < len(ts) and stack:
                if ts[i].raw in OPEN_CLOSE:
                    stack.append(OPEN_CLOSE[ts[i].raw])
                elif ts[i].raw in OPEN_CLOSE.values():
                    if ts[i].raw != stack.pop():
                        raise UnsupportedIR("unbalanced formal parameter group")
                i += 1
            if stack:
                raise UnsupportedIR("unterminated formal parameter group")
            continue
        i += 1
    else:
        raise UnsupportedIR("unterminated parameter list")

    names = set()
    for start, end in segments:
        if end == start + 1 and ts[start].raw == b"...":
            continue
        i = type_end(start, end)
        while i < end:
            token = ts[i]
            if token.kind == "%":
                if i != end - 1:
                    raise UnsupportedIR("formal name is not the final parameter token")
                names.add(token.name)
                roles[i] = "local"
                i += 1
            elif token.raw in OPEN_CLOSE:
                attribute = ts[i-1].raw if i > start else b""
                i = group(i, end, attribute in TYPED_PARAM_ATTRIBUTES)
            else:
                i += 1
    # Prefix/prologue/personality constants containing percent tokens need a
    # separate grammar. They are deliberately not inferred from named_types.
    if any(t.kind == "%" for t in ts[closing+1:]):
        raise UnsupportedIR("percent identifier in unsupported function suffix")
    for i, token in enumerate(ts):
        if token.kind == "%" and i not in roles:
            raise UnsupportedIR("unclassified function header percent identifier")
    return names


def classify(data: bytes, known_types: set[bytes] | None = None, scope_start: int = 0) -> list[Token]:
    if b"\x00" in data:
        raise UnsupportedIR("NUL bytes: expected textual LLVM IR, not an object/bitcode file")
    tokens = lex(data)
    by_line: dict[int, list[int]] = {}
    for i, t in enumerate(tokens):
        if t.kind not in ("comment", "text"):
            by_line.setdefault(t.line, []).append(i)
    # Recognize LLVM's printed single-line function headers. Anonymous packed
    # aggregates can contain braces; the final opening brace starts the body.
    scopes: dict[int, int] = {}
    headers: set[int] = set()
    current = -1
    count = scope_start
    named_types = set(known_types or ())
    for line, indices in by_line.items():
        ts = [tokens[i] for i in indices]
        if any(t.raw in (b"blockaddress", b"uselistorder_bb") for t in ts):
            raise UnsupportedIR("cross-function label references are not supported")
        if ts[0].raw == b"define":
            if current >= 0 or ts[-1].raw != b"{" or not any(t.kind == "@" for t in ts):
                raise UnsupportedIR(f"unsupported function header at line {line}")
            current = count
            count += 1
            headers.add(line)
        elif ts[0].raw == b"declare":
            if current >= 0:
                raise UnsupportedIR("declaration inside function")
            # Declarations have their own parameter-name namespace too.
            scopes[line] = count
            count += 1
            headers.add(line)
        if current >= 0:
            scopes[line] = current
            if ts[0].raw == b"}":
                if len(ts) != 1:
                    raise UnsupportedIR("unsupported function terminator")
                current = -1
        elif ts[0].kind == "%" and len(ts) >= 3 and ts[1].raw == b"=" and ts[2].raw == b"type":
            named_types.add(ts[0].name)
    if current >= 0:
        raise UnsupportedIR("unterminated function")

    local_names: dict[int, set[bytes]] = {}
    label_indices = set()
    for line, indices in by_line.items():
        scope = scopes.get(line, -1)
        if scope < 0:
            continue
        ts = [tokens[i] for i in indices]
        names = local_names.setdefault(scope, set())
        if line in headers:
            names.update(header_local_names(ts, named_types))
        elif len(ts) >= 2 and ts[1].raw == b":" and ts[0].kind in ("word", "string"):
            names.add(decode_name(ts[0].raw))
            label_indices.add(indices[0])
        elif len(ts) >= 2 and ts[0].kind == "%" and ts[1].raw == b"=":
            names.add(ts[0].name)
    for scope, names in local_names.items():
        if names & named_types:
            raise UnsupportedIR(f"local/type name overlap in function {scope}; a full LLVM parser is required")

    result = []
    for i, t in enumerate(tokens):
        scope = scopes.get(t.line, -1)
        if t.kind == "@":
            kind, scope = "global", -1
        elif i in label_indices:
            kind = "local"
            t = Token(t.raw, t.line, t.kind, decode_name(t.raw), t.raw.startswith(b'"'))
        elif t.kind == "%":
            if t.name in named_types:
                kind, scope = "type", -1
            elif scope >= 0:
                kind = "local"
                if generated_family(t.name) and t.name not in local_names.get(scope, set()):
                    raise UnsupportedIR(f"unbound generated local at line {t.line}")
            else:
                raise UnsupportedIR(f"unresolved percent identifier outside function at line {t.line}")
        else:
            kind, scope = "text", -1
        result.append(Token(t.raw, t.line, kind, t.name, t.quoted, scope))
    return result


@dataclass(frozen=True)
class Rename:
    namespace: str
    scope: int
    left: bytes
    right: bytes
    evidence: str
    producer: str = ""


def internal_globals(data: bytes) -> set[bytes]:
    lines = {}
    for token in lex(data):
        if token.kind not in ("text", "comment"):
            lines.setdefault(token.line, []).append(token)
    result = set()
    for ts in lines.values():
        if ts[0].raw != b"define" and ts[0].kind != "@":
            continue
        for i, token in enumerate(ts):
            if token.kind == "@":
                # A global definition starts with its name; a function starts
                # with define/linkage/return type before the function name.
                prefix = ts[:i] if ts[0].raw == b"define" else ts[i+1:]
                if any(t.raw in (b"private", b"internal") for t in prefix):
                    result.add(token.name)
                break
    return result


def load_manifest(path: Path) -> list[Rename]:
    document = json.loads(path.read_text())
    if not isinstance(document, dict) or set(document) != {"version", "renames"} or document["version"] not in (1, 2):
        raise UnsupportedIR("expected version-1 or version-2 explicit rename manifest")
    entries = []
    for row in document["renames"]:
        keys = {"namespace", "scope", "left", "right", "evidence"}
        if document["version"] == 2:
            keys.add("producer")
        if not isinstance(row, dict) or set(row) != keys:
            raise UnsupportedIR("invalid rename manifest entry")
        if row["namespace"] not in ("local", "global", "type") or type(row["scope"]) is not int or not isinstance(row["evidence"], str) or not row["evidence"].strip():
            raise UnsupportedIR("invalid namespace/scope or missing producer evidence")
        if (row["namespace"] == "local" and row["scope"] < 0) or (row["namespace"] != "local" and row["scope"] != -1):
            raise UnsupportedIR("manifest scope does not match namespace")
        if document["version"] == 2 and (not isinstance(row["producer"], str) or not row["producer"].strip()):
            raise UnsupportedIR("version-2 manifest requires a nonempty producer")
        if not isinstance(row["left"], str) or not isinstance(row["right"], str):
            raise UnsupportedIR("manifest names must be decoded strings")
        entries.append(Rename(row["namespace"], row["scope"], row["left"].encode("utf-8"), row["right"].encode("utf-8"), row["evidence"], row.get("producer", "")))
    return entries


def verify(left: bytes, right: bytes, renames: list[Rename] | None = None) -> dict[tuple[str, int, bytes], bytes]:
    """Return the witnessed bijection or raise NotEquivalent/UnsupportedIR."""
    a, b = classify(left), classify(right)
    authorized = set()
    for entry in renames or []:
        if not isinstance(entry, Rename) or not entry.evidence.strip():
            raise UnsupportedIR("rename requires explicit producer evidence")
        authorized.add((entry.namespace, entry.scope, entry.left, entry.right))
    used = set()
    left_internal, right_internal = internal_globals(left), internal_globals(right)
    if len(a) != len(b):
        raise NotEquivalent(f"token count differs: {len(a)} != {len(b)}")
    forward = {}
    reverse = {}
    for x, y in zip(a, b):
        if (x.kind, x.scope) != (y.kind, y.scope):
            raise NotEquivalent(f"namespace/scope differs at lines {x.line}/{y.line}")
        if x.kind == "text":
            if x.raw != y.raw:
                raise NotEquivalent(f"non-identifier text differs at lines {x.line}/{y.line}: {x.raw[:80]!r} != {y.raw[:80]!r}")
            continue
        if x.raw != y.raw:
            permission = (x.kind, x.scope, x.name, y.name)
            if permission not in authorized:
                raise NotEquivalent(f"identifier rename lacks explicit manifest authorization at line {x.line}")
            if x.kind == "global" and (x.name not in left_internal or y.name not in right_internal):
                raise UnsupportedIR("global renaming requires explicit private/internal linkage on both definitions; ABI symbols stay exact")
            entry = next(e for e in renames or [] if (e.namespace, e.scope, e.left, e.right) == permission)
            validate_producer(entry)
            used.add(permission)
            family = generated_family(x.name)
            if not family or family != generated_family(y.name) or x.quoted != y.quoted:
                raise NotEquivalent(f"non-generated or incompatible identifier rename at lines {x.line}/{y.line}: {x.raw!r} != {y.raw!r}")
        key = (x.kind, x.scope, x.name)
        target = (y.kind, y.scope, y.name)
        if key in forward and forward[key] != y.name:
            raise NotEquivalent(f"one source identifier maps to several targets at line {x.line}")
        if target in reverse and reverse[target] != x.name:
            raise NotEquivalent(f"several source identifiers collide at line {x.line}")
        forward[key] = y.name
        reverse[target] = x.name
    if authorized - used:
        raise UnsupportedIR("manifest contains unused rename entries; policy does not match these inputs")
    return forward



def validate_producer(entry: Rename) -> None:
    """Validate producer grammar, never infer authorization from a difference."""
    if not isinstance(entry.producer, str):
        raise UnsupportedIR("producer must be a reviewed producer name")
    if b"#" in entry.left or b"#" in entry.right:
        if entry.producer != "region-extra-stack" or entry.namespace != "local":
            raise UnsupportedIR("stack suffix rename requires local region-extra-stack producer evidence")
    if not entry.producer:
        return  # Legacy complete-name families retain their explicit V1 policy.
    patterns = {
        "auto-region": rb"__auto_[0-9]+",
        "region-extra-stack": rb"__auto_[0-9]+#[1-9][0-9]*",
        "quantifier": rb"__quantifier_[0-9]+",
    }
    if entry.producer not in patterns or entry.namespace != "local":
        raise UnsupportedIR("unknown producer or nonlocal source-offset rename")
    pattern = patterns[entry.producer]
    if not re.fullmatch(pattern, entry.left) or not re.fullmatch(pattern, entry.right):
        raise UnsupportedIR("identifier does not match its declared producer")


MAX_UNIT_BYTES = 64 << 20


def bounded_lines(stream, maximum=MAX_UNIT_BYTES):
    while True:
        line = stream.readline(maximum + 1)
        if not line:
            return
        if len(line) > maximum:
            raise UnsupportedIR("IR line exceeds streaming memory bound")
        yield line


def module_index(stream):
    """First pass retains only named-type and internal-global identities."""
    named, internal = set(), set()
    in_function = False
    for line in bounded_lines(stream):
        stripped = line.lstrip()
        if in_function:
            if stripped.startswith(b"}"):
                in_function = False
            continue
        ts = [t for t in lex(line) if t.kind not in ("text", "comment")]
        if not ts:
            continue
        if ts[0].raw == b"define":
            in_function = True
        if ts[0].kind == "%" and len(ts) >= 3 and ts[1].raw == b"=" and ts[2].raw == b"type":
            named.add(ts[0].name)
        internal.update(internal_globals(line))
    stream.seek(0)
    return named, internal


def units(stream):
    """One whole function or one module line; never materialize a module."""
    parts, size = [], 0
    in_function = False
    for line in bounded_lines(stream):
        stripped = line.lstrip()
        if not in_function and stripped.startswith(b"define "):
            in_function = True
        if in_function:
            size += len(line)
            if size > MAX_UNIT_BYTES:
                raise UnsupportedIR("function exceeds streaming memory bound")
            parts.append(line)
            if stripped.startswith(b"}"):
                yield b"".join(parts)
                parts, size, in_function = [], 0, False
        else:
            yield line
    if in_function:
        raise UnsupportedIR("unterminated streamed function")


def verify_unit(a, b, left_types, right_types, left_internal, right_internal,
                scope, line_number, authorized, used, changed,
                module_forward, module_reverse):
    """Verify one whole namespace unit; never skip identical units."""
    aa = classify(a, left_types, scope)
    bb = classify(b, right_types, scope)
    if len(aa) != len(bb):
        raise NotEquivalent(f"token count differs near line {line_number}")
    forward, reverse = {}, {}
    next_scope = scope
    for x, y in zip(aa, bb):
        if (x.kind, x.scope) != (y.kind, y.scope):
            raise NotEquivalent(f"namespace/scope differs near line {line_number+x.line-1}")
        if x.kind == "text":
            if x.raw != y.raw:
                raise NotEquivalent(f"non-identifier bytes differ near line {line_number+x.line-1}: {x.raw[:80]!r} != {y.raw[:80]!r}")
            continue
        if x.scope >= 0:
            next_scope = max(next_scope, x.scope + 1)
        if x.raw != y.raw:
            permission = (x.kind, x.scope, x.name, y.name)
            if permission not in authorized:
                raise NotEquivalent(f"identifier rename lacks manifest authorization near line {line_number+x.line-1}: {x.raw!r} != {y.raw!r}")
            if x.kind == "global" and (x.name not in left_internal or y.name not in right_internal):
                raise UnsupportedIR("global renaming requires private/internal definitions on both sides")
            family = generated_family(x.name)
            if not family or family != generated_family(y.name) or x.quoted != y.quoted:
                raise NotEquivalent("non-generated or incompatible identifier rename")
            used.add(permission)
            changed[(x.kind, x.scope, x.name)] = y.name
        f, r = (forward, reverse) if x.kind == "local" else (module_forward, module_reverse)
        key, target = (x.kind, x.scope, x.name), (y.kind, y.scope, y.name)
        if key in f and f[key] != y.name:
            raise NotEquivalent("one source identifier maps to several targets")
        if target in r and r[target] != x.name:
            raise NotEquivalent("several source identifiers collide, including unchanged bindings")
        f[key], r[target] = y.name, x.name
    # Even a function with no named locals consumes an ordinal.
    # classify preserves text on ordinary words (name is empty), but its
    # first significant raw token still identifies the unit header.
    first = a.lstrip().split(None, 1)[0] if a.strip() else b""
    if first in (b"define", b"declare"):
        next_scope = max(next_scope, scope + 1)
    return next_scope, line_number + a.count(b"\n")


def verify_streams(left, right, renames=None):
    """Seekable binary streams; memory bounded by largest function + manifest.

    Returns changed mappings only. All unchanged bindings still participate in
    collision checks; local tables are released only after their entire function.
    """
    left_types, left_internal = module_index(left)
    right_types, right_internal = module_index(right)
    authorized = {}
    for entry in renames or []:
        if not isinstance(entry, Rename) or not entry.evidence.strip():
            raise UnsupportedIR("rename requires explicit producer evidence")
        validate_producer(entry)
        key = (entry.namespace, entry.scope, entry.left, entry.right)
        if key in authorized:
            raise UnsupportedIR("duplicate rename manifest entry")
        authorized[key] = entry
    used, changed = set(), {}
    module_forward, module_reverse = {}, {}
    scope, line_number = 0, 1
    for a, b in zip_longest(units(left), units(right)):
        if a is None or b is None:
            raise NotEquivalent("streamed module unit counts differ")
        scope, line_number = verify_unit(a, b, left_types, right_types,
            left_internal, right_internal, scope, line_number, authorized,
            used, changed, module_forward, module_reverse)
    if set(authorized) - used:
        raise UnsupportedIR("manifest contains unused rename entries")
    return changed


CHECKPOINT_VERSION = 1
CHECKPOINT_MAX_BYTES = 64 << 20


def canonical_json(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode("ascii")


def checkpoint_digest(payload):
    return hashlib.sha256(canonical_json(payload)).hexdigest()


def input_identity(stream, path):
    before = os.fstat(stream.fileno())
    digest = hashlib.sha256()
    while True:
        chunk = stream.read(1 << 20)
        if not chunk:
            break
        digest.update(chunk)
    after = os.fstat(stream.fileno())
    stamp = lambda x: (x.st_dev, x.st_ino, x.st_size, x.st_mtime_ns, x.st_ctime_ns)
    if stamp(before) != stamp(after) or stamp(path.stat()) != stamp(after):
        raise UnsupportedIR("input changed while hashing")
    stream.seek(0)
    return {"path": str(path.resolve()), "bytes": after.st_size, "sha256": digest.hexdigest()}, stamp(after)


def atomic_checkpoint(path, payload):
    data = canonical_json({"version": CHECKPOINT_VERSION, "payload": payload,
                           "sha256": checkpoint_digest(payload)}) + b"\n"
    if len(data) > CHECKPOINT_MAX_BYTES:
        raise UnsupportedIR("checkpoint exceeds size bound")
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(dir=path.parent, prefix=".ir-proof-", delete=False) as out:
            temporary = Path(out.name)
            out.write(data)
            out.flush()
            os.fsync(out.fileno())
        os.replace(temporary, path)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def load_checkpoint(path, identity):
    def unique_keys(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise UnsupportedIR("duplicate checkpoint field")
            result[key] = value
        return result
    with path.open("rb") as source:
        data = source.read(CHECKPOINT_MAX_BYTES + 1)
    if len(data) > CHECKPOINT_MAX_BYTES:
        raise UnsupportedIR("checkpoint exceeds size bound")
    outer = json.loads(data, object_pairs_hook=unique_keys)
    if not isinstance(outer, dict) or set(outer) != {"version", "payload", "sha256"} or outer["version"] != CHECKPOINT_VERSION:
        raise UnsupportedIR("unsupported checkpoint envelope")
    payload = outer["payload"]
    if outer["sha256"] != checkpoint_digest(payload):
        raise UnsupportedIR("checkpoint checksum mismatch")
    fields = {"identity", "left_offset", "right_offset", "scope", "line", "units", "complete", "forward", "reverse", "used", "changed"}
    if not isinstance(payload, dict) or set(payload) != fields or payload["identity"] != identity:
        raise UnsupportedIR("checkpoint input/manifest/checker identity mismatch")
    for field in ("left_offset", "right_offset", "scope", "units"):
        if type(payload[field]) is not int or payload[field] < 0:
            raise UnsupportedIR("invalid checkpoint progress counter")
    if type(payload["line"]) is not int or payload["line"] < 1 or type(payload["complete"]) is not bool:
        raise UnsupportedIR("invalid checkpoint line/completion state")
    return payload


def encode_mapping(mapping):
    return [[kind, scope, name.hex(), value.hex()] for (kind, scope, name), value in sorted(mapping.items())]


def decode_mapping(rows, module_only=False):
    result = {}
    if not isinstance(rows, list):
        raise UnsupportedIR("invalid checkpoint binding table")
    for row in rows:
        if not isinstance(row, list) or len(row) != 4:
            raise UnsupportedIR("invalid checkpoint binding row")
        kind, scope, name, value = row
        if kind not in ("global", "type", "local") or type(scope) is not int:
            raise UnsupportedIR("invalid checkpoint binding namespace")
        if (kind == "local" and scope < 0) or (kind != "local" and scope != -1) or (module_only and kind == "local"):
            raise UnsupportedIR("invalid checkpoint binding scope")
        if not isinstance(name, str) or not isinstance(value, str):
            raise UnsupportedIR("invalid checkpoint binding name")
        key = kind, scope, bytes.fromhex(name)
        if key in result:
            raise UnsupportedIR("duplicate checkpoint binding")
        result[key] = bytes.fromhex(value)
    return result


def validate_cursor(stream, offset, expected_scope, expected_line):
    """Recheck that progress is an exact complete-unit boundary, not mid-function."""
    stream.seek(0)
    scope, line_number, inside = 0, 1, False
    while stream.tell() < offset:
        line = stream.readline(MAX_UNIT_BYTES + 1)
        if not line or len(line) > MAX_UNIT_BYTES or stream.tell() > offset:
            raise UnsupportedIR("checkpoint byte offset is not a unit boundary")
        stripped = line.lstrip()
        if not inside and stripped.startswith((b"define ", b"declare ")):
            scope += 1
            if stripped.startswith(b"define "):
                inside = True
        elif inside and stripped.startswith(b"}"):
            inside = False
        line_number += line.count(b"\n")
    if inside or (scope, line_number) != (expected_scope, expected_line):
        raise UnsupportedIR("checkpoint function/line boundary mismatch")
    stream.seek(offset)


def verify_checkpoint(left_path, right_path, checkpoint_path, renames=None,
                      budget_seconds=15.0, max_units=None):
    """Return changed mappings only at fully checked EOF; None means checkpointed.

    Checkpoints are trusted local proof-work state with corruption checksums,
    never an untrusted compiler cache authority. They are not adversary signatures.
    """
    if budget_seconds <= 0 or (max_units is not None and max_units < 1):
        raise UnsupportedIR("positive checkpoint work budget required")
    entries = list(renames or [])
    authorized = {}
    for entry in entries:
        if not isinstance(entry, Rename) or not entry.evidence.strip():
            raise UnsupportedIR("rename requires explicit producer evidence")
        validate_producer(entry)
        key = entry.namespace, entry.scope, entry.left, entry.right
        if key in authorized:
            raise UnsupportedIR("duplicate rename manifest entry")
        authorized[key] = entry
    manifest = [[e.namespace, e.scope, e.left.hex(), e.right.hex(), e.evidence, e.producer] for e in entries]
    with left_path.open("rb") as left, right_path.open("rb") as right:
        left_id, left_stamp = input_identity(left, left_path)
        right_id, right_stamp = input_identity(right, right_path)
        identity = {"left": left_id, "right": right_id, "python": sys.version,
                    "manifest_sha256": checkpoint_digest(manifest),
                    "checker_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
        left_types, left_internal = module_index(left)
        right_types, right_internal = module_index(right)
        identity["indexes_sha256"] = checkpoint_digest([[x.hex() for x in sorted(names)] for names in (left_types, right_types, left_internal, right_internal)])
        if checkpoint_path.exists():
            state = load_checkpoint(checkpoint_path, identity)
        else:
            state = {"identity": identity, "left_offset": 0, "right_offset": 0,
                     "scope": 0, "line": 1, "units": 0, "complete": False,
                     "forward": [], "reverse": [], "used": [], "changed": []}
        forward = decode_mapping(state["forward"], True)
        reverse = decode_mapping(state["reverse"], True)
        changed = decode_mapping(state["changed"])
        for (kind, scope, name), target in forward.items():
            if reverse.get((kind, scope, target)) != name:
                raise UnsupportedIR("checkpoint forward/reverse maps disagree")
        if len(forward) != len(reverse):
            raise UnsupportedIR("checkpoint binding map cardinalities disagree")
        used_map = decode_mapping(state["used"])
        used = {(kind, scope, name, target) for (kind, scope, name), target in used_map.items()}
        if not used.issubset(authorized):
            raise UnsupportedIR("checkpoint contains unauthorized used permissions")
        if {(k[0], k[1], k[2], v) for k, v in changed.items()} != used:
            raise UnsupportedIR("checkpoint changed/used records disagree")
        for stream, key in ((left, "left_offset"), (right, "right_offset")):
            if state[key] > identity["left" if key == "left_offset" else "right"]["bytes"]:
                raise UnsupportedIR("checkpoint offset exceeds input")
            validate_cursor(stream, state[key], state["scope"], state["line"])
        # The cooperative budget covers new proof work. Hash/index/cursor
        # validation is mandatory startup and may already exceed a small budget;
        # counting it would otherwise checkpoint only one line per invocation.
        started = time.monotonic()
        completed_units = 0
        for a, b in zip_longest(units(left), units(right)):
            if state["complete"]:
                raise UnsupportedIR("completed checkpoint has unverified trailing units")
            if a is None or b is None:
                raise NotEquivalent("streamed module unit counts differ")
            state["scope"], state["line"] = verify_unit(a, b, left_types, right_types,
                left_internal, right_internal, state["scope"], state["line"],
                authorized, used, changed, forward, reverse)
            state["left_offset"], state["right_offset"] = left.tell(), right.tell()
            state["units"] += 1
            completed_units += 1
            if time.monotonic() - started >= budget_seconds or (max_units is not None and completed_units >= max_units):
                break
        else:
            if set(authorized) - used:
                raise UnsupportedIR("manifest contains unused rename entries")
            state["complete"] = True
        # Reject ordinary concurrent source changes rather than checkpointing a
        # mixed invocation. Full cryptographic input hashes are rechecked on resume.
        stamp = lambda x: (x.st_dev, x.st_ino, x.st_size, x.st_mtime_ns, x.st_ctime_ns)
        if (stamp(os.fstat(left.fileno())) != left_stamp or stamp(os.fstat(right.fileno())) != right_stamp
                or stamp(left_path.stat()) != left_stamp or stamp(right_path.stat()) != right_stamp):
            raise UnsupportedIR("input changed during checkpoint verification")
        state["forward"], state["reverse"] = encode_mapping(forward), encode_mapping(reverse)
        state["changed"] = encode_mapping(changed)
        state["used"] = encode_mapping({(kind, scope, name): target for kind, scope, name, target in used})
        atomic_checkpoint(checkpoint_path, state)
        return changed if state["complete"] else None


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("left", type=Path)
    parser.add_argument("right", type=Path)
    parser.add_argument("--manifest", type=Path, help="explicit per-pair rename authorization with producer evidence; default permits no renames")
    parser.add_argument("--checkpoint", type=Path, help="atomic locally trusted proof-work checkpoint")
    parser.add_argument("--budget-seconds", type=float, default=15.0, help="checkpoint after complete units; use an external process cap too")
    args = parser.parse_args()
    try:
        entries = load_manifest(args.manifest) if args.manifest else None
        if args.checkpoint:
            mapping = verify_checkpoint(args.left, args.right, args.checkpoint, entries, args.budget_seconds)
            if mapping is None:
                print("incomplete; verified complete units checkpointed (no equivalence claim)")
                return 3
        else:
            with args.left.open("rb") as left, args.right.open("rb") as right:
                mapping = verify_streams(left, right, entries)
    except (NotEquivalent, OSError, ValueError, TypeError, KeyError) as err:
        print(str(err), file=sys.stderr)
        return 1 if isinstance(err, NotEquivalent) and not isinstance(err, UnsupportedIR) else 2
    changed = sum(key[2] != value for key, value in mapping.items())
    print(f"equivalent; {changed} generated identifiers renamed bijectively")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
