package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/joho/godotenv/autoload"

	"calnet_server/control/control_service"
	"calnet_server/control/database"
	"calnet_server/control/server"
)

func gracefulShutdown(apiServer *http.Server, db database.Service, done chan bool) {
	// Create context that listens for the interrupt signal from the OS.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Listen for the interrupt signal.
	<-ctx.Done()

	log.Println("shutting down gracefully, press Ctrl+C again to force")

	// The context is used to inform the server it has 5 seconds to finish
	// the request it is currently handling
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := apiServer.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown with error: %v", err)
	}

	if err := db.Close(); err != nil {
		log.Printf("error closing database: %s", err)
	}

	log.Println("Server exiting")
	// Notify the main goroutine that the shutdown is complete
	done <- true
}

func main() {
	// Initialize dependencies
	port, err := strconv.Atoi(os.Getenv("PORT"))
	if err != nil {
		log.Fatalf("Invalid PORT environment variable: %v", err)
	}

	// Initialize database
	db := database.New()

	// Initialize control service
	controlService := control_service.New(db)

	// Create server with dependencies
	srv := server.NewServer(port, db, controlService)

	// Create a done channel to signal when the shutdown is complete
	done := make(chan bool, 1)

	// Run graceful shutdown in a separate goroutine
	go gracefulShutdown(srv, db, done)

	err = srv.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed to start: %v", err)
	}

	// Wait for graceful shutdown to complete
	<-done
}
