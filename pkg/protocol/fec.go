// Package protocol — fec.go implements Forward Error Correction using
// Reed-Solomon erasure coding. For every 4 data shards, 1 parity shard
// is generated. This allows recovery of any single lost packet within
// a group without requiring a retransmission round-trip.
//
// At 20% packet loss, the probability of losing 2+ packets in a 5-packet
// group is ~5.12%, meaning ~95% of groups self-heal automatically.
package protocol

import (
	"fmt"

	"github.com/klauspost/reedsolomon"
)

// FECEncoder wraps a Reed-Solomon encoder configured for 4 data + 1 parity.
type FECEncoder struct {
	enc reedsolomon.Encoder
}

// NewFECEncoder creates a new Reed-Solomon encoder for the telemedicine
// protocol's 4+1 configuration.
func NewFECEncoder() (*FECEncoder, error) {
	enc, err := reedsolomon.New(DataShardsPerGroup, ParityShardsPerGroup)
	if err != nil {
		return nil, fmt.Errorf("reedsolomon.New: %w", err)
	}
	return &FECEncoder{enc: enc}, nil
}

// EncodeGroup takes up to 4 data payloads and produces 5 shards
// (4 data + 1 parity). All shards are padded to the same length
// as required by Reed-Solomon.
//
// Parameters:
//   - dataPayloads: slice of 1-4 data payloads (last group may have < 4)
//
// Returns:
//   - shards: 5 equal-length byte slices (data[0..3] + parity[4])
//   - shardSize: the padded shard size (needed to strip padding on decode)
func (f *FECEncoder) EncodeGroup(dataPayloads [][]byte) (shards [][]byte, shardSize int, err error) {
	if len(dataPayloads) == 0 || len(dataPayloads) > DataShardsPerGroup {
		return nil, 0, fmt.Errorf("need 1-%d data payloads, got %d", DataShardsPerGroup, len(dataPayloads))
	}

	// Find the maximum payload size in this group to determine shard size.
	maxLen := 0
	for _, p := range dataPayloads {
		if len(p) > maxLen {
			maxLen = len(p)
		}
	}
	if maxLen == 0 {
		maxLen = 1 // Reed-Solomon needs at least 1 byte
	}

	// Build the shard array: 4 data + 1 parity, all padded to maxLen.
	shards = make([][]byte, TotalShardsPerGroup)
	for i := 0; i < DataShardsPerGroup; i++ {
		shards[i] = make([]byte, maxLen)
		if i < len(dataPayloads) {
			copy(shards[i], dataPayloads[i])
			// Remaining bytes stay zero (padding)
		}
		// Empty slots (when group has < 4 payloads) stay all-zero
	}
	// Allocate the parity shard
	shards[DataShardsPerGroup] = make([]byte, maxLen)

	// Compute parity
	if err := f.enc.Encode(shards); err != nil {
		return nil, 0, fmt.Errorf("reed-solomon encode: %w", err)
	}

	return shards, maxLen, nil
}

// FECDecoder wraps a Reed-Solomon encoder used for reconstruction.
type FECDecoder struct {
	enc reedsolomon.Encoder
}

// NewFECDecoder creates a new decoder (uses the same RS codec internally).
func NewFECDecoder() (*FECDecoder, error) {
	enc, err := reedsolomon.New(DataShardsPerGroup, ParityShardsPerGroup)
	if err != nil {
		return nil, fmt.Errorf("reedsolomon.New: %w", err)
	}
	return &FECDecoder{enc: enc}, nil
}

// DecodeGroup attempts to reconstruct missing shards from the available ones.
//
// Parameters:
//   - shards: array of 5 shard slots. Missing/lost shards must be nil.
//   - shardSize: the expected shard size (from EncodeGroup)
//
// Returns:
//   - the reconstructed data shards (indices 0..3), or error if
//     too many shards are missing for recovery.
func (f *FECDecoder) DecodeGroup(shards [][]byte, shardSize int) ([][]byte, error) {
	if len(shards) != TotalShardsPerGroup {
		return nil, fmt.Errorf("need exactly %d shard slots, got %d", TotalShardsPerGroup, len(shards))
	}

	// Count missing shards
	missing := 0
	for _, s := range shards {
		if s == nil {
			missing++
		}
	}

	// With 1 parity shard, we can recover at most 1 missing shard
	if missing > ParityShardsPerGroup {
		return nil, fmt.Errorf("too many missing shards: %d (max recoverable: %d)", missing, ParityShardsPerGroup)
	}

	// If nothing is missing, no reconstruction needed
	if missing == 0 {
		return shards[:DataShardsPerGroup], nil
	}

	// Reconstruct the missing shard(s)
	if err := f.enc.Reconstruct(shards); err != nil {
		return nil, fmt.Errorf("reed-solomon reconstruct: %w", err)
	}

	// Verify integrity after reconstruction
	ok, err := f.enc.Verify(shards)
	if err != nil {
		return nil, fmt.Errorf("reed-solomon verify: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("reed-solomon verification failed after reconstruction")
	}

	return shards[:DataShardsPerGroup], nil
}
