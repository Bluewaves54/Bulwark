// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"PKGuard/common/config"
	"PKGuard/common/rules"
)

const (
	// formatJSON is the format identifier for PyPI Simple JSON (PEP 691) responses.
	formatJSON = "json"
	// ctPyPISimpleJSON is the Content-Type for PyPI Simple Index v1 JSON responses.
	ctPyPISimpleJSON = "application/vnd.pypi.simple.v1+json"

	hdrContentType = "Content-Type"
	mimeJSON       = "application/json"
	hdrXCache      = "X-Cache"
	errMsgUpstream = "upstream error"
	pypiTimeFmt    = "2006-01-02T15:04:05"
)

var externalPathPattern = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@/%-]*$`)

// handleHealth returns 200 OK for liveness checks.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set(hdrContentType, mimeJSON)
	fmt.Fprint(w, `{"status":"ok"}`)
}

// handleReady probes the upstream and returns 200 if reachable, 503 otherwise.
func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	probeURL := s.cfg.Upstream.URL + "/simple/"
	req, err := http.NewRequest(http.MethodGet, probeURL, nil)
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

// handleSimpleRedirect redirects /simple/<pkg> → /simple/<pkg>/ (trailing slash canonical form).
func (s *Server) handleSimpleRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
}

// handleSimple handles GET /simple/<pkg>/ — the PyPI simple index endpoint.
// It supports both HTML (text/html) and JSON (PEP 691) responses.
func (s *Server) handleSimple(w http.ResponseWriter, r *http.Request) {
	pkg := r.PathValue("pkg")
	pkg = normalizePyPIName(pkg)
	s.reqTotal.Add(1)

	cacheKey := "simple:" + pkg + ":" + preferredFormat(r)
	if entry := s.cache.Get(cacheKey); entry != nil {
		w.Header().Set(hdrContentType, entry.ContentType)
		w.Header().Set(hdrXCache, "HIT")
		w.WriteHeader(entry.StatusCode)
		w.Write(entry.Body) //nolint:errcheck
		return
	}

	meta, err := s.fetchPyPIMeta(pkg)
	if err != nil {
		s.logger.Warn("upstream metadata fetch failed",
			slog.String("package", pkg),
			slog.String("error", err.Error()),
		)
		http.Error(w, errMsgUpstream, http.StatusBadGateway)
		return
	}

	allowed, denied := s.filterVersions(pkg, meta)
	s.reqAllowed.Add(int64(len(allowed)))
	s.reqDenied.Add(int64(len(denied)))

	format := preferredFormat(r)
	var body []byte
	var ct string

	if format == formatJSON {
		body, ct = buildJSONSimpleIndex(pkg, meta, allowed)
	} else {
		body, ct = buildHTMLSimpleIndex(pkg, meta, allowed)
	}

	entry := &rules.CacheEntry{Body: body, ContentType: ct, StatusCode: http.StatusOK}
	s.cache.Set(cacheKey, entry)

	w.Header().Set(hdrContentType, ct)
	w.Header().Set(hdrXCache, "MISS")
	if len(denied) > 0 {
		w.Header().Set("X-Curation-Policy-Notice", fmt.Sprintf("%d version(s) filtered by policy", len(denied)))
	}
	w.WriteHeader(http.StatusOK)
	w.Write(body) //nolint:errcheck
}

// handlePackageJSON handles GET /pypi/<pkg>/json — the PyPI JSON metadata endpoint.
func (s *Server) handlePackageJSON(w http.ResponseWriter, r *http.Request) {
	pkg := r.PathValue("pkg")
	pkg = normalizePyPIName(pkg)
	s.reqTotal.Add(1)

	cacheKey := "json:" + pkg
	if entry := s.cache.Get(cacheKey); entry != nil {
		w.Header().Set(hdrContentType, entry.ContentType)
		w.Header().Set(hdrXCache, "HIT")
		w.WriteHeader(entry.StatusCode)
		w.Write(entry.Body) //nolint:errcheck
		return
	}

	upstreamURL := s.cfg.Upstream.URL + "/pypi/" + url.PathEscape(pkg) + "/json"
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "read error", http.StatusBadGateway)
		return
	}

	if resp.StatusCode != http.StatusOK {
		w.Header().Set(hdrContentType, resp.Header.Get(hdrContentType))
		w.WriteHeader(resp.StatusCode)
		w.Write(body) //nolint:errcheck
		return
	}

	// Filter the JSON response through the rule engine.
	filtered, removed, filterErr := filterPyPIJSONResponse(body, pkg, s.engine)
	if filterErr != nil {
		s.logger.Warn("filtering pypi json failed",
			slog.String("package", pkg),
			slog.String("error", filterErr.Error()),
		)
		if s.cfg.Policy.FailMode == config.FailModeClosed {
			http.Error(w, "policy inspection failed; request blocked by fail_mode:closed", http.StatusBadGateway)
			return
		}
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
	w.Header().Set(hdrXCache, "MISS")
	if removed > 0 {
		w.Header().Set("X-Curation-Policy-Notice", fmt.Sprintf("%d version(s) filtered by policy", removed))
	}
	w.WriteHeader(http.StatusOK)
	w.Write(filtered) //nolint:errcheck
}

// filterPyPIJSONResponse applies the rule engine to a PyPI JSON API response,
// removing denied versions from the releases map while preserving all other fields.
func filterPyPIJSONResponse(body []byte, pkg string, engine *rules.RuleEngine) ([]byte, int, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, 0, fmt.Errorf("parsing pypi json: %w", err)
	}

	license := extractPyPIJSONLicense(raw)
	var releases map[string]json.RawMessage
	if relRaw, ok := raw["releases"]; ok {
		if err := json.Unmarshal(relRaw, &releases); err != nil {
			return nil, 0, fmt.Errorf("parsing releases: %w", err)
		}
	}
	if len(releases) == 0 {
		return body, 0, nil
	}

	allVersions := buildPyPIJSONVersionMetas(releases, license)
	pkgMeta := rules.PackageMeta{Name: pkg, Versions: allVersions}

	pkgDec := engine.EvaluatePackage(pkgMeta)
	if !pkgDec.Allow && !pkgDec.DryRun {
		return rewriteReleases(raw, map[string]json.RawMessage{}, len(allVersions))
	}

	removed := 0
	for _, vm := range allVersions {
		dec := engine.EvaluateVersion(pkgMeta, vm)
		if dec.Allow || dec.DryRun {
			continue
		}
		delete(releases, vm.Version)
		removed++
	}

	if removed == 0 {
		return body, 0, nil
	}
	return rewriteReleases(raw, releases, removed)
}

// extractPyPIJSONLicense extracts the license string from the info block of a PyPI JSON response.
func extractPyPIJSONLicense(raw map[string]json.RawMessage) string {
	infoRaw, ok := raw["info"]
	if !ok {
		return ""
	}
	var info struct {
		License string `json:"license"`
	}
	if err := json.Unmarshal(infoRaw, &info); err != nil {
		return ""
	}
	return strings.TrimSpace(info.License)
}

// buildPyPIJSONVersionMetas converts release entries into VersionMeta slices for the rule engine.
func buildPyPIJSONVersionMetas(releases map[string]json.RawMessage, license string) []rules.VersionMeta {
	metas := make([]rules.VersionMeta, 0, len(releases))
	for ver, filesRaw := range releases {
		var pub time.Time
		var files []struct {
			UploadTime string `json:"upload_time"`
		}
		if err := json.Unmarshal(filesRaw, &files); err == nil && len(files) > 0 {
			pub, _ = time.Parse(pypiTimeFmt, files[0].UploadTime)
		}
		metas = append(metas, rules.VersionMeta{
			Version: ver, PublishedAt: pub, License: license,
		})
	}
	return metas
}

// rewriteReleases replaces the releases map in a PyPI JSON response and re-marshals it.
func rewriteReleases(raw map[string]json.RawMessage, releases map[string]json.RawMessage, removed int) ([]byte, int, error) {
	filtered, err := json.Marshal(releases)
	if err != nil {
		return nil, 0, fmt.Errorf("marshalling releases: %w", err)
	}
	raw["releases"] = filtered
	result, err := json.Marshal(raw)
	if err != nil {
		return nil, 0, fmt.Errorf("marshalling json: %w", err)
	}
	return result, removed, nil
}

// handleExternal handles GET /external?url=<url> — proxies a tarball URL.
// Only hosts in allowed_external_hosts are permitted.
func (s *Server) handleExternal(w http.ResponseWriter, r *http.Request) {
	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		http.Error(w, "missing url parameter", http.StatusBadRequest)
		return
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		http.Error(w, "invalid url", http.StatusBadRequest)
		return
	}

	// Security: reject requests to internal/private hosts.
	if isPrivateHost(parsed.Host) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}

	validatedURL, err := buildValidatedExternalURL(parsed, s.cfg.Upstream.AllowedExternalHosts, s.logger)
	if err != nil {
		s.logger.Warn("external url validation failed", "url", rawURL, "error", err.Error())
		http.Error(w, "host not allowed", http.StatusForbidden)
		return
	}
	safeURL := validatedURL.String()

	// Evaluate package/version rules from the download URL (fail-open if extraction fails).
	if dec := s.evaluateExternalURL(safeURL); dec != nil {
		s.reqDenied.Add(1)
		http.Error(w, "blocked by policy", http.StatusForbidden)
		return
	}

	req, err := http.NewRequest(http.MethodGet, safeURL, nil)
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
}

// isPrivateHost returns true for hostnames that should never be proxied externally.
func isPrivateHost(host string) bool {
	h := strings.ToLower(strings.Split(host, ":")[0])
	for _, blocked := range []string{"localhost", "127.0.0.1", "::1", "0.0.0.0"} {
		if h == blocked {
			return true
		}
	}
	// Block RFC-1918-style names.
	return strings.HasPrefix(h, "192.168.") || strings.HasPrefix(h, "10.") || strings.HasPrefix(h, "172.")
}

// isAllowedExternalHost validates that the given hostname is in the allowlist.
// Hostname matching is exact and case-insensitive.
func isAllowedExternalHost(host string, allowlist []string, logger *slog.Logger) bool {
	h := strings.ToLower(strings.Split(host, ":")[0])

	// Deny if no allowlist is configured.
	if len(allowlist) == 0 {
		logger.Warn("allowed_external_hosts is empty - denying external host", "host", h)
		return false
	}

	for _, allowedHost := range allowlist {
		allowedHost = strings.ToLower(strings.TrimSpace(allowedHost))
		if allowedHost == "" {
			continue
		}
		if allowedHost == h {
			return true
		}
	}

	return false
}

// buildValidatedExternalURL validates and canonicalizes the user-supplied URL.
// The outbound URL is rebuilt from validated components to avoid forwarding
// attacker-controlled hosts or ports.
func buildValidatedExternalURL(parsed *url.URL, allowlist []string, logger *slog.Logger) (*url.URL, error) {
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return nil, errors.New("missing host")
	}
	if parsed.Port() != "" {
		return nil, errors.New("explicit ports are not allowed")
	}
	if !isAllowedExternalHost(hostname, allowlist, logger) {
		return nil, errors.New("host not in allowlist")
	}

	cleanPath := path.Clean("/" + strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if cleanPath == "." {
		cleanPath = "/"
	}
	if !externalPathPattern.MatchString(cleanPath) {
		return nil, errors.New("invalid path")
	}

	// Use HTTPS for external fetches and drop query/fragment from user input.
	return &url.URL{Scheme: "https", Host: hostname, Path: cleanPath}, nil
}

// pypiMeta holds the parsed /pypi/<pkg>/json response we care about.
type pypiMeta struct {
	releases map[string]pypiRelease
	files    []pypiFile // from /simple/<pkg>/ if used
	license  string
}

type pypiRelease struct {
	uploadTime time.Time
}

type pypiFile struct {
	filename   string
	url        string
	sha256     string
	version    string
	uploadTime time.Time
}

// pypiJSONResponse is the minimal PyPI JSON API shape we need.
type pypiJSONResponse struct {
	Info struct {
		License string `json:"license"`
	} `json:"info"`
	Releases map[string][]struct {
		UploadTime string `json:"upload_time"`
		Filename   string `json:"filename"`
		URL        string `json:"url"`
		Digests    struct {
			SHA256 string `json:"sha256"`
		} `json:"digests"`
	} `json:"releases"`
}

// fetchPyPIMeta fetches package metadata from the upstream PyPI JSON API.
func (s *Server) fetchPyPIMeta(pkg string) (*pypiMeta, error) {
	upstreamURL := s.cfg.Upstream.URL + "/pypi/" + url.PathEscape(pkg) + "/json"
	req, err := http.NewRequest(http.MethodGet, upstreamURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	s.addUpstreamAuth(req)

	resp, err := s.upstream.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("package not found")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}

	var raw pypiJSONResponse
	if err = json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parsing json: %w", err)
	}

	meta := &pypiMeta{releases: make(map[string]pypiRelease), license: strings.TrimSpace(raw.Info.License)}
	for ver, files := range raw.Releases {
		var uploadTime time.Time
		if len(files) > 0 && files[0].UploadTime != "" {
			uploadTime, _ = time.Parse(pypiTimeFmt, files[0].UploadTime)
		}
		meta.releases[ver] = pypiRelease{uploadTime: uploadTime}
		for _, f := range files {
			var ft time.Time
			if f.UploadTime != "" {
				ft, _ = time.Parse(pypiTimeFmt, f.UploadTime)
			}
			meta.files = append(meta.files, pypiFile{
				filename:   f.Filename,
				url:        f.URL,
				sha256:     f.Digests.SHA256,
				version:    ver,
				uploadTime: ft,
			})
		}
	}
	return meta, nil
}

// filterVersions evaluates all known versions and separates allowed from denied.
func (s *Server) filterVersions(pkg string, meta *pypiMeta) (allowed, denied []string) {
	allVersions := make([]rules.VersionMeta, 0, len(meta.releases))
	for ver, rel := range meta.releases {
		allVersions = append(allVersions, rules.VersionMeta{Version: ver, PublishedAt: rel.uploadTime, License: meta.license})
	}
	pkgMeta := rules.PackageMeta{Name: pkg, Versions: allVersions}

	pkgDec := s.engine.EvaluatePackage(pkgMeta)
	if !pkgDec.Allow {
		for ver := range meta.releases {
			denied = append(denied, ver)
		}
		return allowed, denied
	}

	for _, vm := range allVersions {
		dec := s.engine.EvaluateVersion(pkgMeta, vm)
		if dec.Allow {
			allowed = append(allowed, vm.Version)
		} else {
			denied = append(denied, vm.Version)
		}
		if dec.DryRun {
			s.reqDryRun.Add(1)
		}
	}
	return allowed, denied
}

// preferredFormat returns "json" or "html" based on the Accept header.
func preferredFormat(r *http.Request) string {
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, ctPyPISimpleJSON) {
		return formatJSON
	}
	return "html"
}

// buildHTMLSimpleIndex returns an HTML simple index page for the allowed versions.
func buildHTMLSimpleIndex(pkg string, meta *pypiMeta, allowed []string) ([]byte, string) {
	allowedSet := make(map[string]bool, len(allowed))
	for _, v := range allowed {
		allowedSet[v] = true
	}

	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html>\n<html><head><title>Links for ")
	sb.WriteString(pkg)
	sb.WriteString("</title></head>\n<body>\n<h1>Links for ")
	sb.WriteString(pkg)
	sb.WriteString("</h1>\n")

	for _, f := range meta.files {
		if !allowedSet[f.version] {
			continue
		}
		sb.WriteString(`<a href="`)
		sb.WriteString(f.url)
		if f.sha256 != "" {
			sb.WriteString("#sha256=")
			sb.WriteString(f.sha256)
		}
		sb.WriteString(`">`)
		sb.WriteString(f.filename)
		sb.WriteString("</a><br/>\n")
	}
	sb.WriteString("</body></html>\n")
	return []byte(sb.String()), "text/html; charset=utf-8"
}

