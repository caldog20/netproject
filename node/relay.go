package node

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"sync"
	"time"

	"calnet_server/keys"

	"github.com/coder/websocket"
)

// RelayClient represents a client for the relay websocket connection
type RelayClient struct {
	baseURL    string
	nodeKey    keys.PublicKey
	httpClient *http.Client
	conn       *websocket.Conn
	mu         sync.RWMutex
}

// NewRelayClient creates a new relay client
func NewRelayClient(baseURL string, nodeKey keys.PublicKey) *RelayClient {
	return &RelayClient{
		baseURL: baseURL,
		nodeKey: nodeKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Connect establishes a websocket connection to the relay endpoint
func (c *RelayClient) Connect(ctx context.Context) error {
	// Create a new request
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/relay", c.baseURL), nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Add the node key header
	req.Header.Set("x-node-key", c.nodeKey.EncodeToString())

	// Connect to the websocket
	conn, _, err := websocket.Dial(ctx, req.URL.String(), &websocket.DialOptions{
		HTTPClient: c.httpClient,
		HTTPHeader: req.Header,
	})
	if err != nil {
		return fmt.Errorf("failed to dial websocket: %w", err)
	}
	// defer resp.Body.Close()

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	return nil
}

// SendPacket sends a binary packet to a destination node
func (c *RelayClient) SendPacket(ctx context.Context, dstNodeID uint64, data []byte) error {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("not connected")
	}

	// Create a packet with the destination node ID
	packet := make([]byte, 8+len(data))
	binary.BigEndian.PutUint64(packet[:8], dstNodeID)
	copy(packet[8:], data)

	// Send the packet
	return conn.Write(ctx, websocket.MessageBinary, packet)
}

// ReadPacket reads a binary packet from the websocket
func (c *RelayClient) ReadPacket(ctx context.Context) (srcNodeID uint64, data []byte, err error) {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return 0, nil, fmt.Errorf("not connected")
	}

	// Read the message
	mt, packet, err := conn.Read(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to read packet: %w", err)
	}

	if mt != websocket.MessageBinary {
		return 0, nil, fmt.Errorf("invalid message type: %v", mt)
	}

	if len(packet) < 8 {
		return 0, nil, fmt.Errorf("packet too short")
	}

	// Extract source node ID and data
	srcNodeID = binary.BigEndian.Uint64(packet[:8])
	data = packet[8:]

	return srcNodeID, data, nil
}

// Close closes the websocket connection
func (c *RelayClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil
	}

	err := c.conn.Close(websocket.StatusNormalClosure, "client closing connection")
	c.conn = nil
	return err
}

// IsConnected returns whether the client is currently connected
func (c *RelayClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn != nil
}
