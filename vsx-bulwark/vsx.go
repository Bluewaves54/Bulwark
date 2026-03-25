// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"Bulwark/common/config"
	"Bulwark/common/rules"
)

const (
	hdrContentType    = "Content-Type"
	mimeJSON          = "application/json"
	errMsgUpstream    = "upstream error"
	blockReasonAllVer = "all available versions blocked by policy"
	schemeHTTPS       = "https"
	schemeHTTP        = "http"
)

// proxyBaseURL returns the proxy's own base URL (scheme + host) so that
// asset download URIs in responses can be rewritten to route through
// the proxy instead of going directly to the upstream CDN.
func (s *Server) proxyBaseURL(r *http.Request) string {
	scheme := schemeHTTPS
	if s.cfg.Server.TLSCertFile == "" {
		scheme = schemeHTTP
	}
	return scheme + "://" + r.Host
}

// hopByHopHeaders is the set of headers that MUST NOT be forwarded between
// HTTP hops per RFC 7230 §6.1. Every other request header is forwarded to
// the upstream, making the proxy automatically forward-compatible with new
// VS Code versions that add additional request headers.
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// stripResponseHeaders lists upstream response headers that must NOT be
// forwarded to the local client. Strict-Transport-Security is the most
// critical: when Open VSX (or any HTTPS upstream) returns an HSTS header
// and the proxy blindly forwards it, Chromium caches the HSTS policy for
// "localhost" and silently upgrades every subsequent http:// request to
// https://, which fails because the proxy has no TLS listener. This causes
// VS Code to report "Failed to fetch" for ALL gallery requests.
//
// CORS headers are set by corsAndLogMiddleware on every response. Forwarding
// them from upstream would produce a multi-value Access-Control-Allow-Origin
// header (e.g. "vscode-file://vscode-app, *") which browsers treat as invalid
// and block entirely (CORS policy: multiple values not allowed).
var stripResponseHeaders = map[string]bool{
	"Strict-Transport-Security":        true,
	"Public-Key-Pins":                  true,
	"Public-Key-Pins-Report-Only":      true,
	"Access-Control-Allow-Origin":      true,
	"Access-Control-Allow-Credentials": true,
	"Access-Control-Allow-Methods":     true,
	"Access-Control-Allow-Headers":     true,
	"Access-Control-Expose-Headers":    true,
	"Access-Control-Max-Age":           true,
}

// forwardResponseHeaders copies upstream response headers to the client
// writer, skipping hop-by-hop headers and headers that are unsafe for a
// localhost HTTP proxy (e.g. HSTS).
func forwardResponseHeaders(src http.Header, dst http.Header) {
	for k, vals := range src {
		if hopByHopHeaders[k] || stripResponseHeaders[k] {
			continue
		}
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}

// forwardRequestHeaders copies all request headers from src to dst, excluding
// RFC 7230 hop-by-hop headers and any caller-specified exclusions. The
// deny-list strategy means the proxy is automatically forward-compatible:
// headers added by future VS Code versions are forwarded without any code change.
func forwardRequestHeaders(src, dst *http.Request, exclude ...string) {
	excl := make(map[string]bool, len(exclude))
	for _, h := range exclude {
		excl[textproto.CanonicalMIMEHeaderKey(h)] = true
	}
	for k, vals := range src.Header {
		if hopByHopHeaders[k] || excl[k] {
			continue
		}
		dst.Header[k] = vals
	}
}

// extensionID builds the canonical VS Code extension ID: "namespace.name".
func extensionID(namespace, name string) string {
	return strings.ToLower(namespace) + "." + strings.ToLower(name)
}

// handleExtension serves a filtered Open VSX extension metadata response.
// Endpoint: GET /api/{namespace}/{extension}.
func (s *Server) handleExtension(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	if namespace == "-" {
		s.handlePassthrough(w, r)
		return
	}
	extension := r.PathValue("extension")
	extID := extensionID(namespace, extension)

	s.reqTotal.Add(1)

	cacheKey := "ext:" + extID
	if entry := s.cache.Get(cacheKey); entry != nil {
		w.Header().Set(hdrContentType, entry.ContentType)
		w.Header().Set("X-Cache", "HIT")
		w.WriteHeader(entry.StatusCode)
		w.Write(entry.Body) //nolint:errcheck
		return
	}

	// Deny explicitly-blocked extensions before hitting the upstream.
	// Without this early exit, removed IOC extensions (e.g. Glassworm) would
	// return a 404 passthrough instead of a 403 policy block.
	pkgMeta := rules.PackageMeta{Name: extID}
	pkgDec := s.engine.EvaluatePackage(pkgMeta)
	if !pkgDec.Allow {
		s.reqDenied.Add(1)
		s.logger.Info("extension blocked by policy",
			slog.String("extension", extID),
			slog.String("rule", pkgDec.RuleName),
			slog.String("reason", pkgDec.Reason),
		)
		errBody := fmt.Sprintf(`{"error":"[Bulwark] %s: %s"}`, extID, pkgDec.Reason)
		entry := &rules.CacheEntry{Body: []byte(errBody), ContentType: mimeJSON, StatusCode: http.StatusForbidden}
		s.cache.Set(cacheKey, entry)
		w.Header().Set(hdrContentType, mimeJSON)
		w.Header().Set("X-Cache", "MISS")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(errBody)) //nolint:errcheck
		return
	}
	if pkgDec.DryRun {
		s.reqDryRun.Add(1)
	}

	upstreamURL := s.upstreamAPIURL("/api/" + namespace + "/" + extension)
	if s.isMarketplace() {
		s.handleExtensionViaGallery(w, r, namespace, extension, extID, pkgMeta)
		return
	}
	body, ct, statusCode, err := s.fetchUpstream(upstreamURL, r)
	if err != nil {
		s.logger.Warn("upstream fetch failed",
			slog.String("extension", extID),
			slog.String("error", err.Error()),
		)
		http.Error(w, errMsgUpstream, http.StatusBadGateway)
		return
	}

	if statusCode >= 500 {
		http.Error(w, errMsgUpstream, http.StatusBadGateway)
		return
	}
	if statusCode != http.StatusOK {
		w.Header().Set(hdrContentType, ct)
		w.WriteHeader(statusCode)
		w.Write(body) //nolint:errcheck
		return
	}

	filtered, removed, blockReason, filterErr := filterExtensionResponse(body, extID, s.engine, s.logger)
	if filterErr != nil {
		s.logger.Warn("filtering extension metadata failed",
			slog.String("extension", extID),
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

	if ct == "" {
		ct = mimeJSON
	}

	if blockReason != "" {
		errBody := fmt.Sprintf(`{"error":"[Bulwark] %s: %s"}`, extID, blockReason)
		entry := &rules.CacheEntry{Body: []byte(errBody), ContentType: mimeJSON, StatusCode: http.StatusForbidden}
		s.cache.Set(cacheKey, entry)
		w.Header().Set(hdrContentType, mimeJSON)
		w.Header().Set("X-Cache", "MISS")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(errBody)) //nolint:errcheck
		return
	}

	entry := &rules.CacheEntry{Body: filtered, ContentType: ct, StatusCode: http.StatusOK}
	s.cache.Set(cacheKey, entry)

	w.Header().Set(hdrContentType, ct)
	w.Header().Set("X-Cache", "MISS")
	if removed > 0 {
		w.Header().Set("X-Curation-Policy-Notice", fmt.Sprintf("%d item(s) filtered by policy", removed))
	}
	w.WriteHeader(http.StatusOK)
	w.Write(filtered) //nolint:errcheck
}

// handleExtensionVersion serves a filtered version-specific metadata response.
// Endpoint: GET /api/{namespace}/{extension}/{version}.
func (s *Server) handleExtensionVersion(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	if namespace == "-" {
		s.handlePassthrough(w, r)
		return
	}
	extension := r.PathValue("extension")
	version := r.PathValue("version")
	extID := extensionID(namespace, extension)

	s.reqTotal.Add(1)

	pkgMeta := rules.PackageMeta{Name: extID}
	pkgDec := s.engine.EvaluatePackage(pkgMeta)
	if !pkgDec.Allow {
		s.reqDenied.Add(1)
		s.logger.Info("extension blocked",
			slog.String("extension", extID),
			slog.String("rule", pkgDec.RuleName),
			slog.String("reason", pkgDec.Reason),
		)
		errBody := fmt.Sprintf(`{"error":"[Bulwark] %s: %s"}`, extID, pkgDec.Reason)
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(errBody)) //nolint:errcheck
		return
	}
	if pkgDec.DryRun {
		s.reqDryRun.Add(1)
	}

	upstreamURL := s.upstreamAPIURL("/api/" + namespace + "/" + extension + "/" + version)
	body, ct, statusCode, err := s.fetchUpstream(upstreamURL, r)
	if err != nil {
		s.logger.Warn("upstream fetch failed",
			slog.String("extension", extID),
			slog.String("version", version),
			slog.String("error", err.Error()),
		)
		http.Error(w, errMsgUpstream, http.StatusBadGateway)
		return
	}

	if statusCode >= 500 {
		http.Error(w, errMsgUpstream, http.StatusBadGateway)
		return
	}
	if statusCode != http.StatusOK {
		w.Header().Set(hdrContentType, ct)
		w.WriteHeader(statusCode)
		w.Write(body) //nolint:errcheck
		return
	}

	ver := extractVersionMeta(body, version)

	if s.engine.RequiresAgeFiltering(extID, version) && ver.PublishedAt.IsZero() {
		s.reqDenied.Add(1)
		s.logger.Warn("version request denied: metadata unavailable for age check",
			slog.String("extension", extID), slog.String("version", version))
		errBody := fmt.Sprintf(`{"error":"[Bulwark] %s@%s: version metadata unavailable - cannot verify age policy"}`, extID, version)
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(errBody)) //nolint:errcheck
		return
	}

	dec := s.engine.EvaluateVersion(pkgMeta, ver)
	if !dec.Allow {
		s.reqDenied.Add(1)
		s.logger.Info("version blocked",
			slog.String("extension", extID),
			slog.String("version", version),
			slog.String("rule", dec.RuleName),
			slog.String("reason", dec.Reason),
		)
		errBody := fmt.Sprintf(`{"error":"[Bulwark] %s@%s: %s"}`, extID, version, dec.Reason)
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(errBody)) //nolint:errcheck
		return
	}
	if dec.DryRun {
		s.reqDryRun.Add(1)
	}

	s.reqAllowed.Add(1)
	w.Header().Set(hdrContentType, ct)
	w.WriteHeader(http.StatusOK)
	w.Write(body) //nolint:errcheck
}

