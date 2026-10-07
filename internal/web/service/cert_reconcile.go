package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/singbox"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

// CertPushResult reports one node's certificate reconcile outcome. A node that
// needs pushing on every tick is a symptom worth seeing, not one to absorb.
type CertPushResult struct {
	NodeID    int    `json:"nodeId"`
	Name      string `json:"name"`
	Pushed    int    `json:"pushed"`
	Unchanged int    `json:"unchanged"`
	Error     string `json:"error,omitempty"`
}

// certMaterialRef is one TLS pair as the node's own inbound config carries it. The
// master reads the same two paths from its own disk, which is where they are renewed.
type certMaterialRef struct {
	certFile string
	keyFile  string
}

// PushCertsToNodes copies the master's current TLS material to every enabled node,
// for each certificate that node's own inbounds serve. Deriving the set from those
// inbounds is what keeps the master's other tenants' certificates off a VPN node.
func (s *NodeService) PushCertsToNodes(ctx context.Context) ([]CertPushResult, error) {
	mgr := runtime.GetManager()
	if mgr == nil {
		return nil, errors.New("runtime manager unavailable")
	}
	var nodes []*model.Node
	if err := database.GetDB().Where("enable = ?", true).Order("id").Find(&nodes).Error; err != nil {
		return nil, err
	}
	ids := make([]int, len(nodes))
	for i, n := range nodes {
		ids[i] = n.Id
	}
	results, panics := fanoutInboundResults(ids, nodeFanoutConcurrency, func(i int) CertPushResult {
		res := CertPushResult{NodeID: nodes[i].Id, Name: nodes[i].Name}
		if err := pushNodeCerts(ctx, mgr, nodes[i], &res); err != nil {
			res.Error = err.Error()
		}
		return res
	})
	for i, panicErr := range panics {
		if panicErr != nil {
			results[i] = CertPushResult{NodeID: ids[i], Name: nodes[i].Name, Error: panicErr.Error()}
		}
	}
	return results, nil
}

// pushNodeCerts converges one node. One failed domain must not abandon the rest:
// a certificate skipped here is one that stops following the master.
func pushNodeCerts(ctx context.Context, mgr *runtime.Manager, n *model.Node, res *CertPushResult) error {
	refs, err := nodeCertMaterialRefs(n.Id)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return nil
	}
	rt, err := mgr.RuntimeFor(&n.Id)
	if err != nil {
		return err
	}
	var failures []error
	for _, ref := range refs {
		changed, err := pushOneCert(ctx, rt, ref)
		switch {
		case err != nil:
			failures = append(failures, fmt.Errorf("%s: %w", ref.certFile, err))
		case changed:
			res.Pushed++
			logger.Warningf("node %s installed renewed TLS material for %s and reloaded its sing-box", n.Name, ref.certFile)
		default:
			res.Unchanged++
		}
	}
	return errors.Join(failures...)
}

func pushOneCert(ctx context.Context, rt runtime.Runtime, ref certMaterialRef) (bool, error) {
	cert, err := os.ReadFile(ref.certFile)
	if err != nil {
		return false, err
	}
	key, err := os.ReadFile(ref.keyFile)
	if err != nil {
		return false, err
	}
	return rt.PushCertMaterial(ctx, runtime.CertMaterial{
		CertFile: ref.certFile,
		KeyFile:  ref.keyFile,
		Cert:     string(cert),
		Key:      string(key),
	})
}

// nodeCertMaterialRefs returns the TLS pairs the node's own inbounds serve, deduped
// by path. Two inbounds sharing one domain must cost one push, not two restarts.
func nodeCertMaterialRefs(nodeID int) ([]certMaterialRef, error) {
	var inbounds []*model.Inbound
	if err := database.GetDB().Where("node_id = ?", nodeID).Order("id").Find(&inbounds).Error; err != nil {
		return nil, err
	}
	seen := map[certMaterialRef]struct{}{}
	var refs []certMaterialRef
	add := func(certFile, keyFile string) {
		certFile, keyFile = strings.TrimSpace(certFile), strings.TrimSpace(keyFile)
		if certFile == "" || keyFile == "" {
			return
		}
		ref := certMaterialRef{certFile: certFile, keyFile: keyFile}
		if _, ok := seen[ref]; ok {
			return
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	for _, ib := range inbounds {
		if ib.Protocol.IsSingbox() {
			if strings.TrimSpace(ib.Settings) == "" {
				continue
			}
			var settings singbox.InboundSettings
			if err := json.Unmarshal([]byte(ib.Settings), &settings); err != nil {
				logger.Warningf("inbound %s on node %d has unreadable settings, so its certificate cannot be renewed: %v", ib.Tag, nodeID, err)
				continue
			}
			add(settings.CertificatePath, settings.KeyPath)
			continue
		}
		for _, ref := range certPairsInStreamSettings(ib.StreamSettings) {
			add(ref.certFile, ref.keyFile)
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].certFile < refs[j].certFile })
	return refs, nil
}

// certPairsInStreamSettings returns the certificate/key path pairs an inbound's
// stream settings reference. TLS material nests under several keys depending on
// the security type, so the walk visits every object carrying a certificateFile.
func certPairsInStreamSettings(streamSettings string) []certMaterialRef {
	streamSettings = strings.TrimSpace(streamSettings)
	if streamSettings == "" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(streamSettings), &parsed); err != nil {
		return nil
	}
	var out []certMaterialRef
	var walk func(node any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if certFile, ok := v["certificateFile"].(string); ok && strings.TrimSpace(certFile) != "" {
				keyFile, _ := v["keyFile"].(string)
				out = append(out, certMaterialRef{certFile: certFile, keyFile: keyFile})
			}
			for _, val := range v {
				walk(val)
			}
		case []any:
			for _, item := range v {
				walk(item)
			}
		}
	}
	walk(parsed)
	return out
}
