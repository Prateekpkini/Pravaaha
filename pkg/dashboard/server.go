// Package dashboard — server.go implements a minimal, read-only HTTP
// dashboard for the telemedicine server. It exposes:
//
//   - GET /           → HTML dashboard page
//   - GET /api/vitals → JSON array of recent vitals
//   - GET /api/images → JSON array of recent image records
//   - GET /api/stats  → JSON object with summary statistics
//   - GET /images/*   → Static file server for received images
//
// The dashboard auto-refreshes every 5 seconds to show incoming data
// in near-real-time.
package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/pravaaha/telemedicine-gateway/pkg/storage"
)

//go:embed templates/index.html
var templateFS embed.FS

// DashboardServer serves the read-only telemedicine dashboard.
type DashboardServer struct {
	db       *storage.ServerDB
	imageDir string
	port     int
}

// NewDashboardServer creates a new dashboard server.
func NewDashboardServer(db *storage.ServerDB, imageDir string, port int) *DashboardServer {
	return &DashboardServer{
		db:       db,
		imageDir: imageDir,
		port:     port,
	}
}

// Start begins serving the dashboard on the configured port.
// This blocks until the server is shut down.
func (d *DashboardServer) Start() error {
	mux := http.NewServeMux()

	// Serve the main HTML dashboard page.
	mux.HandleFunc("/", d.handleIndex)

	// JSON API endpoints for the dashboard to poll.
	mux.HandleFunc("/api/vitals", d.handleVitals)
	mux.HandleFunc("/api/images", d.handleImages)
	mux.HandleFunc("/api/stats", d.handleStats)

	// Static file server for received images.
	imageFS := http.FileServer(http.Dir(d.imageDir))
	mux.Handle("/images/", http.StripPrefix("/images/", imageFS))

	addr := fmt.Sprintf(":%d", d.port)
	log.Printf("[dashboard] serving on http://0.0.0.0%s", addr)

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	return server.ListenAndServe()
}

// handleIndex serves the embedded HTML dashboard.
func (d *DashboardServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := templateFS.ReadFile("templates/index.html")
	if err != nil {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// vitalsJSON is the JSON representation of a vitals record for the API.
type vitalsJSON struct {
	PatientID  string  `json:"patient_id"`
	Timestamp  string  `json:"timestamp"`
	HeartRate  int     `json:"heart_rate"`
	SpO2       int     `json:"spo2"`
	SysBP      int     `json:"sys_bp"`
	DiaBP      int     `json:"dia_bp"`
	TempC      float64 `json:"temp_c"`
	ReceivedAt string  `json:"received_at"`
}

// handleVitals returns recent vitals as JSON.
func (d *DashboardServer) handleVitals(w http.ResponseWriter, r *http.Request) {
	records, err := d.db.GetRecentVitals(50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var result []vitalsJSON
	for _, rec := range records {
		result = append(result, vitalsJSON{
			PatientID:  rec.PatientID,
			Timestamp:  time.Unix(rec.Timestamp, 0).UTC().Format(time.RFC3339),
			HeartRate:  rec.HeartRate,
			SpO2:       rec.SpO2,
			SysBP:      rec.SysBP,
			DiaBP:      rec.DiaBP,
			TempC:      rec.TempC,
			ReceivedAt: time.Unix(rec.ReceivedAt, 0).UTC().Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(result)
}

// imageJSON is the JSON representation of an image record.
type imageJSON struct {
	PatientID  string `json:"patient_id"`
	ImageID    string `json:"image_id"`
	FileName   string `json:"file_name"`
	FileSize   int64  `json:"file_size"`
	URL        string `json:"url"`
	ReceivedAt string `json:"received_at"`
}

// handleImages returns recent image records as JSON.
func (d *DashboardServer) handleImages(w http.ResponseWriter, r *http.Request) {
	records, err := d.db.GetRecentImages(20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var result []imageJSON
	for _, rec := range records {
		fileName := filepath.Base(rec.FilePath)
		result = append(result, imageJSON{
			PatientID:  rec.PatientID,
			ImageID:    rec.ImageID,
			FileName:   fileName,
			FileSize:   rec.FileSize,
			URL:        "/images/" + fileName,
			ReceivedAt: time.Unix(rec.ReceivedAt, 0).UTC().Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(result)
}

// statsJSON is the summary statistics for the dashboard.
type statsJSON struct {
	TotalVitals int    `json:"total_vitals"`
	TotalImages int    `json:"total_images"`
	ServerTime  string `json:"server_time"`
	Uptime      string `json:"uptime"`
}

var startTime = time.Now()

// handleStats returns summary statistics as JSON.
func (d *DashboardServer) handleStats(w http.ResponseWriter, r *http.Request) {
	vCount, _ := d.db.GetVitalsCount()
	iCount, _ := d.db.GetImagesCount()

	uptime := time.Since(startTime).Round(time.Second)

	stats := statsJSON{
		TotalVitals: vCount,
		TotalImages: iCount,
		ServerTime:  time.Now().UTC().Format(time.RFC3339),
		Uptime:      uptime.String(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(stats)
}