// handleVsixDownload proxies a .vsix file download after policy evaluation.
// Endpoint: GET /api/{namespace}/{extension}/{version}/file/{fileName...}.
func (s *Server) handleVsixDownload(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	if namespace == "-" {
		s.handlePassthrough(w, r)
		return
	}
	extension := r.PathValue("extension")
	version := r.PathValue("version")
	extID := extensionID(namespace, extension)

	s.reqTotal.Add(1)

	pkgMeta := rules.PackageMeta{Name: extID}
	pkgDec := s.engine.EvaluatePackage(pkgMeta)
	if !pkgDec.Allow {
		s.reqDenied.Add(1)
		s.logger.Info("vsix download blocked",
			slog.String("extension", extID),
			slog.String("rule", pkgDec.RuleName),
			slog.String("reason", pkgDec.Reason),
		)
		errBody := fmt.Sprintf(`{"error":"[Bulwark] %s: %s"}`, extID, pkgDec.Reason)
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(errBody)) //nolint:errcheck
		return
	}
	if pkgDec.DryRun {
		s.reqDryRun.Add(1)
	}

	ver := s.fetchVersionMetaFromAPI(namespace, extension, version)

	if s.engine.RequiresAgeFiltering(extID, version) && ver.PublishedAt.IsZero() {
		s.reqDenied.Add(1)
		s.logger.Warn("vsix download denied: version metadata unavailable for age check",
			slog.String("extension", extID), slog.String("version", version))
		errBody := fmt.Sprintf(`{"error":"[Bulwark] %s@%s: version metadata unavailable - cannot verify age policy"}`, extID, version)
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(errBody)) //nolint:errcheck
		return
	}

	dec := s.engine.EvaluateVersion(pkgMeta, ver)
	if !dec.Allow {
		s.reqDenied.Add(1)
		s.logger.Info("vsix download version blocked",
			slog.String("extension", extID),
			slog.String("version", version),
			slog.String("rule", dec.RuleName),
			slog.String("reason", dec.Reason),
		)
		errBody := fmt.Sprintf(`{"error":"[Bulwark] %s@%s: %s"}`, extID, version, dec.Reason)
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(errBody)) //nolint:errcheck
		return
	}
	if dec.DryRun {
		s.reqDryRun.Add(1)
	}

	upstreamURL := s.upstreamVsixURL(r.URL.Path, namespace, extension, version)
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

	forwardResponseHeaders(resp.Header, w.Header())
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
	s.reqAllowed.Add(1)
}

// handleQuery intercepts Open VSX query requests and filters the results.
// Endpoint: GET/POST /api/-/query.
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	s.reqTotal.Add(1)

	upstreamURL := s.upstreamAPIURL(r.URL.Path)
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	var reqBody io.Reader
	if r.Method == http.MethodPost {
		reqBody = r.Body
	}

	req, err := http.NewRequest(r.Method, upstreamURL, reqBody)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodPost {
		req.Header.Set(hdrContentType, r.Header.Get(hdrContentType))
	}
	req.Header.Set("Accept", mimeJSON)
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

	filtered, removed := filterQueryResponse(body, s.engine, s.logger)
	s.reqDenied.Add(int64(removed))
	s.reqAllowed.Add(1)

	ct := resp.Header.Get(hdrContentType)
	if ct == "" {
		ct = mimeJSON
	}
	w.Header().Set(hdrContentType, ct)
	if removed > 0 {
		w.Header().Set("X-Curation-Policy-Notice", fmt.Sprintf("%d extension(s) filtered by policy", removed))
	}
	w.WriteHeader(http.StatusOK)
	w.Write(filtered) //nolint:errcheck
}

