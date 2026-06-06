# Plain recursive enum — perf baseline before AoS storage (docs/76 Slice 2)

After Slice 0+0b the docs/76 zero-ceremony surface runs, but storage is still columnar SoA.
binary-trees N=18 (checksum 68332206), -O3:

| form | time | RSS | per-node work |
|---|---:|---:|---|
| struct + `new[auto]` + region (AoS, pointers) | 0.40 s | 22 MB | 1 bump alloc + field writes |
| **plain `enum` (Slice 0/0b, SoA-backed)** | **2.09 s** | **88 MB** | ~4 darray pushes (tag+index+handle+payload columns) |

The ~5× time / ~4× memory gap is the columnar per-node multiplier. **Slice 2 (the `packedEnumABIAoS`
storage mode — one contiguous `{common, tag, payload}` record per node, index handle) is what closes
it**: one record write per node, matching the struct form. Target: parity with the 0.40 s / 22 MB
baseline.
