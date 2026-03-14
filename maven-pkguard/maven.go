// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"PKGuard/common/config"
	"PKGuard/common/rules"
)

// Package-level string constants to satisfy SonarQube duplicate-literal rules.
const (
	hdrContentType      = "Content-Type"
	mimeJSON            = "application/json"
	mimeXML             = "application/xml"
	errFmtParseMetadata = "parsing maven-metadata.xml: %w"
	blockReasonAllVer   = "all available versions blocked by policy"
)

// handleHealth returns 200 OK for liveness checks.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set(hdrContentType, mimeJSON)
	fmt.Fprint(w, `{"status":"ok"}`)
}

// handleReady probes the upstream and returns 200 if reachable, 503 otherwise.
func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	req, err := http.NewRequest(http.MethodGet, s.cfg.Upstream.URL, nil)
	if err != nil {
		http.Error(w, `{"status":"error"}`, http.StatusServiceUnavailable)
		return
	}
	resp, err := s.upstream.Do(req)
	if err != nil || resp.StatusCode >= 500 {
		if resp != nil {
			resp.Body.Close()
		}
		http.Error(w, `{"status":"error"}`, http.StatusServiceUnavailable)
		return
	}
	resp.Body.Close()
	w.Header().Set(hdrContentType, mimeJSON)
	fmt.Fprint(w, `{"status":"ok"}`)
}

// handleMetrics returns JSON-encoded atomic counters.
func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set(hdrContentType, mimeJSON)
	fmt.Fprintf(w, `{"requests_total":%d,"requests_allowed":%d,"requests_denied":%d,"requests_dry_run":%d}`,
		s.reqTotal.Load(), s.reqAllowed.Load(), s.reqDenied.Load(), s.reqDryRun.Load())
}

// handleMaven dispatches incoming Maven repository requests.
//
// Dispatch logic:
//   - /*/maven-metadata.xml* → metadata handler (parse, filter, rewrite)
//   - /*.sha1 | *.md5 | *.sha256 → checksum handler (404 for filtered metadata, proxy for others)
//   - everything else → artifact proxy (JAR, POM, AAR, etc.)
func (s *Server) handleMaven(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	s.reqTotal.Add(1)

	switch {
	case IsChecksumRequest(path):
		s.handleChecksum(w, r)
	case IsMetadataRequest(path):
		s.handleMetadata(w, r)
	default:
		s.handleArtifact(w, r)
	}
}

// handleMetadata fetches maven-metadata.xml, filters versions, and returns filtered XML.
func (s *Server) handleMetadata(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	cacheKey := "meta:" + path
	if entry := s.cache.Get(cacheKey); entry != nil {
		w.Header().Set(hdrContentType, entry.ContentType)
		w.Header().Set("X-Cache", "HIT")
		w.WriteHeader(entry.StatusCode)
		w.Write(entry.Body) //nolint:errcheck
		return
	}

	body, status, err := s.fetchUpstream(path)
	if err != nil {
		s.logger.Warn("metadata upstream error",
			slog.String("path", path),
			slog.String("error", err.Error()),
		)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
		w.Write(body) //nolint:errcheck
		return
	}

	group, artifact, _, _, pErr := ParseMavenPath(path)
	if pErr != nil {
		// Cannot parse path — proxy as-is.
		w.Header().Set(hdrContentType, mimeXML)
		w.WriteHeader(http.StatusOK)
		w.Write(body) //nolint:errcheck
		return
	}

	allVersions, pErr := ParseMetadataVersionMeta(body)
	if pErr != nil {
		s.logger.Warn("parsing maven-metadata.xml failed",
			slog.String("group", group),
			slog.String("artifact", artifact),
			slog.String("error", pErr.Error()),
		)
		if s.cfg.Policy.FailMode == config.FailModeClosed {
			http.Error(w, "policy inspection failed; request blocked by fail_mode:closed", http.StatusBadGateway)
			return
		}
		w.Header().Set(hdrContentType, mimeXML)
		w.WriteHeader(http.StatusOK)
		w.Write(body) //nolint:errcheck
		return
	}

	pkgName := group + ":" + artifact
	pkgMeta := rules.PackageMeta{Name: pkgName, Versions: allVersions}
	if s.enforcePackagePolicy(w, pkgMeta, int64(len(allVersions))) {
		return
	}

	allowed, denied := s.filterVersionList(pkgMeta, allVersions)
	s.reqDenied.Add(int64(denied))

	// When all versions are removed by version-level rules, return 403.
	if len(allowed) == 0 && denied > 0 {
		errMsg := fmt.Sprintf("[PKGuard] %s: %s", pkgName, blockReasonAllVer)
		entry := &rules.CacheEntry{Body: []byte(errMsg), ContentType: "text/plain", StatusCode: http.StatusForbidden}
		s.cache.Set(cacheKey, entry)
		w.Header().Set(hdrContentType, "text/plain")
		w.Header().Set("X-Cache", "MISS")
		http.Error(w, errMsg, http.StatusForbidden)
		return
	}

	filtered, fErr := FilterMetadataXML(body, allowed)
	if fErr != nil {
		s.logger.Warn("filtering maven-metadata.xml failed",
			slog.String("group", group),
			slog.String("artifact", artifact),
			slog.String("error", fErr.Error()),
		)
		if s.cfg.Policy.FailMode == config.FailModeClosed {
			http.Error(w, "policy inspection failed; request blocked by fail_mode:closed", http.StatusBadGateway)
			return
		}
		filtered = body
	}

	ct := mimeXML
	entry := &rules.CacheEntry{Body: filtered, ContentType: ct, StatusCode: http.StatusOK}
	s.cache.Set(cacheKey, entry)

	w.Header().Set(hdrContentType, ct)
	w.Header().Set("X-Cache", "MISS")
	if denied > 0 {
		w.Header().Set("X-Curation-Policy-Notice", fmt.Sprintf("%d version(s) filtered by policy", denied))
	}
	w.WriteHeader(http.StatusOK)
	w.Write(filtered) //nolint:errcheck
}