// handlePassthrough proxies a request to upstream without policy evaluation.
// Used for search and other non-install endpoints.
func (s *Server) handlePassthrough(w http.ResponseWriter, r *http.Request) {
	s.reqTotal.Add(1)

	upstreamURL := s.upstreamAPIURL(r.URL.Path)
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequest(http.MethodGet, upstreamURL, nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	forwardRequestHeaders(r, req)
	s.addUpstreamAuth(req)

	resp, err := s.upstream.Do(req)
	if err != nil {
		http.Error(w, errMsgUpstream, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	forwardResponseHeaders(resp.Header, w.Header())
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
	s.reqAllowed.Add(1)
}

// handleGalleryPassthrough forwards the request to the upstream VS Code–compatible
// gallery endpoint (/vscode/gallery or /vscode/item) preserving the HTTP method
// and body. Used for gallery routes that do not need policy filtering (e.g.
// asset fetch, item page). VSIX downloads via the gallery vspackage path and
// extension queries are handled by dedicated handlers.
func (s *Server) handleGalleryPassthrough(w http.ResponseWriter, r *http.Request) {
	s.reqTotal.Add(1)

	var upstreamURL string
	if strings.HasPrefix(r.URL.Path, "/vscode/item") {
		upstreamURL = s.upstreamItemURL(r.URL.Path)
	} else {
		upstreamURL = s.upstreamGalleryURL(r.URL.Path)
	}
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, r.Body)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Forward all non-hop-by-hop request headers to the upstream. This deny-list
	// approach (rather than a whitelist) means new VS Code headers such as
	// X-Market-Client-Id (added in 1.112) are forwarded automatically.
	forwardRequestHeaders(r, req)
	s.addUpstreamAuth(req)

	resp, err := s.upstream.Do(req)
	if err != nil {
		http.Error(w, errMsgUpstream, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	forwardResponseHeaders(resp.Header, w.Header())
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
	s.reqAllowed.Add(1)
}

// ─── Open VSX data types ────────────────────────────────────────────────────

// ─── VS Code gallery data types ─────────────────────────────────────────────

// handleGalleryQuery intercepts VS Code gallery extensionquery POST requests,
// forwards them to the upstream, and filters blocked extensions from the response.
// Endpoint: POST /vscode/gallery/extensionquery.
func (s *Server) handleGalleryQuery(w http.ResponseWriter, r *http.Request) {
	s.reqTotal.Add(1)

	upstreamURL := s.upstreamGalleryURL(r.URL.Path)
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	// Read the request body so we can inspect and optionally modify it.
	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	// When the upstream is the Microsoft Marketplace, strip the
	// IncludeStatistics flag (0x200) from the query flags. With this
	// flag set the Marketplace limits the response to a single (latest)
	// version per extension, which prevents version-level policy
	// filtering (e.g. blocking only the pre-release version while
	// keeping the stable one). Statistics are still returned in the
	// response regardless, so downstream clients are unaffected.
	if s.isMarketplace() {
		reqBody = stripMarketplaceStatsFlag(reqBody)
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, strings.NewReader(string(reqBody)))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Accept-Encoding is intentionally excluded: the proxy reads and re-serialises
	// the response JSON to filter policy-blocked extensions. Forwarding it would
	// bypass Go's transparent decompression and pass gzip bytes to json.Unmarshal.
	// All other non-hop-by-hop headers (including X-Market-Client-Id, required
	// since VS Code 1.112) are forwarded automatically via the deny-list approach.
	forwardRequestHeaders(r, req, "Accept-Encoding")
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

	filtered, removed := filterGalleryQueryResponse(body, s.engine, s.logger, s.proxyBaseURL(r))
	s.reqDenied.Add(int64(removed))
	if removed == 0 {
		s.reqAllowed.Add(1)
	}

	ct := resp.Header.Get(hdrContentType)
	if ct == "" {
		ct = mimeJSON
	}
	w.Header().Set(hdrContentType, ct)
	if removed > 0 {
		w.Header().Set("X-Curation-Policy-Notice", fmt.Sprintf("%d extension(s) filtered by policy", removed))
	}
	w.WriteHeader(http.StatusOK)
	w.Write(filtered) //nolint:errcheck
}

// handleGalleryExtensionLatest intercepts the extensionUrlTemplate endpoint
// that VS Code uses to resolve the latest version of an installed extension.
// Path: GET /vscode/gallery/vscode/{pub}/{name}/latest.
// Without this handler the request falls through to handleGalleryPassthrough,
// which proxies the upstream response unfiltered.  VS Code's Node.js main
// process is not subject to CORS so it would read the metadata and proceed to
// install a blocked extension despite the gallery query filtering.
func (s *Server) handleGalleryExtensionLatest(w http.ResponseWriter, r *http.Request) {
	publisher := r.PathValue("pub")
	name := r.PathValue("name")
	extID := extensionID(publisher, name)

	s.reqTotal.Add(1)

	// Package-level check first (deny-list, typosquatting, namespace).
	pkgMeta := rules.PackageMeta{Name: extID}
	pkgDec := s.engine.EvaluatePackage(pkgMeta)
	if !pkgDec.Allow {
		s.reqDenied.Add(1)
		s.logger.Info("gallery extension latest blocked by policy",
			slog.String("extension", extID),
			slog.String("rule", pkgDec.RuleName),
			slog.String("reason", pkgDec.Reason),
		)
		errBody := fmt.Sprintf(`{"error":"[Bulwark] %s: %s"}`, extID, pkgDec.Reason)
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(errBody)) //nolint:errcheck
		return
	}

	// Proxy the request to the upstream.
	var upstreamURL string
	if s.isMarketplace() {
		upstreamURL = s.upstreamGalleryURL(r.URL.Path)
	} else {
		upstreamURL = s.cfg.Upstream.URL + r.URL.Path
	}
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	forwardRequestHeaders(r, req, "Accept-Encoding")
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

	// Try to extract the latest version timestamp and apply age/version rules.
	ver := extractVersionMeta(body, "latest")
	if ver.PublishedAt.IsZero() {
		// Gallery-format response: try marketplace timestamp extraction.
		ver = extractMarketplaceVersionMeta(body, "")
	}

	// If age filtering is required and we have no timestamp, block (fail-closed).
	if s.engine.RequiresAgeFiltering(extID, ver.Version) && ver.PublishedAt.IsZero() {
		s.reqDenied.Add(1)
		s.logger.Warn("gallery extension latest denied: metadata unavailable for age check",
			slog.String("extension", extID))
		errBody := fmt.Sprintf(`{"error":"[Bulwark] %s: version metadata unavailable - cannot verify age policy"}`, extID)
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(errBody)) //nolint:errcheck
		return
	}

	// Version-level rules (age, pre-release, license).
	if !ver.PublishedAt.IsZero() {
		vDec := s.engine.EvaluateVersion(pkgMeta, ver)
		if !vDec.Allow {
			s.reqDenied.Add(1)
			s.logger.Info("gallery extension latest blocked by version rule",
				slog.String("extension", extID),
				slog.String("rule", vDec.RuleName),
				slog.String("reason", vDec.Reason),
			)
			errBody := fmt.Sprintf(`{"error":"[Bulwark] %s: %s"}`, extID, vDec.Reason)
			w.Header().Set(hdrContentType, mimeJSON)
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(errBody)) //nolint:errcheck
			return
		}
	}

	body = rewriteLatestResponseAssetURIs(body, publisher, name, s.proxyBaseURL(r))

	s.reqAllowed.Add(1)
	ct := resp.Header.Get(hdrContentType)
	if ct == "" {
		ct = mimeJSON
	}
	w.Header().Set(hdrContentType, ct)
	w.WriteHeader(http.StatusOK)
	w.Write(body) //nolint:errcheck
}

// handleGalleryVspackage proxies a gallery VSIX download after policy evaluation.
// VS Code may use this path when it does not honour resourceUrlTemplate.
// Endpoint: GET /vscode/gallery/publishers/{pub}/vsextensions/{ext}/{ver}/vspackage.
func (s *Server) handleGalleryVspackage(w http.ResponseWriter, r *http.Request) {
	publisher := r.PathValue("pub")
	extension := r.PathValue("ext")
	version := r.PathValue("ver")
	extID := extensionID(publisher, extension)

	s.reqTotal.Add(1)

	pkgMeta := rules.PackageMeta{Name: extID}
	pkgDec := s.engine.EvaluatePackage(pkgMeta)
	if !pkgDec.Allow {
		s.reqDenied.Add(1)
		s.logger.Info("gallery vspackage download blocked",
			slog.String("extension", extID),
			slog.String("rule", pkgDec.RuleName),
			slog.String("reason", pkgDec.Reason),
		)
		errBody := fmt.Sprintf(`{"error":"[Bulwark] %s: %s"}`, extID, pkgDec.Reason)
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(errBody)) //nolint:errcheck
		return
	}
	if pkgDec.DryRun {
		s.reqDryRun.Add(1)
	}

	ver := s.fetchVersionMetaFromAPI(publisher, extension, version)

	if s.engine.RequiresAgeFiltering(extID, version) && ver.PublishedAt.IsZero() {
		s.reqDenied.Add(1)
		s.logger.Warn("gallery vspackage denied: metadata unavailable for age check",
			slog.String("extension", extID), slog.String("version", version))
		errBody := fmt.Sprintf(`{"error":"[Bulwark] %s@%s: version metadata unavailable - cannot verify age policy"}`, extID, version)
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(errBody)) //nolint:errcheck
		return
	}

	dec := s.engine.EvaluateVersion(pkgMeta, ver)
	if !dec.Allow {
		s.reqDenied.Add(1)
		s.logger.Info("gallery vspackage version blocked",
			slog.String("extension", extID),
			slog.String("version", version),
			slog.String("rule", dec.RuleName),
			slog.String("reason", dec.Reason),
		)
		errBody := fmt.Sprintf(`{"error":"[Bulwark] %s@%s: %s"}`, extID, version, dec.Reason)
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(errBody)) //nolint:errcheck
		return
	}
	if dec.DryRun {
		s.reqDryRun.Add(1)
	}

	upstreamURL := s.upstreamGalleryURL(r.URL.Path)
	req, err := http.NewRequest(http.MethodGet, upstreamURL, nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	forwardRequestHeaders(r, req)
	s.addUpstreamAuth(req)

	resp, err := s.upstream.Do(req)
	if err != nil {
		http.Error(w, errMsgUpstream, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	forwardResponseHeaders(resp.Header, w.Header())
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
	s.reqAllowed.Add(1)
}

// handleGalleryAssetByName handles gallery asset download requests that are
// routed through the proxy via rewritten assetUri/fallbackAssetUri URLs.
// For VSIX downloads it applies full policy checks (package + version rules).
// Other asset types (icons, manifests, changelogs) are forwarded without checks.
// Endpoint: GET /_apis/public/gallery/publisher/{pub}/extension/{ext}/{ver}/assetbyname/{assetType...}.
func (s *Server) handleGalleryAssetByName(w http.ResponseWriter, r *http.Request) {
	publisher := r.PathValue("pub")
	extension := r.PathValue("ext")
	version := r.PathValue("ver")
	assetType := r.PathValue("assetType")
	extID := extensionID(publisher, extension)

	s.reqTotal.Add(1)

	// Only VSIX package downloads need policy evaluation.
	if strings.Contains(assetType, "VSIXPackage") {
		pkgMeta := rules.PackageMeta{Name: extID}
		pkgDec := s.engine.EvaluatePackage(pkgMeta)
		if !pkgDec.Allow && !pkgDec.DryRun {
			s.reqDenied.Add(1)
			s.logger.Info("gallery asset download blocked",
				slog.String("extension", extID),
				slog.String("rule", pkgDec.RuleName),
				slog.String("reason", pkgDec.Reason),
			)
			errBody := fmt.Sprintf(`{"error":"[Bulwark] %s: %s"}`, extID, pkgDec.Reason)
			w.Header().Set(hdrContentType, mimeJSON)
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(errBody)) //nolint:errcheck
			return
		}

		ver := s.fetchVersionMetaFromAPI(publisher, extension, version)
		if s.engine.RequiresAgeFiltering(extID, version) && ver.PublishedAt.IsZero() {
			s.reqDenied.Add(1)
			s.logger.Warn("gallery asset denied: metadata unavailable for age check",
				slog.String("extension", extID), slog.String("version", version))
			errBody := fmt.Sprintf(`{"error":"[Bulwark] %s@%s: version metadata unavailable - cannot verify age policy"}`, extID, version)
			w.Header().Set(hdrContentType, mimeJSON)
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(errBody)) //nolint:errcheck
			return
		}

		dec := s.engine.EvaluateVersion(pkgMeta, ver)
		if !dec.Allow && !dec.DryRun {
			s.reqDenied.Add(1)
			s.logger.Info("gallery asset version blocked",
				slog.String("extension", extID),
				slog.String("version", version),
				slog.String("rule", dec.RuleName),
				slog.String("reason", dec.Reason),
			)
			errBody := fmt.Sprintf(`{"error":"[Bulwark] %s@%s: %s"}`, extID, version, dec.Reason)
			w.Header().Set(hdrContentType, mimeJSON)
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(errBody)) //nolint:errcheck
			return
		}
	}

	// Forward to upstream. Build the canonical marketplace path.
	upstreamURL := s.cfg.Upstream.URL + "/_apis/public/gallery/publisher/" +
		publisher + "/extension/" + extension + "/" + version + "/assetbyname/" + assetType
	req, err := http.NewRequest(http.MethodGet, upstreamURL, nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	forwardRequestHeaders(r, req)
	s.addUpstreamAuth(req)

	resp, err := s.upstream.Do(req)
	if err != nil {
		http.Error(w, errMsgUpstream, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	forwardResponseHeaders(resp.Header, w.Header())
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
	s.reqAllowed.Add(1)
}

// vsxQueryResponse wraps the Open VSX query API response.
type vsxQueryResponse struct {
	Offset     int                 `json:"offset"`
	TotalSize  int                 `json:"totalSize"`
	Extensions []vsxQueryExtension `json:"extensions"`
}

// vsxQueryExtension is a single entry in query results.
type vsxQueryExtension struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Timestamp string `json:"timestamp"`
	// Preserve all other fields via raw re-marshal.
	Extra map[string]json.RawMessage `json:"-"`
}

// ─── Filtering logic ────────────────────────────────────────────────────────

// rawString extracts a string value from a raw JSON map.
func rawString(raw map[string]json.RawMessage, key string) string {
	var s string
	if v, ok := raw[key]; ok {
		json.Unmarshal(v, &s) //nolint:errcheck
	}
	return s
}

// rawBool extracts a bool value from a raw JSON map.
func rawBool(raw map[string]json.RawMessage, key string) bool {
	var b bool
	if v, ok := raw[key]; ok {
		json.Unmarshal(v, &b) //nolint:errcheck
	}
	return b
}

// filterVersions evaluates policy rules against each version in allVersions,
// deleting blocked entries in-place. Returns the count of removed versions.
func filterVersions(allVersions map[string]string, pkgMeta rules.PackageMeta, latestVersion, latestTimestamp, latestLicense string, latestPreRelease bool, engine *rules.RuleEngine, logger *slog.Logger, extID string) int {
	removed := 0
	for ver := range allVersions {
		vm := rules.VersionMeta{Version: ver}
		if ver == latestVersion {
			vm = buildLatestVersionMeta(latestVersion, latestTimestamp, latestLicense, latestPreRelease)
		}
		// For non-latest versions the Open VSX API only provides a URL, not a
		// timestamp.  When age filtering is required and the timestamp is absent,
		// deny the version (fail-closed) so a brand-new extension cannot slip
		// through because its metadata is unavailable.
		if vm.PublishedAt.IsZero() && engine.RequiresAgeFiltering(extID, ver) {
			logger.Info("version blocked: metadata unavailable for age check",
				slog.String("extension", extID),
				slog.String("version", ver),
			)
			delete(allVersions, ver)
			removed++
			continue
		}
		dec := engine.EvaluateVersion(pkgMeta, vm)
		if dec.Allow || dec.DryRun {
			continue
		}
		logger.Info("version blocked",
			slog.String("extension", extID),
			slog.String("version", ver),
			slog.String("rule", dec.RuleName),
			slog.String("reason", dec.Reason),
		)
		delete(allVersions, ver)
		removed++
	}
	return removed
}

// filterTransitiveDeps strips denied extension IDs from a manifest relationship
// array such as "extensionPack" or "extensionDependencies". It mutates raw
// in-place and returns the number of removed references plus a human-readable
// block reason for the first blocked dependency (empty when none was blocked).
func filterTransitiveDeps(raw map[string]json.RawMessage, field string, engine *rules.RuleEngine, logger *slog.Logger, parentExtID string) (int, string) {
	v, ok := raw[field]
	if !ok {
		return 0, ""
	}
	var deps []string
	if err := json.Unmarshal(v, &deps); err != nil || len(deps) == 0 {
		return 0, ""
	}
	kept := make([]string, 0, len(deps))
	removed := 0
	firstBlockReason := ""
	for _, dep := range deps {
		dep = strings.ToLower(strings.TrimSpace(dep))
		dec := engine.EvaluatePackage(rules.PackageMeta{Name: dep})
		if !dec.Allow && !dec.DryRun {
			logger.Info("transitive extension reference stripped",
				slog.String("parent", parentExtID),
				slog.String("field", field),
				slog.String("dependency", dep),
				slog.String("rule", dec.RuleName),
				slog.String("reason", dec.Reason),
			)
			if firstBlockReason == "" {
				firstBlockReason = fmt.Sprintf("dependency '%s' denied: %s", dep, dec.Reason)
			}
			removed++
			continue
		}
		kept = append(kept, dep)
	}
	if removed == 0 {
		return 0, ""
	}
	b, _ := json.Marshal(kept)
	raw[field] = b
	return removed, firstBlockReason
}

// filterExtensionResponse parses an extension metadata response, applies policy
// to filter denied versions from allVersions, and returns the modified JSON.
func filterExtensionResponse(body []byte, extID string, engine *rules.RuleEngine, logger *slog.Logger) ([]byte, int, string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, 0, "", fmt.Errorf("parsing extension metadata: %w", err)
	}

	pkgMeta := rules.PackageMeta{Name: extID}
	pkgDec := engine.EvaluatePackage(pkgMeta)
	if !pkgDec.Allow && !pkgDec.DryRun {
		logger.Info("extension blocked by policy",
			slog.String("extension", extID),
			slog.String("rule", pkgDec.RuleName),
			slog.String("reason", pkgDec.Reason),
		)
		empty := fmt.Sprintf(`{"error":"[Bulwark] %s: %s"}`, extID, pkgDec.Reason)
		return []byte(empty), 1, pkgDec.Reason, nil
	}

	var allVersions map[string]string
	if v, ok := raw["allVersions"]; ok {
		json.Unmarshal(v, &allVersions) //nolint:errcheck
	}

	removed := 0
	if allVersions != nil {
		removed = filterVersions(allVersions, pkgMeta,
			rawString(raw, "version"), rawString(raw, "timestamp"),
			rawString(raw, "license"), rawBool(raw, "preRelease"),
			engine, logger, extID)

		if len(allVersions) == 0 && removed > 0 {
			empty := fmt.Sprintf(`{"error":"[Bulwark] %s: %s"}`, extID, blockReasonAllVer)
			return []byte(empty), removed, blockReasonAllVer, nil
		}

		avBytes, _ := json.Marshal(allVersions)
		raw["allVersions"] = avBytes
	}

	// Strip denied extensions from transitive installation fields.
	// GlassWorm abuses extensionPack/extensionDependencies to pull malicious
	// extensions through benign-looking parents (Socket Research, March 2026).
	// extensionPack members are optional bundles — strip silently.
	// extensionDependencies are required; block the parent when any dep is denied
	// so VS Code surfaces a meaningful error instead of partially installing.
	packRemoved, _ := filterTransitiveDeps(raw, "extensionPack", engine, logger, extID)
	depsRemoved, depsBlock := filterTransitiveDeps(raw, "extensionDependencies", engine, logger, extID)
	removed += packRemoved + depsRemoved

	// Required dependency blocked → reject the parent extension entirely.
	if depsBlock != "" {
		blockMsg := fmt.Sprintf("blocked: %s", depsBlock)
		logger.Info("extension blocked due to denied required dependency",
			slog.String("extension", extID),
			slog.String("reason", blockMsg),
		)
		empty := fmt.Sprintf(`{"error":"[Bulwark] %s: %s"}`, extID, blockMsg)
		return []byte(empty), removed, blockMsg, nil
	}

	filtered, err := json.Marshal(raw)
	if err != nil {
		return nil, 0, "", fmt.Errorf("re-marshalling extension metadata: %w", err)
	}
	return filtered, removed, "", nil
}

