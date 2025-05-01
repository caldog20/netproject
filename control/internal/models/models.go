package models

import (
	"calnet_server/keys"
	"net/netip"
	"time"
)

type Node struct {
	ID     uint64
	IP     netip.Addr
	Prefix netip.Prefix
	// Hostname string
	// User string
	// ControlKey keys.PublicKey
	NodeKey keys.PublicKey

	NodeKeyExpiry time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
