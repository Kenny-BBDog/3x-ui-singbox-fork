package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/singbox"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// SingboxService mirrors XrayService for the sing-box core: it renders the
// sing-box config from the SAME inbounds/clients tables (protocols anytls
// and hysteria2sb), supervises the process, and persists per-client traffic
// into the same client_traffics store — so quotas, expiry, IP limits and
// node sync behave identically for sing-box and Xray inbounds.
type SingboxService struct {
	inboundService InboundService
	clientService  ClientService
}

func NewSingboxService(inboundService InboundService, clientService ClientService) *SingboxService {
	return &SingboxService{inboundService: inboundService, clientService: clientService}
}

// HasSingboxInbounds reports whether any enabled local sing-box inbound
// exists (used to decide whether to start/stop the process at all).
func (s *SingboxService) HasSingboxInbounds() bool {
	inbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return false
	}
	for _, in := range inbounds {
		if in.Enable && in.Protocol.IsSingbox() && in.NodeID == nil {
			return true
		}
	}
	return false
}

// GetSingboxConfig renders the sing-box config from DB state — the exact
// counterpart of XrayService.GetXrayConfig, including the quota/expiry
// enable-map semantics.
//
// Credentials come from each inbound's own settings.clients[] (a customer holds
// a different sing-box password per machine), while enable/disable comes from
// the shared client_traffics rows — so quota and expiry still enforce across
// every protocol and every node the customer is attached to.
func (s *SingboxService) GetSingboxConfig() ([]byte, error) {
	inbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return nil, err
	}
	inputs := make([]singbox.RenderInput, 0)
	for _, in := range inbounds {
		if !in.Protocol.IsSingbox() || in.NodeID != nil {
			continue
		}
		// enableMap mirrors the Xray path: a client disabled by quota or
		// expiry is dropped from the running config.
		enabled := make(map[string]bool, len(in.ClientStats))
		for _, st := range in.ClientStats {
			enabled[st.Email] = st.Enable
		}
		inputs = append(inputs, singbox.RenderInput{Inbound: *in, Enabled: enabled})
	}
	return singbox.RenderConfig(inputs, s.clashSecret())
}

// clashSecret returns (or lazily creates) the panel setting holding the
// clash-api secret used between the panel and the sing-box process.
func (s *SingboxService) clashSecret() string {
	settingService := SettingService{}
	secret, err := settingService.GetSingboxClashSecret()
	if err != nil || secret == "" {
		secret = "sb-" + fmt.Sprintf("%x", time.Now().UnixNano())
		_ = settingService.SetSingboxClashSecret(secret)
	}
	return secret
}

// Apply regenerates and applies the config. A check failure or no-op change
// leaves the running process untouched (live connections survive).
func (s *SingboxService) Apply() error {
	cfg, err := s.GetSingboxConfig()
	if err != nil {
		return err
	}
	return singbox.GetManager().Apply(cfg)
}

// Reconcile is Apply + the "should this even run" decision; called at panel
// start and after inbound/client mutations.
func (s *SingboxService) Reconcile() error {
	if !s.HasSingboxInbounds() {
		singbox.GetManager().StopAll()
		return nil
	}
	return s.Apply()
}

// Status returns process info for the UI.
func (s *SingboxService) Status() (running bool, version string, lastErr string) {
	running = singbox.GetManager().IsRunning()
	lastErr = singbox.GetManager().LastError()
	version, err := singbox.Version()
	if err != nil {
		version = ""
	}
	return running, version, lastErr
}

// Restart re-applies the config unconditionally.
func (s *SingboxService) Restart() error {
	cfg, err := s.GetSingboxConfig()
	if err != nil {
		return err
	}
	if err := singbox.CheckConfig(cfg); err != nil {
		return err
	}
	// force restart: bypass the fingerprint no-op guard
	singbox.GetManager().ForceRestart(cfg)
	return nil
}

// ---------------------------------------------------------------- traffic

// sbTrafficState tracks last-seen per-user totals from the v2ray gRPC stats
// service so each poll persists only the delta. Counters are cumulative for
// the process lifetime; a process restart resets them, so the baselines are
// cleared whenever a fresh process is detected (first poll after restart).
type sbTrafficState struct {
	mu       sync.Mutex
	lastUp   map[string]int64
	lastDown map[string]int64
}

