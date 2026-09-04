# git hooks: auto-install elisac

These hooks rebuild + reinstall the Go compiler to `~/.elisac/elisac-stage0`
on every history change (commit / merge / branch switch), so the canonical
binary on PATH never lags the source tree. `~/.elisac/elisac` is a symlink to
it; the self-hosted compiler installs beside it as `elisac-stage1` (from
Elisa-compiler, `scripts/install_stage1.sh`), so the name on a command line
says which compiler ran. Warm rebuild ~0.6s; cold ~10s.
On a build failure the last-good binary is kept and errors go to
`/tmp/elisac-autoinstall.log`.

Git does not enable a repo's tracked hooks automatically (for security).
Enable them once per clone:

    git config core.hooksPath compiler/githooks
