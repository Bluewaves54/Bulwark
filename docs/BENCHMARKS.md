# Benchmarks

This document records the Go benchmark results for the core filtering pipelines of the Bulwark. Benchmarks are the authoritative reference for evaluating whether a future change introduces measurable performance regression.

## How to Run

```bash
# Run all benchmarks from the repository root
for mod in common/rules pypi-bulwark npm-bulwark maven-bulwark; do
  (cd $mod && go test -run=^$ -bench=. -benchmem ./...)
done
```

To run a single module with a longer timer for more stable numbers:

```bash
cd pypi-bulwark
go test -run=^$ -bench=. -benchmem -benchtime=5s ./...
```

## Results

**Platform:** Apple M4 Pro, 12 cores, macOS, Go 1.26  
**Recorded:** March 2026

### Rule Engine (`common/rules`)

The rule engine performs zero allocations per evaluation — all decisions are stack-allocated value types.

| Benchmark                                    | Iterations | ns/op  | B/op | allocs/op |
| -------------------------------------------- | ---------- | ------ | ---- | --------- |
| `EvaluatePackage` — allowed (trusted list)   | 61 398 913 | 52.23  | 0    | 0         |
| `EvaluatePackage` — denied (namespace block) | 53 134 438 | 64.95  | 0    | 0         |
| `EvaluateVersion` — stable, old enough       | 10 485 553 | 334.6  | 0    | 0         |
| `EvaluateVersion` — pre-release (blocked)    | 15 716 848 | 230.3  | 0    | 0         |
| `EvaluateVersion` — too new (age blocked)    | 10 566 804 | 334.9  | 0    | 0         |
| `EvaluateVersion` × 10 versions (bulk)       | 1 000 000  | 3 175  | 0    | 0         |
| `EvaluateVersion` × 50 versions (bulk)       | 226 284    | 15 927 | 0    | 0         |
| `EvaluateVersion` × 200 versions (bulk)      | 56 712     | 62 798 | 0    | 0         |

**Key insight:** evaluating 200 versions per metadata request costs ~63 µs. At 1 000 concurrent CI requests per second, rule evaluation contributes less than **63 ms of total CPU time per second** — negligible compared to upstream network latency.

---

### PyPI JSON Filter (`pypi-bulwark`)

Includes JSON unmarshal + rule evaluation + JSON re-marshal for the `/pypi/<pkg>/json` endpoint.

| Benchmark                | Versions | ns/op   | Throughput | B/op    | allocs/op |
| ------------------------ | -------- | ------- | ---------- | ------- | --------- |
| `FilterPyPIJSONResponse` | 10       | 44 909  | 35.6 MB/s  | 11 787  | 160       |
| `FilterPyPIJSONResponse` | 50       | 216 453 | 36.8 MB/s  | 52 615  | 644       |
| `FilterPyPIJSONResponse` | 200      | 859 551 | 37.4 MB/s  | 207 680 | 2 448     |

**Key insight:** Filtering a package with 200 releases takes ~860 µs. The throughput is stable at ~36 MB/s regardless of version count, meaning the bottleneck is JSON parsing rather than rule evaluation. A typical PyPI package has 20–80 releases; the proxy adds **~45–216 µs** of filter overhead per uncached metadata request.

---

### npm Packument Filter (`npm-bulwark`)

Includes JSON unmarshal + rule evaluation + JSON re-marshal + tarball URL rewriting for the `/<pkg>` endpoint.

| Benchmark            | Versions | ns/op   | Throughput | B/op    | allocs/op |
| -------------------- | -------- | ------- | ---------- | ------- | --------- |
| `FilterNpmPackument` | 10       | 34 185  | 51.2 MB/s  | 11 970  | 204       |
| `FilterNpmPackument` | 50       | 166 114 | 52.0 MB/s  | 55 400  | 892       |
| `FilterNpmPackument` | 200      | 670 228 | 52.0 MB/s  | 222 482 | 3 450     |

**Key insight:** npm packument filtering throughput is ~52 MB/s. A large package like `lodash` with 200+ versions is filtered in ~670 µs. The proxy adds **~34–670 µs** of filter overhead per uncached packument. This is dwarfed by network round-trip time to `registry.npmjs.org` (~100–300 ms over WAN).

---

### Maven Metadata Filter (`maven-bulwark`)

Includes XML parse + rule evaluation + XML re-serialisation for the `maven-metadata.xml` endpoint.

| Benchmark                     | Versions | ns/op   | Throughput | B/op    | allocs/op |
| ----------------------------- | -------- | ------- | ---------- | ------- | --------- |
| `ParseAndFilterMavenMetadata` | 10       | 32 271  | 16.3 MB/s  | 20 552  | 381       |
| `ParseAndFilterMavenMetadata` | 50       | 114 484 | 16.8 MB/s  | 58 584  | 1 278     |
| `ParseAndFilterMavenMetadata` | 200      | 428 137 | 17.0 MB/s  | 208 377 | 4 633     |

**Key insight:** XML parsing is ~2.2× slower than JSON parsing at equivalent payload sizes (~17 MB/s vs ~37 MB/s). Maven `maven-metadata.xml` files are typically small (5–50 versions); at 50 versions the overhead is **~114 µs**, well within acceptable bounds for CI build tooling.

---

### End-to-End HTTP Proxy Latency

These benchmarks measure the **full round-trip through the proxy**: HTTP accept → upstream fetch → filter/cache → HTTP response. The mock upstream has near-zero latency, so the numbers represent **pure proxy-added overhead** — the extra time your installs pay versus talking to the registry directly.

All tests use 50 versions per package with pre-release filtering enabled.

| Proxy | Scenario | ns/op | ms/op | B/op | allocs/op |
| ----- | -------- | ----- | ----- | ---- | --------- |
| npm   | Uncached (first fetch + filter) | 367 879 | **0.37** | 89 087 | 1 077 |
| npm   | Cached (subsequent fetches) | 88 717 | **0.09** | 6 222 | 82 |
| PyPI  | Uncached (first fetch + filter) | 454 467 | **0.45** | 84 228 | 821 |
| PyPI  | Cached (subsequent fetches) | 82 865 | **0.08** | 6 142 | 77 |
| Maven | Uncached (first fetch + filter) | 299 335 | **0.30** | 77 097 | 1 446 |
| Maven | Cached (subsequent fetches) | 81 344 | **0.08** | 6 281 | 78 |

**Key insights:**

- **Uncached (worst case):** The proxy adds **0.3–0.5 ms** of overhead per metadata request. This is the cost for the first request to a package (upstream fetch + parse + filter + cache store).
- **Cached (typical case):** After the first fetch, subsequent requests are served from cache in **~0.08–0.09 ms** (80–90 µs). This is the common path in CI builds where multiple packages share dependencies.
- **In real-world terms:** A `npm install` that resolves 200 unique packages pays ~0.5 ms × 200 = **~100 ms total proxy overhead** on a cold cache, or ~0.09 ms × 200 = **~18 ms** on a warm cache. For comparison, a single DNS lookup costs 10–50 ms, and one WAN round-trip to `registry.npmjs.org` costs 100–300 ms.
- **Tarball/artifact downloads** are pass-through (no filtering) — the proxy adds only HTTP forwarding latency (~0.1 ms).

---

## Performance Regression Gate

Any code change that increases the `ns/op` of a benchmark by **more than 20%** relative to the values above must be accompanied by a documented justification in the PR description. Re-run the full benchmark suite and update this file with the new baseline numbers when:

- A new filtering code path is introduced.
- A dependency update touches JSON or XML parsing performance.
- A rule engine change alters the per-version evaluation cost.
