// cmd/client/main.go — Entry point for the Pravaaha Telemedicine Client.
//
// The client runs on a field worker's low-power device. It provides:
//   - CLI commands to input vitals and queue images
//   - A local SQLite store-and-forward queue for offline resilience
//   - A background worker that transmits queued data via the custom UDP protocol
//   - Mock data generation for testing
//
// Usage:
//
//	pravaaha-client [flags] <command>
//
// Commands:
//
//	mock          Generate and queue mock vitals + test image
//	send-vitals   Queue a vitals record (interactive or via flags)
//	send-image    Queue an image file for transmission
//	status        Show queue statistics
//
// Flags:
//
//	-server    Server address (default: 127.0.0.1)
//	-port      Server UDP port (default: 9000)
//	-db        Local queue database path (default: pravaaha_client.db)
package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math/big"
	"bytes"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/google/uuid"

	"github.com/pravaaha/telemedicine-gateway/pkg/protocol"
	"github.com/pravaaha/telemedicine-gateway/pkg/storage"
)

func main() {
	// ── Parse flags ──
	serverHost := flag.String("server", "127.0.0.1", "Server address")
	serverPort := flag.Int("port", 9000, "Server UDP port")
	dbPath := flag.String("db", "pravaaha_client.db", "Local queue database path")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	command := args[0]

	// ── Initialize the local queue ──
	queue, err := storage.NewClientQueue(*dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize queue: %v", err)
	}
	defer queue.Close()

	// Reset any items stuck in SENDING state from a previous crash.
	queue.ResetStale(30 * time.Second)

	// ── Execute the command ──
	switch command {
	case "mock":
		runMock(queue)
	case "send-vitals":
		runSendVitals(queue, args[1:])
	case "send-image":
		runSendImage(queue, args[1:])
	case "status":
		runStatus(queue)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}

	// ── Start the background sender ──
	log.Println("Starting background sender...")
	sender, err := protocol.NewUDPSender(*serverHost, *serverPort)
	if err != nil {
		log.Fatalf("Failed to create UDP sender: %v", err)
	}
	defer sender.Close()

	// Sender worker goroutine — continuously drains the queue.
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	go senderWorker(queue, sender, stopCh, doneCh)

	// ── Graceful shutdown ──
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	close(stopCh)
	<-doneCh
	log.Println("Goodbye.")
}

// printUsage displays the help text.
func printUsage() {
	fmt.Println(`
╔══════════════════════════════════════════════════════════╗
║     Pravaaha — Low-Bandwidth Telemedicine Client        ║
╚══════════════════════════════════════════════════════════╝

Usage: pravaaha-client [flags] <command>

Commands:
  mock          Generate and queue mock vitals + test image
  send-vitals   Queue a vitals record via flags
  send-image    Queue an image file for transmission
  status        Show queue statistics

Flags:
  -server string   Server address (default "127.0.0.1")
  -port   int      Server UDP port (default 9000)
  -db     string   Local queue database path (default "pravaaha_client.db")

Examples:
  pravaaha-client mock
  pravaaha-client send-image /path/to/xray.jpg
  pravaaha-client status`)
}

// -----------------------------------------------------------------------
// Command: mock — generate test data
// -----------------------------------------------------------------------

func runMock(queue *storage.ClientQueue) {
	log.Println("Generating mock data...")

	patientIDs := []string{"P-1001", "P-1002", "P-1003", "P-1004", "P-1005"}

	// Generate 10 mock vitals records with realistic ranges.
	for i := 0; i < 10; i++ {
		pid := patientIDs[i%len(patientIDs)]
		v := protocol.Vitals{
			PatientID: pid,
			Timestamp: time.Now().Add(time.Duration(-i) * time.Minute).Unix(),
			HeartRate: uint8(randRange(55, 110)),
			SpO2:      uint8(randRange(88, 100)),
			SysBP:     uint8(randRange(90, 180)),
			DiaBP:     uint8(randRange(55, 105)),
			TempC:     float32(randRange(360, 395)) / 10.0,
		}

		data, err := cbor.Marshal(&v)
		if err != nil {
			log.Printf("Failed to encode vitals: %v", err)
			continue
		}

		if _, err := queue.Enqueue("vitals", data, ""); err != nil {
			log.Printf("Failed to enqueue vitals: %v", err)
			continue
		}
		log.Printf("  Queued vitals: patient=%s HR=%d SpO2=%d BP=%d/%d T=%.1f°C",
			v.PatientID, v.HeartRate, v.SpO2, v.SysBP, v.DiaBP, v.TempC)
	}

	// Generate a small synthetic PNG test image (~5KB).
	imgID := uuid.New().String()
	imgData := generateTestImage()
	if _, err := queue.Enqueue("image", imgData, imgID); err != nil {
		log.Printf("Failed to enqueue test image: %v", err)
	} else {
		log.Printf("  Queued test image: id=%s size=%d bytes", imgID, len(imgData))
	}

	log.Println("Mock data generation complete.")
}

