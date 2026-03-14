# Benchmarks

This document records the Go benchmark results for the core filtering pipelines of the PKGuard. Benchmarks are the authoritative reference for evaluating whether a future change introduces measurable performance regression.

## How to Run

```bash
# Run all benchmarks from the repository root
for mod in common/rules pypi-pkguard npm-pkguard maven-pkguard; do
  (cd $mod && go test -run=^$ -bench=. -benchmem ./...)
done
```

To run a single module with a longer timer for more stable numbers:

```bash
cd pypi-pkguard
go test -run=^$ -bench=. -benchmem -benchtime=5s ./...
```

## Results

**Platform:** AMD Ryzen 7 5800X, 16 threads, Windows 10, Go 1.26  
**Recorded:** March 2026

### Rule Engine (`common/rules`)

The rule engine performs zero allocations per evaluation — all decisions are stack-allocated value types.

| Benchmark                                    | Iterations | ns/op  | B/op | allocs/op |
| -------------------------------------------- | ---------- | ------ | ---- | --------- |
| `EvaluatePackage` — allowed (trusted list)   | 63 544 400 | 53.75  | 0    | 0         |
| `EvaluatePackage` — denied (namespace block) | 58 603 765 | 60.88  | 0    | 0         |
| `EvaluateVersion` — stable, old enough       | 12 013 791 | 296    | 0    | 0         |
| `EvaluateVersion` — pre-release (blocked)    | 15 559 748 | 231    | 0    | 0         |
| `EvaluateVersion` — too new (age blocked)    | 10 596 138 | 301    | 0    | 0         |
| `EvaluateVersion` × 10 versions (bulk)       | 1 274 497  | 2 812  | 0    | 0         |
| `EvaluateVersion` × 50 versions (bulk)       | 256 759    | 14 474 | 0    | 0         |
| `EvaluateVersion` × 200 versions (bulk)      | 64 147     | 55 920 | 0    | 0         |

**Key insight:** evaluating 200 versions per metadata request costs ~56 µs. At 1 000 concurrent CI requests per second, rule evaluation contributes less than **56 ms of total CPU time per second** — negligible compared to upstream network latency.

---

### PyPI JSON Filter (`pypi-pkguard`)

Includes JSON unmarshal + rule evaluation + JSON re-marshal for the `/pypi/<pkg>/json` endpoint.

| Benchmark                | Versions | ns/op   | Throughput | B/op    | allocs/op |
| ------------------------ | -------- | ------- | ---------- | ------- | --------- |
| `FilterPyPIJSONResponse` | 10       | 48 004  | 33.3 MB/s  | 11 793  | 160       |
| `FilterPyPIJSONResponse` | 50       | 229 284 | 34.7 MB/s  | 52 666  | 644       |
| `FilterPyPIJSONResponse` | 200      | 914 218 | 35.1 MB/s  | 208 967 | 2 449     |

**Key insight:** Filtering a package with 200 releases takes ~914 µs. The throughput is stable at ~35 MB/s regardless of version count, meaning the bottleneck is JSON parsing rather than rule evaluation. A typical PyPI package has 20–80 releases; the proxy adds **~50–230 µs** of overhead per uncached metadata request.

---

### npm Packument Filter (`npm-pkguard`)

Includes JSON unmarshal + rule evaluation + JSON re-marshal + tarball URL rewriting for the `/<pkg>` endpoint.

| Benchmark            | Versions | ns/op   | Throughput | B/op    | allocs/op |
| -------------------- | -------- | ------- | ---------- | ------- | --------- |
| `FilterNpmPackument` | 10       | 35 477  | 49.3 MB/s  | 11 014  | 184       |
| `FilterNpmPackument` | 50       | 173 704 | 49.7 MB/s  | 50 631  | 792       |
| `FilterNpmPackument` | 200      | 691 893 | 50.3 MB/s  | 203 506 | 3 051     |

**Key insight:** npm packument filtering throughput is ~50 MB/s. A large package like `lodash` with 200+ versions is filtered in ~692 µs. The proxy adds **~35–700 µs** per uncached packument. This is dwarfed by network round-trip time to `registry.npmjs.org` (~100–300 ms over WAN).

---

### Maven Metadata Filter (`maven-pkguard`)

Includes XML parse + rule evaluation + XML re-serialisation for the `maven-metadata.xml` endpoint.

| Benchmark                     | Versions | ns/op   | Throughput | B/op    | allocs/op |
| ----------------------------- | -------- | ------- | ---------- | ------- | --------- |
| `ParseAndFilterMavenMetadata` | 10       | 36 918  | 14.3 MB/s  | 20 552  | 381       |
| `ParseAndFilterMavenMetadata` | 50       | 134 164 | 14.3 MB/s  | 58 584  | 1 278     |
| `ParseAndFilterMavenMetadata` | 200      | 525 286 | 13.9 MB/s  | 208 377 | 4 633     |

**Key insight:** XML parsing is ~2.5× slower than JSON parsing at equivalent payload sizes (~14 MB/s vs ~35 MB/s). Maven `maven-metadata.xml` files are typically small (5–50 versions); at 50 versions the overhead is **~134 µs**, well within acceptable bounds for CI build tooling.

---

## Performance Regression Gate

Any code change that increases the `ns/op` of a benchmark by **more than 20%** relative to the values above must be accompanied by a documented justification in the PR description. Re-run the full benchmark suite and update this file with the new baseline numbers when:

- A new filtering code path is introduced.
- A dependency update touches JSON or XML parsing performance.
- A rule engine change alters the per-version evaluation cost.
