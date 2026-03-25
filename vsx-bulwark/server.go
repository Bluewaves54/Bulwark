// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"Bulwark/common/config"
	"Bulwark/common/rules"
)

const (
	levelDebug = "debug"
	levelInfo  = "info"
	levelWarn  = "warn"
	levelError = "error"
	formatJSON = "json"

	// galleryPrefixOpenVSX is the gallery path prefix used by Open VSX.
	galleryPrefixOpenVSX = "/vscode/gallery"
	// galleryPrefixMarketplace is the gallery path prefix used by the Microsoft Marketplace.
	galleryPrefixMarketplace = "/_apis/public/gallery"
)

// Server holds all runtime state for the vsx-bulwark proxy.
type Server struct {
	cfg        *config.Config
	engine     *rules.RuleEngine
	cache      *rules.Cache
	upstream   *http.Client
	mux        *http.ServeMux
	handler    http.Handler // mux wrapped with CORS and request-logging middleware
	logger     *slog.Logger
	logLevel   *slog.LevelVar
	reqTotal   atomic.Int64
	reqAllowed atomic.Int64
	reqDenied  atomic.Int64
	reqDryRun  atomic.Int64
}

func buildServer(cfg *config.Config, logger *slog.Logger, logLevel *slog.LevelVar) (*Server, error) {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.Upstream.TLS.InsecureSkipVerify, //nolint:gosec // user-configured
		},
	}
	if cfg.Upstream.TLS.InsecureSkipVerify {
		logger.Warn("TLS verification disabled for upstream — not recommended in production")
	}

	s := &Server{
		cfg:      cfg,
		engine:   rules.New(cfg.Policy),
		cache:    rules.NewCache(time.Duration(cfg.Cache.TTLSeconds) * time.Second),
		upstream: &http.Client{Timeout: time.Duration(cfg.Upstream.TimeoutSeconds) * time.Second, Transport: transport},
		logger:   logger,
		logLevel: logLevel,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	if cfg.Metrics.Enabled {
		mux.HandleFunc("GET /metrics", s.handleMetrics)
	}
	mux.HandleFunc("GET /admin/log-level", s.handleGetLogLevel)
	mux.HandleFunc("PUT /admin/log-level", s.handleSetLogLevel)

	// Open VSX API routes.
	mux.HandleFunc("GET /api/-/query", s.handleQuery)
	mux.HandleFunc("POST /api/-/query", s.handleQuery)
	mux.HandleFunc("GET /api/{namespace}/{extension}/{version}/file/{fileName...}", s.handleVsixDownload)
	mux.HandleFunc("GET /api/{namespace}/{extension}/{version}", s.handleExtensionVersion)
	mux.HandleFunc("GET /api/{namespace}/{extension}", s.handleExtension)
	// Catch-all for search and any other API paths — proxy as-is.
	mux.HandleFunc("GET /api/{path...}", s.handlePassthrough)

	// VS Code–compatible gallery routes (used when product.json serviceUrl points
	// to this proxy). The extension query endpoint is policy-filtered so blocked
	// extensions are removed from search/install results. The vspackage download
	// route enforces the same policy as handleVsixDownload. Other gallery routes
	// are forwarded to Open VSX without filtering.
	mux.HandleFunc("POST /vscode/gallery/extensionquery", s.handleGalleryQuery)
	mux.HandleFunc("GET /vscode/gallery/publishers/{pub}/vsextensions/{ext}/{ver}/vspackage", s.handleGalleryVspackage)
	mux.HandleFunc("GET /vscode/gallery/publisher/{pub}/extension/{ext}/{ver}/assetbyname/{assetType...}", s.handleGalleryAssetByName)
	// Intercept the extensionUrlTemplate path that VS Code uses to resolve
	// "get latest version" queries. Without this, the catch-all passthrough
	// would proxy the upstream response unchecked, allowing VS Code's Node.js
	// main process (which bypasses CORS) to discover and install blocked
	// extensions even when the extensionquery response filters them out.
	mux.HandleFunc("GET /vscode/gallery/vscode/{pub}/{name}/latest", s.handleGalleryExtensionLatest)
	mux.HandleFunc("GET /vscode/gallery/{path...}", s.handleGalleryPassthrough)
	mux.HandleFunc("POST /vscode/gallery/{path...}", s.handleGalleryPassthrough)
	mux.HandleFunc("GET /vscode/item/{path...}", s.handleGalleryPassthrough)
	mux.HandleFunc("POST /vscode/item/{path...}", s.handleGalleryPassthrough)

	// Microsoft Marketplace API prefix aliases — some VS Code builds/cached
	// states construct URLs using /_apis/public/gallery regardless of the
	// configured product.json serviceUrl.
	mux.HandleFunc("POST /_apis/public/gallery/extensionquery", s.handleGalleryQuery)
	mux.HandleFunc("GET /_apis/public/gallery/publishers/{pub}/vsextensions/{ext}/{ver}/vspackage", s.handleGalleryVspackage)
	mux.HandleFunc("GET /_apis/public/gallery/publisher/{pub}/extension/{ext}/{ver}/assetbyname/{assetType...}", s.handleGalleryAssetByName)
	mux.HandleFunc("GET /_apis/public/gallery/vscode/{pub}/{name}/latest", s.handleGalleryExtensionLatest)
	mux.HandleFunc("GET /_apis/public/gallery/{path...}", s.handleGalleryPassthrough)
	mux.HandleFunc("POST /_apis/public/gallery/{path...}", s.handleGalleryPassthrough)

	s.mux = mux
	s.handler = corsAndLogMiddleware(mux, logger)

	return s, nil
}

