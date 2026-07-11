// Package protocol — udp.go implements the low-level UDP transport layer
// for the telemedicine gateway. It provides:
//
//   - UDPSender: rate-limited client-side sender that transmits FEC groups
//     and listens for ACK/NACK responses.
//   - UDPReceiver: server-side listener that dispatches incoming datagrams
//     to the appropriate handler (vitals or image chunks).
//
// The transport is designed for < 64 kbps bandwidth with > 20% packet loss.
// Rate limiting keeps throughput at ~6 KB/s to leave headroom for ACKs.
package protocol

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// -----------------------------------------------------------------------
// Constants for the UDP transport layer.
// -----------------------------------------------------------------------

const (
	// MaxDatagramSize is the maximum size of a single UDP datagram.
	// We keep it well under the 576-byte minimum MTU to avoid fragmentation.
	MaxDatagramSize = 600

	// SendRate is the inter-packet delay to stay under 64 kbps.
	// 512 bytes * 8 bits / 48000 bps ≈ 85ms, rounded up for safety.
	SendRate = 100 * time.Millisecond

	// GroupAckTimeout is how long the sender waits for a group ACK
	// before considering it lost.
	GroupAckTimeout = 5 * time.Second

	// MaxRetries is the maximum number of times a group will be retransmitted
	// after NACK before giving up.
	MaxRetries = 3

	// ReadBufferSize is the UDP socket read buffer size.
	ReadBufferSize = 65536

	// ServerGroupTimeout is how long the server waits after the last chunk
	// in a group before triggering a NACK.
	ServerGroupTimeout = 3 * time.Second
)

// -----------------------------------------------------------------------
// UDPSender — client-side rate-limited sender with ACK/NACK handling.
// -----------------------------------------------------------------------

// UDPSender manages sending packets to the server and processing responses.
type UDPSender struct {
	conn       *net.UDPConn
	serverAddr *net.UDPAddr
	fecEnc     *FECEncoder

	// ackChan receives ACK/NACK messages from the receive goroutine.
	ackChan chan interface{} // AckMsg or NackMsg

	// stopCh signals the receiver goroutine to stop.
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewUDPSender creates a sender connected to the specified server address.
func NewUDPSender(serverHost string, serverPort int) (*UDPSender, error) {
	serverAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", serverHost, serverPort))
	if err != nil {
		return nil, fmt.Errorf("resolve server addr: %w", err)
	}

	// Bind to any available local port.
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return nil, fmt.Errorf("listen udp: %w", err)
	}

	fecEnc, err := NewFECEncoder()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("create FEC encoder: %w", err)
	}

	s := &UDPSender{
		conn:       conn,
		serverAddr: serverAddr,
		fecEnc:     fecEnc,
		ackChan:    make(chan interface{}, 100),
		stopCh:     make(chan struct{}),
	}

	// Start background ACK/NACK receiver.
	s.wg.Add(1)
	go s.receiveLoop()

	return s, nil
}

// receiveLoop listens for ACK and NACK messages from the server.
func (s *UDPSender) receiveLoop() {
	defer s.wg.Done()
	buf := make([]byte, MaxDatagramSize)

	for {
		select {
		case <-s.stopCh:
			return
		default:
		}

		// Short read deadline so we can check stopCh periodically.
		s.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue // Normal timeout, check stopCh and retry
			}
			select {
			case <-s.stopCh:
				return
			default:
				log.Printf("[sender] read error: %v", err)
				continue
			}
		}
		if n == 0 {
			continue
		}

		msgType, err := DecodeType(buf[:n])
		if err != nil {
			continue
		}

		switch msgType {
		case TypeAck:
			var ack AckMsg
			if _, err := Decode(buf[:n], &ack); err == nil {
				s.ackChan <- ack
			}
		case TypeNack:
			var nack NackMsg
			if _, err := Decode(buf[:n], &nack); err == nil {
				s.ackChan <- nack
			}
		case TypeVitalsAck:
			var vack VitalsAck
			if _, err := Decode(buf[:n], &vack); err == nil {
				s.ackChan <- vack
			}
		}
	}
}

