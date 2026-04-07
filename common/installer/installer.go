// SPDX-License-Identifier: Apache-2.0

// Package installer provides one-click setup and uninstall for Bulwark proxies.
// Each proxy binary embeds its best-practices config and calls Setup or Uninstall
// to install, configure the package manager, and create OS autostart entries.
package installer

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	bulwarkDir   = ".bulwark"
	binSubdir    = "bin"
	vsxStateFile = "vsx-targets.json"
	dirPerm      = 0o755
	cfgPerm      = 0o644
	binPerm      = 0o755

	// OSDarwin is the GOOS value for macOS.
	OSDarwin = "darwin"
	// OSLinux is the GOOS value for Linux.
	OSLinux = "linux"
	// OSWindows is the GOOS value for Windows.
	OSWindows = "windows"

	// EcosystemNpm is the ecosystem identifier for npm.
	EcosystemNpm = "npm"
	// EcosystemPypi is the ecosystem identifier for PyPI.
	EcosystemPypi = "pypi"
	// EcosystemMaven is the ecosystem identifier for Maven.
	EcosystemMaven = "maven"
	// EcosystemVsx is the ecosystem identifier for VS Code extensions (Open VSX).
	EcosystemVsx = "vsx"

	// registryMarketplace is the registry type for Microsoft VS Code variants.
	registryMarketplace = "marketplace"
	// registryOpenVSX is the registry type for open-source VS Code builds.
	registryOpenVSX = "openvsx"
	// defaultOpenVSXURL is the default upstream URL for open-source VS Code builds.
	defaultOpenVSXURL = "https://open-vsx.org"
)

// ProxyInfo describes a Bulwark proxy ecosystem for installation.
type ProxyInfo struct {
	// Ecosystem is the short name: "npm", "pypi", or "maven".
	Ecosystem string
	// BinaryName is the proxy binary name without extension.
	BinaryName string
	// Port is the default proxy listen port.
	Port int
	// ConfigData is the embedded best-practices YAML configuration.
	ConfigData []byte
}

// Paths holds resolved filesystem paths used during setup and uninstall.
type Paths struct {
	Base   string
	EcoDir string
	BinDir string
	Config string
	Binary string
}

// ResolvePaths computes installation paths relative to the given home directory.
func ResolvePaths(p ProxyInfo, home, goos string) Paths {
	base := filepath.Join(home, bulwarkDir)
	binName := p.BinaryName
	if goos == OSWindows {
		binName += ".exe"
	}
	return Paths{
		Base:   base,
		EcoDir: filepath.Join(base, p.BinaryName),
		BinDir: filepath.Join(base, binSubdir),
		Config: filepath.Join(base, p.BinaryName, "config.yaml"),
		Binary: filepath.Join(base, binSubdir, binName),
	}
}

// --- Content generators (pure functions) ---

// PipConfig returns the pip configuration file content.
func PipConfig(port int) string {
	return fmt.Sprintf("[global]\nindex-url = http://localhost:%d/simple/\ntrusted-host = localhost\n", port)
}

// MavenSettingsXML returns a Maven settings.xml that mirrors central through Bulwark.
func MavenSettingsXML(port int) string {
	return fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"+
		"<settings xmlns=\"http://maven.apache.org/SETTINGS/1.0.0\"\n"+
		"          xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\"\n"+
		"          xsi:schemaLocation=\"http://maven.apache.org/SETTINGS/1.0.0\n"+
		"                              http://maven.apache.org/xsd/settings-1.0.0.xsd\">\n"+
		"  <mirrors>\n"+
		"    <mirror>\n"+
		"      <id>bulwark-maven</id>\n"+
		"      <mirrorOf>central</mirrorOf>\n"+
		"      <url>http://localhost:%d</url>\n"+
		"    </mirror>\n"+
		"  </mirrors>\n"+
		"</settings>\n", port)
}

// LaunchdPlistXML returns a macOS LaunchAgent plist.
func LaunchdPlistXML(label, binary, config string) string {
	return fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"+
		"<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\"\n"+
		"  \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n"+
		"<plist version=\"1.0\">\n"+
		"<dict>\n"+
		"  <key>Label</key>\n"+
		"  <string>%s</string>\n"+
		"  <key>ProgramArguments</key>\n"+
		"  <array>\n"+
		"    <string>%s</string>\n"+
		"    <string>-config</string>\n"+
		"    <string>%s</string>\n"+
		"  </array>\n"+
		"  <key>RunAtLoad</key>\n"+
		"  <true/>\n"+
		"  <key>KeepAlive</key>\n"+
		"  <true/>\n"+
		"  <key>StandardOutPath</key>\n"+
		"  <string>/tmp/%s.log</string>\n"+
		"  <key>StandardErrorPath</key>\n"+
		"  <string>/tmp/%s.log</string>\n"+
		"</dict>\n"+
		"</plist>\n", label, binary, config, label, label)
}

// SystemdUnitFile returns a systemd user service unit.
func SystemdUnitFile(desc, binary, config string) string {
	return fmt.Sprintf("[Unit]\n"+
		"Description=Bulwark %s\n"+
		"After=network.target\n"+
		"\n"+
		"[Service]\n"+
		"Type=simple\n"+
		"ExecStart=%s -config %s\n"+
		"Restart=on-failure\n"+
		"RestartSec=5\n"+
		"\n"+
		"[Install]\n"+
		"WantedBy=default.target\n", desc, binary, config)
}

// WindowsBatchFile returns a Windows startup batch script.
func WindowsBatchFile(binary, config string) string {
	return fmt.Sprintf("@echo off\r\nstart \"\" /B \"%s\" -config \"%s\"\r\n", binary, config)
}

// --- File operations ---

// CopyFile copies src to dst, creating or truncating dst.
func CopyFile(src, dst string) error {
	srcAbs, _ := filepath.Abs(src)
	dstAbs, _ := filepath.Abs(dst)
	if srcAbs == dstAbs {
		return nil // already in place
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening source: %w", err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, binPerm)
	if err != nil {
		return fmt.Errorf("creating destination: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copying data: %w", err)
	}
	return nil
}

// --- TLS certificate generation ---

const (
	// tlsCertPerm is the file permission for TLS certificate files.
	tlsCertPerm = 0o644
	// tlsKeyPerm is the file permission for TLS private key files.
	tlsKeyPerm = 0o600
	// tlsCertValidityYears is how long a generated self-signed cert is valid.
	tlsCertValidityYears = 10
)

// GenerateSelfSignedCert creates a self-signed TLS certificate and private key
// for localhost. The certificate includes SANs for localhost, 127.0.0.1, and
// ::1, making it suitable for local proxy use. The cert is valid for
// tlsCertValidityYears years.
func GenerateSelfSignedCert(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generating key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generating serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "Bulwark Proxy (localhost)",
			Organization: []string{"Bulwark"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(tlsCertValidityYears * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("creating certificate: %w", err)
	}

	certFile, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, tlsCertPerm)
	if err != nil {
		return fmt.Errorf("creating cert file: %w", err)
	}
	defer certFile.Close()

	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return fmt.Errorf("encoding cert: %w", err)
	}

	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshalling key: %w", err)
	}

	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, tlsKeyPerm)
	if err != nil {
		return fmt.Errorf("creating key file: %w", err)
	}
	defer keyFile.Close()

	if err := pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}); err != nil {
		return fmt.Errorf("encoding key: %w", err)
	}

	return nil
}

