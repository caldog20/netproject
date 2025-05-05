package control_service

import (
	"context"
	"log"
	"net/netip"
	"os"
	"slices"
	"sync"
	"time"

	"calnet_server/control/database"
	"calnet_server/control/internal/models"
	"calnet_server/keys"
	"calnet_server/types"
)

// TODO: The purpose of separating control_service from the http handlers
// is to allow flexibility and de-coupling. Probably don't need to pass the JSON message types
// directly to this service, instead create middle-man types to convert from DAL to business logic types

const (
	NodeKeyValidFor = ((time.Hour * 24) * 30)
)

// Service represents the business logic layer that coordinates between
// the HTTP handlers and the data access layer.
type Service interface {
	LoginNode(ctx context.Context, loginReq types.LoginRequest) (*types.LoginResponse, error)
	LogoutNode(ctx context.Context, nodeKey keys.PublicKey) error
	Update(ctx context.Context, req *types.UpdateRequest) (*types.UpdateResponse, error)
	VerifyNodeForRelay(ctx context.Context, nodeKey keys.PublicKey) (uint64, error)
}

type service struct {
	db     database.Service
	prefix netip.Prefix

	mu          sync.Mutex
	nextIP      netip.Addr
	notifyChans map[uint64]chan struct{}
}

// New creates a new instance of the business service
func New(db database.Service) Service {
	prefix := netip.MustParsePrefix(os.Getenv("PREFIX"))

	nodes, err := db.GetNodes(context.Background())
	if err != nil {
		panic("error getting nodes from database for ipam")
	}

	nextIP := prefix.Addr().Next()
	var allocated []netip.Addr
	for _, n := range nodes {
		allocated = append(allocated, n.IP)
	}

	for slices.Contains(allocated, nextIP) {
		nextIP = nextIP.Next()
	}

	return &service{
		db:          db,
		prefix:      prefix,
		nextIP:      nextIP,
		notifyChans: map[uint64]chan struct{}{},
	}
}

func (s *service) LoginNode(ctx context.Context, loginReq types.LoginRequest) (*types.LoginResponse, error) {
	nodeKey := loginReq.NodeKey
	// Validate node key
	if nodeKey.IsZero() {
		return nil, ErrInvalidNodeKey
	}

	// Get node from database
	// If the node is not found, try to register it
	// If the node is found, validate the key is not expired
	node, err := s.db.GetNode(ctx, nodeKey)
	if err != nil {
		if err == database.ErrNodeNotFound {
			return s.registerNode(ctx, loginReq)
		}
		log.Printf("database error: %s", err)
		return nil, ErrDatabase
	}

	// keyExpired := false
	// // If the node is found, validate the key is not expired
	// if node.NodeKeyExpiry.After(time.Now()) {
	// 	keyExpired = true
	// }

	// config := &types.NodeConfig{
	// 	ID: node.ID,
	// 	IP: node.IP.String(),
	// 	Prefix: node.Prefix.String(),
	// }

	resp := &types.LoginResponse{
		LoggedIn:  true,
		KeyExpiry: node.NodeKeyExpiry,
	}

	log.Printf("node %s logged in", node.NodeKey.EncodeToString())
	log.Printf("%+v", node)
	return resp, nil
}

func (s *service) LogoutNode(ctx context.Context, nodeKey keys.PublicKey) error {
	log.Printf("node logged out: %s", nodeKey.EncodeToString())
	return nil
}

func (s *service) getNotifyChan(nodeID uint64) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.notifyChans[nodeID]
	if ok {
		return c
	}

	c = make(chan struct{}, 5)
	s.notifyChans[nodeID] = c
	s.notifyAllExceptLocked(nodeID)
	return c
}

func (s *service) notifyAllExceptLocked(nodeID uint64) {
	for id, c := range s.notifyChans {
		if id == nodeID {
			continue
		}
		select {
		case c <- struct{}{}:
		default:
			continue
		}
	}
}

