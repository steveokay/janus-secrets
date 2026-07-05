package audit

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"time"
)

const domainTag = "janus:audit:v1"

// genesisPrevHash is the prev_hash of the first event (seq 1).
func genesisPrevHash() []byte { return make([]byte, 32) }

// computeHash returns the SHA-256 chain hash of one event. Every string field
// is length-prefixed; nullable fields carry a presence byte so NULL and "" do
// not collide. seq and the timestamp are fixed 8-byte big-endian; the timestamp
// is unix-NANOSECONDS (per spec). The Recorder truncates occurred_at to the
// microsecond before both hashing and storing, so a value read back from a
// Postgres timestamptz (microsecond precision) re-hashes to the same bytes —
// verify would otherwise fail on the lost sub-microsecond nanos.
func computeHash(prevHash []byte, seq int64, occurredAt time.Time,
	actorKind string, actorID *string, actorName, action, resource string,
	detail *string, result string, resultCode *string, ip string) []byte {
	h := sha256.New()
	h.Write([]byte(domainTag))
	h.Write(prevHash)
	writeInt64(h, seq)
	writeInt64(h, occurredAt.UnixNano())
	writeStr(h, actorKind)
	writeNullable(h, actorID)
	writeStr(h, actorName)
	writeStr(h, action)
	writeStr(h, resource)
	writeNullable(h, detail)
	writeStr(h, result)
	writeNullable(h, resultCode)
	writeStr(h, ip)
	return h.Sum(nil)
}

func writeInt64(h hash.Hash, v int64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v)) // #nosec G115 -- bit reinterpretation of a signed value, intentional
	_, _ = h.Write(b[:])
}

func writeStr(h hash.Hash, s string) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(len(s))) // #nosec G115 -- audit field lengths are far below 2^32
	_, _ = h.Write(b[:])
	_, _ = h.Write([]byte(s))
}

func writeNullable(h hash.Hash, s *string) {
	if s == nil {
		_, _ = h.Write([]byte{0x00})
		return
	}
	_, _ = h.Write([]byte{0x01})
	writeStr(h, *s)
}