// installCertInTrustStore attempts to add the generated certificate to the
// operating system's trust store so that Chromium-based editors (VS Code)
// accept the HTTPS connection without certificate warnings.
//
// This is a best-effort operation: if it fails the proxy still works but the
// user must install the certificate manually.
func installCertInTrustStore(certPath, goos string, out io.Writer) {
	switch goos {
	case OSWindows:
		cmd := exec.Command("certutil", "-user", "-addstore", "Root", certPath) //nolint:gosec // user-initiated setup
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(out, "[warn] Could not install cert in trust store: %v\n", err)
			fmt.Fprintf(out, "       certutil output: %s\n", strings.TrimSpace(string(output)))
			fmt.Fprintf(out, "       Install manually: certutil -user -addstore Root %s\n", certPath)
		} else {
			fmt.Fprintf(out, "[ok] Certificate added to user trust store\n")
		}
	case OSDarwin:
		homeDir, hdErr := os.UserHomeDir()
		if hdErr != nil || homeDir == "" {
			fmt.Fprintf(out, "[warn] Could not determine home directory for login keychain: %v\n", hdErr)
			fmt.Fprintf(out, "       Install manually: security add-trusted-cert -r trustAsRoot -k ~/Library/Keychains/login.keychain-db %s\n", certPath)
			return
		}
		cmd := exec.Command("security", "add-trusted-cert", "-r", "trustAsRoot", //nolint:gosec // user-initiated setup
			"-k", filepath.Join(homeDir, "Library", "Keychains", "login.keychain-db"),
			certPath)
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(out, "[warn] Could not install cert in trust store: %v\n", err)
			fmt.Fprintf(out, "       security output: %s\n", strings.TrimSpace(string(output)))
			fmt.Fprintf(out, "       Install manually: security add-trusted-cert -r trustAsRoot -k ~/Library/Keychains/login.keychain-db %s\n", certPath)
		} else {
			fmt.Fprintf(out, "[ok] Certificate added to login keychain\n")
		}
	default:
		fmt.Fprintf(out, "[info] Auto trust-store installation not supported on %s.\n", goos)
		fmt.Fprintf(out, "       Install manually: sudo cp %s /usr/local/share/ca-certificates/bulwark.crt && sudo update-ca-certificates\n", certPath)
	}
}

// isTopLevelKey reports whether line starts a new top-level YAML key
// (non-indented, non-comment, non-empty).
func isTopLevelKey(line, trimmed string) bool {
	return len(line) > 0 && line[0] != ' ' && line[0] != '\t' && !strings.HasPrefix(trimmed, "#")
}

// PatchConfigForTLS modifies the raw config YAML bytes to set
// tls_cert_file and tls_key_file under the server: section.
func PatchConfigForTLS(configData []byte, certPath, keyPath string) []byte {
	lines := strings.Split(string(configData), "\n")
	var result []string
	inServer := false
	certWritten := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "server:" {
			inServer = true
			result = append(result, line)
			continue
		}
		if inServer && isTopLevelKey(line, trimmed) {
			result, certWritten = appendTLSIfNeeded(result, certWritten, certPath, keyPath)
			inServer = false
		}
		if inServer {
			line, certWritten = replaceTLSLine(line, trimmed, certWritten, certPath, keyPath)
		}
		result = append(result, line)
	}
	if inServer {
		result, _ = appendTLSIfNeeded(result, certWritten, certPath, keyPath)
	}
	return []byte(strings.Join(result, "\n"))
}

// appendTLSIfNeeded appends tls_cert_file and tls_key_file lines if not yet written.
func appendTLSIfNeeded(result []string, written bool, certPath, keyPath string) ([]string, bool) {
	if written {
		return result, written
	}
	result = append(result,
		fmt.Sprintf("  tls_cert_file: %q", certPath),
		fmt.Sprintf("  tls_key_file: %q", keyPath),
	)
	return result, true
}

// replaceTLSLine replaces existing tls_cert_file / tls_key_file lines in the server section.
func replaceTLSLine(line, trimmed string, certWritten bool, certPath, keyPath string) (string, bool) {
	if strings.HasPrefix(trimmed, "tls_cert_file:") {
		return fmt.Sprintf("  tls_cert_file: %q", certPath), true
	}
	if strings.HasPrefix(trimmed, "tls_key_file:") {
		return fmt.Sprintf("  tls_key_file: %q", keyPath), certWritten
	}
	return line, certWritten
}

// --- Package manager config (file-based) ---

// PipConfigPaths returns the directory and file path for pip config.
func PipConfigPaths(home, goos string) (string, string) {
	if goos == OSWindows {
		dir := filepath.Join(home, "AppData", "Roaming", "pip")
		return dir, filepath.Join(dir, "pip.ini")
	}
	dir := filepath.Join(home, ".config", "pip")
	return dir, filepath.Join(dir, "pip.conf")
}

// VsxConfigDirs returns the user-data directories for VS Code, VSCodium, and
// Code OSS. Each directory may contain a product.json that overrides the gallery
// URL. These are user-owned, survive editor updates, and work like pip.conf or
// settings.xml. Microsoft VS Code is included so that the proxy can intercept
// its marketplace traffic via Open VSX.
func VsxConfigDirs(home, goos string) []string {
	names := []string{"Code", "Code - Insiders", "VSCodium", "Code - OSS"}
	dirs := make([]string, 0, len(names))
	for _, name := range names {
		var dir string
		switch goos {
		case OSWindows:
			dir = filepath.Join(home, "AppData", "Roaming", name)
		case OSDarwin:
			dir = filepath.Join(home, "Library", "Application Support", name)
		default:
			dir = filepath.Join(home, ".config", name)
		}
		dirs = append(dirs, dir)
	}
	return dirs
}

// VsxInstallDirs returns the product.json parent directories inside each VS
// Code and VS Code Insiders installation on Windows. On Windows, Microsoft VS
// Code reads product.json from its installation directory
// (AppData\Local\Programs\Microsoft VS Code\resources\app) rather than the
// user-data folder, so those paths must be patched separately.
//
// The function checks both the standard flat layout
// (<install>/resources/app) and any hash-versioned sub-directories created by
// the Squirrel installer (<install>/<hash>/resources/app).
//
// Returns nil on Linux and macOS where user-data product.json overrides are
// supported by the open-source VS Code builds.
func VsxInstallDirs(home, goos string) []string {
	if goos != OSWindows {
		return nil
	}
	names := []string{"Microsoft VS Code", "Microsoft VS Code Insiders"}
	var dirs []string
	for _, name := range names {
		instBase := filepath.Join(home, "AppData", "Local", "Programs", name)
		// Standard layout: <install>/resources/app
		standard := filepath.Join(instBase, "resources", "app")
		if _, err := os.Stat(standard); err == nil {
			dirs = append(dirs, standard)
		}
		// Squirrel/hash layout: <install>/<hash>/resources/app
		matches, _ := filepath.Glob(filepath.Join(instBase, "*", "resources", "app"))
		dirs = append(dirs, matches...)
	}
	return dirs
}

