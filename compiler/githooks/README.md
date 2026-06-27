# git hooks: auto-install elisac

These hooks rebuild + reinstall the `elisac` compiler to `~/.elisac/elisac`
on every history change (commit / merge / branch switch), so the canonical
binary on PATH never lags the source tree. Warm rebuild ~0.6s; cold ~10s.
On a build failure the last-good binary is kept and errors go to
`/tmp/elisac-autoinstall.log`.

Git does not enable a repo's tracked hooks automatically (for security).
Enable them once per clone:

    git config core.hooksPath compiler/githooks