func (s *service) notifyAll() {
	s.notifyAllExcept(0)
}

func (s *service) notifyAllExcept(nodeID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifyAllExcept(nodeID)
}

func (s *service) Update(ctx context.Context, req *types.UpdateRequest) (*types.UpdateResponse, error) {
	if req.NodeKey.IsZero() {
		return nil, ErrInvalidNodeKey
	}

	node, err := s.db.GetNode(ctx, req.NodeKey)
	if err != nil {
		return nil, err
	}

	if req.NodeKey != node.NodeKey {
		return nil, ErrInvalidNodeKey
	}

	// TODO: Maybe return this in response instead of error
	if nodeKeyExpired(node.NodeKeyExpiry) {
		return nil, ErrNodeKeyExpired
	}

	resp := &types.UpdateResponse{}

	// Node is requesting full sync, which means it may be syncing for the first time
	// or has reconnected and wants to refresh the state
	// Send update immediately
	if req.FullUpdate {
		resp.Config = &types.NodeConfig{
			ID:     node.ID,
			IP:     node.IP.String(),
			Prefix: node.Prefix.String(),
		}

		peers, err := s.getPeersOfNode(ctx, node.ID)
		if err != nil {
			return nil, err
		}

		resp.Peers = peers
		resp.PeerCount = len(peers)
		return resp, nil
	}

	// Node is not requesting fast full sync, check notify chan for updates
	c := s.getNotifyChan(node.ID)
	t := time.NewTimer(time.Second * 10)
	select {
	case <-c:
		peers, err := s.getPeersOfNode(ctx, node.ID)
		if err != nil {
			return nil, err
		}

		resp.Peers = peers
		resp.PeerCount = len(peers)
	case <-t.C:
	}

	return resp, nil
}

func (s *service) getPeersOfNode(ctx context.Context, nodeID uint64) ([]types.Peer, error) {
	// Get details of all nodes except requesting node
	// TODO: Break this out into a separate function
	nodes, err := s.db.GetNodes(ctx)
	if err != nil {
		log.Printf("error getting nodes from database: %s", err)
		return nil, err
	}

	if len(nodes) == 0 {
		return nil, nil
	}

	var peers []types.Peer
	for _, n := range nodes {
		// Skip requesting node
		if n.ID == nodeID {
			continue
		}
		peers = append(peers, types.Peer{
			ID:        n.ID,
			IP:        n.IP.String(),
			PublicKey: n.NodeKey,
		})
	}

	return peers, nil
}

func (s *service) registerNode(ctx context.Context, loginReq types.LoginRequest) (*types.LoginResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	nextIP := s.nextIP

	node := &models.Node{
		IP:            nextIP,
		Prefix:        s.prefix,
		NodeKey:       loginReq.NodeKey,
		NodeKeyExpiry: time.Now().Add(NodeKeyValidFor),
	}

	err := s.db.CreateNode(ctx, node)
	if err != nil {
		log.Printf("error creating node: %s", err)
		return nil, err
	}

	s.nextIP = s.nextIP.Next()

	log.Printf("node %s registered successfully", node.NodeKey.EncodeToString())
	log.Printf("%+v", node)
	return &types.LoginResponse{
		LoggedIn:  true,
		KeyExpiry: node.NodeKeyExpiry,
	}, nil
}

func (s *service) VerifyNodeForRelay(ctx context.Context, nodeKey keys.PublicKey) (uint64, error) {
	if nodeKey.IsZero() {
		return 0, ErrInvalidNodeKey
	}

	node, err := s.db.GetNode(ctx, nodeKey)
	if err != nil {
		if err == database.ErrNodeNotFound {
			return 0, ErrNotFound
		}
		log.Printf("database error: %s", err)
		return 0, ErrDatabase
	}

	if nodeKeyExpired(node.NodeKeyExpiry) {
		return 0, ErrNodeKeyExpired
	}

	return node.ID, nil
}

func nodeKeyExpired(expires time.Time) bool {
	return time.Now().After(expires)
}