// galleryProxyField specifies an extensionsGallery product.json key and the
// proxy sub-path it should be redirected to. Extending this slice is the only
// change required when a future VS Code version introduces a new gallery URL field.
type galleryProxyField struct {
	Field     string
	ProxyPath string
}

// galleryProxyFields lists every extensionsGallery field that the proxy
// redirects. Entries are ordered oldest-to-newest. Append here when a new
// VS Code release introduces an additional gallery-related URL field.
var galleryProxyFields = []galleryProxyField{
	{Field: "serviceUrl", ProxyPath: "/vscode/gallery"},
	{Field: "itemUrl", ProxyPath: "/vscode/item"},
	{Field: "resourceUrlTemplate", ProxyPath: "/api/{publisher}/{name}/{version}/file/{path}"},
	// VS Code 1.112+: CDN URL for extension update-check requests.
	// Routing through the proxy allows policy enforcement on update queries.
	{Field: "extensionUrlTemplate", ProxyPath: "/vscode/gallery/vscode/{publisher}/{name}/latest"},
}

// mergeGalleryJSONURL parses an existing product.json byte slice, updates all
// proxy-URL fields inside extensionsGallery (see galleryProxyFields) to point
// at baseURL, and returns the updated JSON. All other top-level fields and all
// other extensionsGallery sub-fields (controlUrl, nlsBaseUrl, mcpUrl, etc.)
// are preserved so that VS Code features that depend on those fields continue
// to work after patching.
func mergeGalleryJSONURL(data []byte, baseURL string) ([]byte, error) {
	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing product.json: %w", err)
	}
	// Reuse any existing extensionsGallery so vendor-specific fields are kept.
	gallery, _ := doc["extensionsGallery"].(map[string]interface{})
	if gallery == nil {
		gallery = make(map[string]interface{})
	}
	for _, f := range galleryProxyFields {
		gallery[f.Field] = baseURL + f.ProxyPath
	}
	doc["extensionsGallery"] = gallery
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("serialising product.json: %w", err)
	}
	return append(out, '\n'), nil
}

// mergeGalleryJSON is a convenience wrapper around mergeGalleryJSONURL that
// builds the base URL from a localhost port number.
func mergeGalleryJSON(data []byte, port int) ([]byte, error) {
	return mergeGalleryJSONURL(data, fmt.Sprintf("https://localhost:%d", port))
}

// patchInstallProductJSON merges Bulwark gallery URLs into the product.json
// found in dir (a VS Code installation resources/app directory). It is
// safe to call on non-existent paths — the function silently skips missing
// directories so that it can be called speculatively on candidate paths.
//
// Both this function and writeVsxProductJSON use mergeGalleryJSON, which
// merges the proxy-URL fields (see galleryProxyFields) into the existing
// extensionsGallery so that all other vendor-specific fields are preserved.
func patchInstallProductJSON(dir string, baseURL string, out io.Writer) {
	if _, err := os.Stat(dir); err != nil {
		return // VS Code not installed at this path
	}
	cfgFile := filepath.Join(dir, "product.json")
	existing, err := os.ReadFile(cfgFile)
	if err != nil {
		fmt.Fprintf(out, "[warn] read %s: %v\n", cfgFile, err)
		return
	}
	backup := cfgFile + ".bulwark-backup"
	if _, berr := os.Stat(backup); os.IsNotExist(berr) {
		// Only write backup on first setup so the original is always recoverable.
		os.WriteFile(backup, existing, cfgPerm) //nolint:errcheck // best-effort backup
		fmt.Fprintf(out, "[ok] Existing product.json backed up to %s\n", backup)
	}
	patched, err := mergeGalleryJSONURL(existing, baseURL)
	if err != nil {
		fmt.Fprintf(out, "[warn] %s: %v\n", cfgFile, err)
		return
	}
	if err := os.WriteFile(cfgFile, patched, cfgPerm); err != nil {
		fmt.Fprintf(out, "[warn] write %s: %v\n", cfgFile, err)
		return
	}
	fmt.Fprintf(out, "[ok] Gallery (install dir) configured: %s\n", cfgFile)
}

// restoreInstallProductJSON restores product.json in a VS Code installation
// directory from its Bulwark backup, or removes the patched file if no backup
// exists.
// restoreUserDataProductJSON restores or removes the product.json in a VS Code
// user-data directory. If a Bulwark backup exists the original is restored;
// otherwise the proxy-written file is removed.
func restoreUserDataProductJSON(dir string, out io.Writer) {
	cfgFile := filepath.Join(dir, "product.json")
	backup := cfgFile + ".bulwark-backup"
	if data, err := os.ReadFile(backup); err == nil {
		os.WriteFile(cfgFile, data, cfgPerm) //nolint:errcheck // best-effort restore
		os.Remove(backup)                    //nolint:errcheck // best-effort cleanup
		fmt.Fprintf(out, "[ok] Restored %s from backup\n", cfgFile)
	} else {
		os.Remove(cfgFile) //nolint:errcheck // may not exist
	}
}

func restoreInstallProductJSON(dir string, out io.Writer) {
	if _, err := os.Stat(dir); err != nil {
		return
	}
	cfgFile := filepath.Join(dir, "product.json")
	backup := cfgFile + ".bulwark-backup"
	if data, err := os.ReadFile(backup); err == nil {
		os.WriteFile(cfgFile, data, cfgPerm) //nolint:errcheck // best-effort restore
		os.Remove(backup)                    //nolint:errcheck // best-effort cleanup
		fmt.Fprintf(out, "[ok] Restored %s from backup\n", cfgFile)
	}
}

// VsxRepairInstallDirs checks each VS Code installation-directory product.json
// and re-patches any file that no longer contains the proxy URL. This is
// called at proxy startup to automatically recover after a VS Code update:
// the Squirrel installer on Windows creates a new versioned directory on every
// update, abandoning the previously patched file. Re-running the patch is safe
// and idempotent — backup files written by the initial setup are not
// overwritten, preserving the ability to uninstall back to the original state.
//
// The function is a no-op on Linux and macOS where user-data product.json
// overrides survive editor updates without any intervention.
func VsxRepairInstallDirs(home, goos string, port int, out io.Writer) {
	baseURL := fmt.Sprintf("https://localhost:%d", port)
	proxyURL := fmt.Sprintf("localhost:%d", port)
	for _, dir := range collectVSCodeInstallDirs(configuredVSCodeTargets(home, goos)) {
		cfgFile := filepath.Join(dir, "product.json")
		data, err := os.ReadFile(cfgFile)
		if err != nil {
			continue // dir no longer exists (old Squirrel slot removed)
		}
		if strings.Contains(string(data), proxyURL) {
			continue // already patched — VS Code has not updated since last setup
		}
		// The product.json was replaced by a VS Code update. Re-patch silently.
		fmt.Fprintf(out, "VS Code update detected; re-patching gallery config (path: %s)\n", cfgFile)
		patchInstallProductJSON(dir, baseURL, out)
	}
}

