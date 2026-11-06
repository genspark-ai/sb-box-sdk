# SB-Box SDKs

Official client libraries for the SB-Box sandbox API (`https://www.genspark.ai/api/sb-box/v1`).
All four expose the same surface and are published independently:

| Language | Package | Directory |
|---|---|---|
| Python 3.9+ | `pip install genspark-sbbox` | [`python/`](python/) |
| TypeScript / Node 18+ | `npm install @genspark/sb-box` | [`typescript/`](typescript/) |
| Go 1.21+ | `go get github.com/genspark-ai/sb-box-sdk/go` | [`go/`](go/) |
| Rust (tokio) | `cargo add genspark-sbbox` | [`rust/`](rust/) |

Every SDK reads the key from `SBBOX_API_KEY` (or `GSK_API_KEY`) and the base URL from
`SBBOX_BASE_URL`; lifetimes are seconds; killed sandboxes answer 404.

- `API.md` — the REST contract the SDKs implement (condensed from the public user guide).
- `DESIGN.md` — the shared surface, error model, retry policy and test matrix.

Each package has its own README, offline test suite and `examples/smoke.*` that exercises a real
environment end to end (create → exec → files → run code → freeze → resume → kill).
