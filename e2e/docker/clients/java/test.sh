#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# E2E tests: real Maven / Gradle dependency resolution through the Maven PKGuard.
# Expects MAVEN_PROXY_URL to be set (e.g. http://maven-proxy:8080).
#
# Rule coverage:
#   allow-all baseline             — Tests 1-9
#   min_package_age_days (deny)    — Tests 10-11  (Maven range, Gradle dynamic)
#   min_package_age_days (pass)    — Tests 12-13  (Maven exact, Gradle exact)
#   block_snapshots (deny)         — Test 14 (curl artifact request returns 403)
#   block_snapshots (pass)         — Test 15 (stable artifact returns 200)
#   block_pre_release (deny)       — Test 16 (curl artifact 4.13-beta-3 returns 403)
#   block_pre_release (pass)       — Test 17 (stable artifact returns 200)
#   explicit deny (deny)           — Test 18 (Maven build with junit:junit fails)
#   explicit deny (pass)           — Test 19 (commons-io passes through)
#   global defaults (deny)         — Test 20 (junit range blocked by global age)
#   global defaults (pass)         — Test 21 (commons-io bypasses global age)
#   version_patterns deny          — Test 22 (curl artifact matching -beta returns 403)
#   version_patterns pass          — Test 23 (stable artifact returns 200)
#   real-life config pass          — Tests 24-26 (multi-rule production config)
#   real-life config deny          — Tests 27-30 (multi-rule production config)

set -euo pipefail

PASS=0
FAIL=0
TESTS=0
E2E_PHASE="${E2E_PHASE:-full}"

