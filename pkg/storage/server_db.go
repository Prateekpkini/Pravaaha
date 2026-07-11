// Package storage — server_db.go implements the server-side SQLite database
// for persisting received vitals and image metadata.
//
// The server stores vitals as structured records and images as files on disk,
// with metadata (patient ID, image ID, file path) tracked in SQLite.
package storage

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

// -----------------------------------------------------------------------
// Data types for the server database.
// -----------------------------------------------------------------------

// VitalsRecord represents a stored vitals record with server-side metadata.
type VitalsRecord struct {
	ID         int64
	PatientID  string
	Timestamp  int64
	HeartRate  int
	SpO2       int
	SysBP      int
	DiaBP      int
	TempC      float64
	ReceivedAt int64
}

// ImageRecord represents metadata for a received image.
type ImageRecord struct {
	ID         int64
	PatientID  string
	ImageID    string
	FilePath   string
	FileSize   int64
	ReceivedAt int64
}

// -----------------------------------------------------------------------
// ServerDB — SQLite database for the hospital server.
// -----------------------------------------------------------------------

// ServerDB manages the server-side storage of received patient data.
type ServerDB struct {
	db *sql.DB
}

// NewServerDB opens (or creates) the server SQLite database.
func NewServerDB(dbPath string) (*ServerDB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Enable WAL for better concurrent access (dashboard reads + UDP writes).
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	// Create tables.
	createSQL := `
	CREATE TABLE IF NOT EXISTS vitals (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		patient_id  TEXT    NOT NULL,
		timestamp   INTEGER NOT NULL,
		heart_rate  INTEGER,
		spo2        INTEGER,
		sys_bp      INTEGER,
		dia_bp      INTEGER,
		temp_c      REAL,
		received_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_vitals_patient ON vitals(patient_id);
	CREATE INDEX IF NOT EXISTS idx_vitals_received ON vitals(received_at);

	CREATE TABLE IF NOT EXISTS images (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		patient_id  TEXT    NOT NULL,
		image_id    TEXT    UNIQUE NOT NULL,
		file_path   TEXT    NOT NULL,
		file_size   INTEGER DEFAULT 0,
		received_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_images_patient ON images(patient_id);
	CREATE INDEX IF NOT EXISTS idx_images_received ON images(received_at);
	`
	if _, err := db.Exec(createSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("create tables: %w", err)
	}

	log.Printf("[server-db] initialized at %s", dbPath)
	return &ServerDB{db: db}, nil
}

// SaveVitals inserts a new vitals record into the database.
func (s *ServerDB) SaveVitals(patientID string, timestamp int64, hr, spo2, sys, dia int, tempC float64) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT INTO vitals (patient_id, timestamp, heart_rate, spo2, sys_bp, dia_bp, temp_c, received_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		patientID, timestamp, hr, spo2, sys, dia, tempC, now,
	)
	if err != nil {
		return fmt.Errorf("save vitals: %w", err)
	}
	log.Printf("[server-db] saved vitals for patient %s", patientID)
	return nil
}

// SaveImage inserts image metadata into the database.
func (s *ServerDB) SaveImage(patientID, imageID, filePath string, fileSize int64) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO images (patient_id, image_id, file_path, file_size, received_at)
		 VALUES (?, ?, ?, ?, ?)`,
		patientID, imageID, filePath, fileSize, now,
	)
	if err != nil {
		return fmt.Errorf("save image: %w", err)
	}
	log.Printf("[server-db] saved image metadata: patient=%s image=%s path=%s", patientID, imageID, filePath)
	return nil
}

// GetRecentVitals retrieves the most recent vitals records.
func (s *ServerDB) GetRecentVitals(limit int) ([]VitalsRecord, error) {
	rows, err := s.db.Query(
		`SELECT id, patient_id, timestamp, heart_rate, spo2, sys_bp, dia_bp, temp_c, received_at
		 FROM vitals ORDER BY received_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get vitals: %w", err)
	}
	defer rows.Close()

	var records []VitalsRecord
	for rows.Next() {
		var r VitalsRecord
		if err := rows.Scan(&r.ID, &r.PatientID, &r.Timestamp, &r.HeartRate,
			&r.SpO2, &r.SysBP, &r.DiaBP, &r.TempC, &r.ReceivedAt); err != nil {
			return nil, fmt.Errorf("scan vitals: %w", err)
		}
		records = append(records, r)
	}
	return records, nil
}

// GetRecentImages retrieves the most recent image records.
func (s *ServerDB) GetRecentImages(limit int) ([]ImageRecord, error) {
	rows, err := s.db.Query(
		`SELECT id, patient_id, image_id, file_path, file_size, received_at
		 FROM images ORDER BY received_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get images: %w", err)
	}
	defer rows.Close()

	var records []ImageRecord
	for rows.Next() {
		var r ImageRecord
		if err := rows.Scan(&r.ID, &r.PatientID, &r.ImageID, &r.FilePath,
			&r.FileSize, &r.ReceivedAt); err != nil {
			return nil, fmt.Errorf("scan images: %w", err)
		}
		records = append(records, r)
	}
	return records, nil
}

// GetVitalsCount returns the total number of vitals records.
func (s *ServerDB) GetVitalsCount() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM vitals`).Scan(&count)
	return count, err
}

// GetImagesCount returns the total number of image records.
func (s *ServerDB) GetImagesCount() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM images`).Scan(&count)
	return count, err
}

// Close closes the database connection.
func (s *ServerDB) Close() error {
	return s.db.Close()
}
