package database

import (
	"calnet_server/control/internal/models"
	"calnet_server/keys"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"strconv"
	"time"

	_ "github.com/joho/godotenv/autoload"
	_ "github.com/mattn/go-sqlite3"
)

var (
	ErrNodeNotFound = errors.New("node not found")
)

// Service represents a service that interacts with a database.
type Service interface {
	// Health returns a map of health status information.
	// The keys and values in the map are service-specific.
	Health() map[string]string

	// Close terminates the database connection.
	// It returns an error if the connection cannot be closed.
	Close() error

	GetNodes(ctx context.Context) ([]models.Node, error)
	CreateNode(ctx context.Context, node *models.Node) error
	UpdateNode(ctx context.Context, node *models.Node) error
	DeleteNode(ctx context.Context, nodeID uint64) error

	GetNode(ctx context.Context, nodeKey keys.PublicKey) (*models.Node, error)
}

type service struct {
	db *sql.DB
}

var (
	dburl      = os.Getenv("BLUEPRINT_DB_URL")
	dbInstance *service
)

// getDBURL returns the database URL with WAL mode and synchronous mode parameters
func getDBURL() string {
	// Add WAL mode and synchronous mode parameters to the URL
	// _journal=WAL enables Write-Ahead Logging
	// _synchronous=NORMAL provides a good balance between durability and performance
	// _busy_timeout=5000 sets a 5 second timeout for busy database
	return fmt.Sprintf("%s?_journal=WAL&_synchronous=NORMAL&_busy_timeout=5000", dburl)
}

// createTables creates the necessary database tables if they don't exist
func (s *service) createTables(ctx context.Context) error {
	// Create nodes table
	query := `
	CREATE TABLE IF NOT EXISTS nodes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ip TEXT NOT NULL,
		prefix TEXT NOT NULL,
		public_key TEXT NOT NULL,
		key_expiry DATETIME NOT NULL
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_nodes_ip ON nodes(ip);
	CREATE INDEX IF NOT EXISTS idx_nodes_public_key ON nodes(public_key);
	`

	_, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}

	return nil
}

func New() Service {
	// Reuse Connection
	if dbInstance != nil {
		return dbInstance
	}

	db, err := sql.Open("sqlite3", getDBURL())
	if err != nil {
		// This will not be a connection error, but a DSN parse error or
		// another initialization error.
		log.Fatal(err)
	}

	// Create a new service instance
	srv := &service{
		db: db,
	}

	// Create tables with a timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.createTables(ctx); err != nil {
		log.Fatalf("Failed to create tables: %v", err)
	}

	dbInstance = srv
	return dbInstance
}

// Health checks the health of the database connection by pinging the database.
// It returns a map with keys indicating various health statistics.
func (s *service) Health() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	stats := make(map[string]string)

	// Ping the database
	err := s.db.PingContext(ctx)
	if err != nil {
		stats["status"] = "down"
		stats["error"] = fmt.Sprintf("db down: %v", err)
		log.Fatalf("db down: %v", err) // Log the error and terminate the program
		return stats
	}

	// Database is up, add more statistics
	stats["status"] = "up"
	stats["message"] = "It's healthy"

	// Get database stats (like open connections, in use, idle, etc.)
	dbStats := s.db.Stats()
	stats["open_connections"] = strconv.Itoa(dbStats.OpenConnections)
	stats["in_use"] = strconv.Itoa(dbStats.InUse)
	stats["idle"] = strconv.Itoa(dbStats.Idle)
	stats["wait_count"] = strconv.FormatInt(dbStats.WaitCount, 10)
	stats["wait_duration"] = dbStats.WaitDuration.String()
	stats["max_idle_closed"] = strconv.FormatInt(dbStats.MaxIdleClosed, 10)
	stats["max_lifetime_closed"] = strconv.FormatInt(dbStats.MaxLifetimeClosed, 10)

	// Evaluate stats to provide a health message
	if dbStats.OpenConnections > 40 { // Assuming 50 is the max for this example
		stats["message"] = "The database is experiencing heavy load."
	}

	if dbStats.WaitCount > 1000 {
		stats["message"] = "The database has a high number of wait events, indicating potential bottlenecks."
	}

	if dbStats.MaxIdleClosed > int64(dbStats.OpenConnections)/2 {
		stats["message"] = "Many idle connections are being closed, consider revising the connection pool settings."
	}

	if dbStats.MaxLifetimeClosed > int64(dbStats.OpenConnections)/2 {
		stats["message"] = "Many connections are being closed due to max lifetime, consider increasing max lifetime or revising the connection usage pattern."
	}

	return stats
}

