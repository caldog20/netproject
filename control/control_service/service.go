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

const (
	NodeKeyValidFor = ((time.Hour * 24) * 30)
)

// Service represents the business logic layer that coordinates between
// the HTTP handlers and the data access layer.
type Service interface {
	LoginNode(ctx context.Context, loginReq types.LoginRequest) (*types.LoginResponse, error)
	LogoutNode(ctx context.Context, nodeKey keys.PublicKey) error
	Poll(ctx context.Context, req *types.UpdateRequest) (*types.UpdateResponse, error)
}

type service struct {
	db     database.Service
	mu     sync.Mutex
	nextIP netip.Addr
	prefix netip.Prefix
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
		db:     db,
		prefix: prefix,
		nextIP: nextIP,
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
	// if node.NodeKeyExpiry.Before(time.Now()) {
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

func (s *service) Poll(ctx context.Context, req *types.UpdateRequest) (*types.UpdateResponse, error) {
	return nil, nil
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