// VSCodeVariant identifies a specific VS Code distribution.
type VSCodeVariant struct {
	// Name is the human-readable variant name (e.g., "Code", "VSCodium").
	Name string
	// RegistryType is "marketplace" for Microsoft or "openvsx" for open-source builds.
	RegistryType string
	// DefaultUpstreamURL is the default upstream registry URL for this variant.
	DefaultUpstreamURL string
}

// Known VS Code variants and their default registries.
var (
	VariantMicrosoftCode = VSCodeVariant{
		Name: "Code", RegistryType: registryMarketplace,
		DefaultUpstreamURL: "https://marketplace.visualstudio.com",
	}
	VariantMicrosoftInsiders = VSCodeVariant{
		Name: "Code - Insiders", RegistryType: registryMarketplace,
		DefaultUpstreamURL: "https://marketplace.visualstudio.com",
	}
	VariantVSCodium = VSCodeVariant{
		Name: "VSCodium", RegistryType: registryOpenVSX,
		DefaultUpstreamURL: defaultOpenVSXURL,
	}
	VariantCodeOSS = VSCodeVariant{
		Name: "Code - OSS", RegistryType: registryOpenVSX,
		DefaultUpstreamURL: defaultOpenVSXURL,
	}
)

// allVSCodeVariants returns all known variants in priority order (Microsoft first).
func allVSCodeVariants() []VSCodeVariant {
	return []VSCodeVariant{
		VariantMicrosoftCode,
		VariantMicrosoftInsiders,
		VariantVSCodium,
		VariantCodeOSS,
	}
}

// vsxConfigDirName maps a variant name to the config directory name used by
// VsxConfigDirs. It returns the same name since the config directory names
// match the variant names exactly.
func vsxConfigDirForVariant(home, goos string, v VSCodeVariant) string {
	switch goos {
	case OSWindows:
		return filepath.Join(home, "AppData", "Roaming", v.Name)
	case OSDarwin:
		return filepath.Join(home, "Library", "Application Support", v.Name)
	default:
		return filepath.Join(home, ".config", v.Name)
	}
}

// VSCodeTarget describes the filesystem locations used by one VS Code variant.
type VSCodeTarget struct {
	Variant     VSCodeVariant
	ConfigDir   string
	InstallDirs []string
}

type vsxSetupState struct {
	Targets []vsxSetupTarget `json:"targets"`
}

type vsxSetupTarget struct {
	Name         string   `json:"name"`
	RegistryType string   `json:"registry_type"`
	ConfigDir    string   `json:"config_dir,omitempty"`
	InstallDirs  []string `json:"install_dirs,omitempty"`
}

func vsxStatePath(home string) string {
	return filepath.Join(home, bulwarkDir, "vsx-bulwark", vsxStateFile)
}

func dirExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func installRootNamesForVariant(v VSCodeVariant) []string {
	switch v.Name {
	case VariantMicrosoftCode.Name:
		return []string{"Microsoft VS Code"}
	case VariantMicrosoftInsiders.Name:
		return []string{"Microsoft VS Code Insiders"}
	default:
		return nil
	}
}

func vsxInstallDirsForVariant(home, goos string, v VSCodeVariant) []string {
	if goos != OSWindows {
		return nil
	}
	var dirs []string
	for _, name := range installRootNamesForVariant(v) {
		instBase := filepath.Join(home, "AppData", "Local", "Programs", name)
		standard := filepath.Join(instBase, "resources", "app")
		if dirExists(standard) {
			dirs = append(dirs, standard)
		}
		matches, _ := filepath.Glob(filepath.Join(instBase, "*", "resources", "app"))
		dirs = append(dirs, matches...)
	}
	return dirs
}

// ResolveVSCodeTargets returns the editor variants that appear to be installed,
// together with the exact config and install dirs Bulwark should touch.
func ResolveVSCodeTargets(home, goos string) []VSCodeTarget {
	var targets []VSCodeTarget
	for _, v := range allVSCodeVariants() {
		configDir := vsxConfigDirForVariant(home, goos, v)
		installDirs := vsxInstallDirsForVariant(home, goos, v)
		if !dirExists(configDir) && len(installDirs) == 0 {
			continue
		}
		targets = append(targets, VSCodeTarget{
			Variant:     v,
			ConfigDir:   configDir,
			InstallDirs: installDirs,
		})
	}
	return targets
}

func allVSCodeTargets(home, goos string) []VSCodeTarget {
	targets := make([]VSCodeTarget, 0, len(allVSCodeVariants()))
	for _, v := range allVSCodeVariants() {
		targets = append(targets, VSCodeTarget{
			Variant:     v,
			ConfigDir:   vsxConfigDirForVariant(home, goos, v),
			InstallDirs: vsxInstallDirsForVariant(home, goos, v),
		})
	}
	return targets
}

func detectVSCodeTargetsForSetup(home, goos string, out io.Writer) []VSCodeTarget {
	targets := ResolveVSCodeTargets(home, goos)
	if len(targets) == 0 {
		fmt.Fprintln(out, "[info] No VS Code variant detected; writing overlays for all known VS Code-family user-data dirs")
		return allVSCodeTargets(home, goos)
	}
	var names []string
	for _, target := range targets {
		names = append(names, target.Variant.Name)
	}
	fmt.Fprintf(out, "[ok] Targeting VS Code variant(s): %s\n", strings.Join(names, ", "))
	return targets
}