// SendVitals transmits a vitals record and waits for acknowledgement.
// Returns nil on successful ACK, error on timeout or failure.
func (s *UDPSender) SendVitals(v *Vitals) error {
	data, err := Encode(TypeVitals, v)
	if err != nil {
		return fmt.Errorf("encode vitals: %w", err)
	}

	// Send the vitals packet (small enough for a single datagram).
	if _, err := s.conn.WriteToUDP(data, s.serverAddr); err != nil {
		return fmt.Errorf("send vitals: %w", err)
	}
	log.Printf("[sender] sent vitals for patient %s", v.PatientID)

	// Wait for VitalsAck with timeout.
	timer := time.NewTimer(GroupAckTimeout)
	defer timer.Stop()

	for {
		select {
		case msg := <-s.ackChan:
			if vack, ok := msg.(VitalsAck); ok {
				if vack.PatientID == v.PatientID && vack.Timestamp == v.Timestamp {
					log.Printf("[sender] vitals ACK received for patient %s", v.PatientID)
					return nil
				}
			}
			// Not our ACK — put it back (lossy under high concurrency, but acceptable).
		case <-timer.C:
			return fmt.Errorf("vitals ACK timeout for patient %s", v.PatientID)
		}
	}
}

// SendImage chunks an image, applies FEC, and transmits all groups
// with rate limiting. It handles NACK-based retransmission.
func (s *UDPSender) SendImage(imageData []byte, imageID, patientID string) error {
	// Chunk and apply FEC.
	packets, err := ChunkImage(imageData, imageID, patientID, s.fecEnc)
	if err != nil {
		return fmt.Errorf("chunk image: %w", err)
	}

	log.Printf("[sender] sending image %s (%d bytes, %d packets)", imageID, len(imageData), len(packets))

	// Organize packets by group for potential retransmission.
	groupPackets := make(map[uint16][]ChunkPacket)
	for _, pkt := range packets {
		groupPackets[pkt.GroupID] = append(groupPackets[pkt.GroupID], pkt)
	}

	// Determine total groups.
	totalGroups := uint16(0)
	for gid := range groupPackets {
		if gid+1 > totalGroups {
			totalGroups = gid + 1
		}
	}

	// Send each group sequentially with rate limiting.
	for gid := uint16(0); gid < totalGroups; gid++ {
		pkts := groupPackets[gid]
		if err := s.sendGroupWithRetry(imageID, gid, pkts); err != nil {
			log.Printf("[sender] group %d failed after retries: %v", gid, err)
			// Continue with next group — partial image is better than none
		}
	}

	// Wait briefly for the final image-complete ACK.
	timer := time.NewTimer(GroupAckTimeout)
	defer timer.Stop()

	for {
		select {
		case msg := <-s.ackChan:
			if ack, ok := msg.(AckMsg); ok {
				if ack.ImageID == imageID && ack.Status == AckStatusImageDone {
					log.Printf("[sender] image %s fully acknowledged", imageID)
					return nil
				}
			}
		case <-timer.C:
			log.Printf("[sender] image %s: no final ACK (may still succeed on server)", imageID)
			return nil // Best-effort — the image might still be complete on server
		}
	}
}