// Close closes the database connection.
// It logs a message indicating the disconnection from the specific database.
// If the connection is successfully closed, it returns nil.
// If an error occurs while closing the connection, it returns the error.
func (s *service) Close() error {
	log.Printf("Disconnected from database: %s", dburl)
	return s.db.Close()
}

// GetNode retrieves a node from the database by its public key
func (s *service) GetNode(ctx context.Context, nodeKey keys.PublicKey) (*models.Node, error) {
	query := `SELECT id, ip, public_key, key_expiry, created_at, updated_at FROM nodes WHERE public_key = ?`
	row := s.db.QueryRowContext(ctx, query, nodeKey.EncodeToString())

	var node models.Node
	var ipStr, prefixStr, pubKeyStr string
	err := row.Scan(&node.ID, &ipStr, &prefixStr, &pubKeyStr, &node.NodeKeyExpiry, &node.CreatedAt, &node.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNodeNotFound
		}
		return nil, fmt.Errorf("failed to scan node: %w", err)
	}

	// Parse IP address
	ip, err := netip.ParseAddr(ipStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse IP address: %w", err)
	}
	node.IP = ip

	// Parse prefix
	prefix, err := netip.ParsePrefix(prefixStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse IP prefix: %w", err)
	}
	node.Prefix = prefix

	// Parse public key
	if err := node.NodeKey.DecodeFromString(pubKeyStr); err != nil {
		log.Printf("failed to parse public key: %v", err)
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	return &node, nil
}

// GetNodes retrieves all nodes from the database
func (s *service) GetNodes(ctx context.Context) ([]models.Node, error) {
	query := `SELECT id, ip, public_key, key_expiry, created_at, updated_at FROM nodes`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query nodes: %w", err)
	}
	defer rows.Close()

	var nodes []models.Node
	for rows.Next() {
		var node models.Node
		var ipStr, prefixStr, pubKeyStr string
		err := rows.Scan(&node.ID, &ipStr, &prefixStr, &pubKeyStr, &node.NodeKeyExpiry, &node.CreatedAt, &node.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan node: %w", err)
		}

		// Parse IP address
		ip, err := netip.ParseAddr(ipStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse IP address: %w", err)
		}
		node.IP = ip

		// Parse prefix
		prefix, err := netip.ParsePrefix(prefixStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse IP prefix: %w", err)
		}
		node.Prefix = prefix

		// Parse public key
		if err := node.NodeKey.DecodeFromString(pubKeyStr); err != nil {
			return nil, fmt.Errorf("failed to parse public key: %w", err)
		}

		nodes = append(nodes, node)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating nodes: %w", err)
	}

	return nodes, nil
}

// CreateNode inserts a new node into the database
func (s *service) CreateNode(ctx context.Context, node *models.Node) error {
	query := `INSERT INTO nodes (ip, public_key, key_expiry, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`

	now := time.Now()
	node.CreatedAt = now
	node.UpdatedAt = now

	result, err := s.db.ExecContext(ctx, query,
		node.IP.String(),
		node.Prefix.String(),
		node.NodeKey.EncodeToString(),
		node.NodeKeyExpiry,
		node.CreatedAt,
		node.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create node: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}

	node.ID = uint64(id)
	return nil
}

// UpdateNode updates an existing node in the database
func (s *service) UpdateNode(ctx context.Context, node *models.Node) error {
	query := `UPDATE nodes SET ip = ?, public_key = ?, key_expiry = ?, updated_at = ? WHERE id = ?`

	node.UpdatedAt = time.Now()

	result, err := s.db.ExecContext(ctx, query,
		node.IP.String(),
		node.Prefix.String(),
		node.NodeKey.EncodeToString(),
		node.NodeKeyExpiry,
		node.UpdatedAt,
		node.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update node: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("node with ID %d not found", node.ID)
	}

	return nil
}

// DeleteNode removes a node from the database
func (s *service) DeleteNode(ctx context.Context, nodeID uint64) error {
	query := `DELETE FROM nodes WHERE id = ?`

	result, err := s.db.ExecContext(ctx, query, nodeID)
	if err != nil {
		return fmt.Errorf("failed to delete node: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("node with ID %d not found", nodeID)
	}

	return nil
}