// buildLatestVersionMeta constructs a VersionMeta from the root-level fields
// of the extension response for the latest version.
func buildLatestVersionMeta(version, timestamp, license string, preRelease bool) rules.VersionMeta {
	vm := rules.VersionMeta{
		Version:    version,
		License:    license,
		PreRelease: preRelease,
	}
	if ts := strings.TrimSpace(timestamp); ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			vm.PublishedAt = t.UTC()
		}
	}
	return vm
}

// extractVersionMeta parses version metadata from an Open VSX version response body.
func extractVersionMeta(body []byte, version string) rules.VersionMeta {
	meta := rules.VersionMeta{Version: version}

	var data struct {
		Timestamp  string `json:"timestamp"`
		License    string `json:"license"`
		PreRelease bool   `json:"preRelease"`
		Version    string `json:"version"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return meta
	}

	if ts := strings.TrimSpace(data.Timestamp); ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			meta.PublishedAt = t.UTC()
		}
	}
	meta.License = strings.TrimSpace(data.License)
	meta.PreRelease = data.PreRelease
	return meta
}

// isQueryExtensionBlockedByVersion checks whether a single Open VSX query
// result should be blocked by version-level rules (age, pre-release).
// Returns true if the extension should be removed from results.
func isQueryExtensionBlockedByVersion(ext vsxQueryExtension, extID string, pkgMeta rules.PackageMeta, engine *rules.RuleEngine, logger *slog.Logger) bool {
	vm := rules.VersionMeta{Version: ext.Version}
	if ts := strings.TrimSpace(ext.Timestamp); ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			vm.PublishedAt = t.UTC()
		}
	}
	if engine.RequiresAgeFiltering(extID, ext.Version) && vm.PublishedAt.IsZero() {
		logger.Warn("query result filtered: metadata unavailable for age check",
			slog.String("extension", extID))
		return true
	}
	vDec := engine.EvaluateVersion(pkgMeta, vm)
	if !vDec.Allow && !vDec.DryRun {
		logger.Info("query result filtered by version rule",
			slog.String("extension", extID),
			slog.String("version", ext.Version),
			slog.String("rule", vDec.RuleName),
			slog.String("reason", vDec.Reason),
		)
		return true
	}
	return false
}

// filterQueryResponse filters extensions from an Open VSX query response.
// Returns filtered JSON and the count of removed extensions.
// Evaluates both package-level rules and version-level rules (age, pre-release)
// so that new extensions are blocked at the search stage, not only at download.
func filterQueryResponse(body []byte, engine *rules.RuleEngine, logger *slog.Logger) ([]byte, int) {
	var resp vsxQueryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return body, 0
	}

	filtered := make([]vsxQueryExtension, 0, len(resp.Extensions))
	removed := 0
	for _, ext := range resp.Extensions {
		extID := extensionID(ext.Namespace, ext.Name)
		pkgMeta := rules.PackageMeta{Name: extID}
		dec := engine.EvaluatePackage(pkgMeta)
		if !dec.Allow && !dec.DryRun {
			logger.Info("query result filtered",
				slog.String("extension", extID),
				slog.String("rule", dec.RuleName),
				slog.String("reason", dec.Reason),
			)
			removed++
			continue
		}

		if isQueryExtensionBlockedByVersion(ext, extID, pkgMeta, engine, logger) {
			removed++
			continue
		}

		filtered = append(filtered, ext)
	}

	resp.Extensions = filtered
	resp.TotalSize -= removed

	out, err := json.Marshal(resp)
	if err != nil {
		return body, 0
	}
	return out, removed
}

// setResultMetadataCount overwrites the TotalCount entry inside a gallery
// extensionquery result's resultMetadata array with exactCount (the actual
// number of extensions in the filtered response). Previous versions subtracted
// the removed count from the original TotalCount, but that value is a *global*
// total across all pages (e.g. 95 000 for @recentlyPublished). If the proxy
// filters items from the current page, the global total minus a small page
// delta still far exceeds the page contents. VS Code's PagedModel allocates
// that many slots in its virtual list, tries to render items that do not exist,
// and crashes with "Cannot read properties of undefined (reading 'identifier')".
// Setting TotalCount = len(kept extensions) prevents this entirely.
func setResultMetadataCount(resultMap map[string]json.RawMessage, exactCount int) {
	rmRaw, ok := resultMap["resultMetadata"]
	if !ok {
		return
	}
	var metadata []map[string]json.RawMessage
	if err := json.Unmarshal(rmRaw, &metadata); err != nil {
		return
	}
	changed := false
	for _, item := range metadata {
		if updateResultCountItem(item, exactCount) {
			changed = true
		}
	}
	if changed {
		if b, err := json.Marshal(metadata); err == nil {
			resultMap["resultMetadata"] = b
		}
	}
}

// updateResultCountItem updates TotalCount in a single resultMetadata item.
// Returns true if a count was updated.
func updateResultCountItem(item map[string]json.RawMessage, exactCount int) bool {
	var mType string
	if v, ok := item["metadataType"]; !ok || json.Unmarshal(v, &mType) != nil || mType != "ResultCount" {
		return false
	}
	itemsRaw, ok := item["metadataItems"]
	if !ok {
		return false
	}
	var items []map[string]json.RawMessage
	if json.Unmarshal(itemsRaw, &items) != nil {
		return false
	}
	updated := false
	for _, mi := range items {
		var name string
		if v, ok := mi["name"]; !ok || json.Unmarshal(v, &name) != nil || name != "TotalCount" {
			continue
		}
		if b, err := json.Marshal(exactCount); err == nil {
			mi["count"] = b
			updated = true
		}
	}
	if updated {
		if b, err := json.Marshal(items); err == nil {
			item["metadataItems"] = b
		}
	}
	return updated
}

// filterGalleryQueryResponse filters extensions from a VS Code gallery
// extensionquery response. Returns filtered JSON and the count of removed
// extensions. Works at the raw JSON level to preserve all unknown fields.
// filterExtensionsInResult filters blocked extensions from a single gallery result object.
// It returns the updated result JSON and the number of removed extensions.
func filterExtensionsInResult(rawResult json.RawMessage, engine *rules.RuleEngine, logger *slog.Logger, proxyBase string) (json.RawMessage, int) {
	var resultMap map[string]json.RawMessage
	if err := json.Unmarshal(rawResult, &resultMap); err != nil {
		return rawResult, 0
	}

	var allExts []json.RawMessage
	if e, ok := resultMap["extensions"]; ok {
		json.Unmarshal(e, &allExts) //nolint:errcheck // allExts stays nil on error, handled by loop below.
	}

	kept := make([]json.RawMessage, 0, len(allExts))
	removed := 0
	for _, rawExt := range allExts {
		filtered, blocked := filterGalleryExtension(rawExt, engine, logger, proxyBase)
		if blocked {
			removed++
			continue
		}
		kept = append(kept, filtered)
	}

	keptBytes, _ := json.Marshal(kept)
	resultMap["extensions"] = keptBytes
	if removed > 0 {
		setResultMetadataCount(resultMap, len(kept))
	}
	newResult, _ := json.Marshal(resultMap)
	return newResult, removed
}

// filterGalleryExtension evaluates a raw gallery extension JSON against policy
// rules. Instead of blocking the entire extension when any version fails, it
// filters the versions array — keeping only versions that pass version-level
// rules (age, pre-release). The extension is fully blocked only when:
//   - A package-level rule denies it (deny-list, typosquat), or
//   - ALL versions are removed by version-level rules.
//
// Returns (filteredJSON, true) when the extension is fully blocked, or
// (filteredJSON, false) when at least one version survives. The returned JSON
// has blocked versions stripped so VS Code only shows installable versions.
func filterGalleryExtension(rawExt json.RawMessage, engine *rules.RuleEngine, logger *slog.Logger, proxyBase string) (json.RawMessage, bool) {
	var ext struct {
		Publisher struct {
			PublisherName string `json:"publisherName"`
		} `json:"publisher"`
		ExtensionName string `json:"extensionName"`
	}
	if err := json.Unmarshal(rawExt, &ext); err != nil {
		return rawExt, false
	}
	extID := extensionID(ext.Publisher.PublisherName, ext.ExtensionName)
	pkgMeta := rules.PackageMeta{Name: extID}

	dec := engine.EvaluatePackage(pkgMeta)
	if !dec.Allow && !dec.DryRun {
		logger.Info("gallery query result filtered",
			slog.String("extension", extID),
			slog.String("rule", dec.RuleName),
			slog.String("reason", dec.Reason),
		)
		return nil, true
	}

	// Parse all fields preserving unknown ones.
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(rawExt, &rawMap); err != nil {
		return rawExt, false
	}

	versionsRaw, ok := rawMap["versions"]
	if !ok {
		return rawExt, false
	}
	var versions []json.RawMessage
	if err := json.Unmarshal(versionsRaw, &versions); err != nil || len(versions) == 0 {
		return rawExt, false
	}

	kept := filterGalleryVersions(versions, extID, pkgMeta, engine, logger)
	if len(kept) == 0 {
		logger.Info("gallery query result fully filtered: all versions blocked",
			slog.String("extension", extID),
		)
		return nil, true
	}

	// Rewrite asset URIs in kept versions to route downloads through the proxy.
	if proxyBase != "" {
		for i, v := range kept {
			kept[i] = rewriteVersionAssetURIs(v, ext.Publisher.PublisherName, ext.ExtensionName, proxyBase)
		}
	}

	if len(kept) == len(versions) && proxyBase == "" {
		return rawExt, false
	}

	keptBytes, _ := json.Marshal(kept)
	rawMap["versions"] = keptBytes
	result, _ := json.Marshal(rawMap)
	return result, false
}

// filterGalleryVersions evaluates each version in a gallery extension's versions
// array against version-level rules (age, pre-release). Returns only the
// versions that pass policy. Unparseable version entries are kept (fail-open for
// individual items to avoid silently dropping data).
func filterGalleryVersions(versions []json.RawMessage, extID string, pkgMeta rules.PackageMeta, engine *rules.RuleEngine, logger *slog.Logger) []json.RawMessage {
	kept := make([]json.RawMessage, 0, len(versions))
	for _, rawVer := range versions {
		var ver struct {
			Version     string `json:"version"`
			LastUpdated string `json:"lastUpdated"`
			Properties  []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"properties"`
		}
		if json.Unmarshal(rawVer, &ver) != nil {
			kept = append(kept, rawVer)
			continue
		}
		vm := rules.VersionMeta{Version: ver.Version, PreRelease: isMarketplacePreRelease(ver.Properties)}
		if ts := strings.TrimSpace(ver.LastUpdated); ts != "" {
			vm.PublishedAt = parseMarketplaceTimestamp(ts)
		}
		if engine.RequiresAgeFiltering(extID, ver.Version) && vm.PublishedAt.IsZero() {
			logger.Info("gallery version filtered: metadata unavailable for age check",
				slog.String("extension", extID), slog.String("version", ver.Version))
			continue
		}
		vdec := engine.EvaluateVersion(pkgMeta, vm)
		if !vdec.Allow && !vdec.DryRun {
			logger.Info("gallery version filtered by rule",
				slog.String("extension", extID), slog.String("version", ver.Version),
				slog.String("rule", vdec.RuleName), slog.String("reason", vdec.Reason))
			continue
		}
		kept = append(kept, rawVer)
	}
	return kept
}

