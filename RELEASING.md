# Releasing

Each SDK is versioned and published independently from this repository by pushing a tag.
Publishing uses registry **trusted publishing** (GitHub OIDC) — no registry tokens are stored.

| SDK | Bump the version in | Tag | Workflow | Registry binding (one-time, by a registry owner) |
|---|---|---|---|---|
| Python | `python/sbbox/_version.py`, `python/CHANGELOG.md` | `python-vX.Y.Z` | `publish-python.yml` | PyPI project `genspark-sbbox` → owner `genspark-ai`, repo `sb-box-sdk`, workflow `publish-python.yml`, environment `pypi` |
| TypeScript | `typescript/package.json` (+ lockfile), `CHANGELOG.md` | `typescript-vX.Y.Z` | `publish-typescript.yml` | npm package `@genspark/sb-box` → trusted publisher GitHub Actions, repo `genspark-ai/sb-box-sdk`, workflow `publish-typescript.yml`, environment `npm` |
| Rust | `rust/Cargo.toml`, `CHANGELOG.md` | `rust-vX.Y.Z` | `publish-rust.yml` | crates.io crate `genspark-sbbox` → trusted publisher repo `genspark-ai/sb-box-sdk`, workflow `publish-rust.yml`, environment `crates-io` |
| Go | `go/CHANGELOG.md` | `go/vX.Y.Z` | none — Go modules are served from the git tag | none |

Every publish workflow refuses a tag that does not match the version in the source tree.
Create the GitHub environments `pypi`, `npm`, `crates-io` under *Settings → Environments*
(optionally with required reviewers) before the first release.

```bash
git tag python-v0.1.0 && git push origin python-v0.1.0
git tag typescript-v0.1.0 && git push origin typescript-v0.1.0
git tag rust-v0.1.0 && git push origin rust-v0.1.0
git tag go/v0.1.0 && git push origin go/v0.1.0
```
