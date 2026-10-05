package xray

// ClientTraffic represents traffic statistics and limits for a specific client.
// It tracks upload/download usage, expiry times, and online status for inbound clients.
type ClientTraffic struct {
	Id        int    `json:"id" form:"id" gorm:"primaryKey;autoIncrement" example:"14825"`
	InboundId int    `json:"inboundId" form:"inboundId" gorm:"index:idx_client_traffics_inbound" example:"1"`
	Enable    bool   `json:"enable" form:"enable" example:"true"`
	Email     string `json:"email" form:"email" gorm:"unique" example:"user1"`
	UUID      string `json:"uuid" form:"uuid" gorm:"-" example:"e18c9a96-71bf-48d4-933f-8b9a46d4290c"`
	SubId     string `json:"subId" form:"subId" gorm:"-" example:"i7tvdpeffi0hvvf1"`
	Up        int64  `json:"up" form:"up" example:"1048576"`
	Down      int64  `json:"down" form:"down" example:"2097152"`
	// RawUp and RawDown are the unweighted bytes behind Up/Down. Up/Down carry
	// the weighted value (what quota enforcement compares against Total), so a
	// node with a traffic multiplier depletes a quota faster; these keep the true
	// byte count so a mis-set multiplier stays auditable and correctable rather
	// than being permanently baked into the total. Equal to Up/Down while every
	// inbound is 1x.
	RawUp      int64 `json:"rawUp" form:"rawUp" gorm:"column:raw_up;default:0" example:"1048576"`
	RawDown    int64 `json:"rawDown" form:"rawDown" gorm:"column:raw_down;default:0" example:"2097152"`
	ExpiryTime int64 `json:"expiryTime" form:"expiryTime" gorm:"index:idx_client_traffics_renew,priority:1" example:"1735689600000"`
	Total      int64 `json:"total" form:"total" example:"10737418240"`
	Reset      int   `json:"reset" form:"reset" gorm:"default:0;index:idx_client_traffics_renew,priority:2" example:"0"`
	// ResetDay renews on that day of each calendar month instead of every
	// Reset days; 0 disables monthly renewal.
	ResetDay int `json:"resetDay" form:"resetDay" gorm:"default:0" example:"0"`
	// ResetWeekday renews weekly at panel-local midnight: 1 Monday through 7 Sunday.
	ResetWeekday int `json:"resetWeekday" form:"resetWeekday" gorm:"default:0" example:"0"`
	// ResetMax caps how many times auto-renew may fire; 0 means no cap.
	ResetMax int `json:"resetMax" form:"resetMax" gorm:"default:0" example:"0"`
	// ResetCount is how many have fired, so a prepaid plan stops on its own.
	ResetCount   int   `json:"resetCount" form:"resetCount" gorm:"default:0" example:"0"`
	LastOnline   int64 `json:"lastOnline" form:"lastOnline" gorm:"default:0" example:"1735680000000"`
	LastSubFetch int64 `json:"lastSubFetch" form:"lastSubFetch" gorm:"default:0" example:"1735680000000"`
}