// rewriteVersionAssetURIs rewrites assetUri, fallbackAssetUri and files[].source
// in a gallery version entry to route all asset downloads through the proxy.
// This prevents VS Code from downloading VSIXes directly from the upstream CDN,
// ensuring every download is subject to policy evaluation.
func rewriteVersionAssetURIs(rawVer json.RawMessage, publisher, extension, proxyBase string) json.RawMessage {
	var verMap map[string]json.RawMessage
	if json.Unmarshal(rawVer, &verMap) != nil {
		return rawVer
	}
	var version string
	if v, ok := verMap["version"]; ok {
		json.Unmarshal(v, &version) //nolint:errcheck
	}
	if version == "" || publisher == "" || extension == "" {
		return rawVer
	}

	basePath := "/_apis/public/gallery/publisher/" + publisher +
		"/extension/" + extension + "/" + version + "/assetbyname"
	proxyAssetURI := proxyBase + basePath

	if b, err := json.Marshal(proxyAssetURI); err == nil {
		verMap["assetUri"] = b
		verMap["fallbackAssetUri"] = b
	}

	rewriteFileSources(verMap, proxyAssetURI)

	result, err := json.Marshal(verMap)
	if err != nil {
		return rawVer
	}
	return result
}

// rewriteFileSources rewrites the source URLs in the files array of a version entry.
func rewriteFileSources(verMap map[string]json.RawMessage, proxyAssetURI string) {
	filesRaw, ok := verMap["files"]
	if !ok {
		return
	}
	var files []map[string]json.RawMessage
	if json.Unmarshal(filesRaw, &files) != nil {
		return
	}
	for i, f := range files {
		var assetType string
		if at, ok := f["assetType"]; ok {
			json.Unmarshal(at, &assetType) //nolint:errcheck
		}
		if assetType == "" {
			continue
		}
		if b, err := json.Marshal(proxyAssetURI + "/" + assetType); err == nil {
			files[i]["source"] = b
		}
	}
	if b, err := json.Marshal(files); err == nil {
		verMap["files"] = b
	}
}