func collectVSCodeInstallDirs(targets []VSCodeTarget) []string {
	seen := make(map[string]struct{})
	var dirs []string
	for _, target := range targets {
		for _, dir := range target.InstallDirs {
			if _, ok := seen[dir]; ok {
				continue
			}
			seen[dir] = struct{}{}
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func lookupVSCodeVariant(name, registryType string) VSCodeVariant {
	for _, v := range allVSCodeVariants() {
		if v.Name == name {
			return v
		}
	}
	return VSCodeVariant{Name: name, RegistryType: registryType}
}

func saveVSXSetupState(home string, targets []VSCodeTarget) error {
	state := vsxSetupState{Targets: make([]vsxSetupTarget, 0, len(targets))}
	for _, target := range targets {
		state.Targets = append(state.Targets, vsxSetupTarget{
			Name:         target.Variant.Name,
			RegistryType: target.Variant.RegistryType,
			ConfigDir:    target.ConfigDir,
			InstallDirs:  target.InstallDirs,
		})
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("serialising VSX setup state: %w", err)
	}
	path := vsxStatePath(home)
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("creating VSX setup state dir: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), cfgPerm); err != nil {
		return fmt.Errorf("writing VSX setup state: %w", err)
	}
	return nil
}

func loadVSXSetupState(home string) ([]VSCodeTarget, error) {
	data, err := os.ReadFile(vsxStatePath(home))
	if err != nil {
		return nil, err
	}
	var state vsxSetupState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing VSX setup state: %w", err)
	}
	targets := make([]VSCodeTarget, 0, len(state.Targets))
	for _, target := range state.Targets {
		targets = append(targets, VSCodeTarget{
			Variant:     lookupVSCodeVariant(target.Name, target.RegistryType),
			ConfigDir:   target.ConfigDir,
			InstallDirs: append([]string(nil), target.InstallDirs...),
		})
	}
	return targets, nil
}

func configuredVSCodeTargets(home, goos string) []VSCodeTarget {
	targets, err := loadVSXSetupState(home)
	if err == nil && len(targets) > 0 {
		return targets
	}
	return allVSCodeTargets(home, goos)
}

func clearVSXSetupState(home string) {
	os.Remove(vsxStatePath(home)) //nolint:errcheck // best-effort cleanup
}

// DetectVSCodeVariants returns the VS Code variants that appear to be installed
// on the system. Detection checks for the user-data config directory and, on
// Windows, the installation directory. The returned slice is ordered with
// Microsoft variants first.
func DetectVSCodeVariants(home, goos string) []VSCodeVariant {
	var found []VSCodeVariant
	for _, target := range ResolveVSCodeTargets(home, goos) {
		found = append(found, target.Variant)
	}
	return found
}

// ReadExistingRegistryURL reads the extensionsGallery.serviceUrl from an
// installed VS Code's product.json to determine which upstream registry is
// currently configured. It checks install directories first (Windows), then
// user-data directories.
func ReadExistingRegistryURL(home, goos string) string {
	// Check Windows install dirs first (Microsoft VS Code).
	for _, dir := range VsxInstallDirs(home, goos) {
		if url := readGalleryServiceURL(filepath.Join(dir, "product.json")); url != "" {
			return url
		}
	}
	// Check user-data dirs.
	for _, dir := range VsxConfigDirs(home, goos) {
		if url := readGalleryServiceURL(filepath.Join(dir, "product.json")); url != "" {
			return url
		}
	}
	return ""
}

// readGalleryServiceURL extracts extensionsGallery.serviceUrl from a product.json file.
func readGalleryServiceURL(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var doc struct {
		Gallery struct {
			ServiceURL string `json:"serviceUrl"`
		} `json:"extensionsGallery"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return ""
	}
	return doc.Gallery.ServiceURL
}

// ChooseVSCodeUpstream determines the correct upstream URL and registry type
// based on detected VS Code variants. If only open-source variants are found,
// returns Open VSX. If any Microsoft variant is found, returns the Microsoft
// Marketplace. Falls back to Open VSX when no variant is detected.
func ChooseVSCodeUpstream(variants []VSCodeVariant) (upstreamURL, registryType string) {
	for _, v := range variants {
		if v.RegistryType == registryMarketplace {
			return v.DefaultUpstreamURL, v.RegistryType
		}
	}
	if len(variants) > 0 {
		return variants[0].DefaultUpstreamURL, variants[0].RegistryType
	}
	return defaultOpenVSXURL, registryOpenVSX
}

// patchUpstreamLine rewrites a single line inside the upstream: YAML section.
// It returns the (possibly replaced) line and true if registry_type was written.
func patchUpstreamLine(line, trimmed, upstreamURL, registryType string) (string, bool) {
	if strings.HasPrefix(trimmed, "url:") {
		return fmt.Sprintf("  url: %q", upstreamURL), false
	}
	if strings.HasPrefix(trimmed, "registry_type:") {
		return fmt.Sprintf("  registry_type: %q", registryType), true
	}
	return line, false
}

// PatchConfigForVSCode modifies the raw config YAML bytes to set the correct
// upstream URL and registry type based on detected VS Code variants.
func PatchConfigForVSCode(configData []byte, upstreamURL, registryType string) []byte {
	lines := strings.Split(string(configData), "\n")
	var result []string
	inUpstream := false
	registryTypeWritten := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "upstream:" {
			inUpstream = true
			result = append(result, line)
			continue
		}
		// Detect end of upstream section (next top-level key).
		if inUpstream && isTopLevelKey(line, trimmed) {
			if !registryTypeWritten {
				result = append(result, fmt.Sprintf("  registry_type: %q", registryType))
				registryTypeWritten = true
			}
			inUpstream = false
		}
		if inUpstream {
			var written bool
			line, written = patchUpstreamLine(line, trimmed, upstreamURL, registryType)
			registryTypeWritten = registryTypeWritten || written
		}
		result = append(result, line)
	}
	if inUpstream && !registryTypeWritten {
		result = append(result, fmt.Sprintf("  registry_type: %q", registryType))
	}
	return []byte(strings.Join(result, "\n"))
}

// autoConfigureVSCodeUpstream detects installed VS Code variants, reads the
// current registry configuration, and patches the config data to use the
// correct upstream. It prints detection results to out.
func autoConfigureVSCodeUpstream(configData []byte, home, goos string, out io.Writer) []byte {
	variants := DetectVSCodeVariants(home, goos)
	if len(variants) == 0 {
		fmt.Fprintln(out, "[info] No VS Code installation detected; using default upstream (Open VSX)")
		return configData
	}
	var names []string
	for _, v := range variants {
		names = append(names, v.Name)
	}
	fmt.Fprintf(out, "[ok] Detected VS Code variant(s): %s\n", strings.Join(names, ", "))

	existingURL := ReadExistingRegistryURL(home, goos)
	if existingURL != "" {
		fmt.Fprintf(out, "[info] Current registry: %s\n", existingURL)
	}

	upstreamURL, registryType := ChooseVSCodeUpstream(variants)
	fmt.Fprintf(out, "[ok] Configuring upstream: %s (registry type: %s)\n", upstreamURL, registryType)
	return PatchConfigForVSCode(configData, upstreamURL, registryType)
}

// VsxGalleryProductJSONURL returns the product.json content that redirects
// extension gallery traffic through a Bulwark proxy at the given baseURL
// (e.g. "https://localhost:18003" or "https://bulwark.corp.com:18003").
// Includes _comment and _revert keys so the user understands what the file
// is and how to undo it. All gallery proxy fields (see galleryProxyFields)
// are included so that the overlay completely defines extensionsGallery.
func VsxGalleryProductJSONURL(baseURL string) string {
	return fmt.Sprintf(`{
  "_comment": "Written by vsx-bulwark setup. Routes extension installs through the Bulwark security proxy.",
  "_revert": "To restore the original gallery, delete this file (or rename the .bulwark-backup file back to product.json) and restart the editor.",
  "extensionsGallery": {
    "serviceUrl": "%s/vscode/gallery",
    "itemUrl": "%s/vscode/item",
    "resourceUrlTemplate": "%s/api/{publisher}/{name}/{version}/file/{path}",
    "extensionUrlTemplate": "%s/vscode/gallery/vscode/{publisher}/{name}/latest"
  }
}
`, baseURL, baseURL, baseURL, baseURL)
}

// VsxGalleryProductJSON returns the product.json for a localhost proxy on the
// given port. It delegates to VsxGalleryProductJSONURL.
func VsxGalleryProductJSON(port int) string {
	return VsxGalleryProductJSONURL(fmt.Sprintf("https://localhost:%d", port))
}

func writePypiConfig(p ProxyInfo, home, goos string, out io.Writer) {
	cfgDir, cfgFile := PipConfigPaths(home, goos)
	if err := os.MkdirAll(cfgDir, dirPerm); err != nil {
		fmt.Fprintf(out, "[warn] pip config dir: %v\n", err)
		return
	}
	if err := os.WriteFile(cfgFile, []byte(PipConfig(p.Port)), cfgPerm); err != nil {
		fmt.Fprintf(out, "[warn] pip config: %v\n", err)
		return
	}
	fmt.Fprintf(out, "[ok] pip index configured: %s\n", cfgFile)
}

func writeMavenConfig(p ProxyInfo, home string, out io.Writer) {
	m2Dir := filepath.Join(home, ".m2")
	if err := os.MkdirAll(m2Dir, dirPerm); err != nil {
		fmt.Fprintf(out, "[warn] .m2 dir: %v\n", err)
		return
	}
	settingsPath := filepath.Join(m2Dir, "settings.xml")
	if data, err := os.ReadFile(settingsPath); err == nil {
		backup := settingsPath + ".bulwark-backup"
		os.WriteFile(backup, data, cfgPerm) //nolint:errcheck // best-effort backup
		fmt.Fprintf(out, "[ok] Existing settings.xml backed up to %s\n", backup)
	}
	if err := os.WriteFile(settingsPath, []byte(MavenSettingsXML(p.Port)), cfgPerm); err != nil {
		fmt.Fprintf(out, "[warn] Maven settings: %v\n", err)
		return
	}
	fmt.Fprintf(out, "[ok] Maven mirror configured: %s\n", settingsPath)
}

func writePkgMgrConfig(p ProxyInfo, home, goos string, out io.Writer) {
	switch p.Ecosystem {
	case EcosystemPypi:
		writePypiConfig(p, home, goos, out)
	case EcosystemMaven:
		writeMavenConfig(p, home, out)
	case EcosystemNpm:
		fmt.Fprintf(out, "[info] npm registry will be configured after service activation\n")
	case EcosystemVsx:
		baseURL := fmt.Sprintf("https://localhost:%d", p.Port)
		targets := detectVSCodeTargetsForSetup(home, goos, out)
		for _, target := range targets {
			writeVsxProductJSON(target.ConfigDir, baseURL, out, target.InstallDirs)
		}
		for _, dir := range collectVSCodeInstallDirs(targets) {
			patchInstallProductJSON(dir, baseURL, out)
		}
		if err := saveVSXSetupState(home, targets); err != nil {
			fmt.Fprintf(out, "[warn] VSX setup state: %v\n", err)
		}
	}
}

// resolveVsxOverlayContent builds the content for a user-data product.json
// overlay. It tries to read the install-dir product.json (preferring the
// .bulwark-backup original) and merge the proxy URLs, so that vendor-specific
// extensionsGallery fields are preserved. Falls back to a minimal overlay.
func resolveVsxOverlayContent(baseURL string, installDirs []string) []byte {
	for _, idir := range installDirs {
		installProd := filepath.Join(idir, "product.json")
		for _, src := range []string{installProd + ".bulwark-backup", installProd} {
			if raw, err := os.ReadFile(src); err == nil {
				if merged, mergeErr := mergeGalleryJSONURL(raw, baseURL); mergeErr == nil {
					return merged
				}
			}
		}
	}
	return []byte(VsxGalleryProductJSONURL(baseURL))
}

// writeVsxProductJSON writes a product.json gallery overlay into a single
// editor user-data directory, backing up any existing file first.
//
// baseURL is the proxy base URL (e.g. "https://localhost:18003" or
// "https://bulwark.corp.com:18003"). When installDirs is non-empty the
// function merges the proxy URLs into the install-dir product.json to preserve
// vendor-specific extensionsGallery fields. If no install dir is readable a
// minimal overlay is written.
func writeVsxProductJSON(dir string, baseURL string, out io.Writer, installDirs []string) {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		fmt.Fprintf(out, "[warn] %s: %v\n", dir, err)
		return
	}
	cfgFile := filepath.Join(dir, "product.json")
	if data, err := os.ReadFile(cfgFile); err == nil {
		backup := cfgFile + ".bulwark-backup"
		if _, berr := os.Stat(backup); os.IsNotExist(berr) {
			os.WriteFile(backup, data, cfgPerm) //nolint:errcheck // best-effort backup
			fmt.Fprintf(out, "[ok] Existing product.json backed up to %s\n", backup)
		}
	}
	content := resolveVsxOverlayContent(baseURL, installDirs)
	if err := os.WriteFile(cfgFile, content, cfgPerm); err != nil {
		fmt.Fprintf(out, "[warn] %s: %v\n", cfgFile, err)
		return
	}
	fmt.Fprintf(out, "[ok] Gallery configured: %s\n", cfgFile)
}

// --- Autostart ---

// AutostartDir returns the OS-specific autostart directory.
func AutostartDir(goos, home string) string {
	switch goos {
	case OSDarwin:
		return filepath.Join(home, "Library", "LaunchAgents")
	case OSLinux:
		return filepath.Join(home, ".config", "systemd", "user")
	case OSWindows:
		return filepath.Join(home, "AppData", "Roaming", "Microsoft",
			"Windows", "Start Menu", "Programs", "Startup")
	default:
		return ""
	}
}

// AutostartFileName returns the filename for the autostart entry.
func AutostartFileName(goos, ecosystem string) string {
	switch goos {
	case OSDarwin:
		return fmt.Sprintf("com.bulwark.%s.plist", ecosystem)
	case OSLinux:
		return fmt.Sprintf("bulwark-%s.service", ecosystem)
	case OSWindows:
		return fmt.Sprintf("bulwark-%s.bat", ecosystem)
	default:
		return ""
	}
}

// AutostartContent returns the file content for the autostart entry.
func AutostartContent(p ProxyInfo, paths Paths, goos string) string {
	switch goos {
	case OSDarwin:
		label := fmt.Sprintf("com.bulwark.%s", p.Ecosystem)
		return LaunchdPlistXML(label, paths.Binary, paths.Config)
	case OSLinux:
		return SystemdUnitFile(p.BinaryName, paths.Binary, paths.Config)
	case OSWindows:
		return WindowsBatchFile(paths.Binary, paths.Config)
	default:
		return ""
	}
}

func writeAutostartFile(p ProxyInfo, paths Paths, home, goos string, out io.Writer) {
	dir := AutostartDir(goos, home)
	if dir == "" {
		fmt.Fprintf(out, "[info] Autostart not supported on %s; start manually\n", goos)
		return
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		fmt.Fprintf(out, "[warn] autostart dir: %v\n", err)
		return
	}

	fileName := AutostartFileName(goos, p.Ecosystem)
	content := AutostartContent(p, paths, goos)
	filePath := filepath.Join(dir, fileName)

	if err := os.WriteFile(filePath, []byte(content), cfgPerm); err != nil {
		fmt.Fprintf(out, "[warn] autostart file: %v\n", err)
		return
	}
	fmt.Fprintf(out, "[ok] Autostart entry: %s\n", filePath)
}

// --- Orchestration ---

// SetupFiles performs all file-system changes for installation. It does NOT run
// external commands (npm, launchctl, systemctl). Use Setup for a complete install.
func SetupFiles(p ProxyInfo, home, exePath, goos string, out io.Writer) error {
	paths := ResolvePaths(p, home, goos)

	for _, d := range []string{paths.EcoDir, paths.BinDir} {
		if err := os.MkdirAll(d, dirPerm); err != nil {
			return fmt.Errorf("creating %s: %w", d, err)
		}
	}

	configData := p.ConfigData
	if p.Ecosystem == EcosystemVsx {
		configData = autoConfigureVSCodeUpstream(configData, home, goos, out)

		// Generate a self-signed TLS certificate so the proxy can serve HTTPS.
		// VS Code's Chromium CSP (connect-src 'self' https: ws:) blocks plain
		// HTTP connections, so HTTPS is required for the gallery proxy.
		certPath := filepath.Join(paths.EcoDir, "tls.crt")
		keyPath := filepath.Join(paths.EcoDir, "tls.key")
		_, certErr := os.Stat(certPath)
		_, keyErr := os.Stat(keyPath)
		if certErr != nil || keyErr != nil {
			if err := GenerateSelfSignedCert(certPath, keyPath); err != nil {
				return fmt.Errorf("generating TLS certificate: %w", err)
			}
			fmt.Fprintf(out, "[ok] TLS certificate generated: %s\n", certPath)
			installCertInTrustStore(certPath, goos, out)
		} else {
			fmt.Fprintf(out, "[ok] Reusing existing TLS certificate: %s\n", certPath)
		}
		configData = PatchConfigForTLS(configData, certPath, keyPath)
	}

	if err := os.WriteFile(paths.Config, configData, cfgPerm); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	fmt.Fprintf(out, "[ok] Config written to %s\n", paths.Config)

	if err := CopyFile(exePath, paths.Binary); err != nil {
		return fmt.Errorf("copying binary: %w", err)
	}
	fmt.Fprintf(out, "[ok] Binary installed to %s\n", paths.Binary)

	writePkgMgrConfig(p, home, goos, out)
	writeAutostartFile(p, paths, home, goos, out)
	PrintPostSetup(p, paths, out)
	return nil
}

// SetupClientOnly configures VS Code on this machine to use a remote Bulwark
// proxy without installing a local proxy binary or autostart entry. It is
// intended for developer laptops in corporate environments where vsx-bulwark
// runs on a shared server.
//
// serverURL must be an https URL (e.g. "https://bulwark.corp.com:18003").
// Only product.json files are written; no binary is copied and no autostart
// entry is created. Use -uninstall to revert.
func SetupClientOnly(serverURL string, home, goos string, out io.Writer) error {
	u, err := url.Parse(serverURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("server URL must be an https URL with a host (e.g. https://bulwark.corp.com:18003), got: %s", serverURL)
	}
	// Normalise: strip any trailing slash so gallery URL construction is clean.
	baseURL := strings.TrimRight(serverURL, "/")

	fmt.Fprintln(out, "=== Bulwark Client-Only VSX Setup ===")
	fmt.Fprintf(out, "Configuring VS Code to use Bulwark at: %s\n\n", baseURL)

	targets := detectVSCodeTargetsForSetup(home, goos, out)
	for _, target := range targets {
		writeVsxProductJSON(target.ConfigDir, baseURL, out, target.InstallDirs)
	}
	for _, dir := range collectVSCodeInstallDirs(targets) {
		patchInstallProductJSON(dir, baseURL, out)
	}
	if err := saveVSXSetupState(home, targets); err != nil {
		return err
	}

	fmt.Fprintf(out, "\n[ok] VS Code configured to use Bulwark at %s\n", baseURL)
	fmt.Fprintf(out, "     Restart VS Code for the change to take effect.\n")
	fmt.Fprintf(out, "     To revert: run %s-bulwark -uninstall\n", EcosystemVsx)
	return nil
}

// UninstallFiles removes files installed by SetupFiles.
func UninstallFiles(p ProxyInfo, home, goos string, out io.Writer) error {
	paths := ResolvePaths(p, home, goos)

	// Remove autostart entry.
	dir := AutostartDir(goos, home)
	if dir != "" {
		fileName := AutostartFileName(goos, p.Ecosystem)
		if fileName != "" {
			os.Remove(filepath.Join(dir, fileName)) //nolint:errcheck // may not exist
			fmt.Fprintf(out, "[ok] Removed autostart entry\n")
		}
	}

	// Remove pip config.
	if p.Ecosystem == EcosystemPypi {
		_, cfgFile := PipConfigPaths(home, goos)
		os.Remove(cfgFile) //nolint:errcheck // may not exist
		fmt.Fprintf(out, "[ok] Removed pip config\n")
	}

	// Restore VSX product.json for each editor.
	if p.Ecosystem == EcosystemVsx {
		for _, target := range configuredVSCodeTargets(home, goos) {
			restoreUserDataProductJSON(target.ConfigDir, out)
		}
		for _, dir := range collectVSCodeInstallDirs(configuredVSCodeTargets(home, goos)) {
			restoreInstallProductJSON(dir, out)
		}
		clearVSXSetupState(home)
		fmt.Fprintf(out, "[ok] VS Code/VSCodium/Code OSS gallery config removed\n")
	}

	// Restore Maven settings from backup.
	if p.Ecosystem == EcosystemMaven {
		settingsPath := filepath.Join(home, ".m2", "settings.xml")
		backup := settingsPath + ".bulwark-backup"
		if data, err := os.ReadFile(backup); err == nil {
			os.WriteFile(settingsPath, data, cfgPerm) //nolint:errcheck // best-effort restore
			os.Remove(backup)                         //nolint:errcheck // best-effort cleanup
			fmt.Fprintf(out, "[ok] Restored Maven settings.xml from backup\n")
		} else {
			os.Remove(settingsPath) //nolint:errcheck // may not exist
			fmt.Fprintf(out, "[ok] Removed Maven settings.xml\n")
		}
	}

	os.RemoveAll(paths.EcoDir) //nolint:errcheck // best-effort
	fmt.Fprintf(out, "[ok] Removed %s\n", paths.EcoDir)

	os.Remove(paths.Binary) //nolint:errcheck // may not exist
	fmt.Fprintf(out, "[ok] Removed %s\n", paths.Binary)

	fmt.Fprintf(out, "\n%s-bulwark has been uninstalled.\n", p.Ecosystem)
	return nil
}

// --- External command helpers (not unit-tested; documented exemption from coverage) ---

func activateNpm(port int, out io.Writer) {
	url := fmt.Sprintf("http://localhost:%d/", port)
	if _, err := exec.LookPath("npm"); err != nil {
		fmt.Fprintf(out, "[info] npm not found; set registry manually:\n")
		fmt.Fprintf(out, "       npm config set registry %s\n", url)
		return
	}
	cmd := exec.Command("npm", "config", "set", "registry", url) //nolint:gosec // user-initiated setup
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(out, "[warn] npm config: %s (%v)\n", strings.TrimSpace(string(output)), err)
		return
	}
	fmt.Fprintf(out, "[ok] npm registry set to %s\n", url)
}

func activateLaunchd(plistPath string, out io.Writer) {
	exec.Command("launchctl", "unload", plistPath).Run() //nolint:errcheck,gosec // best-effort unload before reload
	cmd := exec.Command("launchctl", "load", plistPath)  //nolint:gosec // user-initiated
	if _, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(out, "[warn] launchctl load failed; start manually\n")
		return
	}
	fmt.Fprintf(out, "[ok] LaunchAgent loaded\n")
}

func activateSystemd(unitName string, out io.Writer) {
	exec.Command("systemctl", "--user", "daemon-reload").Run()              //nolint:errcheck,gosec // best-effort
	cmd := exec.Command("systemctl", "--user", "enable", "--now", unitName) //nolint:gosec // user-initiated
	if _, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(out, "[warn] systemctl enable failed; start manually\n")
		return
	}
	fmt.Fprintf(out, "[ok] systemd service enabled: %s\n", unitName)
}

func deactivateNpm(out io.Writer) {
	if _, err := exec.LookPath("npm"); err != nil {
		return
	}
	exec.Command("npm", "config", "delete", "registry").Run() //nolint:errcheck,gosec // best-effort
	fmt.Fprintf(out, "[ok] npm registry restored to default\n")
}

func deactivateLaunchd(plistPath string, out io.Writer) {
	exec.Command("launchctl", "unload", plistPath).Run() //nolint:errcheck,gosec // best-effort
	fmt.Fprintf(out, "[ok] LaunchAgent unloaded\n")
}

func deactivateSystemd(unitName string, out io.Writer) {
	exec.Command("systemctl", "--user", "disable", "--now", unitName).Run() //nolint:errcheck,gosec // best-effort
	exec.Command("systemctl", "--user", "daemon-reload").Run()              //nolint:errcheck,gosec // best-effort
	fmt.Fprintf(out, "[ok] systemd service disabled\n")
}

// --- Public API ---

// ActivateServices runs OS-specific commands to start services and configure
// tools that require CLI execution (npm config, launchctl, systemctl).
func ActivateServices(p ProxyInfo, home, goos string, out io.Writer) {
	if p.Ecosystem == EcosystemNpm {
		activateNpm(p.Port, out)
	}

	switch goos {
	case OSDarwin:
		label := fmt.Sprintf("com.bulwark.%s", p.Ecosystem)
		plistPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
		activateLaunchd(plistPath, out)
	case OSLinux:
		unitName := fmt.Sprintf("bulwark-%s.service", p.Ecosystem)
		activateSystemd(unitName, out)
	case OSWindows:
		paths := ResolvePaths(p, home, goos)
		fmt.Fprintf(out, "[ok] Proxy will start automatically on login\n")
		fmt.Fprintf(out, "     To start now: %s -config %s\n", paths.Binary, paths.Config)
	}
}

// DeactivateServices stops running services before uninstall.
func DeactivateServices(p ProxyInfo, home, goos string, out io.Writer) {
	if p.Ecosystem == EcosystemNpm {
		deactivateNpm(out)
	}

	switch goos {
	case OSDarwin:
		label := fmt.Sprintf("com.bulwark.%s", p.Ecosystem)
		plistPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
		deactivateLaunchd(plistPath, out)
	case OSLinux:
		unitName := fmt.Sprintf("bulwark-%s.service", p.Ecosystem)
		deactivateSystemd(unitName, out)
	}
}

// Setup performs a complete installation: writes files, configures the package
// manager, creates autostart entries, and activates the service.
func Setup(p ProxyInfo, out io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("finding home directory: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}
	goos := runtime.GOOS
	if err := SetupFiles(p, home, exe, goos, out); err != nil {
		return err
	}
	ActivateServices(p, home, goos, out)
	return nil
}

// Uninstall stops the service, removes files, and restores package manager config.
func Uninstall(p ProxyInfo, out io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("finding home directory: %w", err)
	}
	goos := runtime.GOOS
	DeactivateServices(p, home, goos, out)
	return UninstallFiles(p, home, goos, out)
}

// PrintPostSetup prints instructions after a successful setup.
func PrintPostSetup(p ProxyInfo, paths Paths, out io.Writer) {
	fmt.Fprintf(out, "\n=== %s-bulwark installed successfully ===\n\n", p.Ecosystem)
	fmt.Fprintf(out, "Binary:  %s\n", paths.Binary)
	fmt.Fprintf(out, "Config:  %s\n", paths.Config)
	fmt.Fprintf(out, "Port:    %d\n\n", p.Port)
	fmt.Fprintf(out, "To start manually:\n")
	fmt.Fprintf(out, "  %s -config %s\n\n", paths.Binary, paths.Config)
	fmt.Fprintf(out, "To reconfigure rules after install:\n")
	fmt.Fprintf(out, "  1. Edit %s\n", paths.Config)
	fmt.Fprintf(out, "  2. Restart the proxy (the service restarts automatically on reboot).\n\n")
	fmt.Fprintf(out, "To uninstall:\n")
	fmt.Fprintf(out, "  %s -uninstall\n", paths.Binary)
}

// InstalledConfigPath returns the standard config file path for a proxy
// installation under the given home directory.
func InstalledConfigPath(p ProxyInfo, home, goos string) string {
	return ResolvePaths(p, home, goos).Config
}

// IsInstalledAt checks whether the proxy config exists at the standard
// installation location under the given home directory.
func IsInstalledAt(p ProxyInfo, home, goos string) bool {
	_, err := os.Stat(InstalledConfigPath(p, home, goos))
	return err == nil
}

// SetupFilesOnly installs files and configures the package manager but does not
// activate autostart services (launchctl/systemd). The autostart entry is still
// written so the proxy starts automatically on next login. Use this when the
// calling process will continue running as the proxy.
func SetupFilesOnly(p ProxyInfo, out io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("finding home directory: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}
	return SetupFilesOnlyAt(p, home, exe, runtime.GOOS, out)
}

// SetupFilesOnlyAt is the testable implementation of SetupFilesOnly.
func SetupFilesOnlyAt(p ProxyInfo, home, exe, goos string, out io.Writer) error {
	if err := SetupFiles(p, home, exe, goos, out); err != nil {
		return err
	}
	if p.Ecosystem == EcosystemNpm {
		activateNpm(p.Port, out)
	}
	return nil
}
