// Package protocol — chunker.go handles splitting images into 512-byte
// chunks for UDP transmission and reassembling them on the server side.
//
// The chunker organizes data chunks into FEC groups of 4, generating parity
// shards via the FEC encoder. The reassembler buffers incoming chunks,
// applies FEC recovery, and emits the complete image once all groups are
// reconstructed.
package protocol

import (
	"fmt"
	"sync"
	"time"
)

// -----------------------------------------------------------------------
// Chunker — splits an image into FEC-protected chunk groups.
// -----------------------------------------------------------------------

// ChunkImage splits raw image data into ChunkPackets organized into FEC groups.
// For every 4 data chunks, 1 parity chunk is added (5 packets per group).
//
// Parameters:
//   - imageData: raw image bytes
//   - imageID:   unique identifier for this image transfer
//   - patientID: patient this image belongs to
//
// Returns a slice of ChunkPackets ready for transmission, ordered by group
// then by shard index within each group.
func ChunkImage(imageData []byte, imageID, patientID string, fecEnc *FECEncoder) ([]ChunkPacket, error) {
	if len(imageData) == 0 {
		return nil, fmt.Errorf("cannot chunk empty image data")
	}

	// Split raw data into MaxPayloadSize-byte pieces.
	var rawChunks [][]byte
	for offset := 0; offset < len(imageData); offset += MaxPayloadSize {
		end := offset + MaxPayloadSize
		if end > len(imageData) {
			end = len(imageData)
		}
		chunk := make([]byte, end-offset)
		copy(chunk, imageData[offset:end])
		rawChunks = append(rawChunks, chunk)
	}

	totalDataChunks := uint16(len(rawChunks))

	// Organize into FEC groups of DataShardsPerGroup (4) chunks each.
	var packets []ChunkPacket
	groupID := uint16(0)
	for i := 0; i < len(rawChunks); i += DataShardsPerGroup {
		end := i + DataShardsPerGroup
		if end > len(rawChunks) {
			end = len(rawChunks)
		}
		groupData := rawChunks[i:end]

		// Record original payload lengths before FEC padding.
		originalLens := make([]uint16, len(groupData))
		for j, d := range groupData {
			originalLens[j] = uint16(len(d))
		}

		// Generate FEC shards (pads internally to equal length).
		shards, _, err := fecEnc.EncodeGroup(groupData)
		if err != nil {
			return nil, fmt.Errorf("FEC encode group %d: %w", groupID, err)
		}

		// Create data chunk packets (shards 0..3).
		for j := 0; j < DataShardsPerGroup; j++ {
			pktPayloadLen := uint16(0)
			if j < len(originalLens) {
				pktPayloadLen = originalLens[j]
			}
			packets = append(packets, ChunkPacket{
				PatientID:   patientID,
				ImageID:     imageID,
				TotalChunks: totalDataChunks,
				ChunkIndex:  uint16(i + j),
				GroupID:     groupID,
				IsParity:    false,
				ShardIndex:  uint8(j),
				PayloadLen:  pktPayloadLen,
				Payload:     shards[j],
			})
		}

		// Create parity chunk packet (shard 4).
		packets = append(packets, ChunkPacket{
			PatientID:   patientID,
			ImageID:     imageID,
			TotalChunks: totalDataChunks,
			ChunkIndex:  0, // Not meaningful for parity
			GroupID:     groupID,
			IsParity:    true,
			ShardIndex:  uint8(DataShardsPerGroup),
			PayloadLen:  uint16(len(shards[DataShardsPerGroup])),
			Payload:     shards[DataShardsPerGroup],
		})

		groupID++
	}

	return packets, nil
}

// -----------------------------------------------------------------------
// Reassembler — buffers incoming chunks and reconstructs images.
// -----------------------------------------------------------------------

// groupState tracks the received shards for a single FEC group.
type groupState struct {
	shards      [TotalShardsPerGroup][]byte   // Received shard data (nil if missing)
	payloadLens [TotalShardsPerGroup]uint16    // Original payload lengths
	present     [TotalShardsPerGroup]bool      // Which shards have arrived
	received    int                            // Count of received shards
	shardSize   int                            // Size of the (padded) shards
	lastRecvAt  time.Time                      // Time of last received shard
}

// imageState tracks all groups for a single image transfer.
type imageState struct {
	patientID   string
	totalChunks uint16                   // Total data chunks expected
	totalGroups uint16                   // Total FEC groups expected
	groups      map[uint16]*groupState   // GroupID → state
	completed   map[uint16]bool          // Groups that have been decoded
	dataChunks  map[uint16][]byte        // ChunkIndex → decoded payload
	mu          sync.Mutex
	createdAt   time.Time
}

// Reassembler collects incoming ChunkPackets and reassembles complete images.
type Reassembler struct {
	images  map[string]*imageState // ImageID → state
	fecDec  *FECDecoder
	mu      sync.Mutex

	// Callback invoked when a complete image is reassembled.
	// Parameters: patientID, imageID, complete image bytes.
	OnImageComplete func(patientID, imageID string, data []byte)

	// Callback invoked when a group cannot be recovered by FEC.
	// Parameters: imageID, groupID, missing shard indices.
	OnGroupNack func(imageID string, groupID uint16, missingShards []uint8)
}

// NewReassembler creates a Reassembler with the provided callbacks.
func NewReassembler(
	onComplete func(patientID, imageID string, data []byte),
	onNack func(imageID string, groupID uint16, missingShards []uint8),
) (*Reassembler, error) {
	dec, err := NewFECDecoder()
	if err != nil {
		return nil, err
	}
	return &Reassembler{
		images:          make(map[string]*imageState),
		fecDec:          dec,
		OnImageComplete: onComplete,
		OnGroupNack:     onNack,
	}, nil
}