// filterVersionList evaluates each version in allVersions against the rule engine and
// returns the list of allowed version strings together with the denied count.
func (s *Server) filterVersionList(pkgMeta rules.PackageMeta, allVersions []rules.VersionMeta) ([]string, int) {
	allowed := make([]string, 0, len(allVersions))
	denied := 0
	for _, vm := range allVersions {
		dec := s.engine.EvaluateVersion(pkgMeta, vm)
		if dec.Allow {
			allowed = append(allowed, vm.Version)
			if dec.DryRun {
				s.reqDryRun.Add(1)
			}
		} else {
			s.logger.Info("version blocked",
				slog.String("package", pkgMeta.Name),
				slog.String("version", vm.Version),
				slog.String("rule", dec.RuleName),
				slog.String("reason", dec.Reason),
			)
			denied++
		}
	}
	return allowed, denied
}

// handleChecksum handles checksum sidecar requests (.sha1, .md5, .sha256).
// If the base file is a maven-metadata.xml that was filtered, return 404
// to prevent stale checksums from being served.
func (s *Server) handleChecksum(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	basePath := checksumBasePath(path)

	// If the base is a metadata file, check whether we have a cached filtered version.
	if IsMetadataRequest(basePath) {
		cacheKey := "meta:" + basePath
		if s.cache.Get(cacheKey) != nil {
			// We cached a (possibly filtered) metadata response.
			// Always return 404 to force the client to recompute; the client will
			// use the filtered content it received via the metadata endpoint.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Not cached — proxy normally. Metadata was never filtered.
	}

	s.handleArtifact(w, r)
}

// handleArtifact proxies an artifact request (JAR, POM, AAR) directly to upstream.
func (s *Server) handleArtifact(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	publishedAt := s.fetchUpstreamLastModified(path)

	// Extract version from path to apply version-level policy.
	group, artifact, version, _, err := ParseMavenPath(path)
	if err == nil && version != "" {
		pkgMeta := rules.PackageMeta{Name: group + ":" + artifact}
		if s.enforcePackagePolicy(w, pkgMeta, 1) {
			return
		}

		// If age filtering is configured but PublishedAt is unavailable, deny the request
		// to prevent bypass via direct artifact URLs (fail-closed for missing metadata).
		if s.engine.RequiresAgeFiltering(group+":"+artifact, version) && publishedAt.IsZero() {
			s.reqDenied.Add(1)
			s.logger.Warn("artifact request denied: version metadata unavailable for age check",
				slog.String("group", group), slog.String("artifact", artifact), slog.String("version", version))
			http.Error(w, "version metadata unavailable - cannot verify age policy", http.StatusForbidden)
			return
		}

		ver := rules.VersionMeta{Version: version, PublishedAt: publishedAt}
		dec := s.engine.EvaluateVersion(pkgMeta, ver)
		if !dec.Allow {
			s.reqDenied.Add(1)
			s.logger.Info("artifact version blocked",
				slog.String("group", group),
				slog.String("artifact", artifact),
				slog.String("version", version),
				slog.String("rule", dec.RuleName),
				slog.String("reason", dec.Reason),
			)
			http.Error(w, "version blocked by policy", http.StatusForbidden)
			return
		}
		if dec.DryRun {
			s.reqDryRun.Add(1)
		}
	}

	body, status, err := s.fetchUpstream(path)
	if err != nil {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	w.WriteHeader(status)
	w.Write(body) //nolint:errcheck
	s.reqAllowed.Add(1)
}

// enforcePackagePolicy returns true when a package request is blocked and the
// response has already been written.
func (s *Server) enforcePackagePolicy(w http.ResponseWriter, pkgMeta rules.PackageMeta, denyCount int64) bool {
	pkgDec := s.engine.EvaluatePackage(pkgMeta)
	if !pkgDec.Allow {
		s.reqDenied.Add(denyCount)
		s.logger.Info("package blocked by policy",
			slog.String("package", pkgMeta.Name),
			slog.String("rule", pkgDec.RuleName),
			slog.String("reason", pkgDec.Reason),
		)
		http.Error(w, "package blocked by policy", http.StatusForbidden)
		return true
	}
	if pkgDec.DryRun {
		s.reqDryRun.Add(1)
	}
	return false
}

// fetchUpstream fetches the given path from the configured upstream and returns
// the body bytes, HTTP status code, and any transport error.
func (s *Server) fetchUpstream(path string) ([]byte, int, error) {
	upstreamURL := strings.TrimRight(s.cfg.Upstream.URL, "/") + path
	req, err := http.NewRequest(http.MethodGet, upstreamURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("creating request: %w", err)
	}
	s.addUpstreamAuth(req)

	resp, err := s.upstream.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("upstream request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("reading body: %w", err)
	}
	return body, resp.StatusCode, nil
}

// fetchUpstreamLastModified attempts to read Last-Modified from an upstream
// artifact response and parse it as a publish timestamp for age checks.
// Returns zero time when unavailable.
func (s *Server) fetchUpstreamLastModified(path string) time.Time {
	upstreamURL := strings.TrimRight(s.cfg.Upstream.URL, "/") + path
	req, err := http.NewRequest(http.MethodHead, upstreamURL, nil)
	if err != nil {
		return time.Time{}
	}
	s.addUpstreamAuth(req)

	resp, err := s.upstream.Do(req)
	if err != nil {
		return time.Time{}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return time.Time{}
	}

	lm := strings.TrimSpace(resp.Header.Get("Last-Modified"))
	if lm == "" {
		return time.Time{}
	}
	if t, parseErr := time.Parse(time.RFC1123, lm); parseErr == nil {
		return t.UTC()
	}
	if t, parseErr := time.Parse(time.RFC1123Z, lm); parseErr == nil {
		return t.UTC()
	}
	return time.Time{}
}

// addUpstreamAuth attaches configured credentials to an outgoing request.
func (s *Server) addUpstreamAuth(req *http.Request) {
	if s.cfg.Upstream.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.Upstream.Token)
	} else if s.cfg.Upstream.Username != "" {
		req.SetBasicAuth(s.cfg.Upstream.Username, s.cfg.Upstream.Password)
	}
}