// rewriteLatestResponseAssetURIs rewrites versions[].assetUri,
// versions[].fallbackAssetUri and versions[].files[].source in a gallery
// latest response so asset downloads remain subject to proxy policy.
func rewriteLatestResponseAssetURIs(body []byte, publisher, extension, proxyBase string) []byte {
	if proxyBase == "" {
		return body
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return body
	}

	versionsRaw, ok := raw["versions"]
	if !ok {
		return body
	}

	var versions []json.RawMessage
	if err := json.Unmarshal(versionsRaw, &versions); err != nil || len(versions) == 0 {
		return body
	}

	for i, rawVer := range versions {
		versions[i] = rewriteVersionAssetURIs(rawVer, publisher, extension, proxyBase)
	}

	updatedVersions, err := json.Marshal(versions)
	if err != nil {
		return body
	}
	raw["versions"] = updatedVersions

	updatedBody, err := json.Marshal(raw)
	if err != nil {
		return body
	}

	return updatedBody
}

func filterGalleryQueryResponse(body []byte, engine *rules.RuleEngine, logger *slog.Logger, proxyBase string) ([]byte, int) {
	var rawResp map[string]json.RawMessage
	if err := json.Unmarshal(body, &rawResp); err != nil {
		return body, 0
	}

	var rawResults []json.RawMessage
	if r, ok := rawResp["results"]; ok {
		if err := json.Unmarshal(r, &rawResults); err != nil {
			return body, 0
		}
	}

	totalRemoved := 0
	for i, rawResult := range rawResults {
		filtered, removed := filterExtensionsInResult(rawResult, engine, logger, proxyBase)
		rawResults[i] = filtered
		totalRemoved += removed
	}

	newResultsBytes, _ := json.Marshal(rawResults)
	rawResp["results"] = newResultsBytes
	out, err := json.Marshal(rawResp)
	if err != nil {
		return body, 0
	}
	return out, totalRemoved
}