// isMarketplace returns true when the upstream is the Microsoft Marketplace.
func (s *Server) isMarketplace() bool {
	return s.cfg.Upstream.RegistryType == config.RegistryMarketplace
}

// upstreamGalleryURL maps a proxy gallery path to the correct upstream URL.
// Incoming paths may use either /vscode/gallery or /_apis/public/gallery;
// both are normalised before translation to the upstream format.
func (s *Server) upstreamGalleryURL(proxyPath string) string {
	// Normalise: convert /_apis/public/gallery → /vscode/gallery so the
	// translation logic below works for both inbound gallery prefixes.
	path := proxyPath
	if strings.HasPrefix(path, galleryPrefixMarketplace) {
		path = galleryPrefixOpenVSX + path[len(galleryPrefixMarketplace):]
	}
	if s.isMarketplace() {
		return s.cfg.Upstream.URL + strings.Replace(path, galleryPrefixOpenVSX, galleryPrefixMarketplace, 1)
	}
	return s.cfg.Upstream.URL + path
}

// upstreamItemURL maps a proxy /vscode/item path to the correct upstream.
// Open VSX: /vscode/item/...; Microsoft Marketplace: /items/...
func (s *Server) upstreamItemURL(proxyPath string) string {
	if s.isMarketplace() {
		return s.cfg.Upstream.URL + strings.Replace(proxyPath, "/vscode/item", "/items", 1)
	}
	return s.cfg.Upstream.URL + proxyPath
}

// upstreamAPIURL maps a proxy /api/ path to the correct upstream URL.
// For Open VSX the path is forwarded as-is. For the Microsoft Marketplace
// the Open VSX REST API is not available, so API calls are translated to
// the corresponding gallery endpoint.
func (s *Server) upstreamAPIURL(proxyPath string) string {
	return s.cfg.Upstream.URL + proxyPath
}

// upstreamVsixURL constructs the VSIX download URL for the upstream registry.
// proxyPath is the original request path for Open VSX passthrough.
func (s *Server) upstreamVsixURL(proxyPath, namespace, extension, version string) string {
	if s.isMarketplace() {
		return s.cfg.Upstream.URL + "/_apis/public/gallery/publishers/" +
			namespace + "/vsextensions/" + extension + "/" + version + "/vspackage"
	}
	return s.cfg.Upstream.URL + proxyPath
}

