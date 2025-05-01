package server

import (
	"fmt"
	"net/http"
	"time"

	"calnet_server/control/control_service"
	"calnet_server/control/database"
)

type Server struct {
	port int

	db      database.Service
	control control_service.Service
}

// NewServer creates a new HTTP server with the given dependencies
func NewServer(port int, db database.Service, control control_service.Service) *http.Server {
	srv := &Server{
		port:    port,
		db:      db,
		control: control,
	}

	// Declare Server config
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", srv.port),
		Handler:      srv.RegisterRoutes(),
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	return server
}