// buildJSONSimpleIndex returns a PEP 691 JSON simple index for the allowed versions.
func buildJSONSimpleIndex(pkg string, meta *pypiMeta, allowed []string) ([]byte, string) {
	allowedSet := make(map[string]bool, len(allowed))
	for _, v := range allowed {
		allowedSet[v] = true
	}

	type fileEntry struct {
		Filename string            `json:"filename"`
		URL      string            `json:"url"`
		Hashes   map[string]string `json:"hashes"`
	}
	type simpleJSON struct {
		Meta  map[string]string `json:"meta"`
		Name  string            `json:"name"`
		Files []fileEntry       `json:"files"`
	}

	resp := simpleJSON{
		Meta: map[string]string{"api-version": "1.0"},
		Name: pkg,
	}
	for _, f := range meta.files {
		if !allowedSet[f.version] {
			continue
		}
		fe := fileEntry{
			Filename: f.filename,
			URL:      f.url,
			Hashes:   make(map[string]string),
		}
		if f.sha256 != "" {
			fe.Hashes["sha256"] = f.sha256
		}
		resp.Files = append(resp.Files, fe)
	}
	b, err := json.Marshal(resp)
	if err != nil {
		// Fallback to empty valid response.
		b = []byte(`{"meta":{"api-version":"1.0"},"name":"` + pkg + `","files":[]}`)
	}
	return b, ctPyPISimpleJSON
}

