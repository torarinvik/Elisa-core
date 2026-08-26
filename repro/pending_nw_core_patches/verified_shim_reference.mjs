// Minimal standalone Elisa wasm host shim, written fresh in the scratchpad
// because nw-core/shells/web/ is unreachable this session. Same contract as the
// project's shim: supply the libc subset the Elisa runtime bottoms out in, and
// adapt each import to its DECLARED signature (stage0 lowers some pointer/size
// externs as i64 and others as i32 on wasm32, so a fixed convention cannot
// satisfy the module).
const PAGE = 65536;
const VT = { 0x7f: "i32", 0x7e: "i64", 0x7d: "f32", 0x7c: "f64" };

function leb(d, at) { let r = 0, s = 0; for (;;) { const b = d[at++]; r |= (b & 0x7f) << s; s += 7; if (!(b & 0x80)) return [r, at]; } }

// Declared parameter/result types of every imported function, in import order.
export function importSignatures(bytes) {
    let at = 8; const types = []; const sigs = [];
    while (at < bytes.length) {
        const section = bytes[at++]; const [size, next] = leb(bytes, at); at = next; const end = at + size;
        if (section === 1) {
            let [count, p] = leb(bytes, at); at = p;
            for (let i = 0; i < count; i++) {
                at++; let [n, q] = leb(bytes, at); at = q;
                const params = []; for (let k = 0; k < n; k++) params.push(VT[bytes[at + k]]); at += n;
                [n, q] = leb(bytes, at); at = q;
                const results = []; for (let k = 0; k < n; k++) results.push(VT[bytes[at + k]]); at += n;
                types.push({ params, results });
            }
        } else if (section === 2) {
            let [count, p] = leb(bytes, at); at = p;
            for (let i = 0; i < count; i++) {
                let [n, q] = leb(bytes, at); at = q; const mod = new TextDecoder().decode(bytes.subarray(at, at + n)); at += n;
                [n, q] = leb(bytes, at); at = q; const name = new TextDecoder().decode(bytes.subarray(at, at + n)); at += n;
                const kind = bytes[at++];
                const [idx, r] = leb(bytes, at); at = r;
                if (kind === 0) sigs.push({ mod, name, type: types[idx] });
            }
        }
        at = end;
    }
    return sigs;
}

export function createRuntime({ trace = false } = {}) {
    let memory = null, heapNext = 0, heapEnd = 0, heapBase = 0;
    const u8 = () => new Uint8Array(memory.buffer);
    const blocks = [];
    const log = [];

    function grow(bytes) {
        const needed = heapNext + bytes;
        if (needed > heapEnd) {
            const pages = Math.ceil((needed - heapEnd) / PAGE) + 16;
            if (memory.grow(pages) < 0) throw new RangeError("out of wasm memory");
            heapEnd += pages * PAGE;
        }
    }
    function alloc(bytes, align = 16) {
        const size = Number(bytes);
        heapNext = (heapNext + align - 1) & ~(align - 1);
        grow(size);
        const at = heapNext; heapNext += size;
        blocks.push({ at, size });
        if (trace) log.push(`alloc(${size}) -> ${at}`);
        return at;
    }
    function release(at) {
        const start = Number(at);
        const i = blocks.findIndex((b) => b !== null && b.at === start);
        if (i < 0) return 0;
        blocks[i] = null;
        while (blocks.length && blocks[blocks.length - 1] === null) blocks.pop();
        const top = blocks[blocks.length - 1];
        heapNext = top ? top.at + top.size : heapBase;
        if (trace) log.push(`free(${start}) -> heapNext ${heapNext}`);
        return 0;
    }
    const readCStr = (p) => { const b = u8(); let e = p; while (b[e] !== 0) e++; return new TextDecoder().decode(b.subarray(p, e)); };

    let out = "";
    const emit = (t) => { out += t; let n; while ((n = out.indexOf("\n")) >= 0) { console.log(out.slice(0, n)); out = out.slice(n + 1); } };

    const env = {
        mmap: (_a, len) => alloc(len, PAGE),
        munmap: (a) => release(a),
        malloc: (n) => alloc(n),
        free: (p) => release(p),
        memcpy: (d, s, n) => { u8().copyWithin(d, s, s + Number(n)); return d; },
        memmove: (d, s, n) => { u8().copyWithin(d, s, s + Number(n)); return d; },
        memset: (d, v, n) => { u8().fill(v & 0xff, d, d + Number(n)); return d; },
        memcmp: (a, b, n) => { const x = u8(); for (let i = 0; i < Number(n); i++) { const d = x[a + i] - x[b + i]; if (d) return d < 0 ? -1 : 1; } return 0; },
        strlen: (p) => { const b = u8(); let e = p; while (b[e] !== 0) e++; return e - p; },
        printf: (f) => { const t = readCStr(f); emit(t); return t.length; },
        puts: (p) => { emit(readCStr(p) + "\n"); return 0; },
        write: (_fd, p, n) => { emit(new TextDecoder().decode(u8().subarray(p, p + Number(n)))); return n; },
        abort: () => { throw new Error("elisa: abort()"); },
        backtrace: () => 0,
        backtrace_symbols_fd: () => 0,
        exit: (c) => { throw new Error(`elisa: exit(${c})`); },
    };

    return {
        env, log,
        attach(instance) {
            memory = instance.exports.memory;
            // __heap_base is exported as a WebAssembly.Global, not a number.
            // Guessing when it is missing silently allocates on top of the
            // module's static data: string literals get overwritten mid-run and
            // the failure looks like corrupt program logic, not a bad heap base.
            const raw = instance.exports.__heap_base;
            const base = raw instanceof WebAssembly.Global ? raw.value
                       : typeof raw === "number" ? raw : null;
            if (base === null) {
                throw new Error(
                    "elisa: module does not export __heap_base -- link with " +
                    "`wasm-ld --export=__heap_base`. Refusing to guess a heap " +
                    "base: a wrong one silently overwrites static data.");
            }
            heapNext = base;
            heapBase = heapNext;
            heapEnd = memory.buffer.byteLength;
            if (instance.exports.__wasm_call_ctors) instance.exports.__wasm_call_ctors();
        },
        flush() { if (out) { console.log(out); out = ""; } },
        pages: () => memory.buffer.byteLength / PAGE,
        memory: () => memory,
    };
}

export async function instantiate(bytes, opts = {}) {
    const rt = createRuntime(opts);
    const view = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
    const sigs = importSignatures(view);

    // Adapt each import to its declared signature: a result declared i64 must be
    // handed back as BigInt, and BigInt arguments become Numbers for the JS body.
    const imports = { env: {} };
    for (const { mod, name, type } of sigs) {
        const target = (mod === "env" && rt.env[name]) || (() => 0);
        const wantsBig = type.results[0] === "i64";
        imports[mod] ??= {};
        imports[mod][name] = (...args) => {
            const r = target(...args.map((a) => (typeof a === "bigint" ? Number(a) : a)));
            if (!type.results.length) return;
            return wantsBig ? BigInt(r ?? 0) : Number(r ?? 0);
        };
    }
    const { instance } = await WebAssembly.instantiate(view, imports);
    rt.attach(instance);
    return { instance, exports: instance.exports, runtime: rt };
}
