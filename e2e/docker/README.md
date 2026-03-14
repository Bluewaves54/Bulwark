# Docker E2E Tests — Real Client Integration

End-to-end tests that run **real package manager clients** (pip, pip3, uv, npm, yarn, pnpm, Maven, Gradle)
through the curation proxies inside Docker containers. These tests validate the full
request path from client → proxy → upstream registry and back.

The suite covers every rule type across all three ecosystems, with both a **deny path** (rule fires and
blocks the request) and a **pass path** (rule allows the request or does not apply):

| Rule                                         | PyPI | npm | Maven |
| -------------------------------------------- | :--: | :-: | :---: |
| `allow-all` baseline                         |  ✓   |  ✓  |   ✓   |
| `min_package_age_days` deny (unpinned/range) |  ✓   |  ✓  |   ✓   |
| `min_package_age_days` pass (exact pin)      |  ✓   |  ✓  |   ✓   |
| `pinned_versions` bypass age                 |  ✓   |  ✓  |   ✓   |
| `block_pre_release` deny                     |  ✓   |  ✓  |   ✓   |
| `block_pre_release` pass (stable)            |  ✓   |  ✓  |   ✓   |
| `block_snapshots` deny                       |  —   |  —  |   ✓   |
| `block_snapshots` pass (stable)              |  —   |  —  |   ✓   |
| `explicit deny` (action: deny) deny          |  ✓   |  ✓  |   ✓   |
| `explicit deny` pass (unblocked package)     |  ✓   |  ✓  |   ✓   |
| `global defaults` deny (no-rule package)     |  ✓   |  ✓  |   ✓   |
| `bypass_age_filter` pass (exempt package)    |  ✓   |  ✓  |   ✓   |
| `version_patterns` deny (regex match)        |  ✓   |  ✓  |   ✓   |
| `version_patterns` pass (no match)           |  ✓   |  ✓  |   ✓   |
| `install_scripts` deny (has postinstall)     |  —   |  ✓  |   —   |
| `install_scripts` pass (no scripts)          |  —   |  ✓  |   —   |
| `trusted_packages` pass (trusted scope)      |  —   |  ✓  |   —   |
| `trusted_packages` deny (untrusted package)  |  —   |  ✓  |   —   |
| **real-life** multi-rule pass (combined)     |  ✓   |  ✓  |   ✓   |
| **real-life** multi-rule deny (combined)     |  ✓   |  ✓  |   ✓   |

## Prerequisites

- Docker Engine 20.10+ with Docker Compose V2
- Internet access (proxies reach upstream registries: pypi.org, registry.npmjs.org, repo1.maven.org)

## Quick Start

```bash
# Run all tests (Linux/macOS)
cd e2e/docker && ./run.sh

# Run and remove compose-built images afterwards
cd e2e/docker && ./run.sh --cleanup-images

# Run and remove compose-built images plus builder cache
cd e2e/docker && ./run.sh --cleanup-all

# Run all tests (Windows PowerShell)
cd e2e\docker && .\run.ps1

# Run and remove compose-built images afterwards
cd e2e\docker && .\run.ps1 -CleanupImages

# The runner executes phased orchestration (one proxy + one test container at a time)
```

## Run Individual Ecosystems

```bash
# Python only (pip, pip3, uv)
./run.sh --python-only

# Node.js only (npm, yarn, pnpm)
./run.sh --node-only

# Java only (Maven, Gradle)
./run.sh --java-only
```

## Cleanup Behavior

- Default behavior keeps built images and build cache for faster subsequent runs.
- Use `--cleanup-images` (or `-CleanupImages` in PowerShell) to remove images built by this compose project after the run.
- Use `--cleanup-builder-cache` (or `-CleanupBuilderCache`) to prune Docker builder cache.
- Use `--cleanup-all` to enable both image cleanup and builder-cache pruning.