// AddChunk processes an incoming ChunkPacket. It buffers the shard,
// attempts FEC recovery when enough shards arrive, and triggers
// callbacks as appropriate.
func (r *Reassembler) AddChunk(pkt ChunkPacket) {
	r.mu.Lock()
	img, exists := r.images[pkt.ImageID]
	if !exists {
		// Calculate total groups from total data chunks.
		totalGroups := pkt.TotalChunks / DataShardsPerGroup
		if pkt.TotalChunks%DataShardsPerGroup != 0 {
			totalGroups++
		}
		img = &imageState{
			patientID:   pkt.PatientID,
			totalChunks: pkt.TotalChunks,
			totalGroups: totalGroups,
			groups:      make(map[uint16]*groupState),
			completed:   make(map[uint16]bool),
			dataChunks:  make(map[uint16][]byte),
			createdAt:   time.Now(),
		}
		r.images[pkt.ImageID] = img
	}
	r.mu.Unlock()

	img.mu.Lock()
	defer img.mu.Unlock()

	// Skip if this group is already fully decoded.
	if img.completed[pkt.GroupID] {
		return
	}

	// Get or create group state.
	grp, exists := img.groups[pkt.GroupID]
	if !exists {
		grp = &groupState{}
		img.groups[pkt.GroupID] = grp
	}

	// Record the shard.
	si := int(pkt.ShardIndex)
	if si >= TotalShardsPerGroup || grp.present[si] {
		return // Duplicate or invalid index
	}
	grp.shards[si] = pkt.Payload
	grp.payloadLens[si] = pkt.PayloadLen
	grp.present[si] = true
	grp.received++
	grp.lastRecvAt = time.Now()
	if len(pkt.Payload) > grp.shardSize {
		grp.shardSize = len(pkt.Payload)
	}

	// Attempt FEC decode once we have enough shards (≥ DataShardsPerGroup).
	if grp.received >= DataShardsPerGroup {
		r.tryDecodeGroup(img, pkt.ImageID, pkt.GroupID, grp)
	}
}

// tryDecodeGroup attempts to reconstruct missing shards via FEC.
func (r *Reassembler) tryDecodeGroup(img *imageState, imageID string, groupID uint16, grp *groupState) {
	// Build shard slice for the decoder.
	shards := make([][]byte, TotalShardsPerGroup)
	for i := 0; i < TotalShardsPerGroup; i++ {
		if grp.present[i] {
			shards[i] = grp.shards[i]
		} // nil means missing
	}

	decoded, err := r.fecDec.DecodeGroup(shards, grp.shardSize)
	if err != nil {
		// FEC cannot recover — collect missing shard indices for NACK.
		var missing []uint8
		for i := 0; i < TotalShardsPerGroup; i++ {
			if !grp.present[i] {
				missing = append(missing, uint8(i))
			}
		}
		if r.OnGroupNack != nil {
			r.OnGroupNack(imageID, groupID, missing)
		}
		return
	}

	// Store the recovered data chunks.
	img.completed[groupID] = true
	baseIndex := uint16(groupID) * DataShardsPerGroup
	for i := 0; i < DataShardsPerGroup; i++ {
		chunkIdx := baseIndex + uint16(i)
		if chunkIdx >= img.totalChunks {
			break // Last group may have fewer data chunks
		}
		// Use original payload length to strip FEC padding.
		payloadLen := grp.payloadLens[i]
		if payloadLen == 0 && i < len(decoded) {
			payloadLen = uint16(len(decoded[i]))
		}
		data := decoded[i]
		if int(payloadLen) < len(data) {
			data = data[:payloadLen]
		}
		img.dataChunks[chunkIdx] = data
	}

	// Check if the entire image is complete.
	if uint16(len(img.completed)) == img.totalGroups {
		r.assembleImage(img, imageID)
	}
}

// assembleImage concatenates all decoded chunks into the final image.
func (r *Reassembler) assembleImage(img *imageState, imageID string) {
	var totalSize int
	for i := uint16(0); i < img.totalChunks; i++ {
		totalSize += len(img.dataChunks[i])
	}
	result := make([]byte, 0, totalSize)
	for i := uint16(0); i < img.totalChunks; i++ {
		result = append(result, img.dataChunks[i]...)
	}

	if r.OnImageComplete != nil {
		r.OnImageComplete(img.patientID, imageID, result)
	}

	// Clean up state to free memory.
	r.mu.Lock()
	delete(r.images, imageID)
	r.mu.Unlock()
}

// CheckTimeouts scans all in-progress images for groups that have
// stalled (no new shard received within the timeout). For such groups,
// it triggers NACK callbacks so the client can retransmit.
func (r *Reassembler) CheckTimeouts(groupTimeout time.Duration) {
	r.mu.Lock()
	// Copy image IDs to avoid holding the lock during callbacks.
	imageIDs := make([]string, 0, len(r.images))
	for id := range r.images {
		imageIDs = append(imageIDs, id)
	}
	r.mu.Unlock()

	for _, imgID := range imageIDs {
		r.mu.Lock()
		img, exists := r.images[imgID]
		r.mu.Unlock()
		if !exists {
			continue
		}

		img.mu.Lock()
		for gID, grp := range img.groups {
			if img.completed[gID] {
				continue
			}
			if time.Since(grp.lastRecvAt) > groupTimeout && grp.received > 0 {
				// Group has stalled — send NACK for missing shards.
				var missing []uint8
				for i := 0; i < TotalShardsPerGroup; i++ {
					if !grp.present[i] {
						missing = append(missing, uint8(i))
					}
				}
				if r.OnGroupNack != nil && len(missing) > 0 {
					r.OnGroupNack(imgID, gID, missing)
				}
			}
		}
		img.mu.Unlock()
	}
}
