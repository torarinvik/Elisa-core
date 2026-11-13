# Repository instructions

- Keep every source file at or below 600 lines. Split larger files by responsibility.
- Do not commit temporary scripts or temporary documentation.
- Apply a 5-second wall-clock timeout to every Stage0 or Stage1 compiler rebuild, including rebuilds triggered by scripts and Git hooks. If a rebuild times out, profile and optimize it before retrying under the same limit. Measure incremental rebuilds after edits with existing compiler caches.