func createLogger(format, level, filePath string) (*slog.Logger, *slog.LevelVar, *os.File) {
	var lvl slog.LevelVar
	switch level {
	case levelDebug:
		lvl.Set(slog.LevelDebug)
	case levelWarn:
		lvl.Set(slog.LevelWarn)
	case levelError:
		lvl.Set(slog.LevelError)
	default:
		lvl.Set(slog.LevelInfo)
	}
	opts := &slog.HandlerOptions{Level: &lvl}

	var w io.Writer = os.Stdout
	var logFile *os.File
	if filePath != "" {
		f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			w = io.MultiWriter(os.Stdout, f)
			logFile = f
		}
	}

	if format == formatJSON {
		return slog.New(slog.NewJSONHandler(w, opts)), &lvl, logFile
	}
	return slog.New(slog.NewTextHandler(w, opts)), &lvl, logFile
}

// parseLogLevel converts a level string to slog.Level. Returns false if invalid.
func parseLogLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(s) {
	case levelDebug:
		return slog.LevelDebug, true
	case levelInfo:
		return slog.LevelInfo, true
	case levelWarn:
		return slog.LevelWarn, true
	case levelError:
		return slog.LevelError, true
	default:
		return slog.LevelInfo, false
	}
}

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

// handleGetLogLevel returns the current log level.
func (s *Server) handleGetLogLevel(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"level":%q}`, strings.ToLower(s.logLevel.Level().String()))
}

// handleSetLogLevel dynamically changes the log level at runtime.
func (s *Server) handleSetLogLevel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Level string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	lvl, ok := parseLogLevel(req.Level)
	if !ok {
		http.Error(w, `{"error":"invalid level; use debug, info, warn, or error"}`, http.StatusBadRequest)
		return
	}
	s.logLevel.Set(lvl)
	s.logger.Info("log level changed", slog.String("new_level", strings.ToLower(lvl.String())))
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"level":%q}`, strings.ToLower(lvl.String()))
}

func loadConfig(path string) (*config.Config, error) {
	return config.Load(path)
}

func applyFlagOverrides(cfg *config.Config, token, username, password string) {
	if token != "" {
		cfg.Upstream.Token = token
	}
	if username != "" {
		cfg.Upstream.Username = username
	}
	if password != "" {
		cfg.Upstream.Password = password
	}
}

func addrFromPort(port int) string {
	return fmt.Sprintf(":%d", port)
}

// vsCodeVersionFromMarketClientID parses the VS Code version from the value of
// the X-Market-Client-Id request header (format: "VSCode 1.112.0"). Returns an
// empty string when the header is absent or does not follow the expected format.
// This is used only for observability — the proxy does not alter its behaviour
// based on the detected version.
func vsCodeVersionFromMarketClientID(headerValue string) string {
	const prefix = "VSCode "
	if strings.HasPrefix(headerValue, prefix) {
		return strings.TrimSpace(headerValue[len(prefix):])
	}
	return ""
}

// initServer loads configuration, applies flag overrides, and builds a ready-to-use
// Server and logger. Extracted from main to allow unit testing of the initialisation path.
func initServer(cfgPath, token, user, pass string) (*Server, *slog.Logger, *os.File, error) {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading config: %w", err)
	}
	applyFlagOverrides(cfg, token, user, pass)
	logger, logLevel, logFile := createLogger(cfg.Logging.Format, cfg.Logging.Level, cfg.Logging.FilePath)
	srv, err := buildServer(cfg, logger, logLevel)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("building server: %w", err)
	}
	return srv, logger, logFile, nil
}

