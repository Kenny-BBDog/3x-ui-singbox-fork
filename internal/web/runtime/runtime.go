package runtime

import (
	"context"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

type Runtime interface {
	Name() string

	AddInbound(ctx context.Context, ib *model.Inbound) error
	DelInbound(ctx context.Context, ib *model.Inbound) error
	UpdateInbound(ctx context.Context, oldIb, newIb *model.Inbound) error

	AddUser(ctx context.Context, ib *model.Inbound, userMap map[string]any) error
	RemoveUser(ctx context.Context, ib *model.Inbound, email string) error

	// Per-client operations that route through the node's clients API on
	// Remote (instead of pushing the whole inbound) so the node applies
	// per-user xray API calls without a DelInbound+AddInbound cycle.
	UpdateUser(ctx context.Context, ib *model.Inbound, email string, payload model.Client) error
	DeleteUser(ctx context.Context, ib *model.Inbound, email string) error
	AddClient(ctx context.Context, ib *model.Inbound, client model.Client) error

	// DeleteClient removes the client identified by email entirely from the
	// runtime's own store: on Remote it hits the node's full-delete endpoint
	// (record, attachments, traffic), unlike DeleteUser which only detaches
	// from one inbound and leaves the node's client record behind. Local has
	// no client store of its own, so it is a no-op there.
	DeleteClient(ctx context.Context, email string) error

	RestartXray(ctx context.Context) error

	ResetClientTraffic(ctx context.Context, ib *model.Inbound, email string) error
	ResetInboundTraffic(ctx context.Context, ib *model.Inbound) error
	ResetAllTraffics(ctx context.Context) error

	// PushCertMaterial installs TLS material on the runtime's host and reports
	// whether it changed anything. A node holds a copy that nothing refreshes, so
	// it expires on a fixed date; Local has no copy of its own — the central panel
	// is where the material comes from — so it is a no-op there, as DeleteClient is.
	PushCertMaterial(ctx context.Context, m CertMaterial) (changed bool, err error)
}

// CertMaterial is one certificate/key pair to distribute onto a node. The paths
// are absolute on the NODE and are confined to its certificate root there.
type CertMaterial struct {
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
	Cert     string `json:"cert"`
	Key      string `json:"key"`
}
