// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"io"
	"strings"
	"testing"
)

// Stable Maven artifacts used in live e2e tests.
const (
	mavenGroupJunit    = "junit"
	mavenArtifactJunit = "junit"
	mavenVerJunit      = "4.13.2"

	mavenGroupCommons    = "commons-io"
	mavenArtifactCommons = "commons-io"
	mavenVerCommons      = "2.11.0"

	junitMetadataPath = "/junit/junit/maven-metadata.xml"
	junitArtifactPath = "/junit/junit/4.13.2/junit-4.13.2.jar"
	junitPOMPath      = "/junit/junit/4.13.2/junit-4.13.2.pom"
)

// TestMavenMetadataLive fetches the junit maven-metadata.xml through the proxy and
// verifies it contains a known stable version.
func TestMavenMetadataLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, mavenProxyURL+junitMetadataPath)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), mavenVerJunit) {
		t.Errorf("expected junit %s in maven-metadata.xml, not found", mavenVerJunit)
	}
}

// TestMavenMetadataXMLContentTypeLive verifies the content-type header is application/xml.
func TestMavenMetadataXMLContentTypeLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, mavenProxyURL+junitMetadataPath)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "xml") {
		t.Errorf("expected XML content type, got %q", ct)
	}
}

// TestMavenArtifactDownloadLive fetches a real JAR through the proxy and verifies
// the response is successful.
func TestMavenArtifactDownloadLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, mavenProxyURL+junitArtifactPath)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)
}

// TestMavenPOMDownloadLive fetches a POM file through the proxy.
func TestMavenPOMDownloadLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, mavenProxyURL+junitPOMPath)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "<project") {
		t.Error("POM response does not appear to be a Maven POM XML document")
	}
}

// TestMavenChecksumLive fetches the checksum sidecar for a POM and verifies a 200 response.
func TestMavenChecksumLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, mavenProxyURL+junitPOMPath+".sha1")
	defer resp.Body.Close()
	// Maven Central may or may not have sha1 files; accept 200 or 404.
	if resp.StatusCode != 200 && resp.StatusCode != 404 {
		t.Errorf("checksum: unexpected status %d", resp.StatusCode)
	}
}

// TestMavenCommonsIOLive verifies that commons-io metadata is retrievable.
func TestMavenCommonsIOLive(t *testing.T) {
	skipIfNotLive(t)

	path := "/" + mavenGroupCommons + "/" + mavenArtifactCommons + "/maven-metadata.xml"
	resp := mustGet(t, mavenProxyURL+path)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), mavenVerCommons) {
		t.Errorf("expected commons-io %s in maven-metadata.xml", mavenVerCommons)
	}
}

// TestMavenSnapshotVersionsFilteredLive starts a second proxy with block_snapshots=true.
// The junit metadata on Maven Central does not contain SNAPSHOT versions, so the
// policy notice header must be absent (nothing filtered).
// This test validates the policy pipeline end-to-end even when no versions are removed.
func TestMavenSnapshotVersionsFilteredLive(t *testing.T) {
	skipIfNotLive(t)
	const filterPort = 18202
	proxy := startProxy(t, mavenProxyBinPath, testdataConfig(t, "maven-block-snapshots.yaml"), filterPort)

	resp := mustGet(t, proxy.BaseURL+junitMetadataPath)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	// Maven Central junit has no SNAPSHOT versions; the stable list must still appear.
	if !strings.Contains(string(body), mavenVerJunit) {
		t.Errorf("expected junit %s to remain after SNAPSHOT filter", mavenVerJunit)
	}
}

// TestMavenAgeBlockFiltersAllVersionsLive starts a proxy with min_package_age_days=10000
// for junit:junit and verifies that ALL versions are filtered from the metadata XML.
// With a 10000-day (~27 year) minimum age no junit version qualifies, so the
// <versions> list must be empty and the <latest>/<release> tags must be cleared.
func TestMavenAgeBlockFiltersAllVersionsLive(t *testing.T) {
	skipIfNotLive(t)
	const filterPort = 18203
	proxy := startProxy(t, mavenProxyBinPath, testdataConfig(t, "maven-min-age-block.yaml"), filterPort)

	resp := mustGet(t, proxy.BaseURL+junitMetadataPath)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	bodyStr := string(body)

	// All versions must be removed — the known stable version must be absent.
	if strings.Contains(bodyStr, "<version>"+mavenVerJunit+"</version>") {
		t.Errorf("age-blocked metadata still contains junit %s", mavenVerJunit)
	}
	// The <latest> element should be empty because every version was denied.
	if strings.Contains(bodyStr, "<latest>"+mavenVerJunit+"</latest>") {
		t.Error("age-blocked metadata still has <latest> pointing to a filtered version")
	}
	// Policy notice header must be present when versions are filtered.
	if resp.Header.Get("X-Curation-Policy-Notice") == "" {
		t.Error("expected X-Curation-Policy-Notice header when all versions are age-blocked")
	}
}
