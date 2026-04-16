#!/usr/bin/env python3
"""Shared synthetic Lua inputs for frontend benchmark scripts."""

from __future__ import annotations

from pathlib import Path


SYNTHETIC_KIND = "mixed_lua_module"
SYNTHETIC_CHUNK_STMT_BUDGET = 14
_TAGS = ("alpha", "beta", "gamma", "delta", "sigma", "omega")


def _chunk_lines(chunk_index: int) -> list[str]:
    base = (chunk_index % 9) + 1
    weight = (chunk_index % 7) + 2
    limit = (chunk_index % 4) + 2
    phase = chunk_index % 3
    tag = _TAGS[chunk_index % len(_TAGS)]
    return [
        f"modules[{chunk_index}] = {{ base = {base}, weight = {weight}, tag = \"{tag}\", items = {{ {base}, {base + 1}, {base + 2}, {base + 3} }} }}",
        f"modules[{chunk_index}].run = function(self, seed_value)",
        "    local acc = seed_value + self.base + self.weight",
        f"    for inner = 1, {limit} do",
        "        local item = self.items[inner] or inner",
        "        if inner % 2 == 0 then",
        "            acc = acc + item",
        "        elseif inner == 1 then",
        "            acc = acc + self.weight",
        "        else",
        "            acc = acc - item + self.base",
        "        end",
        "    end",
        "    local function finalize(extra)",
        "        return acc + extra + #self.tag",
        "    end",
        f"    return finalize({phase + 1})",
        "end",
        f"results[{chunk_index}] = modules[{chunk_index}]:run(seed + {phase})",
        f"if results[{chunk_index}] % 3 == 0 then",
        f"    summary[{chunk_index}] = {{ kind = modules[{chunk_index}].tag, total = results[{chunk_index}] }}",
        "else",
        f"    summary[{chunk_index}] = {{ kind = \"fallback\", total = results[{chunk_index}] + modules[{chunk_index}].base }}",
        "end",
    ]


def build_synthetic_lua_benchmark_input(path: Path, stmt_count: int) -> dict[str, int | str]:
    chunk_count = max(1, (stmt_count + SYNTHETIC_CHUNK_STMT_BUDGET - 1) // SYNTHETIC_CHUNK_STMT_BUDGET)
    lines = [
        "-- Generated mixed Lua benchmark input",
        "local modules = {}",
        "local results = {}",
        "local summary = {}",
        "local seed = 11",
        "",
    ]
    for chunk_index in range(1, chunk_count + 1):
        lines.extend(_chunk_lines(chunk_index))
        lines.append("")
    lines.append("return results, summary")
    text = "\n".join(lines) + "\n"
    path.write_text(text, encoding="utf-8")
    return {
        "kind": SYNTHETIC_KIND,
        "chunk_count": chunk_count,
        "approx_stmt_count": chunk_count * SYNTHETIC_CHUNK_STMT_BUDGET,
        "bytes": len(text.encode("utf-8")),
    }