// runServer starts the HTTP server and blocks until ctx is cancelled, then
// performs a graceful shutdown. serviceName is used only for the startup log line.
func runServer(ctx context.Context, srv *Server, logger *slog.Logger, serviceName string) error {
	httpSrv := &http.Server{
		Addr:         addrFromPort(srv.cfg.Server.Port),
		Handler:      srv.handler,
		ReadTimeout:  time.Duration(srv.cfg.Server.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(srv.cfg.Server.WriteTimeoutSeconds) * time.Second,
		IdleTimeout:  time.Duration(srv.cfg.Server.IdleTimeoutSeconds) * time.Second,
	}

	go func() {
		logger.Info(serviceName+" listening", slog.String("addr", httpSrv.Addr))
		var err error
		if srv.cfg.Server.TLSCertFile != "" && srv.cfg.Server.TLSKeyFile != "" {
			logger.Info("TLS enabled", slog.String("cert", srv.cfg.Server.TLSCertFile))
			err = httpSrv.ListenAndServeTLS(srv.cfg.Server.TLSCertFile, srv.cfg.Server.TLSKeyFile)
		} else {
			err = httpSrv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			logger.Error("listen error", slog.String("error", err.Error()))
		}
	}()

	<-ctx.Done()

	shutCtx, cancel := context.WithTimeout(context.Background(),
		time.Duration(srv.cfg.Server.WriteTimeoutSeconds)*time.Second)
	defer cancel()

	logger.Info("shutting down")
	return httpSrv.Shutdown(shutCtx)
}

// statusRecorder wraps http.ResponseWriter to capture the HTTP status code.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (sr *statusRecorder) WriteHeader(code int) {
	if !sr.wroteHeader {
		sr.status = code
		sr.wroteHeader = true
	}
	sr.ResponseWriter.WriteHeader(code)
}

// corsAndLogMiddleware wraps an http.Handler to add CORS headers, handle
// OPTIONS preflight requests, and log every inbound request at debug level.
// VS Code's Extensions webview makes Fetch API calls that are subject to
// CORS; without these headers the browser silently blocks the response and
// the proxy appears to receive zero traffic.
//
// For preflight (OPTIONS) requests the middleware echoes back the
// Access-Control-Request-Headers so that any header VS Code sends —
// including newly-added ones in future VS Code releases — is automatically
// allowed without requiring a source-code change.
// defaultCORSAllowHeaders is the static list of headers included in every
// CORS response. For OPTIONS preflights the proxy also echoes back any
// Access-Control-Request-Headers so that future VS Code releases can add new
// headers without requiring a proxy update.
const defaultCORSAllowHeaders = "Content-Type, Accept, Accept-Encoding, Accept-Language, " +
	"Authorization, User-Agent, X-Market-Client-Id, X-Market-User-Id, " +
	"X-Market-Telemetry-Id, X-TFS-FedAuthRedirect"

func corsAndLogMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mirror the request Origin so that credentialed fetch calls
		// (credentials:'include') work in the VS Code Extensions webview.
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		if origin != "*" {
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		// Set on every response (informational for non-preflight, enforced for OPTIONS).
		w.Header().Set("Access-Control-Allow-Headers", defaultCORSAllowHeaders)
		w.Header().Set("Access-Control-Expose-Headers", "X-Cache, X-Curation-Policy-Notice")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			// Echo back any headers the client requests so that new VS Code
			// versions that add headers (e.g. X-Market-Telemetry-Id) don't
			// require a proxy update to pass the preflight check.
			if reqHdrs := r.Header.Get("Access-Control-Request-Headers"); reqHdrs != "" {
				w.Header().Set("Access-Control-Allow-Headers", reqHdrs)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sr, r)

		attrs := []any{
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", sr.status),
			slog.Duration("duration", time.Since(start)),
		}
		if v := vsCodeVersionFromMarketClientID(r.Header.Get("X-Market-Client-Id")); v != "" {
			attrs = append(attrs, slog.String("vscode_version", v))
		}
		logger.Debug("request", attrs...)
	})
}
