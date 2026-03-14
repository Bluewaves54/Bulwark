// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"PKGuard/common/config"
	"PKGuard/common/rules"
)

const (
	hdrContentType = "Content-Type"
	mimeJSON       = "application/json"
	errMsgUpstream = "upstream error"
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

// handleNpm is the catch-all route for npm registry requests.
// It dispatches to:
//   - packument handler for metadata requests (no tarball path component)
//   - tarball proxy for /<pkg>/-/<file>.tgz paths
func (s *Server) handleNpm(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if isTarballPath(path) {
		s.handleTarball(w, r)
		return
	}

	s.handlePackument(w, r)
}

// handlePackument serves a filtered npm packument (package metadata).
func (s *Server) handlePackument(w http.ResponseWriter, r *http.Request) {
	pkg := extractPackageName(r.URL.Path)
	s.reqTotal.Add(1)

	cacheKey := "packument:" + pkg
	if entry := s.cache.Get(cacheKey); entry != nil {
		w.Header().Set(hdrContentType, entry.ContentType)
		w.Header().Set("X-Cache", "HIT")
		w.WriteHeader(entry.StatusCode)
		w.Write(entry.Body) //nolint:errcheck
		return
	}

	upstreamURL := s.cfg.Upstream.URL + "/" + encodePkgPath(pkg)
	req, err := http.NewRequest(http.MethodGet, upstreamURL, nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Accept", mimeJSON)
	s.addUpstreamAuth(req)

	resp, err := s.upstream.Do(req)
	if err != nil {
		s.logger.Warn("upstream fetch failed",
			slog.String("package", pkg),
			slog.String("error", err.Error()),
		)
		http.Error(w, errMsgUpstream, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "read error", http.StatusBadGateway)
		return
	}

	if resp.StatusCode >= 500 {
		http.Error(w, errMsgUpstream, http.StatusBadGateway)
		return
	}
	if resp.StatusCode != http.StatusOK {
		w.Header().Set(hdrContentType, resp.Header.Get(hdrContentType))
		w.WriteHeader(resp.StatusCode)
		w.Write(body) //nolint:errcheck
		return
	}

	filtered, removed, err := filterNpmPackument(body, pkg, s.engine, s.cfg.Upstream.URL, s.logger)
	if err != nil {
		s.logger.Warn("filtering packument failed",
			slog.String("package", pkg),
			slog.String("error", err.Error()),
		)
		if s.cfg.Policy.FailMode == config.FailModeClosed {
			http.Error(w, "policy inspection failed; request blocked by fail_mode:closed", http.StatusBadGateway)
			return
		}
		// Serve the unfiltered packument as a safe fallback.
		filtered = body
		removed = 0
	}

	s.reqDenied.Add(int64(removed))

	ct := resp.Header.Get(hdrContentType)
	if ct == "" {
		ct = mimeJSON
	}
	entry := &rules.CacheEntry{Body: filtered, ContentType: ct, StatusCode: http.StatusOK}
	s.cache.Set(cacheKey, entry)

	w.Header().Set(hdrContentType, ct)
	w.Header().Set("X-Cache", "MISS")
	if removed > 0 {
		w.Header().Set("X-Curation-Policy-Notice", fmt.Sprintf("%d version(s) filtered by policy", removed))
		s.reqDenied.Add(0) // already added above; no double-count
	}
	w.WriteHeader(http.StatusOK)
	w.Write(filtered) //nolint:errcheck
}

// handleTarball proxies a tarball download request.
func (s *Server) handleTarball(w http.ResponseWriter, r *http.Request) {
	pkg := extractPackageName(r.URL.Path)
	version := extractVersionFromTarball(r.URL.Path, pkg)

	s.reqTotal.Add(1)

	pkgMeta := rules.PackageMeta{Name: pkg}
	pkgDec := s.engine.EvaluatePackage(pkgMeta)
	if !pkgDec.Allow {
		s.reqDenied.Add(1)
		http.Error(w, "package blocked by policy", http.StatusForbidden)
		return
	}
	if pkgDec.DryRun {
		s.reqDryRun.Add(1)
	}

	if version != "" {
		ver := s.fetchVersionMetaFromPackument(pkg, version)

		// If age filtering is configured but PublishedAt is unavailable, deny the request
		// to prevent bypass via direct tarball URLs (fail-closed for missing metadata).
		if s.engine.RequiresAgeFiltering(pkg, version) && ver.PublishedAt.IsZero() {
			s.reqDenied.Add(1)
			s.logger.Warn("tarball request denied: version metadata unavailable for age check",
				slog.String("package", pkg), slog.String("version", version))
			http.Error(w, "version metadata unavailable - cannot verify age policy", http.StatusForbidden)
			return
		}

		dec := s.engine.EvaluateVersion(pkgMeta, ver)
		if !dec.Allow {
			s.reqDenied.Add(1)
			http.Error(w, "version blocked by policy", http.StatusForbidden)
			return
		}
		if dec.DryRun {
			s.reqDryRun.Add(1)
		}
	}

	upstreamURL := s.cfg.Upstream.URL + r.URL.Path
	req, err := http.NewRequest(http.MethodGet, upstreamURL, nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.addUpstreamAuth(req)

	resp, err := s.upstream.Do(req)
	if err != nil {
		http.Error(w, errMsgUpstream, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
	s.reqAllowed.Add(1)
}

// fetchVersionMetaFromPackument fetches the package packument and returns a
// fully-populated VersionMeta for a specific version. This includes
// PublishedAt, HasInstallScripts, and License so that tarball requests can
// enforce the same policy checks as packument filtering.
func (s *Server) fetchVersionMetaFromPackument(pkg, version string) rules.VersionMeta {
	meta := rules.VersionMeta{Version: version}
	if pkg == "" || version == "" {
		return meta
	}

	upstreamURL := s.cfg.Upstream.URL + "/" + encodePkgPath(pkg)
	req, err := http.NewRequest(http.MethodGet, upstreamURL, nil)
	if err != nil {
		return meta
	}
	req.Header.Set("Accept", mimeJSON)
	s.addUpstreamAuth(req)

	resp, err := s.upstream.Do(req)
	if err != nil {
		return meta
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return meta
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return meta
	}

	var p npmPackument
	if err = json.Unmarshal(body, &p); err != nil {
		return meta
	}

	ts := strings.TrimSpace(p.Time[version])
	if ts != "" {
		if publishedAt, parseErr := time.Parse(time.RFC3339, ts); parseErr == nil {
			meta.PublishedAt = publishedAt.UTC()
		}
	}

	if raw, ok := p.Versions[version]; ok {
		meta.HasInstallScripts, meta.License = extractVersionPolicyFields(raw)
	}

	return meta
}

// npmPackument is the minimum packument structure we need for filtering.
type npmPackument struct {
	Name     string                     `json:"name"`
	Versions map[string]json.RawMessage `json:"versions"`
	DistTags map[string]string          `json:"dist-tags"`
	Time     map[string]string          `json:"time"`
}

// filterNpmPackument removes denied versions from a packument and rewrites tarball URLs.
// Returns the filtered bytes, the count of removed versions, and any error.
func filterNpmPackument(body []byte, pkgName string, engine *rules.RuleEngine, proxyBase string, logger *slog.Logger) ([]byte, int, error) {
	var p npmPackument
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, 0, fmt.Errorf("parsing packument: %w", err)
	}

	allVersions := buildVersionMetas(p)
	pkgMeta := rules.PackageMeta{Name: pkgName, Versions: allVersions}

	pkgDec := engine.EvaluatePackage(pkgMeta)
	if !pkgDec.Allow && !pkgDec.DryRun {
		logger.Info("package blocked by policy",
			slog.String("package", pkgName),
			slog.String("reason", pkgDec.Reason),
		)
		// Return an empty packument body.
		empty := fmt.Sprintf(`{"name":%q,"versions":{},"dist-tags":{}}`, pkgName)
		return []byte(empty), len(p.Versions), nil
	}

	removed := applyVersionFilter(&p, pkgMeta, allVersions, engine)

	filtered, err := json.Marshal(p)
	if err != nil {
		return nil, 0, fmt.Errorf("re-marshalling packument: %w", err)
	}
	return filtered, removed, nil
}

// buildVersionMetas converts packument version timestamps into VersionMeta slices.
func buildVersionMetas(p npmPackument) []rules.VersionMeta {
	versions := make([]rules.VersionMeta, 0, len(p.Versions))
	for ver, raw := range p.Versions {
		var pub time.Time
		if ts, ok := p.Time[ver]; ok && ts != "" {
			pub, _ = time.Parse(time.RFC3339, ts)
		}
		hasInstallScripts, license := extractVersionPolicyFields(raw)
		versions = append(versions, rules.VersionMeta{
			Version:           ver,
			PublishedAt:       pub,
			HasInstallScripts: hasInstallScripts,
			License:           license,
		})
	}
	return versions
}

// extractVersionPolicyFields extracts install-script and licence metadata from a
// single npm version object in a packument.
func extractVersionPolicyFields(raw json.RawMessage) (bool, string) {
	type npmLicense struct {
		Type string `json:"type"`
	}
	type npmVersionPolicy struct {
		Scripts map[string]string `json:"scripts"`
		License interface{}       `json:"license"`
	}

	var v npmVersionPolicy
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, ""
	}

	hasInstallScripts := false
	for _, key := range []string{"preinstall", "install", "postinstall"} {
		if _, ok := v.Scripts[key]; ok {
			hasInstallScripts = true
			break
		}
	}

	license := ""
	switch lic := v.License.(type) {
	case string:
		license = strings.TrimSpace(lic)
	case map[string]interface{}:
		if t, ok := lic["type"].(string); ok {
			license = strings.TrimSpace(t)
		}
	default:
		var l npmLicense
		if b, err := json.Marshal(v.License); err == nil {
			if err = json.Unmarshal(b, &l); err == nil {
				license = strings.TrimSpace(l.Type)
			}
		}
	}

	return hasInstallScripts, license
}

// applyVersionFilter removes denied versions from p and returns the count removed.
func applyVersionFilter(p *npmPackument, pkgMeta rules.PackageMeta, allVersions []rules.VersionMeta, engine *rules.RuleEngine) int {
	removed := 0
	for _, vm := range allVersions {
		dec := engine.EvaluateVersion(pkgMeta, vm)
		if dec.Allow || dec.DryRun {
			continue
		}
		delete(p.Versions, vm.Version)
		delete(p.Time, vm.Version)
		removed++

		// Remove from dist-tags if the denied version was the latest.
		for tag, tagVer := range p.DistTags {
			if tagVer == vm.Version {
				delete(p.DistTags, tag)
			}
		}
	}
	return removed
}

// extractPackageName extracts the package name from an npm request path.
// Handles both scoped (@scope/name) and unscoped (name) packages.
// Input examples:
//   - /lodash → "lodash"
//   - /@babel/core → "@babel/core"
//   - /lodash/-/lodash-4.17.21.tgz → "lodash"
//   - /@babel/core/-/core-7.0.0.tgz → "@babel/core"
func extractPackageName(path string) string {
	path = strings.TrimPrefix(path, "/")
	if strings.HasPrefix(path, "@") {
		// Scoped package.
		parts := strings.SplitN(path, "/", 3)
		if len(parts) >= 2 {
			return "@" + parts[0][1:] + "/" + strings.SplitN(parts[1], "/-/", 2)[0]
		}
	}
	parts := strings.SplitN(path, "/-/", 2)
	return strings.SplitN(parts[0], "/", 2)[0]
}

// extractVersionFromTarball extracts the version from a tarball path component.
// e.g. /lodash/-/lodash-4.17.21.tgz → "4.17.21".
// e.g. /undici-types/-/undici-types-7.22.0.tgz → "7.22.0".
// e.g. /@babel/core/-/core-7.0.0.tgz → "7.0.0".
func extractVersionFromTarball(path, pkg string) string {
	idx := strings.Index(path, "/-/")
	if idx < 0 {
		return ""
	}
	filename := path[idx+3:]
	// Strip .tgz extension.
	filename = strings.TrimSuffix(filename, ".tgz")

	base := pkg
	if slash := strings.LastIndex(base, "/"); slash >= 0 {
		base = base[slash+1:]
	}
	prefix := base + "-"
	if !strings.HasPrefix(filename, prefix) {
		return ""
	}

	return strings.TrimPrefix(filename, prefix)
}

// isTarballPath returns true when the path looks like a tarball download.
func isTarballPath(path string) bool {
	return strings.Contains(path, "/-/") && strings.HasSuffix(path, ".tgz")
}

// encodePkgPath encodes a package name for use in a URL path.
// Scoped packages keep the @scope/name form as-is.
func encodePkgPath(pkg string) string {
	if strings.HasPrefix(pkg, "@") {
		// Replace the / in @scope/name with %2F for registry APIs that require it.
		// Note: npmjs.com accepts both /- and %2F forms.
		return pkg
	}
	return pkg
}

// addUpstreamAuth attaches configured credentials to an outgoing request.
func (s *Server) addUpstreamAuth(req *http.Request) {
	if s.cfg.Upstream.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.Upstream.Token)
	} else if s.cfg.Upstream.Username != "" {
		req.SetBasicAuth(s.cfg.Upstream.Username, s.cfg.Upstream.Password)
	}
}