var sbTraffic = sbTrafficState{
	lastUp:   make(map[string]int64),
	lastDown: make(map[string]int64),
}

// sbStatsConn is a lazily-dialed gRPC connection to the sing-box stats API.
var (
	sbStatsConnOnce sync.Once
	sbStatsConn     *grpc.ClientConn
	sbStatsConnErr  error
)

func sbStatsDial() (*grpc.ClientConn, error) {
	sbStatsConnOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		sbStatsConn, sbStatsConnErr = grpc.DialContext(ctx, singbox.V2RayAPIAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
	return sbStatsConn, sbStatsConnErr
}

// sbGetStats reads one cumulative counter from the sing-box stats service.
// The service is registered under the v2ray.core namespace with the same
// message shapes as Xray's stats API.
func sbGetStats(ctx context.Context, conn *grpc.ClientConn, name string) (int64, error) {
	req := &command.GetStatsRequest{Name: name}
	var out command.GetStatsResponse
	err := conn.Invoke(ctx, "/v2ray.core.app.stats.command.StatsService/GetStats", req, &out)
	if err != nil {
		return 0, err
	}
	return out.GetStat().GetValue(), nil
}

// PollTraffic reads per-user cumulative counters from the sing-box v2ray
// stats API and persists deltas into client_traffics — the same store, and
// the same semantics, as Xray's own traffic job. Runs from cron every ~5s.
func (s *SingboxService) PollTraffic() error {
	if !singbox.GetManager().IsRunning() {
		return nil
	}
	inbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return err
	}
	// collect the (inbound id, email) pairs we meter
	type meter struct {
		inboundId int
	}
	users := make(map[string]meter)
	var tags []string
	for _, in := range inbounds {
		if !in.Protocol.IsSingbox() || in.NodeID != nil {
			continue
		}
		tags = append(tags, in.Tag)
		for _, st := range in.ClientStats {
			if st.Email != "" {
				users[st.Email] = meter{inboundId: in.Id}
			}
		}
	}
	if len(users) == 0 {
		return nil
	}

	conn, err := sbStatsDial()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	sbTraffic.mu.Lock()
	defer sbTraffic.mu.Unlock()

	var updated []*xray.ClientTraffic
	for email, m := range users {
		up, upErr := sbGetStats(ctx, conn, "user>>>"+email+">>>traffic>>>uplink")
		down, downErr := sbGetStats(ctx, conn, "user>>>"+email+">>>traffic>>>downlink")
		if upErr != nil || downErr != nil {
			// counters exist only after the user's first connection; and a
			// not-found error after traffic is a transient/unknown email
			continue
		}
		prevUp := sbTraffic.lastUp[email]
		prevDown := sbTraffic.lastDown[email]
		dUp, dDown := up-prevUp, down-prevDown
		sbTraffic.lastUp[email] = up
		sbTraffic.lastDown[email] = down
		if dUp <= 0 && dDown <= 0 {
			continue
		}
		if dUp < 0 || dDown < 0 {
			// process restarted; counters reset — re-baseline, no delta
			continue
		}
		updated = append(updated, &xray.ClientTraffic{
			InboundId: m.inboundId, Email: email, Up: dUp, Down: dDown,
		})
	}
	if len(updated) == 0 {
		return nil
	}
	_, _, _ = s.inboundService.AddTraffic(nil, updated)
	return nil
}

// ResetTrafficAfterRestart clears the per-process baselines so a fresh
// sing-box process (whose counters restart at zero) is not mis-compared.
func (s *SingboxService) ResetTrafficAfterRestart() {
	sbTraffic.mu.Lock()
	defer sbTraffic.mu.Unlock()
	sbTraffic.lastUp = make(map[string]int64)
	sbTraffic.lastDown = make(map[string]int64)
}

// GetSettings is unused placeholder to satisfy interface checks.
func (s *SingboxService) Describe() string {
	return fmt.Sprintf("singbox service: running=%v", singbox.GetManager().IsRunning())
}