// mavenMetadata is the structure of a maven-metadata.xml file.
type mavenMetadata struct {
	XMLName    xml.Name        `xml:"metadata"`
	GroupID    string          `xml:"groupId"`
	ArtifactID string          `xml:"artifactId"`
	Versioning mavenVersioning `xml:"versioning"`
}

type mavenVersioning struct {
	Release          string                 `xml:"release"`
	Latest           string                 `xml:"latest"`
	LastUpdated      string                 `xml:"lastUpdated"`
	Versions         []string               `xml:"versions>version"`
	SnapshotVersions []mavenSnapshotVersion `xml:"snapshotVersions>snapshotVersion"`
}

type mavenSnapshotVersion struct {
	Value   string `xml:"value"`
	Updated string `xml:"updated"`
}

// ParseMavenPath extracts Maven coordinates from a repository URL path.
// Input: /com/example/mylib/1.0/mylib-1.0.jar
// Output: group="com/example", artifact="mylib", version="1.0", filename="mylib-1.0.jar".
//
// For metadata paths: /com/example/mylib/maven-metadata.xml
// Output: group="com/example", artifact="mylib", version="", filename="maven-metadata.xml".
func ParseMavenPath(urlPath string) (group, artifact, version, filename string, err error) {
	parts := strings.Split(strings.TrimPrefix(urlPath, "/"), "/")
	if len(parts) < 3 {
		return "", "", "", "", fmt.Errorf("too few path components in %q", urlPath)
	}

	filename = parts[len(parts)-1]

	// Metadata paths: .../group.../artifact/maven-metadata.xml[.sha1]
	if IsMetadataRequest(urlPath) || IsChecksumRequest(urlPath) {
		if len(parts) < 3 {
			return "", "", "", "", fmt.Errorf("metadata path too short: %q", urlPath)
		}
		// artifact is second-to-last directory component
		artifact = parts[len(parts)-2]
		if isVersion(artifact) {
			// Version-specific metadata: .../artifact/version/maven-metadata.xml
			version = artifact
			artifact = parts[len(parts)-3]
			group = strings.Join(parts[:len(parts)-3], "/")
		} else {
			group = strings.Join(parts[:len(parts)-2], "/")
		}
		return group, artifact, version, filename, nil
	}

	// Artifact paths: .../group.../artifact/version/filename
	if len(parts) < 4 {
		return "", "", "", "", fmt.Errorf("artifact path too short: %q", urlPath)
	}
	filename = parts[len(parts)-1]
	version = parts[len(parts)-2]
	artifact = parts[len(parts)-3]
	group = strings.Join(parts[:len(parts)-3], "/")
	return group, artifact, version, filename, nil
}