// generateTestImage creates a small synthetic PNG image for testing.
// It produces a gradient pattern with a red cross overlay (~5KB).
func generateTestImage() []byte {
	const w, h = 128, 128
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Blue-to-teal gradient background.
			r := uint8(x * 80 / w)
			g := uint8(100 + y*100/h)
			b := uint8(180 + x*75/w)

			// Draw a red medical cross in the center.
			cx, cy := x-w/2, y-h/2
			if (abs(cx) < 8 && abs(cy) < 30) || (abs(cx) < 30 && abs(cy) < 8) {
				r, g, b = 220, 40, 40
			}

			img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		log.Printf("Failed to encode PNG: %v", err)
		// Fallback: return some random bytes.
		fallback := make([]byte, 5000)
		rand.Read(fallback)
		return fallback
	}
	return buf.Bytes()
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// randRange returns a cryptographically random int in [min, max].
func randRange(min, max int) int {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	if err != nil {
		return min
	}
	return min + int(n.Int64())
}

// -----------------------------------------------------------------------
// Command: send-vitals — queue a vitals record
// -----------------------------------------------------------------------

func runSendVitals(queue *storage.ClientQueue, args []string) {
	fs := flag.NewFlagSet("send-vitals", flag.ExitOnError)
	pid := fs.String("patient", "P-0001", "Patient ID")
	hr := fs.Int("hr", 72, "Heart rate (bpm)")
	spo2 := fs.Int("spo2", 98, "SpO2 (%)")
	sys := fs.Int("sys", 120, "Systolic BP (mmHg)")
	dia := fs.Int("dia", 80, "Diastolic BP (mmHg)")
	temp := fs.Float64("temp", 36.6, "Temperature (°C)")
	fs.Parse(args)

	v := protocol.Vitals{
		PatientID: *pid,
		Timestamp: time.Now().Unix(),
		HeartRate: uint8(*hr),
		SpO2:      uint8(*spo2),
		SysBP:     uint8(*sys),
		DiaBP:     uint8(*dia),
		TempC:     float32(*temp),
	}

	data, err := cbor.Marshal(&v)
	if err != nil {
		log.Fatalf("Failed to encode vitals: %v", err)
	}

	id, err := queue.Enqueue("vitals", data, "")
	if err != nil {
		log.Fatalf("Failed to enqueue: %v", err)
	}

	log.Printf("Queued vitals (id=%d): patient=%s HR=%d SpO2=%d BP=%d/%d T=%.1f°C",
		id, v.PatientID, v.HeartRate, v.SpO2, v.SysBP, v.DiaBP, v.TempC)
}

// -----------------------------------------------------------------------
// Command: send-image — queue an image file
// -----------------------------------------------------------------------

