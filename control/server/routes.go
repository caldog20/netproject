package server

import (
	"calnet_server/control/control_service"
	"calnet_server/keys"
	"calnet_server/types"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
	"weak"

	"github.com/coder/websocket"
)

var (
	mu         sync.RWMutex
	relayConns map[uint64]weak.Pointer[websocket.Conn]
)

func init() {
	relayConns = make(map[uint64]weak.Pointer[websocket.Conn])
}

func (s *Server) RegisterRoutes() http.Handler {
	mux := http.NewServeMux()

	// Register routes
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not Found", http.StatusNotFound)
	})

	mux.HandleFunc("POST /login", s.loginHandler)

	mux.HandleFunc("POST /update", s.updateHandler)

	mux.HandleFunc("GET /health", s.healthHandler)

	mux.HandleFunc("/relay", s.relayHandler)

	// Wrap the mux with CORS middleware
	return s.corsMiddleware(mux)
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		w.Header().
			Set("Access-Control-Allow-Origin", "*")
			// Replace "*" with specific origins if needed
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		w.Header().
			Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-CSRF-Token")
		w.Header().
			Set("Access-Control-Allow-Credentials", "false")
			// Set to "true" if credentials are required

		// Handle preflight OPTIONS requests
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Proceed with the next handler
		next.ServeHTTP(w, r)
	})
}

func (s *Server) loginHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "error reading request body", http.StatusBadRequest)
		return
	}

	req := types.LoginRequest{}
	err = json.Unmarshal(body, &req)
	if err != nil {
		http.Error(w, "error decoding json body", http.StatusBadRequest)
		return
	}

	resp, err := s.control.LoginNode(r.Context(), req)
	if err != nil {
		switch err {
		case control_service.ErrInvalidNodeKey:
			http.Error(w, "invalid node key", http.StatusBadRequest)
			return
		// case control_service.ErrNodeKeyExpired:
		// 	http.Error(w, "node key expired", http.StatusUnauthorized)
		// 	return
		default:
			http.Error(w, "error logging in node", http.StatusInternalServerError)
			return
		}
	}

	jsonResp, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "Failed to marshal response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(jsonResp); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

func (s *Server) updateHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "error reading request body", http.StatusBadRequest)
		return
	}

	req := &types.UpdateRequest{}
	err = json.Unmarshal(body, req)
	if err != nil {
		http.Error(w, "error decoding json body", http.StatusBadRequest)
		return
	}

	resp, err := s.control.Update(r.Context(), req)
	if err != nil {
		switch err {
		case control_service.ErrInvalidNodeKey:
			http.Error(w, "invalid node key", http.StatusBadRequest)
			return
		case control_service.ErrNodeKeyExpired:
			http.Error(w, "node key expired", http.StatusUnauthorized)
			return
		default:
			http.Error(w, "error polling for update", http.StatusInternalServerError)
			return
		}
	}

	jsonResp, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "Failed to marshal response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(jsonResp); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	stats := make(map[string]map[string]string)
	stats["db"] = s.db.Health()
	resp, err := json.MarshalIndent(stats, "", " ")
	if err != nil {
		http.Error(w, "Failed to marshal health check response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(resp); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

func (s *Server) relayHandler(w http.ResponseWriter, r *http.Request) {
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

	nodeID, err := s.control.VerifyNodeForRelay(r.Context(), nodeKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

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
			if !contextDone(ctx) {
				log.Printf("error reading from node %d websocket: %s", nodeID, err)
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
			if !contextDone(ctx) {
				log.Printf("error writing packet to destination node conn: %s", err)
			}
			return
		}
	}
}

func contextDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
