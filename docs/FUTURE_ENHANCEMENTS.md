# Future Enhancements

This document outlines potential future enhancements for the Bulwark, aimed at increasing its robustness, observability, and value in an open-source ecosystem.

## 1. Vulnerability and CVE Feed Integration

Currently, the `RuleEngine` operates purely on heuristics such as version age, namespace protection, velocity anomalies, and typosquatting detection. To significantly boost its security value, the proxy should integrate with established threat databases.

- **OSV (Open Source Vulnerabilities) Integration**: Introduce an abstract interface to fetch and cache vulnerability feeds (like the OSV database).
- **CVE-Based Blocking**: Enable the proxy to actively intercept and block specific package versions if they are flagged with known critical or high-severity CVEs.
- **Fail-Closed for Threat Feeds**: For highly regulated compliance environments (like FedRAMP), if the vulnerability feed itself becomes unreachable, block the request rather than falling back to heuristics alone.

## 2. Fail-Mode: Open vs. Closed ✅ Implemented

**Status: Shipped in current release.**

A `fail_mode` configuration field has been added to `policy:` in all three proxies. Set `fail_mode: "closed"` to block any request where metadata cannot be parsed or policy cannot be applied. The default `fail_mode: "open"` preserves the original pass-through behaviour for zero-friction adoption.

```yaml
policy:
  fail_mode: "closed" # recommended for regulated environments (FedRAMP, SOC 2)
```

See [common/config/config.go](../common/config/config.go) for implementation details and each proxy's `config.yaml` for annotated examples.

## 3. Distributed Caching Capabilities

The current single-node, in-memory TTL caching structure (`sync.RWMutex` + `map`) is highly performant but can lead to fragmented cache states when horizontally scaled across multiple Kubernetes pods. The `max_size_mb` configuration field is accepted but **not yet enforced**; the cache grows unbounded (entries are only removed on TTL expiry). Future work includes:

- **Size-based eviction**: Enforce `max_size_mb` with LRU or entry-count limits to bound memory usage under sustained load.

- **Topology A (Direct to Public Upstreams)**: Introduce an optional backend cache interface (e.g., Redis). This would prevent multiple proxy replicas from simultaneously hammering public upstream registries (npmjs.org, pypi.org) when CI pipelines burst under heavy load.
- **Topology B (Enterprise Registries)**: Keep distributed caching strictly optional and off by default. In deployments where the proxy sits alongside an enterprise artifact repository, these solutions carry their own extensive, highly optimized caching layers. The Bulwark does not need to duplicate this effort; it can simply rely on the downstream registry to cache metadata out-of-the-box once inspected and curated.

## 3. Observability and Dashboards

While the proxy exposes a JSON `/metrics` endpoint with atomic counters, standardizing "day-two" operations will significantly improve developer experience and open-source adoption.

- **Grafana Quick-Start**: Ship pre-built Grafana dashboard configurations (e.g., a `grafana-dashboard.json` inside a `dashboards/` or `deploy/observability/` folder).
- **Key Metrics Visualization**: Ensure platform engineers can immediately visualize critical health and security metrics, including:
  - Traffic throughput and proxy latency percentiles.
  - Policy enforcement counts (`dry_run_blocked` instances vs. actively `denied` requests).
  - Proxy cache Hit vs. Miss ratios.
  - Actions taken categorized by the ecosystem and exact rule tripped (e.g., pre-release, age violation, typosquatting).
