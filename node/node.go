package node

import (
	"calnet_server/keys"
	"calnet_server/types"
	"context"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"sync"
	"time"
)

// type Config struct {
// 	id     uint64
// 	ip     netip.Addr
// 	prefix netip.Prefix
// }

type Node struct {
	mu         sync.Mutex
	running    bool
	publicKey  keys.PublicKey
	privateKey keys.PrivateKey
	keyExpiry  time.Time
	config     *types.NodeConfig
	client     *Client
	controller *Controller
	runCtx     context.Context
	runCancel  context.CancelFunc
	udpPort    int
	controlUrl string
}

type NodeOpts struct {
	ControlUrl string
	UdpPort    int
}

func NewNode(opts *NodeOpts) *Node {
	client := NewClient(opts.ControlUrl, keys.PublicKey{})

	return &Node{
		controller: NewController(),
		client:     client,
		udpPort:    opts.UdpPort,
		controlUrl: opts.ControlUrl,
	}
}

func (n *Node) Up() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.running {
		log.Println("node is already running")
		return
	}

	if n.privateKey.IsZero() {
		log.Println("node requires login first")
		return
	}

	// Always grab fresh copy of state on Up
	update, err := n.client.Update(true)
	if err != nil {
		log.Printf("error getting config from control server: %s", err)
		return
	}
	n.config = update.Config

	n.runCtx, n.runCancel = context.WithCancel(context.Background())

	controllerConfig := &ControllerConfig{
		ControlUrl:  n.controlUrl,
		NodeID:      update.Config.ID,
		NodeIP:      netip.MustParseAddr(update.Config.IP),
		NodePrefix:  netip.MustParsePrefix(update.Config.Prefix),
		UdpPort:     n.udpPort,
		NodePublic:  n.publicKey,
		NodePrivate: n.privateKey,
	}

	// TODO: eventually check error here when controller is upgraded
	n.controller.SetConfig(controllerConfig)
	// Once we have config, tell controller to bring up interfaces
	if err := n.controller.Up(); err != nil {
		log.Printf("error bringing up services: %s", err)
		return
	}
	log.Println("controller services started")
	n.controller.ProcessPeerUpdate(update.Peers, update.PeerCount)
	log.Println("starting update routine")
	if err := n.startUpdateRoutine(); err != nil {
		log.Printf("error starting update routine: %s", err)
		return
	}
	
	n.running = true
	log.Println("node is running")
}

func (n *Node) Login() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.privateKey.IsZero() {
		n.privateKey = keys.NewPrivateKey()
		n.publicKey = n.privateKey.PublicKey()
	}

	n.client.nodeKey = n.publicKey

	resp, err := n.client.Login()
	if err != nil {
		n.privateKey = keys.PrivateKey{}
		n.publicKey = keys.PublicKey{}
		n.client.nodeKey = keys.PublicKey{}
		return fmt.Errorf("node login error: %s", err)
	}

	if !resp.LoggedIn {
		n.privateKey = keys.PrivateKey{}
		n.publicKey = keys.PublicKey{}
		n.client.nodeKey = keys.PublicKey{}
		return fmt.Errorf("node key is expired")
	}

	n.keyExpiry = resp.KeyExpiry
	log.Println("node is logged in")
	return nil
}

func (n *Node) Down() {
	log.Println("node is stopping")
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.running {
		log.Println("node is already down")
		return
	}

	n.runCancel()
	// <-n.controllerClosed
	// <-n.updateRoutineClosed

	n.running = false
	log.Println("node is stopped")
}

func (n *Node) startUpdateRoutine() error {
	_, err := n.client.Health()
	if err != nil {
		return errors.New("error checking health of control server - possibly down")
	}

	isRunDone := func() bool {
		select {
		case <-n.runCtx.Done():
			return true
		default:
			return false
		}
	}

	go func() {
		for {
			if isRunDone() {
				return
			}
			update, err := n.client.Update(false)
			if err != nil {
				log.Printf("error getting update from control server: %s", err)
				return
			}

			if update.Config != nil {
				// config update
			}
			if update.Peers != nil && update.PeerCount != 0 {
				n.controller.ProcessPeerUpdate(update.Peers, update.PeerCount)
			}
		}
	}()

	return nil
}