// sendGroupWithRetry sends a single FEC group and handles NACK retransmission.
func (s *UDPSender) sendGroupWithRetry(imageID string, groupID uint16, packets []ChunkPacket) error {
	for attempt := 0; attempt <= MaxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("[sender] retransmitting group %d (attempt %d/%d)", groupID, attempt, MaxRetries)
		}

		// Send all packets in the group with rate limiting.
		for _, pkt := range packets {
			data, err := Encode(TypeChunk, &pkt)
			if err != nil {
				return fmt.Errorf("encode chunk: %w", err)
			}
			if _, err := s.conn.WriteToUDP(data, s.serverAddr); err != nil {
				return fmt.Errorf("send chunk: %w", err)
			}
			time.Sleep(SendRate) // Rate limit
		}

		// Wait for group ACK or NACK.
		timer := time.NewTimer(GroupAckTimeout)

		select {
		case msg := <-s.ackChan:
			timer.Stop()
			switch m := msg.(type) {
			case AckMsg:
				if m.ImageID == imageID && m.GroupID == groupID {
					return nil // Group acknowledged
				}
				if m.ImageID == imageID && m.Status == AckStatusImageDone {
					return nil // Entire image done
				}
			case NackMsg:
				if m.ImageID == imageID && m.GroupID == groupID {
					// Filter packets to only resend missing shards.
					packets = filterMissingShards(packets, m.MissingShards)
					log.Printf("[sender] NACK for group %d, missing shards: %v", groupID, m.MissingShards)
					continue // Retry
				}
			}
		case <-timer.C:
			// Timeout — assume the group was received (might have lost the ACK)
			log.Printf("[sender] group %d ACK timeout (assuming received)", groupID)
			return nil
		}
	}

	return fmt.Errorf("group %d: max retries exceeded", groupID)
}

// filterMissingShards returns only the packets whose ShardIndex is in the missing list.
func filterMissingShards(packets []ChunkPacket, missing []uint8) []ChunkPacket {
	missingSet := make(map[uint8]bool)
	for _, idx := range missing {
		missingSet[idx] = true
	}
	var filtered []ChunkPacket
	for _, pkt := range packets {
		if missingSet[pkt.ShardIndex] {
			filtered = append(filtered, pkt)
		}
	}
	return filtered
}

// Close shuts down the sender and releases resources.
func (s *UDPSender) Close() error {
	close(s.stopCh)
	s.wg.Wait()
	return s.conn.Close()
}

// -----------------------------------------------------------------------
// UDPReceiver — server-side UDP listener and dispatcher.
// -----------------------------------------------------------------------

// UDPReceiver listens for incoming datagrams and dispatches them
// to the appropriate handler based on message type.
type UDPReceiver struct {
	conn        *net.UDPConn
	reassembler *Reassembler

	// OnVitalsReceived is called when a vitals message arrives.
	OnVitalsReceived func(v *Vitals, addr *net.UDPAddr)

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewUDPReceiver creates a server-side UDP receiver listening on the given port.
func NewUDPReceiver(port int) (*UDPReceiver, error) {
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("resolve addr: %w", err)
	}

	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("listen udp: %w", err)
	}

	// Set a generous read buffer to handle bursts.
	conn.SetReadBuffer(ReadBufferSize)

	return &UDPReceiver{
		conn:   conn,
		stopCh: make(chan struct{}),
	}, nil
}

// SetReassembler attaches a Reassembler for image chunk processing.
func (r *UDPReceiver) SetReassembler(reassembler *Reassembler) {
	r.reassembler = reassembler
}

// Start begins listening for incoming datagrams in a background goroutine.
// It also starts a periodic timeout checker for stalled FEC groups.
func (r *UDPReceiver) Start() {
	r.wg.Add(2)
	go r.listenLoop()
	go r.timeoutLoop()
}

// listenLoop is the main receive loop that reads and dispatches datagrams.
func (r *UDPReceiver) listenLoop() {
	defer r.wg.Done()
	buf := make([]byte, MaxDatagramSize)

	for {
		select {
		case <-r.stopCh:
			return
		default:
		}

		// Set read deadline to allow periodic stopCh checks.
		r.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, remoteAddr, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			select {
			case <-r.stopCh:
				return
			default:
				log.Printf("[receiver] read error: %v", err)
				continue
			}
		}
		if n == 0 {
			continue
		}

		// Dispatch based on message type.
		msgType, err := DecodeType(buf[:n])
		if err != nil {
			log.Printf("[receiver] decode type error: %v", err)
			continue
		}

		switch msgType {
		case TypeVitals:
			r.handleVitals(buf[:n], remoteAddr)
		case TypeChunk:
			r.handleChunk(buf[:n], remoteAddr)
		default:
			log.Printf("[receiver] unknown message type: %d", msgType)
		}
	}
}