// fetchVersionMetaFromAPI fetches a specific version's metadata from the upstream
// API to populate VersionMeta for policy checks on direct VSIX downloads.
func (s *Server) fetchVersionMetaFromAPI(namespace, extension, version string) rules.VersionMeta {
	meta := rules.VersionMeta{Version: version}
	if namespace == "" || extension == "" || version == "" {
		return meta
	}

	if s.isMarketplace() {
		return s.fetchVersionMetaFromMarketplace(namespace, extension, version)
	}

	upstreamURL := s.upstreamAPIURL("/api/" + namespace + "/" + extension + "/" + version)
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

	return extractVersionMeta(body, version)
}

// fetchVersionMetaFromMarketplace queries the Microsoft Marketplace gallery API
// to get version metadata for a specific extension version.
func (s *Server) fetchVersionMetaFromMarketplace(namespace, extension, version string) rules.VersionMeta {
	meta := rules.VersionMeta{Version: version}
	extID := extensionID(namespace, extension)

	queryBody := fmt.Sprintf(`{"filters":[{"criteria":[{"filterType":7,"value":%q}],"pageNumber":1,"pageSize":1}],"flags":402}`, extID)
	upstreamURL := s.cfg.Upstream.URL + "/_apis/public/gallery/extensionquery"
	req, err := http.NewRequest(http.MethodPost, upstreamURL, strings.NewReader(queryBody))
	if err != nil {
		return meta
	}
	req.Header.Set(hdrContentType, mimeJSON)
	req.Header.Set("Accept", "application/json;api-version=3.0-preview.1")
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
	return extractMarketplaceVersionMeta(body, version)
}