## Architecture

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ Phased runner                                                                │
│                                                                              │
│ Phase N:                                                                     │
│   1) Start one proxy container with one config                               │
│   2) Run one ecosystem test container for that phase                         │
│   3) Tear down and move to next config phase                                 │
│                                                                              │
│ Active at any moment:                                                        │
│   - 1 proxy container                                                        │
│   - 1 test container                                                         │
└──────────────────────────────────────────────────────────────────────────────┘
```

## Test Suites

### Python (test-python)

| #   | Test                            | Client | Rule                 | Path |
| --- | ------------------------------- | ------ | -------------------- | ---- |
| 1   | pip install certifi             | pip    | baseline allow-all   | pass |
| 2   | pip3 install six                | pip3   | baseline allow-all   | pass |
| 3   | pip install urllib3==2.0.7      | pip    | baseline allow-all   | pass |
| 4   | uv pip install idna             | uv     | baseline allow-all   | pass |
| 5   | pip install -r requirements.txt | pip    | baseline allow-all   | pass |
| 6   | PyPI JSON API                   | curl   | baseline allow-all   | pass |
| 7   | pip install python-dateutil     | pip    | baseline allow-all   | pass |
| 8   | healthz + metrics               | curl   | baseline allow-all   | pass |
| 9   | min-age deny unpinned           | pip    | min_package_age_days | deny |
| 10  | min-age deny ranged spec        | pip    | min_package_age_days | deny |
| 11  | min-age pinned allow exact      | pip    | pinned_versions+age  | pass |
| 12  | min-age pinned allow req file   | pip    | pinned_versions+age  | pass |
| 13  | block_pre_release deny rc       | pip    | block_pre_release    | deny |
| 14  | block_pre_release allow stable  | pip    | block_pre_release    | pass |
| 15  | explicit deny blocks package    | pip    | action: deny         | deny |
| 16  | explicit deny allows other      | pip    | action: deny         | pass |
| 17  | global defaults deny no-rule    | pip    | global defaults+age  | deny |
| 18  | bypass_age_filter allow exempt  | pip    | bypass_age_filter    | pass |
| 19  | version_patterns deny match     | pip    | version_patterns     | deny |
| 20  | version_patterns allow no-match | pip    | version_patterns     | pass |
| 21  | real-life: trusted setuptools   | pip    | trusted_packages     | pass |
| 22  | real-life: requests==2.31.0     | pip    | multi-rule (age)     | pass |
| 23  | real-life: certifi old stable   | pip    | multi-rule (age)     | pass |
| 24  | real-life: six old stable       | pip    | multi-rule (all)     | pass |
| 25  | real-life: python3-dateutil     | pip    | explicit deny        | deny |
| 26  | real-life: flask pre-release    | pip    | block_pre_release    | deny |
| 27  | real-life: packaging dev ver    | pip    | version_patterns     | deny |

### Node.js (test-node)

| #   | Test                             | Client | Rule                 | Path |
| --- | -------------------------------- | ------ | -------------------- | ---- |
| 1   | npm install lodash               | npm    | baseline allow-all   | pass |
| 2   | npm install ms@2.1.3             | npm    | baseline allow-all   | pass |
| 3   | npm install @types/node          | npm    | baseline allow-all   | pass |
| 4   | yarn add is-odd                  | yarn   | baseline allow-all   | pass |
| 5   | pnpm add debug                   | pnpm   | baseline allow-all   | pass |
| 6   | npm install from package.json    | npm    | baseline allow-all   | pass |
| 7   | packument JSON                   | curl   | baseline allow-all   | pass |
| 8   | healthz + metrics                | curl   | baseline allow-all   | pass |
| 9   | min-age deny unpinned            | npm    | min_package_age_days | deny |
| 10  | min-age deny caret range         | npm    | min_package_age_days | deny |
| 11  | min-age pinned allow exact       | npm    | pinned_versions+age  | pass |
| 12  | min-age pinned allow manifest    | npm    | pinned_versions+age  | pass |
| 13  | block_pre_release deny rc        | npm    | block_pre_release    | deny |
| 14  | block_pre_release allow stable   | npm    | block_pre_release    | pass |
| 15  | explicit deny blocks ms          | npm    | action: deny         | deny |
| 16  | explicit deny allows lodash      | npm    | action: deny         | pass |
| 17  | global defaults deny no-rule     | npm    | global defaults+age  | deny |
| 18  | bypass_age_filter allow ms       | npm    | bypass_age_filter    | pass |
| 19  | version_patterns deny rc match   | npm    | version_patterns     | deny |
| 20  | version_patterns allow stable    | npm    | version_patterns     | pass |
| 21  | install_scripts deny postinstall | npm    | install_scripts      | deny |
| 22  | install_scripts allow no-script  | npm    | install_scripts      | pass |
| 23  | trusted_packages allow @types    | npm    | trusted_packages     | pass |
| 24  | trusted_packages deny untrusted  | npm    | trusted_packages     | deny |
| 26  | real-life: @types/ms trusted     | npm    | trusted_packages     | pass |
| 27  | real-life: @babel/parser trusted | npm    | trusted_packages     | pass |
| 28  | real-life: lodash@4.17.21 age    | npm    | multi-rule (age)     | pass |
| 29  | real-life: esbuild scripts ok    | npm    | install_scripts      | pass |
| 30  | real-life: ms@2.1.3 all pass     | npm    | multi-rule (all)     | pass |
| 31  | real-life: event-stream denied   | npm    | explicit deny        | deny |
| 32  | real-life: react@rc pre-release  | npm    | block_pre_release    | deny |
| 33  | real-life: bcrypt scripts        | npm    | install_scripts      | deny |

### Java (test-java)

| #   | Test                              | Client | Rule                 | Path |
| --- | --------------------------------- | ------ | -------------------- | ---- |
| 1   | mvn dependency:resolve            | Maven  | baseline allow-all   | pass |
| 2   | junit JAR downloaded              | Maven  | baseline allow-all   | pass |
| 3   | commons-io JAR downloaded         | Maven  | baseline allow-all   | pass |
| 4   | guava JAR downloaded              | Maven  | baseline allow-all   | pass |
| 5   | junit POM downloaded              | Maven  | baseline allow-all   | pass |
| 6   | maven-metadata.xml                | curl   | baseline allow-all   | pass |
| 7   | gradle dependencies               | Gradle | baseline allow-all   | pass |
| 8   | gradle build                      | Gradle | baseline allow-all   | pass |
| 9   | healthz + metrics                 | curl   | baseline allow-all   | pass |
| 10  | min-age deny Maven range          | Maven  | min_package_age_days | deny |
| 11  | min-age deny Gradle dynamic       | Gradle | min_package_age_days | deny |
| 12  | min-age pinned allow Maven exact  | Maven  | pinned_versions+age  | pass |
| 13  | min-age pinned allow Gradle exact | Gradle | pinned_versions+age  | pass |
| 14  | block_snapshots deny SNAPSHOT     | curl   | block_snapshots      | deny |
| 15  | block_snapshots allow stable      | curl   | block_snapshots      | pass |
| 16  | block_pre_release deny beta       | curl   | block_pre_release    | deny |
| 17  | block_pre_release allow stable    | curl   | block_pre_release    | pass |
| 18  | explicit deny blocks junit        | Maven  | action: deny         | deny |
| 19  | explicit deny allows commons-io   | Maven  | action: deny         | pass |
| 20  | global defaults deny no-rule      | Maven  | global defaults+age  | deny |
| 21  | bypass_age_filter allow metadata  | curl   | bypass_age_filter    | pass |
| 22  | version_patterns deny beta match  | curl   | version_patterns     | deny |
| 23  | version_patterns allow stable     | curl   | version_patterns     | pass |
| 24  | real-life: commons-io trusted     | curl   | trusted_packages     | pass |
| 25  | real-life: commons-lang3 trusted  | curl   | trusted_packages     | pass |
| 26  | real-life: commons-col4 trusted   | curl   | trusted_packages     | pass |
| 27  | real-life: junit denied (legacy)  | curl   | explicit deny        | deny |
| 28  | real-life: junit beta denied      | curl   | explicit deny+pre    | deny |
| 29  | real-life: guava SNAPSHOT denied  | curl   | block_snapshots      | deny |
| 30  | real-life: mockito RC pattern     | curl   | version_patterns     | deny |

## Cleanup

```bash
cd e2e/docker && docker compose down --remove-orphans --rmi local
```

## Troubleshooting

- **Proxy not healthy:** Check proxy logs with `docker compose logs pypi-proxy`
- **Timeout errors:** Upstream registries may be slow; increase `timeout_seconds` in configs
- **Build failures:** Ensure Docker has internet access and sufficient disk space
- **Windows line endings:** The `.gitattributes` file enforces LF for shell scripts
