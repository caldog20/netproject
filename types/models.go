package types

import (
	"calnet_server/keys"
	"time"
)

type LoginRequest struct {
	NodeKey keys.PublicKey
}

type LoginResponse struct {
	LoggedIn bool `json:"logged_in"`
	// KeyExpired bool `json:"key_expired"`
	KeyExpiry time.Time `json:"key_expiry"`
	// KeyExpired bool `json:"key_expired"`
	// NeedAuth bool `json:"need_auth"`
	// AuthURL string `json:"auth_url"`
}

type UpdateRequest struct{}

type UpdateResponse struct {
	Config *NodeConfig `json:"config;omitempty"`
	Peers  []Peer      `json:"peers"`
}

type NodeConfig struct {
	ID     uint64 `json:"id"`
	IP     string `json:"ip"`
	Prefix string `json:"prefix"`
}

type Peer struct {
	ID        uint64         `json:"id"`
	IP        string         `json:"ip"`
	PublicKey keys.PublicKey `json:"public_key"`
}