// isVersion returns true if s looks like a Maven version string.
func isVersion(s string) bool {
	if len(s) == 0 {
		return false
	}
	// Versions typically start with a digit.
	return s[0] >= '0' && s[0] <= '9'
}

// IsMetadataRequest returns true when the path is a maven-metadata.xml request
// (possibly with a checksum extension like .sha1).
func IsMetadataRequest(path string) bool {
	base := checksumBasePath(path)
	return strings.HasSuffix(base, "maven-metadata.xml")
}

// IsChecksumRequest returns true when the path ends with a known checksum extension.
func IsChecksumRequest(path string) bool {
	return strings.HasSuffix(path, ".sha1") ||
		strings.HasSuffix(path, ".md5") ||
		strings.HasSuffix(path, ".sha256")
}

// checksumBasePath strips a checksum extension from a path, returning the base file path.
// If the path has no checksum extension, it is returned unchanged.
func checksumBasePath(path string) string {
	for _, ext := range []string{".sha1", ".md5", ".sha256"} {
		if strings.HasSuffix(path, ext) {
			return path[:len(path)-len(ext)]
		}
	}
	return path
}

// ParseMetadataXML parses a maven-metadata.xml and returns the list of versions.
func ParseMetadataXML(data []byte) ([]string, error) {
	var meta mavenMetadata
	if err := xml.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf(errFmtParseMetadata, err)
	}
	return meta.Versioning.Versions, nil
}

// ParseMetadataVersionMeta parses maven-metadata.xml and returns version metadata
// with best-effort publish timestamps to support age and velocity rules.
func ParseMetadataVersionMeta(data []byte) ([]rules.VersionMeta, error) {
	var meta mavenMetadata
	if err := xml.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf(errFmtParseMetadata, err)
	}

	defaultPublishedAt := parseMavenLastUpdated(meta.Versioning.LastUpdated)
	snapshotUpdated := make(map[string]time.Time, len(meta.Versioning.SnapshotVersions))
	for _, sv := range meta.Versioning.SnapshotVersions {
		ts := parseMavenLastUpdated(sv.Updated)
		if !ts.IsZero() {
			snapshotUpdated[sv.Value] = ts
		}
	}

	out := make([]rules.VersionMeta, 0, len(meta.Versioning.Versions))
	for _, v := range meta.Versioning.Versions {
		publishedAt := defaultPublishedAt
		if ts, ok := snapshotUpdated[v]; ok {
			publishedAt = ts
		}
		out = append(out, rules.VersionMeta{Version: v, PublishedAt: publishedAt})
	}
	return out, nil
}

// parseMavenLastUpdated parses a Maven metadata timestamp (yyyyMMddHHmmss).
func parseMavenLastUpdated(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	t, err := time.Parse("20060102150405", strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// FilterMetadataXML rewrites maven-metadata.xml to include only allowed versions.
// The <latest> and <release> fields are updated to the highest remaining version.
func FilterMetadataXML(data []byte, allowed []string) ([]byte, error) {
	var meta mavenMetadata
	if err := xml.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf(errFmtParseMetadata, err)
	}

	allowedSet := make(map[string]bool, len(allowed))
	for _, v := range allowed {
		allowedSet[v] = true
	}

	filtered := make([]string, 0, len(allowed))
	for _, v := range meta.Versioning.Versions {
		if allowedSet[v] {
			filtered = append(filtered, v)
		}
	}
	meta.Versioning.Versions = filtered

	// Update latest and release to the last allowed version if the current value was removed.
	if !allowedSet[meta.Versioning.Latest] && len(filtered) > 0 {
		meta.Versioning.Latest = filtered[len(filtered)-1]
	}
	if !allowedSet[meta.Versioning.Release] && len(filtered) > 0 {
		meta.Versioning.Release = filtered[len(filtered)-1]
	}
	if len(filtered) == 0 {
		meta.Versioning.Latest = ""
		meta.Versioning.Release = ""
	}

	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(meta); err != nil {
		return nil, fmt.Errorf("encoding filtered maven-metadata.xml: %w", err)
	}
	return buf.Bytes(), nil
}
