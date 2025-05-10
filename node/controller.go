package node

import (
	"calnet_server/keys"
	"calnet_server/types"
	"errors"
	"log"
	"net"
	"net/netip"
	"slices"
	"sync"

	"golang.org/x/net/ipv4"
)

type Controller struct {
	mu          sync.Mutex
	peersByIP   map[netip.Addr]*Peer
	peersByID   map[uint64]*Peer
	mux         *UdpMux
	relayClient *RelayClient
	tun         Tun
	config      *ControllerConfig
	up          bool
}

func NewController() *Controller {
	return &Controller{
		peersByIP: make(map[netip.Addr]*Peer),
		peersByID: make(map[uint64]*Peer),
	}
}

type ControllerConfig struct {
	ControlUrl  string
	NodeID      uint64
	NodeIP      netip.Addr
	NodePrefix  netip.Prefix
	UdpPort     int
	NodePublic  keys.PublicKey
	NodePrivate keys.PrivateKey
}

func (c *Controller) SetConfig(config *ControllerConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config = config
	// TODO: Verify whats changed here and restart services as required when config changes
	return nil
}

func (c *Controller) Up() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	muxBinded := false
	tunUp := false

	fail := func() {
		if muxBinded {
			c.mux.Unbind()
			c.mux = nil
		}
		if tunUp {
			c.tun.Close()
			c.tun = nil
		}
		c.relayClient = nil
	}

	if c.up {
		return errors.New("controller is already running")
	}

	c.relayClient = NewRelayClient(c.config.ControlUrl, c.config.NodePublic)
	c.mux = NewUdpMux(&UdpMuxOpts{
		NodeID: c.config.NodeID, Port: c.config.UdpPort, RelayClient: c.relayClient})

	// create tunnel interface

	err := c.mux.Bind()
	if err != nil {
		fail()
		return err
	}

	muxBinded = true

	c.tun, err = NewTun()
	if err != nil {
		log.Println("error creating tun")
		fail()
		return err
	}
	tunUp = true

	if err := c.tun.ConfigureIPAddress(c.config.NodeIP, c.config.NodePrefix); err != nil {
		fail()
		log.Println("error creating tun routes")
		return err
	}
	c.startTunLoop()

	c.up = true
	return nil
}

func (c *Controller) Down() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.up {
		return errors.New("controller is not up")
	}

	err := c.mux.Unbind()
	if err != nil {
		return err
	}
	c.mux = nil

	c.tun.Close()
	c.tun = nil
	c.up = false
	return nil
}

func (c *Controller) ProcessPeerUpdate(update []types.Peer, count int) {
	if count == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.up {
		log.Println("controller is not up, skipping process update")
		return
	}

	var newPeers []types.Peer
	var updatePeers []types.Peer
	var delPeers []uint64
	// Loop through peers sent from update
	for _, p := range update {
		// If the peer sent from control server is not found in our state
		// it is a new peer that should be added
		_, ok := c.peersByID[p.ID]
		if !ok {
			newPeers = append(newPeers, p)
			// If it is found, then we need to check for changes and update
		} else {
			updatePeers = append(updatePeers, p)
		}
	}

	// Check for peers we have that no longer exist on control server
	for pid, _ := range c.peersByID {
		if !slices.ContainsFunc(update, func(p types.Peer) bool {
			if p.ID == pid {
				return true
			}
			return false
		}) {
			delPeers = append(delPeers, pid)
		}
	}

	for _, p := range newPeers {
		c.AddPeer(p)
	}
	for _, pid := range delPeers {
		c.DeletePeer(pid)
	}
	for _, p := range updatePeers {
		c.UpdatePeer(p)
	}

	log.Printf("added %d new peers", len(newPeers))
	log.Printf("removed %d peers", len(delPeers))
	log.Printf("updated %d peers", len(updatePeers))
}

func (c *Controller) AddPeer(peer types.Peer) {
	newPeer := NewPeer(
		peer.ID,
		netip.MustParseAddr(peer.IP),
		c.mux,
		c.tun,
	)

	c.mux.RegisterPeer(newPeer)
	c.peersByID[newPeer.id] = newPeer
	c.peersByIP[newPeer.ip] = newPeer
}

func (c *Controller) DeletePeer(peerID uint64)   {}
func (c *Controller) UpdatePeer(peer types.Peer) {}

func (c *Controller) startTunLoop() {
	go func() {
		buf := make([]byte, 1500)
		for {
			n, err := c.tun.Read(buf)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				log.Printf("error reading from tun: %d", err)
				return
			}
			ipHeader, err := ipv4.ParseHeader(buf)
			if err != nil {
				log.Println("not a valid ipv4 packet - dropping")
				continue
			}
			// TODO Move this
			if ipHeader.Dst.Equal(c.config.NodeIP.AsSlice()) {
				continue
			}
			dstIP, ok := netip.AddrFromSlice(ipHeader.Dst)
			dstIP = dstIP.Unmap()
			if !c.config.NodePrefix.Contains(dstIP) {
				continue
			}
			log.Println("tun dst ip - ", dstIP)
			if !ok {
				continue
			}
			c.mu.Lock()
			dstPeer, ok := c.peersByIP[dstIP]
			c.mu.Unlock()
			if !ok {
				continue
			}
			// log.Printf("packet for peer ip %s", dstIP.String())
			packet := make([]byte, n)
			copy(packet, buf[:n])
			dstPeer.inboundTunPacket(packet)
		}
	}()
}
