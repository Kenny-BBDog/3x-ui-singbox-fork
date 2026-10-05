package model

type NodeClientTraffic struct {
	Id     int    `json:"id" gorm:"primaryKey;autoIncrement"`
	NodeId int    `json:"nodeId" gorm:"uniqueIndex:idx_node_email,priority:1;not null"`
	Email  string `json:"email" gorm:"uniqueIndex:idx_node_email,priority:2;not null"`
	Up     int64  `json:"up"`
	Down   int64  `json:"down"`
	// RawUp and RawDown baseline the node's unweighted bytes, mirroring
	// ClientTraffic.RawUp/RawDown. Up/Down baseline the node's already-weighted
	// values, so the master adds the node's weighted delta to the client's row
	// and never re-weights it.
	RawUp   int64 `json:"rawUp" gorm:"column:raw_up;default:0"`
	RawDown int64 `json:"rawDown" gorm:"column:raw_down;default:0"`
}
