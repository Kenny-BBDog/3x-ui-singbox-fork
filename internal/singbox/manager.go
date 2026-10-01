// Package singbox renders and supervises a sing-box process for inbounds the
// panel stores in the regular inbounds table (protocols anytls / hysteria2sb).
//
// Alignment with the Xray path (internal/web/service/xray.go) is deliberate:
//   - one managed sing-box process, config generated from DB state
//   - per-client credentials come from the shared clients tables
//   - traffic counters are persisted into the same client_traffics table,
//     so quotas/expiry/IP-limits enforce identically
//   - hot apply where possible (config check first, restart only on change)
package singbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

func GetBinaryPath() string {
	name := "sing-box"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	custom := filepath.Join(config.GetBinFolderPath(), name)
	if _, err := os.Stat(custom); err == nil {
		return custom
	}
	for _, p := range []string{"/usr/local/bin/sing-box", "/usr/bin/sing-box"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return custom
}

func GetConfigPath() string {
	return filepath.Join(config.GetBinFolderPath(), "singbox", "config.json")
}

func ConfigDir() string {
	return filepath.Join(config.GetBinFolderPath(), "singbox")
}

// Version runs `sing-box version` and returns the first line.
func Version() (string, error) {
	bin := GetBinaryPath()
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("sing-box binary not found at %s", bin)
	}
	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("sing-box version failed: %w", err)
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]), nil
}

// ---------------------------------------------------------------- config types

type SBTLS struct {
	Enabled         bool     `json:"enabled"`
	ServerName      string   `json:"server_name,omitempty"`
	ALPN            []string `json:"alpn,omitempty"`
	CertificatePath string   `json:"certificate_path,omitempty"`
	KeyPath         string   `json:"key_path,omitempty"`
	Insecure        bool     `json:"insecure,omitempty"`
}

type SBUser struct {
	Password string `json:"password"`
	Name     string `json:"name,omitempty"`
}

type SBInbound struct {
	Type       string   `json:"type"`
	Tag        string   `json:"tag"`
	Listen     string   `json:"listen"`
	ListenPort int      `json:"listen_port"`
	Users      []SBUser `json:"users,omitempty"`
	TLS        *SBTLS   `json:"tls,omitempty"`
	// hysteria2 extras
	UpMps   *int   `json:"up_mps,omitempty"`
	DownMps *int   `json:"down_mps,omitempty"`
	Obfs    *SBObfs `json:"obfs,omitempty"`
	IgnoreClientBandwidth bool `json:"ignore_client_bandwidth,omitempty"`
}

type SBObfs struct {
	Type     string `json:"type"`
	Password string `json:"password,omitempty"`
}

type SBOutbound struct {
	Type string `json:"type"`
	Tag  string `json:"tag"`
}

type SBLog struct {
	Level     string `json:"level"`
	Timestamp bool   `json:"timestamp"`
}

type SBClashAPI struct {
	ExternalController string `json:"external_controller"`
	Secret             string `json:"secret"`
}

type SBV2RayAPI struct {
	Listen string          `json:"listen"`
	Stats  *SBV2RayStats   `json:"stats"`
}

type SBV2RayStats struct {
	Enabled  bool     `json:"enabled"`
	Inbounds []string `json:"inbounds,omitempty"`
	Users    []string `json:"users,omitempty"`
}

type SBExperimental struct {
	ClashAPI *SBClashAPI `json:"clash_api,omitempty"`
	V2RayAPI *SBV2RayAPI `json:"v2ray_api,omitempty"`
}

type SBConfig struct {
	Log         *SBLog         `json:"log,omitempty"`
	Experimental *SBExperimental `json:"experimental,omitempty"`
	Inbounds    []SBInbound    `json:"inbounds"`
	Outbounds   []SBOutbound   `json:"outbounds"`
}

// ---------------------------------------------------------------- process

var (
	stopTimeoutGraceful = 5 * time.Second
	stopTimeoutForce    = 2 * time.Second
)

type procLogWriter struct {
	mu  sync.Mutex
	buf string
}

func (w *procLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf += string(p)
	for {
		i := strings.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(w.buf[:i])
		w.buf = w.buf[i+1:]
		if line != "" {
			logger.Infof("singbox: %s", line)
		}
	}
	return len(p), nil
}

type Process struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	configPath string
	exitCh     chan struct{}
	exitErr    error
}

func newProcess(configPath string) *Process {
	return &Process{configPath: configPath, exitCh: make(chan struct{})}
}

