package controller

import (
	"github.com/mhsanaei/3x-ui/v3/internal/singbox"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/gin-gonic/gin"
)

// SingboxController exposes status/control endpoints for the sing-box core
// (anytls / hysteria2sb inbounds). Inbound/client CRUD itself goes through
// the regular inbounds/clients API — sing-box inbounds are first-class rows.
type SingboxController struct {
	BaseController
}

func NewSingboxController(g *gin.RouterGroup) *SingboxController {
	a := &SingboxController{}
	a.initRouter(g)
	return a
}

func (a *SingboxController) initRouter(g *gin.RouterGroup) {
	g.GET("/status", a.status)
	g.GET("/previewConfig", a.previewConfig)
	g.POST("/restart", a.restart)
}

func (a *SingboxController) status(c *gin.Context) {
	s := newSingboxSvc()
	running, version, lastErr := s.Status()
	jsonObj(c, gin.H{
		"running":   running,
		"version":   version,
		"lastError": lastErr,
		"binary":    singbox.GetBinaryPath(),
	}, nil)
}

func (a *SingboxController) previewConfig(c *gin.Context) {
	s := newSingboxSvc()
	cfg, err := s.GetSingboxConfig()
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	if cfg == nil {
		cfg = []byte("{}")
	}
	jsonObj(c, gin.H{"config": string(cfg)}, nil)
}

func (a *SingboxController) restart(c *gin.Context) {
	s := newSingboxSvc()
	jsonObj(c, nil, s.Restart())
}

func newSingboxSvc() *service.SingboxService {
	return service.NewSingboxService(service.InboundService{}, service.ClientService{})
}