// normalizePyPIName lowercases and replaces hyphens/underscores/dots with hyphens (PEP 503).
func normalizePyPIName(name string) string {
	var sb strings.Builder
	for _, ch := range strings.ToLower(name) {
		if ch == '_' || ch == '.' {
			sb.WriteRune('-')
		} else {
			sb.WriteRune(ch)
		}
	}
	return sb.String()
}

// addUpstreamAuth attaches the configured credentials to an outgoing request.
func (s *Server) addUpstreamAuth(req *http.Request) {
	if s.cfg.Upstream.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.Upstream.Token)
	} else if s.cfg.Upstream.Username != "" {
		req.SetBasicAuth(s.cfg.Upstream.Username, s.cfg.Upstream.Password)
	}
}

// evaluateExternalURL runs rule engine evaluation on the package and version
// extracted from a PyPI file download URL. Returns a non-nil FilterDecision
// when the request should be denied. Returns nil when extraction fails (fail-open).
func (s *Server) evaluateExternalURL(rawURL string) *rules.FilterDecision {
	idx := strings.LastIndex(rawURL, "/")
	if idx < 0 {
		return nil
	}
	filename := rawURL[idx+1:]
	if qi := strings.Index(filename, "?"); qi >= 0 {
		filename = filename[:qi]
	}

	pkgName, version := extractPkgVersionFromFilename(filename)
	if pkgName == "" {
		if s.cfg.Policy.FailMode == config.FailModeClosed {
			return &rules.FilterDecision{Allow: false, Reason: "unable to evaluate policy: unrecognized filename"}
		}
		return nil
	}
	pkgName = normalizePyPIName(pkgName)
	pkgMeta := rules.PackageMeta{Name: pkgName}

	pkgDec := s.engine.EvaluatePackage(pkgMeta)
	if !pkgDec.Allow {
		return &pkgDec
	}
	if version != "" {
		ver := rules.VersionMeta{Version: version}

		// If age filtering is configured but PublishedAt is unavailable, deny the request
		// to prevent bypass via direct external URLs (fail-closed for missing metadata).
		if s.engine.RequiresAgeFiltering(pkgName, version) {
			s.logger.Warn("external URL request denied: version metadata unavailable for age check",
				slog.String("package", pkgName), slog.String("version", version))
			return &rules.FilterDecision{Allow: false, Reason: "version metadata unavailable - cannot verify age policy"}
		}

		dec := s.engine.EvaluateVersion(pkgMeta, ver)
		if !dec.Allow {
			return &dec
		}
	}
	return nil
}

// extractPkgVersionFromFilename extracts a package name and version from a PyPI
// distribution filename (sdist or wheel). Returns empty strings if extraction fails.
func extractPkgVersionFromFilename(filename string) (string, string) {
	base := filename
	for _, ext := range []string{".tar.gz", ".tar.bz2", ".zip", ".whl", ".egg"} {
		if strings.HasSuffix(strings.ToLower(base), ext) {
			base = base[:len(base)-len(ext)]
			break
		}
	}
	parts := strings.Split(base, "-")
	for i, part := range parts {
		if i > 0 && len(part) > 0 && part[0] >= '0' && part[0] <= '9' {
			return strings.Join(parts[:i], "-"), part
		}
	}
	return "", ""
}
