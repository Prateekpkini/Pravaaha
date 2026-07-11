// cmd/server/main.go — Entry point for the Pravaaha Telemedicine Server.
//
// The server runs at the specialist hospital. It:
//   - Listens for incoming patient data on a UDP port (default :9000)
//   - Buffers and reassembles image chunks using FEC recovery
//   - Persists vitals to a local SQLite database
//   - Saves received images to a local directory (/data/images)
//   - Serves a read-only HTTP dashboard on port 8080
//
// Usage:
//
//	pravaaha-server [flags]
//
// Flags:
//
//	-udp-port   UDP listening port (default: 9000)
//	-http-port  HTTP dashboard port (default: 8080)
//	-db         Database path (default: pravaaha_server.db)
//	-image-dir  Directory for received images (default: data/images)
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/pravaaha/telemedicine-gateway/pkg/dashboard"
	"github.com/pravaaha/telemedicine-gateway/pkg/protocol"
	"github.com/pravaaha/telemedicine-gateway/pkg/storage"
)

func main() {
	// ── Parse flags ──
	udpPort := flag.Int("udp-port", 9000, "UDP listening port")
	httpPort := flag.Int("http-port", 8080, "HTTP dashboard port")
	dbPath := flag.String("db", "pravaaha_server.db", "Database path")
	imageDir := flag.String("image-dir", "data/images", "Directory for received images")
	flag.Parse()

	fmt.Println(`
╔══════════════════════════════════════════════════════════╗
║     Pravaaha — Low-Bandwidth Telemedicine Server        ║
║     UDP + Reed-Solomon FEC · <64 kbps · >20% Loss       ║
╚══════════════════════════════════════════════════════════╝`)

	// ── Create image directory ──
	if err := os.MkdirAll(*imageDir, 0755); err != nil {
		log.Fatalf("Failed to create image directory: %v", err)
	}
	absImageDir, _ := filepath.Abs(*imageDir)
	log.Printf("Image directory: %s", absImageDir)

	// ── Initialize server database ──
	db, err := storage.NewServerDB(*dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// ── Create the image reassembler with callbacks ──
	reassembler, err := protocol.NewReassembler(
		// OnImageComplete — save the reassembled image to disk and database.
		func(patientID, imageID string, data []byte) {
			fileName := fmt.Sprintf("%s_%s.png", patientID, imageID[:8])
			filePath := filepath.Join(*imageDir, fileName)

			if err := os.WriteFile(filePath, data, 0644); err != nil {
				log.Printf("[server] failed to save image: %v", err)
				return
			}

			if err := db.SaveImage(patientID, imageID, filePath, int64(len(data))); err != nil {
				log.Printf("[server] failed to save image metadata: %v", err)
				return
			}

			log.Printf("[server] ✅ Image reassembled and saved: patient=%s id=%s size=%d path=%s",
				patientID, imageID, len(data), filePath)
		},
		// OnGroupNack — log the NACK (actual NACK packet is sent by UDPReceiver).
		func(imageID string, groupID uint16, missingShards []uint8) {
			log.Printf("[server] ❌ NACK: image=%s group=%d missing=%v", imageID, groupID, missingShards)
		},
	)
	if err != nil {
		log.Fatalf("Failed to create reassembler: %v", err)
	}

	// ── Start the UDP receiver ──
	receiver, err := protocol.NewUDPReceiver(*udpPort)
	if err != nil {
		log.Fatalf("Failed to create UDP receiver: %v", err)
	}
	defer receiver.Close()

	receiver.SetReassembler(reassembler)

	// Handle incoming vitals — save to database.
	receiver.OnVitalsReceived = func(v *protocol.Vitals, addr *net.UDPAddr) {
		if err := db.SaveVitals(
			v.PatientID, v.Timestamp,
			int(v.HeartRate), int(v.SpO2),
			int(v.SysBP), int(v.DiaBP),
			float64(v.TempC),
		); err != nil {
			log.Printf("[server] failed to save vitals: %v", err)
		}
	}

	receiver.Start()
	log.Printf("UDP receiver listening on :%d", *udpPort)

	// ── Start the HTTP dashboard ──
	dash := dashboard.NewDashboardServer(db, *imageDir, *httpPort)
	go func() {
		if err := dash.Start(); err != nil {
			log.Printf("[dashboard] HTTP server error: %v", err)
		}
	}()

	// ── Wait for shutdown signal ──
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Println("Server is running. Press Ctrl+C to stop.")
	<-sigCh

	log.Println("Shutting down...")
	log.Println("Goodbye.")
}