// extractMarketplaceVersionMeta parses version metadata from a Microsoft
// Marketplace gallery query response.
func extractMarketplaceVersionMeta(body []byte, version string) rules.VersionMeta {
	meta := rules.VersionMeta{Version: version}
	var resp struct {
		Results []struct {
			Extensions []struct {
				Versions []marketplaceVersionEntry `json:"versions"`
			} `json:"extensions"`
		} `json:"results"`
	}
	if json.Unmarshal(body, &resp) != nil {
		return meta
	}
	for _, result := range resp.Results {
		for _, ext := range result.Extensions {
			var entry marketplaceVersionEntry
			var ok bool
			if version != "" {
				entry, ok = findMarketplaceVersionEntry(ext.Versions, version)
			} else if len(ext.Versions) > 0 {
				// Empty version: use the first (latest) entry.
				entry, ok = ext.Versions[0], true
			}
			if ok {
				meta.Version = entry.Version
				if ts := strings.TrimSpace(entry.LastUpdated); ts != "" {
					meta.PublishedAt = parseMarketplaceTimestamp(ts)
				}
				meta.PreRelease = isMarketplacePreRelease(entry.Properties)
				return meta
			}
		}
	}
	return meta
}

// marketplaceVersionEntry holds the fields extracted from a single version
// entry in a Microsoft Marketplace gallery response.
type marketplaceVersionEntry struct {
	Version     string `json:"version"`
	LastUpdated string `json:"lastUpdated"`
	Properties  []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"properties"`
}

// isMarketplacePreRelease checks the version properties array for the
// Microsoft.VisualStudio.Code.PreRelease flag.
func isMarketplacePreRelease(props []struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}) bool {
	for _, p := range props {
		if p.Key == marketplacePreReleaseKey {
			return strings.EqualFold(p.Value, "true")
		}
	}
	return false
}

const marketplacePreReleaseKey = "Microsoft.VisualStudio.Code.PreRelease"

// marketplaceStatsFlag is the IncludeStatistics bit (0x200) in the
// VS Code Marketplace query flags. When set, the Marketplace limits the
// versions array to a single entry (the latest), preventing version-level
// policy filtering.
const marketplaceStatsFlag = 512

// stripMarketplaceStatsFlag removes the IncludeStatistics flag from a
// gallery extensionquery request body so the Marketplace returns all
// versions instead of only the latest.
func stripMarketplaceStatsFlag(body []byte) []byte {
	var req map[string]json.RawMessage
	if json.Unmarshal(body, &req) != nil {
		return body
	}
	raw, ok := req["flags"]
	if !ok {
		return body
	}
	var flags int
	if json.Unmarshal(raw, &flags) != nil {
		return body
	}
	if flags&marketplaceStatsFlag == 0 {
		return body
	}
	flags &^= marketplaceStatsFlag
	newFlags, _ := json.Marshal(flags)
	req["flags"] = newFlags
	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}

// findMarketplaceVersionEntry returns the full version entry matching the
// requested version string together with a boolean indicating success.
func findMarketplaceVersionEntry(versions []marketplaceVersionEntry, version string) (marketplaceVersionEntry, bool) {
	for _, ver := range versions {
		if ver.Version == version {
			return ver, true
		}
	}
	return marketplaceVersionEntry{}, false
}

// parseMarketplaceTimestamp parses a Microsoft Marketplace timestamp string
// into a time.Time. Returns the zero value if parsing fails.
func parseMarketplaceTimestamp(ts string) time.Time {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse("2006-01-02T15:04:05.9999999Z", ts); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

// handleExtensionViaGallery serves extension metadata when the upstream is the
// Microsoft Marketplace (which does not support the Open VSX REST API). It
// queries the gallery extensionquery endpoint and reformats the response.
func (s *Server) handleExtensionViaGallery(w http.ResponseWriter, r *http.Request, namespace, extension, extID string, pkgMeta rules.PackageMeta) {
	queryBody := fmt.Sprintf(`{"filters":[{"criteria":[{"filterType":7,"value":%q}],"pageNumber":1,"pageSize":1}],"flags":402}`, extID)
	upstreamURL := s.cfg.Upstream.URL + "/_apis/public/gallery/extensionquery"
	req, err := http.NewRequest(http.MethodPost, upstreamURL, strings.NewReader(queryBody))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	req.Header.Set(hdrContentType, mimeJSON)
	req.Header.Set("Accept", "application/json;api-version=3.0-preview.1")
	s.addUpstreamAuth(req)

	resp, err := s.upstream.Do(req)
	if err != nil {
		s.logger.Warn("marketplace query failed", slog.String("extension", extID), slog.String("error", err.Error()))
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

	// Forward the gallery query response as-is (it is already in the VS Code
	// gallery format). Policy filtering is applied via the gallery query filter.
	filtered, removed := filterGalleryQueryResponse(body, s.engine, s.logger, s.proxyBaseURL(r))
	s.reqDenied.Add(int64(removed))
	if removed == 0 {
		s.reqAllowed.Add(1)
	}

	ct := resp.Header.Get(hdrContentType)
	if ct == "" {
		ct = mimeJSON
	}
	w.Header().Set(hdrContentType, ct)
	w.Header().Set("X-Cache", "MISS")
	if removed > 0 {
		w.Header().Set("X-Curation-Policy-Notice", fmt.Sprintf("%d extension(s) filtered by policy", removed))
	}
	w.WriteHeader(http.StatusOK)
	w.Write(filtered) //nolint:errcheck
}

// fetchUpstream performs a GET to the given upstream URL and returns the response body,
// content type, status code, and any error.
func (s *Server) fetchUpstream(upstreamURL string, originalReq *http.Request) ([]byte, string, int, error) {
	req, err := http.NewRequest(http.MethodGet, upstreamURL, nil)
	if err != nil {
		return nil, "", 0, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", mimeJSON)
	s.addUpstreamAuth(req)

	resp, err := s.upstream.Do(req)
	if err != nil {
		return nil, "", 0, fmt.Errorf("upstream request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", 0, fmt.Errorf("reading response: %w", err)
	}

	ct := resp.Header.Get(hdrContentType)
	return body, ct, resp.StatusCode, nil
}

// addUpstreamAuth attaches configured credentials to an outgoing request.
func (s *Server) addUpstreamAuth(req *http.Request) {
	if s.cfg.Upstream.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.Upstream.Token)
	} else if s.cfg.Upstream.Username != "" {
		req.SetBasicAuth(s.cfg.Upstream.Username, s.cfg.Upstream.Password)
	}
}
