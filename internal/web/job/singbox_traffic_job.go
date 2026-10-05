package job

import (
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// SingboxTrafficJob polls the supervised sing-box process's clash-api for
// per-client byte counters and persists deltas into client_traffics —
// the exact role XrayTrafficJob plays for the Xray core.
type SingboxTrafficJob struct {
	singboxService *service.SingboxService
}

func NewSingboxTrafficJob(s *service.SingboxService) *SingboxTrafficJob {
	return &SingboxTrafficJob{singboxService: s}
}

func (j *SingboxTrafficJob) Run() {
	if err := j.singboxService.PollTraffic(); err != nil {
		logger.Debug("singbox traffic poll failed:", err)
	}
}
