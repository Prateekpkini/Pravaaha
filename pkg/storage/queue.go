// Package storage — queue.go implements the client-side store-and-forward
// queue backed by SQLite (pure Go, no CGO via modernc.org/sqlite).
//
// When a user inputs vitals or selects an image, the data is immediately
// written to the local SQLite database with status PENDING. A background
// worker continuously reads PENDING items, transmits them via the UDP
// protocol, and marks them SENT only when the server acknowledges receipt.
//
// This design ensures zero data loss even during total connectivity outages:
// the queue persists on disk and auto-resumes when the link returns.
package storage

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	// Pure Go SQLite driver — no CGO, produces static binaries.
	_ "modernc.org/sqlite"
)

// -----------------------------------------------------------------------
// Queue status constants.
// -----------------------------------------------------------------------

const (
	StatusPending = "PENDING"
	StatusSending = "SENDING"
	StatusSent    = "SENT"
	StatusFailed  = "FAILED"
)

// QueueItem represents a single outbound item in the store-and-forward queue.
type QueueItem struct {
	ID        int64
	Type      string // "vitals" or "image"
	Payload   []byte // CBOR-encoded data (vitals) or raw image bytes
	ImageID   string // UUID for image items, empty for vitals
	Status    string
	Retries   int
	CreatedAt int64
	UpdatedAt int64
}

// -----------------------------------------------------------------------
// ClientQueue — SQLite-backed store-and-forward queue.
// -----------------------------------------------------------------------

// ClientQueue manages the outbound data queue for the telemedicine client.
type ClientQueue struct {
	db *sql.DB
	mu sync.Mutex
}

// NewClientQueue opens (or creates) the SQLite queue database at the given path.
func NewClientQueue(dbPath string) (*ClientQueue, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Enable WAL mode for better concurrent read/write performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	// Create the outbox table if it doesn't exist.
	createSQL := `
	CREATE TABLE IF NOT EXISTS outbox (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		type       TEXT    NOT NULL,
		payload    BLOB   NOT NULL,
		image_id   TEXT    DEFAULT '',
		status     TEXT    DEFAULT 'PENDING',
		retries    INTEGER DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_outbox_status ON outbox(status);
	`
	if _, err := db.Exec(createSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("create table: %w", err)
	}

	log.Printf("[queue] initialized at %s", dbPath)
	return &ClientQueue{db: db}, nil
}

// Enqueue inserts a new item into the outbox queue with PENDING status.
func (q *ClientQueue) Enqueue(itemType string, payload []byte, imageID string) (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now().Unix()
	result, err := q.db.Exec(
		`INSERT INTO outbox (type, payload, image_id, status, retries, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 0, ?, ?)`,
		itemType, payload, imageID, StatusPending, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("enqueue: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}

	log.Printf("[queue] enqueued %s item (id=%d, imageID=%s, %d bytes)", itemType, id, imageID, len(payload))
	return id, nil
}

// DequeueNext retrieves the oldest PENDING item and atomically sets its
// status to SENDING. Returns nil if the queue is empty.
func (q *ClientQueue) DequeueNext() (*QueueItem, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var item QueueItem
	err := q.db.QueryRow(
		`SELECT id, type, payload, image_id, status, retries, created_at, updated_at
		 FROM outbox WHERE status = ? ORDER BY id ASC LIMIT 1`,
		StatusPending,
	).Scan(&item.ID, &item.Type, &item.Payload, &item.ImageID,
		&item.Status, &item.Retries, &item.CreatedAt, &item.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil // Queue empty
	}
	if err != nil {
		return nil, fmt.Errorf("dequeue: %w", err)
	}

	// Atomically mark as SENDING.
	now := time.Now().Unix()
	if _, err := q.db.Exec(
		`UPDATE outbox SET status = ?, updated_at = ? WHERE id = ?`,
		StatusSending, now, item.ID,
	); err != nil {
		return nil, fmt.Errorf("mark sending: %w", err)
	}

	item.Status = StatusSending
	return &item, nil
}

// MarkSent updates the item's status to SENT after successful transmission.
func (q *ClientQueue) MarkSent(id int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now().Unix()
	_, err := q.db.Exec(
		`UPDATE outbox SET status = ?, updated_at = ? WHERE id = ?`,
		StatusSent, now, id,
	)
	if err != nil {
		return fmt.Errorf("mark sent: %w", err)
	}
	log.Printf("[queue] item %d marked SENT", id)
	return nil
}

// MarkFailed updates the item's status to FAILED and increments the retry counter.
// If retries < maxRetries, the item is reset to PENDING for automatic retry.
func (q *ClientQueue) MarkFailed(id int64, maxRetries int) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	var retries int
	if err := q.db.QueryRow(
		`SELECT retries FROM outbox WHERE id = ?`, id,
	).Scan(&retries); err != nil {
		return fmt.Errorf("get retries: %w", err)
	}

	retries++
	now := time.Now().Unix()
	newStatus := StatusPending // Retry
	if retries >= maxRetries {
		newStatus = StatusFailed // Give up
	}

	_, err := q.db.Exec(
		`UPDATE outbox SET status = ?, retries = ?, updated_at = ? WHERE id = ?`,
		newStatus, retries, now, id,
	)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}

	log.Printf("[queue] item %d: retry %d/%d → %s", id, retries, maxRetries, newStatus)
	return nil
}

// ResetStale resets items stuck in SENDING status (e.g., after a crash)
// back to PENDING so they can be retransmitted.
func (q *ClientQueue) ResetStale(staleTimeout time.Duration) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	cutoff := time.Now().Add(-staleTimeout).Unix()
	result, err := q.db.Exec(
		`UPDATE outbox SET status = ? WHERE status = ? AND updated_at < ?`,
		StatusPending, StatusSending, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("reset stale: %w", err)
	}

	count, _ := result.RowsAffected()
	if count > 0 {
		log.Printf("[queue] reset %d stale items to PENDING", count)
	}
	return int(count), nil
}

// GetPendingCount returns the number of items waiting to be sent.
func (q *ClientQueue) GetPendingCount() (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var count int
	err := q.db.QueryRow(
		`SELECT COUNT(*) FROM outbox WHERE status IN (?, ?)`,
		StatusPending, StatusSending,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count pending: %w", err)
	}
	return count, nil
}

// GetStats returns a summary of queue statistics.
func (q *ClientQueue) GetStats() (pending, sending, sent, failed int, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	rows, err := q.db.Query(
		`SELECT status, COUNT(*) FROM outbox GROUP BY status`,
	)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("get stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			continue
		}
		switch status {
		case StatusPending:
			pending = count
		case StatusSending:
			sending = count
		case StatusSent:
			sent = count
		case StatusFailed:
			failed = count
		}
	}
	return
}

// PurgeCompleted removes all SENT items from the queue to free space.
func (q *ClientQueue) PurgeCompleted() (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	result, err := q.db.Exec(`DELETE FROM outbox WHERE status = ?`, StatusSent)
	if err != nil {
		return 0, fmt.Errorf("purge: %w", err)
	}
	count, _ := result.RowsAffected()
	return int(count), nil
}

// Close closes the underlying database connection.
func (q *ClientQueue) Close() error {
	return q.db.Close()
}