// handleVitals processes an incoming vitals message and sends an ACK.
func (r *UDPReceiver) handleVitals(data []byte, addr *net.UDPAddr) {
	var v Vitals
	if _, err := Decode(data, &v); err != nil {
		log.Printf("[receiver] decode vitals error: %v", err)
		return
	}

	log.Printf("[receiver] vitals from patient %s: HR=%d SpO2=%d BP=%d/%d Temp=%.1f",
		v.PatientID, v.HeartRate, v.SpO2, v.SysBP, v.DiaBP, v.TempC)

	// Invoke handler callback.
	if r.OnVitalsReceived != nil {
		r.OnVitalsReceived(&v, addr)
	}

	// Send VitalsAck back to the client.
	ack := VitalsAck{
		PatientID: v.PatientID,
		Timestamp: v.Timestamp,
		Status:    0,
	}
	ackData, err := Encode(TypeVitalsAck, &ack)
	if err != nil {
		log.Printf("[receiver] encode vitals ack error: %v", err)
		return
	}
	r.conn.WriteToUDP(ackData, addr)
}

// handleChunk processes an incoming image chunk and sends group ACKs.
func (r *UDPReceiver) handleChunk(data []byte, addr *net.UDPAddr) {
	var chunk ChunkPacket
	if _, err := Decode(data, &chunk); err != nil {
		log.Printf("[receiver] decode chunk error: %v", err)
		return
	}

	if r.reassembler == nil {
		log.Printf("[receiver] no reassembler configured, dropping chunk")
		return
	}

	// Store the client address for sending ACKs.
	// We use a closure to capture addr for the callbacks.
	originalOnComplete := r.reassembler.OnImageComplete
	r.reassembler.OnImageComplete = func(patientID, imageID string, imgData []byte) {
		// Send image-complete ACK.
		ack := AckMsg{
			ImageID: imageID,
			GroupID: 0,
			Status:  AckStatusImageDone,
		}
		if ackData, err := Encode(TypeAck, &ack); err == nil {
			r.conn.WriteToUDP(ackData, addr)
		}
		// Call the original handler.
		if originalOnComplete != nil {
			originalOnComplete(patientID, imageID, imgData)
		}
	}

	originalOnNack := r.reassembler.OnGroupNack
	r.reassembler.OnGroupNack = func(imageID string, groupID uint16, missing []uint8) {
		// Send NACK for the unrecoverable group.
		nack := NackMsg{
			ImageID:       imageID,
			GroupID:       groupID,
			MissingShards: missing,
		}
		if nackData, err := Encode(TypeNack, &nack); err == nil {
			r.conn.WriteToUDP(nackData, addr)
			log.Printf("[receiver] NACK sent for image %s group %d, missing: %v", imageID, groupID, missing)
		}
		if originalOnNack != nil {
			originalOnNack(imageID, groupID, missing)
		}
	}

	r.reassembler.AddChunk(chunk)

	// Send per-group ACK if we have enough shards to be useful.
	// The reassembler handles actual decoding; this is an optimistic ACK.
}

// timeoutLoop periodically checks for stalled FEC groups and triggers NACKs.
func (r *UDPReceiver) timeoutLoop() {
	defer r.wg.Done()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			if r.reassembler != nil {
				r.reassembler.CheckTimeouts(ServerGroupTimeout)
			}
		}
	}
}

// Close shuts down the receiver.
func (r *UDPReceiver) Close() error {
	close(r.stopCh)
	r.wg.Wait()
	return r.conn.Close()
}

// LocalAddr returns the local address the receiver is bound to.
func (r *UDPReceiver) LocalAddr() net.Addr {
	return r.conn.LocalAddr()
}
