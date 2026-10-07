package job

import (
	"context"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// nodeCertReconcileTimeout bounds one whole pass over the estate. A node that
// cannot answer in time is reported and retried on the next tick, not waited on.
const nodeCertReconcileTimeout = 2 * time.Minute

// NodeCertReconcileJob carries the master's renewed TLS material to every node.
// Only the master renews, and a node's copy is otherwise frozen, so without this
// every line on a node expires on one fixed date.
//
// The cadence bounds how long a node lags a renewal, and a renewal leaves ~30
// days of validity behind it, so the cadence is a cost trade rather than a safety
// one. A node already at the master's material costs one small POST per domain.
type NodeCertReconcileJob struct {
	nodeService service.NodeService
}

func NewNodeCertReconcileJob() *NodeCertReconcileJob {
	return &NodeCertReconcileJob{}
}

func (j *NodeCertReconcileJob) Run() {
	ctx, cancel := context.WithTimeout(context.Background(), nodeCertReconcileTimeout)
	defer cancel()
	results, err := j.nodeService.PushCertsToNodes(ctx)
	if err != nil {
		logger.Warning("node certificate reconcile failed:", err)
		return
	}
	for _, res := range results {
		if res.Error != "" {
			logger.Warningf("node certificate reconcile for %s: %s", res.Name, res.Error)
		}
	}
}
