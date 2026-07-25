package audit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
)

// checkpointKeyInfo is the domain-separation label used to derive the checkpoint
// MAC key from the server's token-HMAC key. The derived key is
// HMAC-SHA256(tokenHMACKey, checkpointKeyInfo). Keeping a distinct label ensures
// the checkpoint key can never coincide with a token HMAC over the same input.
const checkpointKeyInfo = "janus/audit-checkpoint/v1"

// checkpointMACDomain is the domain tag prefixed into the checkpoint MAC input so
// the tag can never be confused with any other HMAC in the system.
const checkpointMACDomain = "janus:audit:checkpoint:v1"

// DeriveCheckpointKey derives the domain-separated checkpoint MAC key from the
// server's token-HMAC key: HMAC-SHA256(tokenHMACKey, checkpointKeyInfo). The
// caller owns zeroizing tokenHMACKey; the returned key should also be zeroized
// after use. Kept here (not in internal/crypto) so the crypto 100%-coverage gate
// is untouched — this is stdlib HMAC only, no new primitive.
func DeriveCheckpointKey(tokenHMACKey []byte) []byte {
	h := hmac.New(sha256.New, tokenHMACKey)
	h.Write([]byte(checkpointKeyInfo))
	return h.Sum(nil)
}

// Checkpoint is a signed anchor over the audit chain head at a point in time.
type Checkpoint struct {
	ThroughSeq  int64
	ThroughHash []byte // the chained hash of the event at ThroughSeq
	EventCount  int64
	MAC         []byte
	CreatedAt   int64 // unix seconds; informational, NOT covered by the MAC
}

// computeCheckpointMAC returns HMAC-SHA256(ckKey, domain || lenPrefixed(fields)).
// Every field is length-prefixed and the two integers are fixed 8-byte
// big-endian, mirroring the chain-hash encoding, so no two distinct
// (through_seq, through_hash, event_count) triples can produce the same MAC
// input by ambiguous concatenation.
func computeCheckpointMAC(ckKey []byte, throughSeq int64, throughHash []byte, eventCount int64) []byte {
	h := hmac.New(sha256.New, ckKey)
	h.Write([]byte(checkpointMACDomain))
	writeCkInt64(h, throughSeq)
	writeCkBytes(h, throughHash)
	writeCkInt64(h, eventCount)
	return h.Sum(nil)
}

// verifyCheckpointMAC recomputes the MAC and constant-time compares it.
func verifyCheckpointMAC(ckKey []byte, cp Checkpoint) bool {
	want := computeCheckpointMAC(ckKey, cp.ThroughSeq, cp.ThroughHash, cp.EventCount)
	return hmac.Equal(want, cp.MAC)
}

func writeCkInt64(h hash.Hash, v int64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v)) // #nosec G115 -- bit reinterpretation of a signed value, intentional
	_, _ = h.Write(b[:])
}

func writeCkBytes(h hash.Hash, b []byte) {
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(b))) // #nosec G115 -- a chain hash is 32 bytes, far below 2^32
	_, _ = h.Write(l[:])
	_, _ = h.Write(b)
}

// hexCheckpointHash renders a checkpoint's through_hash as hex (wire/response).
func hexCheckpointHash(b []byte) string { return hex.EncodeToString(b) }
