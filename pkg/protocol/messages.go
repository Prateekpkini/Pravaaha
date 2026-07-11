// Package protocol defines the binary message types and CBOR serialization
// for the Low-Bandwidth Telemedicine Gateway.
//
// All messages are wrapped in a MessageEnvelope with a 1-byte type discriminator
// followed by the CBOR-encoded payload. This keeps wire overhead minimal while
// allowing the receiver to dispatch without full deserialization.
package protocol

import (
	"fmt"
	"time"

	"github.com/fxamacker/cbor/v2"
)

// -----------------------------------------------------------------------
// Message type discriminators — first byte of every UDP datagram.
// -----------------------------------------------------------------------

const (
	TypeVitals    uint8 = 1 // Client → Server: patient vitals
	TypeChunk     uint8 = 2 // Client → Server: image chunk (data or parity)
	TypeAck       uint8 = 3 // Server → Client: FEC-group or vitals acknowledged
	TypeNack      uint8 = 4 // Server → Client: unrecoverable FEC group
	TypeVitalsAck uint8 = 5 // Server → Client: vitals receipt confirmed
)

// -----------------------------------------------------------------------
// Vitals — compact patient telemetry record.
// CBOR-encoded size is typically ~30-40 bytes.
// -----------------------------------------------------------------------

// Vitals represents a single snapshot of a patient's vital signs.
// All numeric fields use the smallest integer type that covers the
// physiological range, keeping the serialized size tiny.
type Vitals struct {
	PatientID string  `cbor:"1,keyasint"` // Unique patient identifier
	Timestamp int64   `cbor:"2,keyasint"` // Unix epoch seconds
	HeartRate uint8   `cbor:"3,keyasint"` // Beats per minute (30-220 bpm)
	SpO2      uint8   `cbor:"4,keyasint"` // Blood oxygen saturation % (0-100)
	SysBP     uint8   `cbor:"5,keyasint"` // Systolic blood pressure mmHg (0-255)
	DiaBP     uint8   `cbor:"6,keyasint"` // Diastolic blood pressure mmHg (0-255)
	TempC     float32 `cbor:"7,keyasint"` // Body temperature in °C
}

// NewVitals creates a Vitals with the current timestamp.
func NewVitals(patientID string, hr, spo2, sys, dia uint8, temp float32) *Vitals {
	return &Vitals{
		PatientID: patientID,
		Timestamp: time.Now().Unix(),
		HeartRate: hr,
		SpO2:      spo2,
		SysBP:     sys,
		DiaBP:     dia,
		TempC:     temp,
	}
}

// -----------------------------------------------------------------------
// ChunkPacket — a single image data or parity chunk.
// The payload is at most MaxPayloadSize bytes.
// -----------------------------------------------------------------------

const (
	// MaxPayloadSize is the maximum image data per UDP chunk.
	// 512 bytes keeps the total datagram well under the 576-byte minimum MTU
	// after adding the ChunkHeader + envelope overhead (~50 bytes).
	MaxPayloadSize = 512

	// DataShardsPerGroup is the number of data packets per FEC group.
	DataShardsPerGroup = 4

	// ParityShardsPerGroup is the number of parity packets per FEC group.
	ParityShardsPerGroup = 1

	// TotalShardsPerGroup = DataShardsPerGroup + ParityShardsPerGroup
	TotalShardsPerGroup = DataShardsPerGroup + ParityShardsPerGroup
)

// ChunkPacket carries a single image fragment (data or parity).
type ChunkPacket struct {
	PatientID   string `cbor:"1,keyasint"` // Patient this image belongs to
	ImageID     string `cbor:"2,keyasint"` // UUID identifying the full image
	TotalChunks uint16 `cbor:"3,keyasint"` // Total data chunks (excl. parity)
	ChunkIndex  uint16 `cbor:"4,keyasint"` // Index of this chunk (0-based)
	GroupID     uint16 `cbor:"5,keyasint"` // FEC group this chunk belongs to
	IsParity    bool   `cbor:"6,keyasint"` // true if this is a parity shard
	ShardIndex  uint8  `cbor:"7,keyasint"` // Index within the FEC group (0-4)
	PayloadLen  uint16 `cbor:"8,keyasint"` // Original payload length before padding
	Payload     []byte `cbor:"9,keyasint"` // Image data or parity bytes
}

// -----------------------------------------------------------------------
// AckMsg — server acknowledges receipt of a complete FEC group.
// -----------------------------------------------------------------------

// AckMsg is sent from the server back to the client to confirm
// that an FEC group (or vitals message) was received successfully.
type AckMsg struct {
	ImageID string `cbor:"1,keyasint"` // Which image this ACK relates to
	GroupID uint16 `cbor:"2,keyasint"` // Which FEC group was completed
	Status  uint8  `cbor:"3,keyasint"` // 0 = OK, 1 = image fully received
}

// ACK status constants.
const (
	AckStatusGroupOK    uint8 = 0
	AckStatusImageDone  uint8 = 1
)

// -----------------------------------------------------------------------
// NackMsg — server requests retransmission of unrecoverable chunks.
// -----------------------------------------------------------------------

// NackMsg is sent when FEC cannot recover a group. It lists the
// specific shard indices that are missing so the client can
// retransmit only what is needed.
type NackMsg struct {
	ImageID       string  `cbor:"1,keyasint"` // Which image
	GroupID       uint16  `cbor:"2,keyasint"` // Which FEC group failed
	MissingShards []uint8 `cbor:"3,keyasint"` // Shard indices needed (0-4)
}

// -----------------------------------------------------------------------
// VitalsAck — server confirms receipt of a vitals record.
// -----------------------------------------------------------------------

// VitalsAck confirms that the server persisted a vitals record.
type VitalsAck struct {
	PatientID string `cbor:"1,keyasint"`
	Timestamp int64  `cbor:"2,keyasint"`
	Status    uint8  `cbor:"3,keyasint"` // 0 = OK
}

// -----------------------------------------------------------------------
// MessageEnvelope — wire format for all UDP datagrams.
// Layout: [1 byte type] [CBOR payload]
// -----------------------------------------------------------------------

// Encode serializes any message into a wire-ready datagram.
// First byte is the message type, remainder is CBOR-encoded payload.
func Encode(msgType uint8, payload interface{}) ([]byte, error) {
	data, err := cbor.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("cbor marshal: %w", err)
	}
	// Prepend the type byte
	buf := make([]byte, 1+len(data))
	buf[0] = msgType
	copy(buf[1:], data)
	return buf, nil
}

// Decode extracts the message type and deserializes the CBOR payload
// into the provided target struct.
func Decode(data []byte, target interface{}) (uint8, error) {
	if len(data) < 2 {
		return 0, fmt.Errorf("datagram too short: %d bytes", len(data))
	}
	msgType := data[0]
	if err := cbor.Unmarshal(data[1:], target); err != nil {
		return msgType, fmt.Errorf("cbor unmarshal (type=%d): %w", msgType, err)
	}
	return msgType, nil
}

// DecodeType extracts just the message type from a raw datagram
// without deserializing the payload. Useful for dispatch routing.
func DecodeType(data []byte) (uint8, error) {
	if len(data) < 1 {
		return 0, fmt.Errorf("empty datagram")
	}
	return data[0], nil
}