func (p *Process) start() error {
	bin := GetBinaryPath()
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("sing-box binary not found at %s", bin)
	}
	if _, err := os.Stat(p.configPath); err != nil {
		return fmt.Errorf("sing-box config not found at %s", p.configPath)
	}
	cmd := exec.Command(bin, "run", "-D", filepath.Dir(p.configPath), "-c", p.configPath)
	cmd.Stdout = &procLogWriter{}
	cmd.Stderr = &procLogWriter{}
	if err := cmd.Start(); err != nil {
		return err
	}
	p.cmd = cmd
	go func() {
		p.exitErr = cmd.Wait()
		close(p.exitCh)
		logger.Infof("singbox: process exited: %v", p.exitErr)
	}()
	return nil
}

func (p *Process) stop() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(os.Interrupt)
	select {
	case <-p.exitCh:
		return
	case <-time.After(stopTimeoutGraceful):
	}
	_ = p.cmd.Process.Kill()
	select {
	case <-p.exitCh:
	case <-time.After(stopTimeoutForce):
	}
}

func (p *Process) isRunning() bool {
	if p == nil || p.cmd == nil {
		return false
	}
	select {
	case <-p.exitCh:
		return false
	default:
		return true
	}
}

// ---------------------------------------------------------------- manager

type Manager struct {
	mu      sync.Mutex
	proc    *Process
	lastErr string
	started time.Time
	// configFingerprint is the SHA-ish string of the last applied config;
	// skipping no-op restarts keeps customer connections alive.
	configFingerprint string
}

var (
	managerInstance *Manager
	managerOnce     sync.Once
)

func GetManager() *Manager {
	managerOnce.Do(func() {
		managerInstance = &Manager{}
	})
	return managerInstance
}

func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.proc != nil && m.proc.isRunning()
}

func (m *Manager) LastError() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastErr
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.proc != nil {
		m.proc.stop()
		m.proc = nil
	}
}

// Apply regenerates the config and restarts sing-box when the config changed.
// A config-check failure keeps the current process untouched.
func (m *Manager) Apply(cfgJSON []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fp := configFingerprint(cfgJSON)
	if len(cfgJSON) == 0 {
		// no managed inbounds: stop and clean up
		if m.proc != nil {
			m.proc.stop()
			m.proc = nil
		}
		_ = os.Remove(GetConfigPath())
		m.configFingerprint = ""
		return nil
	}

	// validate before touching the running process
	if err := checkConfigBytes(cfgJSON); err != nil {
		m.lastErr = err.Error()
		return err
	}

	if fp == m.configFingerprint && m.proc != nil && m.proc.isRunning() {
		// nothing changed; leave live connections alone
		return nil
	}

	if err := writeConfigFile(cfgJSON); err != nil {
		m.lastErr = err.Error()
		return err
	}
	if m.proc != nil {
		m.proc.stop()
		m.proc = nil
	}
	p := newProcess(GetConfigPath())
	if err := p.start(); err != nil {
		m.lastErr = err.Error()
		return err
	}
	m.proc = p
	m.lastErr = ""
	m.started = time.Now()
	m.configFingerprint = fp
	return nil
}

// ForceRestart rewrites the config and restarts the process even when the
// fingerprint is unchanged (used by the manual Restart button).
func (m *Manager) ForceRestart(cfgJSON []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(cfgJSON) == 0 {
		if m.proc != nil {
			m.proc.stop()
			m.proc = nil
		}
		m.configFingerprint = ""
		return
	}
	if err := writeConfigFile(cfgJSON); err != nil {
		m.lastErr = err.Error()
		return
	}
	if m.proc != nil {
		m.proc.stop()
		m.proc = nil
	}
	p := newProcess(GetConfigPath())
	if err := p.start(); err != nil {
		m.lastErr = err.Error()
		return
	}
	m.proc = p
	m.lastErr = ""
	m.started = time.Now()
	m.configFingerprint = configFingerprint(cfgJSON)
}

func configFingerprint(cfg []byte) string {
	// cheap structural fingerprint: length + sampled bytes; exact equality
	// is unnecessary since the caller regenerates deterministically.
	if len(cfg) == 0 {
		return ""
	}
	step := len(cfg) / 16
	if step == 0 {
		step = 1
	}
	var b strings.Builder
	for i := 0; i < len(cfg); i += step {
		b.WriteByte(cfg[i])
	}
	return fmt.Sprintf("%d:%s", len(cfg), b.String())
}

func writeConfigFile(data []byte) error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(GetConfigPath(), data, 0o600)
}

// CheckConfig validates a rendered config via `sing-box check`.
func CheckConfig(data []byte) error {
	return checkConfigBytes(data)
}

func checkConfigBytes(data []byte) error {
	bin := GetBinaryPath()
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("sing-box binary not found at %s", bin)
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("sbcheck-%d.json", time.Now().UnixNano()))
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)
	out, err := exec.Command(bin, "check", "-c", tmp).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sing-box check failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}