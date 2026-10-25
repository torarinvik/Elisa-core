#!/usr/bin/env python3
"""Strict textual LLVM IR equivalence modulo a scoped generated-name bijection.

Not an LLVM verifier. Inputs must already be valid LLVM textual IR. Unsupported
scope syntax fails closed; every non-identifier byte remains significant.
"""
from __future__ import annotations
import argparse
import json
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
            if i + 2 >= len(data) or data[i+1] not in HEX or data[i+2] not in HEX:
                raise UnsupportedIR("only LLVM two-hex-digit escapes are supported")
            i += 3
        else:
            i += 1
    raise UnsupportedIR("unterminated quoted LLVM token")


def decode_name(raw: bytes) -> bytes:
    if not raw.startswith(b'"'):
        return raw
    return re.sub(rb"\\([0-9A-Fa-f]{2})", lambda m: bytes([int(m[1], 16)]), raw[1:-1])


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


def classify(data: bytes) -> list[Token]:
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
    count = 0
    named_types = set()
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
            # Type references are identified by the module's type definitions;
            # other percent identifiers in a header are explicit parameter names.
            for j, t in enumerate(ts):
                if t.kind == "%" and t.name in named_types and j + 1 < len(ts) and ts[j+1].raw in (b",", b")"):
                    raise UnsupportedIR("ambiguous named-type/parameter identifier in header")
            names.update(t.name for t in ts if t.kind == "%" and t.name not in named_types)
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
    if not isinstance(document, dict) or set(document) != {"version", "renames"} or document["version"] != 1:
        raise UnsupportedIR("expected version-1 explicit rename manifest")
    entries = []
    for row in document["renames"]:
        if not isinstance(row, dict) or set(row) != {"namespace", "scope", "left", "right", "evidence"}:
            raise UnsupportedIR("invalid rename manifest entry")
        if row["namespace"] not in ("local", "global", "type") or type(row["scope"]) is not int or not isinstance(row["evidence"], str) or not row["evidence"].strip():
            raise UnsupportedIR("invalid namespace/scope or missing producer evidence")
        if (row["namespace"] == "local" and row["scope"] < 0) or (row["namespace"] != "local" and row["scope"] != -1):
            raise UnsupportedIR("manifest scope does not match namespace")
        if not isinstance(row["left"], str) or not isinstance(row["right"], str):
            raise UnsupportedIR("manifest names must be decoded strings")
        entries.append(Rename(row["namespace"], row["scope"], row["left"].encode("utf-8"), row["right"].encode("utf-8"), row["evidence"]))
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


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("left", type=Path)
    parser.add_argument("right", type=Path)
    parser.add_argument("--manifest", type=Path, help="explicit per-pair rename authorization with producer evidence; default permits no renames")
    args = parser.parse_args()
    try:
        mapping = verify(args.left.read_bytes(), args.right.read_bytes(), load_manifest(args.manifest) if args.manifest else None)
    except (NotEquivalent, OSError, ValueError, TypeError, KeyError) as err:
        print(str(err), file=sys.stderr)
        return 1 if isinstance(err, NotEquivalent) and not isinstance(err, UnsupportedIR) else 2
    changed = sum(key[2] != value for key, value in mapping.items())
    print(f"equivalent; {changed} generated identifiers renamed bijectively")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
