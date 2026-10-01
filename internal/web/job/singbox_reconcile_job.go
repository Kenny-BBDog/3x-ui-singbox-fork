package job

import (
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// SingboxReconcileJob keeps the supervised sing-box process in sync with DB
// state: any inbound/client/quota/expiry/node-sync change converges within one
// tick. No-op when the rendered config is unchanged (fingerprint guard in the
// manager), so live connections are never disturbed without a real change.
// This is the sing-box counterpart of Xray's debounced need-restart flow.
type SingboxReconcileJob struct {
	singboxService *service.SingboxService
}

func NewSingboxReconcileJob(s *service.SingboxService) *SingboxReconcileJob {
	return &SingboxReconcileJob{singboxService: s}
}

func (j *SingboxReconcileJob) Run() {
	if err := j.singboxService.Reconcile(); err != nil {
		logger.Debug("singbox reconcile failed:", err)
	}
}