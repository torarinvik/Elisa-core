<p align="center">
  <img src="Elisa%20logo.png" alt="Elisa logo" width="320">
</p>

<h1 align="center">Elisa Core</h1>

<p align="center">
  A systems programming language with explicit control over memory and data layout.
</p>

<p align="center">
  <a href="#quick-start"><strong>Quick start</strong></a> ·
  <a href="#elisa-by-example"><strong>Examples</strong></a> ·
  <a href="docs/18-current-surface-ergonomics.md"><strong>Syntax guide</strong></a> ·
  <a href="compiler/README.md"><strong>Compiler guide</strong></a>
</p>

---

Elisa lets you group allocations into regions with a shared lifetime, write contracts the compiler can check, and call C libraries. This repository contains **Stage0**, the Go implementation of the Elisa compiler, along with its runtime, tests, and language design notes.

## Quick start

### Build the compiler

You’ll need Go 1.25 or newer (CI uses 1.26), Make, Python 3, LLVM development libraries, and Clang. Install Z3 4.16.0 and put it on your `PATH` to run the verification tests.

On macOS with Homebrew, install LLVM with `brew install llvm` and put its tools on your `PATH`:

```sh
export PATH="$(brew --prefix llvm)/bin:$PATH"
```

For Linux and Windows toolchain setup, see the [CI workflow](.github/workflows/ci.yml). CI runs compiler tests on Linux and macOS and checks object generation on Windows.

From the repository root:

```sh
make -C compiler build
```

This writes the compiler to `compiler/bin/elisac`. The build target enforces the repository’s five-second rebuild limit. A cold Go build cache can cause it to time out.

### Compile your first program

Save this Elisa program as `hello.elisa`:

```python
def main() -> i32:
    return 0
```

`def` starts a function, `i32` is a signed 32-bit integer, and indentation marks the function body. The operating system uses the value returned by `main` as the process exit status.

From the repository root on macOS or Linux:

```sh
./compiler/bin/elisac -emit obj -o /tmp/elisa-hello.o hello.elisa
clang /tmp/elisa-hello.o -o /tmp/elisa-hello
/tmp/elisa-hello
echo $?
```

You should see `0`. The compiler produces an object file, and Clang links it into an executable.

## Elisa by example

<!-- Python fences provide GitHub highlighting for Elisa’s indentation-based syntax. -->

### Model choices with enums

Each variant can carry its own data. Here, a delivery is either free pickup or shipping with a fee in cents. `match` gives each case its own branch and binds the shipping fee where it is available.

```python
enum Delivery:
    Pickup
    Ship(fee_cents: i64)

def delivery_fee(delivery: Delivery) -> i64:
    match delivery:
        Delivery.Pickup:
            return 0
        Delivery.Ship(fee_cents):
            return fee_cents
```

### State what a function expects

The `requires` clause tells the compiler that callers must supply a positive group size before dividing.

```python
def share(total: i64, people: i64) -> i64:
    requires people > 0
    return total / people

def example() -> i64:
    return share(42, 6) # 7
```

## Explore the language

- Browse working Elisa programs in [the test fixtures](Code/test_programs/).
- Read the [syntax guide](docs/18-current-surface-ergonomics.md) and [feature overview](Features.md).
- Find compiler options in the [CLI emit-mode reference](docs/34-cli-emit-mode-catalog.md).
- See what has changed in the [changelog](CHANGELOG.md).

## Work on the compiler

Anyone can contribute. See the [contribution guide](CONTRIBUTING.md) to get started.

Read the [compiler guide](compiler/README.md) for the source layout and test commands. The default contributor check is `make -C compiler test`. The [formal model](formal/easm-tal/) and [benchmarks](Code/benchmarks/) live alongside the compiler.

## License

Elisa Core is available under the [Zero-Clause BSD license (0BSD)](LICENSE).
