"""Tokenize and classify LLVM IR symbols for equivalence checks."""

from __future__ import annotations
from dataclasses import dataclass
import re


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
    # Supported LLVM escapes encode a quote as hex; a raw quote always closes.
    # Native bounded searches skip ordinary string bytes, never their contents.
    closing = data.find(b'"', start + 1)
    limit = closing if closing >= 0 else len(data)
    lf = data.find(b"\n", start + 1, limit)
    cr = data.find(b"\r", start + 1, limit)
    newline = min((n for n in (lf, cr) if n >= 0), default=-1)
    stop = newline if newline >= 0 else limit
    slash = data.find(b"\\", start + 1, stop)
    while slash >= 0:
        if slash + 1 < limit and data[slash+1] == 92:
            next_byte = slash + 2
        elif slash + 2 >= limit or data[slash+1] not in HEX or data[slash+2] not in HEX:
            raise UnsupportedIR("only LLVM hex and escaped-backslash forms are supported")
        else:
            next_byte = slash + 3
        slash = data.find(b"\\", next_byte, stop)
    if newline >= 0:
        raise UnsupportedIR("newline in quoted LLVM token")
    if closing < 0:
        raise UnsupportedIR("unterminated quoted LLVM token")
    return closing + 1


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
    named_types = known_types if known_types is not None else set()
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
            if known_types is None:
                named_types.add(ts[0].name)
            elif ts[0].name not in named_types:
                raise UnsupportedIR("complete module type index is missing a definition")
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


