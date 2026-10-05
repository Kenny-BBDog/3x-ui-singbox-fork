package service

import (
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/singbox"
)

// singboxClientsForTest parses an inbound's settings into singbox client entries,
// exposing the per-machine credential mirror the node push consumes.
func singboxClientsForTest(settings string) ([]singbox.InboundClient, model.Inbound, error) {
	entryClients, ibSettings, err := singbox.ParseInboundClients(settings)
	return entryClients, model.Inbound{}, err
}