func runSendImage(queue *storage.ClientQueue, args []string) {
	if len(args) == 0 {
		log.Fatal("Usage: send-image <filepath> [-patient P-0001]")
	}

	filePath := args[0]

	// Optional patient flag.
	fs := flag.NewFlagSet("send-image", flag.ExitOnError)
	pid := fs.String("patient", "P-0001", "Patient ID")
	if len(args) > 1 {
		fs.Parse(args[1:])
	}

	// Read the image file.
	imgData, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Failed to read image: %v", err)
	}

	// We store the patient ID as a prefix in the image ID for downstream routing.
	imgID := uuid.New().String()

	// Store the image data directly as payload (the sender will chunk it).
	// We prepend the patient ID so the sender knows who it belongs to.
	envelope := struct {
		PatientID string `cbor:"1,keyasint"`
		ImageID   string `cbor:"2,keyasint"`
		Data      []byte `cbor:"3,keyasint"`
	}{
		PatientID: *pid,
		ImageID:   imgID,
		Data:      imgData,
	}

	data, err := cbor.Marshal(&envelope)
	if err != nil {
		log.Fatalf("Failed to encode image envelope: %v", err)
	}

	id, err := queue.Enqueue("image", data, imgID)
	if err != nil {
		log.Fatalf("Failed to enqueue: %v", err)
	}

	log.Printf("Queued image (id=%d): file=%s image_id=%s patient=%s size=%d bytes",
		id, filepath.Base(filePath), imgID, *pid, len(imgData))
}

// -----------------------------------------------------------------------
// Command: status — show queue statistics
// -----------------------------------------------------------------------

func runStatus(queue *storage.ClientQueue) {
	pending, sending, sent, failed, err := queue.GetStats()
	if err != nil {
		log.Fatalf("Failed to get stats: %v", err)
	}

	fmt.Println(`
╔══════════════════════════════════════════════╗
║         Pravaaha Client Queue Status         ║
╠══════════════════════════════════════════════╣`)
	fmt.Printf("║  ⏳ Pending:  %-28d  ║\n", pending)
	fmt.Printf("║  📤 Sending:  %-28d  ║\n", sending)
	fmt.Printf("║  ✅ Sent:     %-28d  ║\n", sent)
	fmt.Printf("║  ❌ Failed:   %-28d  ║\n", failed)
	fmt.Printf("║  📊 Total:    %-28d  ║\n", pending+sending+sent+failed)
	fmt.Println("╚══════════════════════════════════════════════╝")
}

// -----------------------------------------------------------------------
// Background sender worker
// -----------------------------------------------------------------------

// senderWorker continuously drains the queue and transmits items via UDP.
func senderWorker(queue *storage.ClientQueue, sender *protocol.UDPSender, stopCh, doneCh chan struct{}) {
	defer close(doneCh)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			// Try to send the next queued item.
			item, err := queue.DequeueNext()
			if err != nil {
				log.Printf("[sender-worker] dequeue error: %v", err)
				continue
			}
			if item == nil {
				continue // Queue empty
			}

			log.Printf("[sender-worker] processing item %d (type=%s)", item.ID, item.Type)

			switch item.Type {
			case "vitals":
				err = sendVitalsItem(sender, item)
			case "image":
				err = sendImageItem(sender, item)
			default:
				log.Printf("[sender-worker] unknown item type: %s", item.Type)
				err = fmt.Errorf("unknown type: %s", item.Type)
			}

			if err != nil {
				log.Printf("[sender-worker] send failed for item %d: %v", item.ID, err)
				queue.MarkFailed(item.ID, 5)
			} else {
				queue.MarkSent(item.ID)
			}
		}
	}
}

// sendVitalsItem deserializes and transmits a vitals record.
func sendVitalsItem(sender *protocol.UDPSender, item *storage.QueueItem) error {
	var v protocol.Vitals
	if err := cbor.Unmarshal(item.Payload, &v); err != nil {
		return fmt.Errorf("unmarshal vitals: %w", err)
	}
	return sender.SendVitals(&v)
}

// sendImageItem deserializes and transmits an image.
func sendImageItem(sender *protocol.UDPSender, item *storage.QueueItem) error {
	// Decode the image envelope.
	var envelope struct {
		PatientID string `cbor:"1,keyasint"`
		ImageID   string `cbor:"2,keyasint"`
		Data      []byte `cbor:"3,keyasint"`
	}
	if err := cbor.Unmarshal(item.Payload, &envelope); err != nil {
		return fmt.Errorf("unmarshal image envelope: %w", err)
	}

	return sender.SendImage(envelope.Data, envelope.ImageID, envelope.PatientID)
}