pass() { PASS=$((PASS + 1)); TESTS=$((TESTS + 1)); echo "  PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); TESTS=$((TESTS + 1)); echo "  FAIL: $1"; }
phase_enabled() { [ "${E2E_PHASE}" = "full" ] || [ "${E2E_PHASE}" = "$1" ]; }

echo "============================================"
echo " Maven/Gradle Client E2E Tests"
echo " Proxy: ${MAVEN_PROXY_URL}"
echo " Phase: ${E2E_PHASE}"
echo "============================================"

# ─── Wait for proxy to be healthy ──────────────────────────────────────────────

echo ""
echo "Waiting for Maven proxy to become healthy..."
for i in $(seq 1 30); do
    if curl -sf "${MAVEN_PROXY_URL}/healthz" > /dev/null 2>&1; then
        echo "Proxy is healthy."
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "FATAL: Proxy did not become healthy within 60s"
        exit 1
    fi
    sleep 2
done

wait_for_proxy() {
        local base_url=$1
        local label=$2
        echo ""
        echo "Waiting for ${label} to become healthy..."
        for i in $(seq 1 30); do
                if curl -sf "${base_url}/healthz" > /dev/null 2>&1; then
                        echo "${label} is healthy."
                        return 0
                fi
                if [ "$i" -eq 30 ]; then
                        echo "FATAL: ${label} did not become healthy within 60s"
                        return 1
                fi
                sleep 2
        done
}

write_maven_settings() {
        local proxy_url=$1
        local out_file=$2
        cat > "${out_file}" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<settings xmlns="http://maven.apache.org/SETTINGS/1.2.0"
                    xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
                    xsi:schemaLocation="http://maven.apache.org/SETTINGS/1.2.0
                                                            http://maven.apache.org/xsd/settings-1.2.0.xsd">
    <mirrors>
        <mirror>
            <id>pkguard-proxy</id>
            <mirrorOf>*</mirrorOf>
            <url>${proxy_url}</url>
        </mirror>
    </mirrors>
</settings>
EOF
}

# ─── Maven settings.xml that routes all traffic through the proxy ──────────────

MAVEN_SETTINGS="/tmp/e2e-settings.xml"
write_maven_settings "${MAVEN_PROXY_URL}" "${MAVEN_SETTINGS}"

LOCAL_REPO="/tmp/mvn-repo"

if phase_enabled "baseline"; then

# ─── Test 1: Maven dependency:resolve ─────────────────────────────────────────

echo ""
echo "--- Test: mvn dependency:resolve (junit, commons-io, guava) ---"
cp -r /projects/maven-test /tmp/maven-test
cd /tmp/maven-test
if mvn -s "${MAVEN_SETTINGS}" -Dmaven.repo.local="${LOCAL_REPO}" \
       dependency:resolve -B -q 2>&1; then
    pass "mvn dependency:resolve"
else
    fail "mvn dependency:resolve"
fi

# ─── Test 2: Verify specific JARs downloaded ──────────────────────────────────

echo ""
echo "--- Test: junit-4.13.2.jar exists in local repo ---"
if [ -f "${LOCAL_REPO}/junit/junit/4.13.2/junit-4.13.2.jar" ]; then
    pass "junit-4.13.2.jar downloaded"
else
    fail "junit-4.13.2.jar not found"
fi

echo ""
echo "--- Test: commons-io-2.14.0.jar exists in local repo ---"
if [ -f "${LOCAL_REPO}/commons-io/commons-io/2.14.0/commons-io-2.14.0.jar" ]; then
    pass "commons-io-2.14.0.jar downloaded"
else
    fail "commons-io-2.14.0.jar not found"
fi

echo ""
echo "--- Test: guava-32.1.3-jre.jar exists in local repo ---"
if [ -f "${LOCAL_REPO}/com/google/guava/guava/32.1.3-jre/guava-32.1.3-jre.jar" ]; then
    pass "guava-32.1.3-jre.jar downloaded"
else
    fail "guava-32.1.3-jre.jar not found"
fi

# ─── Test 3: Maven POM download ──────────────────────────────────────────────

echo ""
echo "--- Test: junit POM downloaded ---"
if [ -f "${LOCAL_REPO}/junit/junit/4.13.2/junit-4.13.2.pom" ]; then
    pass "junit-4.13.2.pom downloaded"
else
    fail "junit-4.13.2.pom not found"
fi

# ─── Test 4: Maven metadata accessible via curl ───────────────────────────────

echo ""
echo "--- Test: maven-metadata.xml accessible ---"
status=$(curl -s -o /dev/null -w "%{http_code}" "${MAVEN_PROXY_URL}/junit/junit/maven-metadata.xml")
if [ "$status" = "200" ]; then
    pass "maven-metadata.xml returns 200"
else
    fail "maven-metadata.xml returned $status"
fi

# ─── Test 5: Gradle dependency resolution ─────────────────────────────────────

echo ""
echo "--- Test: gradle dependencies (junit, commons-io) ---"
cp -r /projects/gradle-test /tmp/gradle-test
cd /tmp/gradle-test
export MAVEN_PROXY_URL
if gradle dependencies --no-daemon -q 2>&1; then
    pass "gradle dependencies"
else
    fail "gradle dependencies"
fi

# ─── Test 6: Gradle build (compile classpath resolution) ──────────────────────

echo ""
echo "--- Test: gradle build ---"
cd /tmp/gradle-test
if gradle build --no-daemon -q 2>&1; then
    pass "gradle build"
else
    fail "gradle build"
fi

# ─── Test 7: healthz and metrics endpoints ────────────────────────────────────

echo ""
echo "--- Test: proxy healthz endpoint ---"
status=$(curl -s -o /dev/null -w "%{http_code}" "${MAVEN_PROXY_URL}/healthz")
if [ "$status" = "200" ]; then
    pass "healthz returns 200"
else
    fail "healthz returned $status"
fi

echo ""
echo "--- Test: proxy metrics endpoint ---"
metrics=$(curl -sf "${MAVEN_PROXY_URL}/metrics" 2>&1)
if echo "$metrics" | grep -q "requests_total"; then
    pass "metrics endpoint returns request counters"
else
    fail "metrics endpoint missing request counters"
fi
fi

# ─── Test 10+: minimum-age policy checks (real clients, pass + fail) ─────────

AGE_BLOCK_URL="${MAVEN_AGE_BLOCK_PROXY_URL:-}"
AGE_PINNED_URL="${MAVEN_AGE_PINNED_PROXY_URL:-}"

if phase_enabled "min-age-block" && [ -n "${AGE_BLOCK_URL}" ]; then
    wait_for_proxy "${AGE_BLOCK_URL}" "Maven age-block proxy"
    AGE_BLOCK_SETTINGS="/tmp/e2e-age-block-settings.xml"
    write_maven_settings "${AGE_BLOCK_URL}" "${AGE_BLOCK_SETTINGS}"

    echo ""
    echo "--- Test: min-age block denies Maven range [4.0,5.0) ---"
    mkdir -p /tmp/maven-age-block-range
    cat > /tmp/maven-age-block-range/pom.xml <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
         xsi:schemaLocation="http://maven.apache.org/POM/4.0.0 http://maven.apache.org/xsd/maven-4.0.0.xsd">
  <modelVersion>4.0.0</modelVersion>
    <groupId>com.pkguard.e2e</groupId>
  <artifactId>maven-age-range</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>junit</groupId>
      <artifactId>junit</artifactId>
      <version>[4.0,5.0)</version>
    </dependency>
  </dependencies>
</project>
EOF
    cd /tmp/maven-age-block-range
    if mvn -s "${AGE_BLOCK_SETTINGS}" -Dmaven.repo.local="/tmp/mvn-repo-age-block-range" \
           dependency:resolve -B -q 2>&1; then
        fail "min-age block denies Maven range [4.0,5.0) (unexpectedly resolved)"
    else
        pass "min-age block denies Maven range [4.0,5.0)"
    fi

    echo ""
    echo "--- Test: min-age block denies Gradle dynamic 4.+ ---"
    mkdir -p /tmp/gradle-age-block-dynamic
    cat > /tmp/gradle-age-block-dynamic/build.gradle <<EOF
plugins {
    id 'java'
}
repositories {
    maven {
        url = System.getenv('MAVEN_PROXY_URL')
        allowInsecureProtocol = true
    }
}
dependencies {
    implementation 'junit:junit:4.+'
}
EOF
    cd /tmp/gradle-age-block-dynamic
    gradle_out=$(MAVEN_PROXY_URL="${AGE_BLOCK_URL}" gradle dependencies --no-daemon -q 2>&1 || true)
    if echo "${gradle_out}" | grep -q "junit:junit:4.+ FAILED"; then
        pass "min-age block denies Gradle dynamic 4.+"
    else
        echo "${gradle_out}"
        fail "min-age block denies Gradle dynamic 4.+ (dynamic version did not fail)"
    fi
fi

if phase_enabled "min-age-pinned" && [ -n "${AGE_PINNED_URL}" ]; then
    wait_for_proxy "${AGE_PINNED_URL}" "Maven age-pinned proxy"
    AGE_PINNED_SETTINGS="/tmp/e2e-age-pinned-settings.xml"
    write_maven_settings "${AGE_PINNED_URL}" "${AGE_PINNED_SETTINGS}"

    echo ""
    echo "--- Test: min-age + pinned allows exact Maven junit:4.13.2 ---"
    cp -r /projects/maven-test /tmp/maven-age-pinned-test
    cd /tmp/maven-age-pinned-test
    if mvn -s "${AGE_PINNED_SETTINGS}" -Dmaven.repo.local="/tmp/mvn-repo-age-pinned" \
           dependency:resolve -B -q 2>&1; then
        pass "min-age + pinned allows exact Maven junit:4.13.2"
    else
        fail "min-age + pinned allows exact Maven junit:4.13.2"
    fi

    echo ""
    echo "--- Test: min-age + pinned allows Gradle exact 4.13.2 ---"
    mkdir -p /tmp/gradle-age-pinned-exact
    cat > /tmp/gradle-age-pinned-exact/build.gradle <<EOF
plugins {
    id 'java'
}
repositories {
    maven {
        url = System.getenv('MAVEN_PROXY_URL')
        allowInsecureProtocol = true
    }
}
dependencies {
    implementation 'junit:junit:4.13.2'
}
EOF
    cd /tmp/gradle-age-pinned-exact
    if MAVEN_PROXY_URL="${AGE_PINNED_URL}" gradle dependencies --no-daemon -q 2>&1; then
        pass "min-age + pinned allows Gradle exact 4.13.2"
    else
        fail "min-age + pinned allows Gradle exact 4.13.2"
    fi
fi

# ─── Tests 14-15: block_snapshots rule ───────────────────────────────────────
# junit:junit:4.13.2-SNAPSHOT does not exist on Maven Central.
# The proxy's handleArtifact function calls EvaluateVersion with the version string;
# IsSnapshot detects "-SNAPSHOT" suffix and the rule blocks the request with 403.
# junit:junit:4.13.2 (stable) must pass through with 200.

BLOCK_SNAPS_URL="${MAVEN_BLOCK_SNAPSHOTS_PROXY_URL:-}"

if phase_enabled "block-snapshots" && [ -n "${BLOCK_SNAPS_URL}" ]; then
    wait_for_proxy "${BLOCK_SNAPS_URL}" "Maven block-snapshots proxy"

    echo ""
    echo "--- Test: block_snapshots denies junit:junit:4.13.2-SNAPSHOT (curl artifact) ---"
    snap_status=$(curl -s -o /dev/null -w "%{http_code}" \
        "${BLOCK_SNAPS_URL}/junit/junit/4.13.2-SNAPSHOT/junit-4.13.2-SNAPSHOT.jar")
    if [ "$snap_status" = "403" ]; then
        pass "block_snapshots denies junit:junit:4.13.2-SNAPSHOT"
    else
        fail "block_snapshots denies junit:junit:4.13.2-SNAPSHOT (got HTTP $snap_status)"
    fi

    echo ""
    echo "--- Test: block_snapshots allows junit:junit:4.13.2 (stable, curl artifact) ---"
    stable_status=$(curl -s -o /dev/null -w "%{http_code}" \
        "${BLOCK_SNAPS_URL}/junit/junit/4.13.2/junit-4.13.2.jar")
    if [ "$stable_status" = "200" ]; then
        pass "block_snapshots allows junit:junit:4.13.2"
    else
        fail "block_snapshots allows junit:junit:4.13.2 (got HTTP $stable_status)"
    fi
fi

# ─── Tests 16-17: block_pre_release rule ─────────────────────────────────────
# junit:junit:4.13-beta-3 exists on Maven Central (verified HTTP 200 on real central).
# The proxy's handleArtifact calls IsPreRelease("4.13-beta-3") → true → 403.
# junit:junit:4.13.2 (stable) must pass through with 200.

BLOCK_PRERELEASE_URL="${MAVEN_BLOCK_PRERELEASE_PROXY_URL:-}"

if phase_enabled "prerelease" && [ -n "${BLOCK_PRERELEASE_URL}" ]; then
    wait_for_proxy "${BLOCK_PRERELEASE_URL}" "Maven block-prerelease proxy"

    echo ""
    echo "--- Test: block_pre_release denies junit:junit:4.13-beta-3 (curl artifact) ---"
    beta_status=$(curl -s -o /dev/null -w "%{http_code}" \
        "${BLOCK_PRERELEASE_URL}/junit/junit/4.13-beta-3/junit-4.13-beta-3.jar")
    if [ "$beta_status" = "403" ]; then
        pass "block_pre_release denies junit:junit:4.13-beta-3"
    else
        fail "block_pre_release denies junit:junit:4.13-beta-3 (got HTTP $beta_status)"
    fi

    echo ""
    echo "--- Test: block_pre_release allows junit:junit:4.13.2 (stable, curl artifact) ---"
    stable_status=$(curl -s -o /dev/null -w "%{http_code}" \
        "${BLOCK_PRERELEASE_URL}/junit/junit/4.13.2/junit-4.13.2.jar")
    if [ "$stable_status" = "200" ]; then
        pass "block_pre_release allows junit:junit:4.13.2"
    else
        fail "block_pre_release allows junit:junit:4.13.2 (got HTTP $stable_status)"
    fi
fi

# ─── Tests 18-19: explicit deny rule (action: deny) ──────────────────────────
# Config: action: deny for junit:junit.
# Maven metadata request for junit should return 403; commons-io (unblocked) should return 200.
# Use curl for metadata checks + Maven build for the pass scenario.

EXPLICIT_DENY_URL="${MAVEN_EXPLICIT_DENY_PROXY_URL:-}"

if phase_enabled "explicit-deny" && [ -n "${EXPLICIT_DENY_URL}" ]; then
    wait_for_proxy "${EXPLICIT_DENY_URL}" "Maven explicit-deny proxy"
    EXPLICIT_DENY_SETTINGS="/tmp/explicit-deny-settings.xml"
    write_maven_settings "${EXPLICIT_DENY_URL}" "${EXPLICIT_DENY_SETTINGS}"

    echo ""
    echo "--- Test: explicit deny blocks junit:junit (Maven build fails) ---"
    mkdir -p /tmp/maven-explicit-deny-fail
    cat > /tmp/maven-explicit-deny-fail/pom.xml <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
         xsi:schemaLocation="http://maven.apache.org/POM/4.0.0 http://maven.apache.org/xsd/maven-4.0.0.xsd">
  <modelVersion>4.0.0</modelVersion>
    <groupId>com.pkguard.e2e</groupId>
  <artifactId>explicit-deny-fail</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>junit</groupId>
      <artifactId>junit</artifactId>
      <version>4.13.2</version>
    </dependency>
  </dependencies>
</project>
EOF
    cd /tmp/maven-explicit-deny-fail
    if mvn -s "${EXPLICIT_DENY_SETTINGS}" -Dmaven.repo.local="/tmp/mvn-repo-explicit-deny-fail" \
           dependency:resolve -B -q 2>&1; then
        fail "explicit deny blocks junit:junit (unexpectedly resolved)"
    else
        pass "explicit deny blocks junit:junit"
    fi

    echo ""
    echo "--- Test: explicit deny allows commons-io:2.11.0 (unblocked, Maven build) ---"
    mkdir -p /tmp/maven-explicit-deny-pass
    cat > /tmp/maven-explicit-deny-pass/pom.xml <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
         xsi:schemaLocation="http://maven.apache.org/POM/4.0.0 http://maven.apache.org/xsd/maven-4.0.0.xsd">
  <modelVersion>4.0.0</modelVersion>
    <groupId>com.pkguard.e2e</groupId>
  <artifactId>explicit-deny-pass</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>commons-io</groupId>
      <artifactId>commons-io</artifactId>
      <version>2.11.0</version>
    </dependency>
  </dependencies>
</project>
EOF
    cd /tmp/maven-explicit-deny-pass
    if mvn -s "${EXPLICIT_DENY_SETTINGS}" -Dmaven.repo.local="/tmp/mvn-repo-explicit-deny-pass" \
           dependency:resolve -B -q 2>&1; then
        pass "explicit deny allows commons-io:2.11.0"
    else
        fail "explicit deny allows commons-io:2.11.0 (Maven build failed)"
    fi
fi

# ─── Tests 20-21: global defaults + bypass_age_filter ────────────────────────
# Config: global min_package_age_days:10000 + block_pre_releases:true.
# commons-io has bypass_age_filter:true rule → passes despite global age.
# junit has no rule → blocked by global age.

GLOBAL_DEFAULTS_URL="${MAVEN_GLOBAL_DEFAULTS_PROXY_URL:-}"

if phase_enabled "global-defaults" && [ -n "${GLOBAL_DEFAULTS_URL}" ]; then
    wait_for_proxy "${GLOBAL_DEFAULTS_URL}" "Maven global-defaults proxy"
    GLOBAL_DEFAULTS_SETTINGS="/tmp/global-defaults-settings.xml"
    write_maven_settings "${GLOBAL_DEFAULTS_URL}" "${GLOBAL_DEFAULTS_SETTINGS}"

    echo ""
    echo "--- Test: global defaults block junit (no-rule package, age block) ---"
    mkdir -p /tmp/maven-global-deny
    cat > /tmp/maven-global-deny/pom.xml <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
         xsi:schemaLocation="http://maven.apache.org/POM/4.0.0 http://maven.apache.org/xsd/maven-4.0.0.xsd">
  <modelVersion>4.0.0</modelVersion>
    <groupId>com.pkguard.e2e</groupId>
  <artifactId>global-deny</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>junit</groupId>
      <artifactId>junit</artifactId>
      <version>[4.0,5.0)</version>
    </dependency>
  </dependencies>
</project>
EOF
    cd /tmp/maven-global-deny
    if mvn -s "${GLOBAL_DEFAULTS_SETTINGS}" -Dmaven.repo.local="/tmp/mvn-repo-global-deny" \
           dependency:resolve -B -q 2>&1; then
        fail "global defaults block junit (unexpectedly resolved)"
    else
        pass "global defaults block junit"
    fi

    echo ""
    echo "--- Test: bypass_age_filter allows commons-io metadata despite global age block ---"
    commons_meta="${GLOBAL_DEFAULTS_URL}/commons-io/commons-io/maven-metadata.xml"
    commons_status=$(curl -s -o /tmp/commons-io-metadata.xml -w "%{http_code}" "${commons_meta}")
    if [ "${commons_status}" = "200" ] && grep -q "<version>2.11.0</version>" /tmp/commons-io-metadata.xml; then
        pass "bypass_age_filter allows commons-io metadata despite global age block"
    else
        fail "bypass_age_filter allows commons-io metadata despite global age block (status ${commons_status})"
    fi
fi

# ─── Tests 22-23: version_patterns deny rule ─────────────────────────────────
# Config: deny pattern "(?i)-M\d+$|-RC\d+$|-alpha|-beta".
# junit:junit:4.13-beta-3 matches "-beta" → 403 at artifact handler.
# junit:junit:4.13.2 (stable) does not match → 200.

VERSION_PATTERN_URL="${MAVEN_VERSION_PATTERN_PROXY_URL:-}"

if phase_enabled "version-pattern" && [ -n "${VERSION_PATTERN_URL}" ]; then
    wait_for_proxy "${VERSION_PATTERN_URL}" "Maven version-pattern proxy"

    echo ""
    echo "--- Test: version_patterns denies junit:junit:4.13-beta-3 (curl artifact) ---"
    beta_status=$(curl -s -o /dev/null -w "%{http_code}" \
        "${VERSION_PATTERN_URL}/junit/junit/4.13-beta-3/junit-4.13-beta-3.jar")
    if [ "$beta_status" = "403" ]; then
        pass "version_patterns denies junit:junit:4.13-beta-3"
    else
        fail "version_patterns denies junit:junit:4.13-beta-3 (got HTTP $beta_status)"
    fi

    echo ""
    echo "--- Test: version_patterns allows junit:junit:4.13.2 (stable, curl artifact) ---"
    stable_status=$(curl -s -o /dev/null -w "%{http_code}" \
        "${VERSION_PATTERN_URL}/junit/junit/4.13.2/junit-4.13.2.jar")
    if [ "$stable_status" = "200" ]; then
        pass "version_patterns allows junit:junit:4.13.2"
    else
        fail "version_patterns allows junit:junit:4.13.2 (got HTTP $stable_status)"
    fi
fi

# ─── Tests 24-30: real-life production config ─────────────────────────────────
# A single proxy with multiple rules active simultaneously:
#   - trusted_packages: org/apache/commons:*, commons-io:* (bypass everything)
#   - defaults: min_package_age_days=7, block_pre_releases=true
#   - explicit deny: junit:junit (force JUnit 5 migration)
#   - block_snapshots: true (on all packages)
#   - version_patterns: block -M\d+$, -RC\d+$ (milestones, release candidates)
#
# Package selection rationale:
#   commons-io:commons-io:2.11.0      — trusted commons-io:*, old       → 200
#   commons-lang3:3.14.0              — trusted org/apache/commons:*    → 200
#   commons-collections4:4.4          — trusted org/apache/commons:*    → 200
#   junit:junit:4.13.2                — explicitly denied (legacy)        → 403
#   junit:junit:4.13-beta-3           — denied (explicit deny + pre-rel)  → 403
#   guava:guava:33.0.0-jre SNAPSHOT   — blocked by block_snapshots        → 403
#   mockito-core:5.0-RC1              — blocked by version pattern        → 403

REAL_LIFE_URL="${MAVEN_REAL_LIFE_PROXY_URL:-}"

if phase_enabled "real-life" && [ -n "${REAL_LIFE_URL}" ]; then
    wait_for_proxy "${REAL_LIFE_URL}" "Maven real-life proxy"

    echo ""
    echo "--- Test 24: trusted commons-io artifact passes all rules (real-life) ---"
    ci_status=$(curl -s -o /dev/null -w "%{http_code}" \
        "${REAL_LIFE_URL}/commons-io/commons-io/2.11.0/commons-io-2.11.0.jar")
    if [ "$ci_status" = "200" ]; then
        pass "real-life: trusted commons-io:commons-io:2.11.0 (HTTP 200)"
    else
        fail "real-life: trusted commons-io:commons-io:2.11.0 (got HTTP $ci_status)"
    fi

    echo ""
    echo "--- Test 25: trusted commons-lang3 artifact passes all rules (real-life) ---"
    cl_status=$(curl -s -o /dev/null -w "%{http_code}" \
        "${REAL_LIFE_URL}/org/apache/commons/commons-lang3/3.14.0/commons-lang3-3.14.0.jar")
    if [ "$cl_status" = "200" ]; then
        pass "real-life: trusted commons-lang3:3.14.0 (HTTP 200)"
    else
        fail "real-life: trusted commons-lang3:3.14.0 (got HTTP $cl_status)"
    fi

    echo ""
    echo "--- Test 26: trusted commons-collections4 artifact passes (real-life) ---"
    cc_status=$(curl -s -o /dev/null -w "%{http_code}" \
        "${REAL_LIFE_URL}/org/apache/commons/commons-collections4/4.4/commons-collections4-4.4.jar")
    if [ "$cc_status" = "200" ]; then
        pass "real-life: trusted commons-collections4:4.4 (HTTP 200)"
    else
        fail "real-life: trusted commons-collections4:4.4 (got HTTP $cc_status)"
    fi

    echo ""
    echo "--- Test 27: junit:junit blocked by explicit deny (legacy, real-life) ---"
    junit_status=$(curl -s -o /dev/null -w "%{http_code}" \
        "${REAL_LIFE_URL}/junit/junit/4.13.2/junit-4.13.2.jar")
    if [ "$junit_status" = "403" ]; then
        pass "real-life: junit:junit:4.13.2 blocked (explicit deny)"
    else
        fail "real-life: junit:junit:4.13.2 (got HTTP $junit_status, expected 403)"
    fi

    echo ""
    echo "--- Test 28: junit:junit pre-release also blocked (real-life) ---"
    junit_beta_status=$(curl -s -o /dev/null -w "%{http_code}" \
        "${REAL_LIFE_URL}/junit/junit/4.13-beta-3/junit-4.13-beta-3.jar")
    if [ "$junit_beta_status" = "403" ]; then
        pass "real-life: junit:junit:4.13-beta-3 blocked"
    else
        fail "real-life: junit:junit:4.13-beta-3 (got HTTP $junit_beta_status, expected 403)"
    fi

    echo ""
    echo "--- Test 29: SNAPSHOT version blocked (real-life) ---"
    snap_status=$(curl -s -o /dev/null -w "%{http_code}" \
        "${REAL_LIFE_URL}/com/google/guava/guava/33.0.0-jre-SNAPSHOT/guava-33.0.0-jre-SNAPSHOT.jar")
    if [ "$snap_status" = "403" ]; then
        pass "real-life: guava SNAPSHOT blocked (block_snapshots)"
    else
        fail "real-life: guava SNAPSHOT (got HTTP $snap_status, expected 403)"
    fi

    echo ""
    echo "--- Test 30: milestone version blocked by version pattern (real-life) ---"
    # Use a non-trusted package (mockito is NOT in org/apache/commons:*).
    # The version 5.0-RC1 matches the pattern -RC\d+$ → should be blocked.
    m_status=$(curl -s -o /dev/null -w "%{http_code}" \
        "${REAL_LIFE_URL}/org/mockito/mockito-core/5.0-RC1/mockito-core-5.0-RC1.jar")
    if [ "$m_status" = "403" ]; then
        pass "real-life: mockito-core:5.0-RC1 blocked (version pattern)"
    else
        fail "real-life: mockito-core:5.0-RC1 (got HTTP $m_status, expected 403)"
    fi
fi

# ─── Summary ──────────────────────────────────────────────────────────────────

echo ""
echo "============================================"
echo " Results: ${PASS} passed, ${FAIL} failed (${TESTS} total)"
echo "============================================"

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
exit 0
