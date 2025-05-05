package testrelay

import (
	"calnet_server/keys"
	"encoding/binary"
	"fmt"
	"log"
	"net/http"
	"sync"
	"weak"

	"github.com/coder/websocket"
)

var (
	mu           sync.RWMutex
	relayConns   map[uint64]weak.Pointer[websocket.Conn]
	testNodeKey1 keys.PublicKey
	testNodeKey2 keys.PublicKey
	NodeID1      uint64 = 1
	NodeID2      uint64 = 2
)

func StartRelayTestServer(tk1, tk2 keys.PublicKey, port int) {
	testNodeKey1 = tk1
	testNodeKey2 = tk2
	relayConns = make(map[uint64]weak.Pointer[websocket.Conn])
	mux := http.NewServeMux()
	mux.HandleFunc("/relay", relayHandler)
	http.ListenAndServe(fmt.Sprintf(":%d", port), mux)
}

func relayHandler(w http.ResponseWriter, r *http.Request) {
	nodeKeyStr := r.Header.Get("x-node-key")
	if nodeKeyStr == "" {
		http.Error(w, "invalid node key", http.StatusBadRequest)
		return
	}

	nodeKey := keys.PublicKey{}
	err := nodeKey.DecodeFromString(nodeKeyStr)
	if err != nil || nodeKey.IsZero() {
		http.Error(w, "invalid node key", http.StatusBadRequest)
		return
	}
	var nodeID uint64
	if nodeKey == testNodeKey1 {
		nodeID = 1
	}
	if nodeKey == testNodeKey2 {
		nodeID = 2
	}

	// nodeID, err := s.control.VerifyNodeForRelay(r.Context(), nodeKey)
	// if err != nil {
	// 	http.Error(w, err.Error(), http.StatusUnauthorized)
	// 	return
	// }

	socket, err := websocket.Accept(w, r, nil)
	if err != nil {
		http.Error(w, "Failed to open websocket", http.StatusInternalServerError)
		return
	}
	defer socket.Close(websocket.StatusGoingAway, "server closing websocket")

	mu.Lock()
	ec, ok := relayConns[nodeID]
	if ok {
		if ec.Value() != nil {
			ec.Value().Close(websocket.StatusGoingAway, "duplicate websocket connection - closing")
		}
	}
	relayConns[nodeID] = weak.Make(socket)
	mu.Unlock()

	ctx := r.Context()

	for {
		mt, packet, err := socket.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return
			}
			return
		}

		if mt != websocket.MessageBinary {
			log.Printf("invalid websocket message type on node conn: %d - dropping packet", nodeID)
			continue
		}

		dstNodeID := binary.BigEndian.Uint64(packet[:8])
		if dstNodeID == 0 || dstNodeID == nodeID {
			log.Printf("invalid destination node ID in relay packet - dropping packet")
			continue
		}

		mu.RLock()
		conn, ok := relayConns[dstNodeID]
		mu.RUnlock()

		if conn.Value() == nil || !ok {
			log.Printf("destination node conn not found - dropping packet")
			continue
		}

		binary.BigEndian.PutUint64(packet[:8], nodeID)
		err = conn.Value().Write(ctx, websocket.MessageBinary, packet)
		if err != nil {
			log.Println("error writing to websocket, closing")
			return
		}
	}
}
