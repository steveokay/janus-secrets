package backupsched

import (
	"context"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// RehearsalResult is the value-free outcome of a restore-rehearsal: whether the
// artifact verified, which object was checked, and structural counts. It carries
// no key material, no ciphertext, and no plaintext — only paths, counts, and a
// human note.
type RehearsalResult struct {
	ObjectKey   string `json:"object_key"`
	Verified    bool   `json:"verified"`
	Records     int    `json:"records"`
	Tables      int    `json:"tables"`
	SizeBytes   int64  `json:"size_bytes"`
	SchemaVer   int64  `json:"schema_version"`
	Decryptable bool   `json:"decryptable"`
	Note        string `json:"note"`
}

// ErrNoBackups is returned by Rehearse when no backup object exists to verify.
var ErrNoBackups = errors.New("backupsched: no backup objects found under prefix")

// Rehearse downloads a backup from S3 and verifies it restores WITHOUT touching
// the live instance. It never writes to the database and never mutates any live
// state: the artifact is streamed through the verifier (header + row-structure
// validation, plus best-effort decryptability of the wrapped material against
// the current unseal) and then discarded. When key == "" the latest backup under
// the prefix is used. All error paths are sanitized (no SDK message that could
// carry a bucket/ARN/account id).
func (s *Service) Rehearse(ctx context.Context, key string) (RehearsalResult, error) {
	cl, err := s.newClient(ctx, s.cfg.S3)
	if err != nil {
		return RehearsalResult{}, errors.New("rehearse: s3 client init failed")
	}
	if key == "" {
		keys, err := s.listBackups(ctx, cl)
		if err != nil {
			return RehearsalResult{}, errors.New("rehearse: list objects failed")
		}
		if len(keys) == 0 {
			return RehearsalResult{}, ErrNoBackups
		}
		key = keys[0] // newest-first
	} else if strings.Contains(key, "..") {
		// Defensive: a rehearsal target is an object key, never a path with
		// traversal. (S3 keys are opaque, but reject the obvious footgun.)
		return RehearsalResult{}, errors.New("rehearse: invalid object key")
	}

	out, err := cl.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.S3.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return RehearsalResult{}, errors.New("rehearse: download failed")
	}
	defer drainAndClose(out.Body)

	res, err := s.verify.Verify(ctx, out.Body)
	if err != nil {
		return RehearsalResult{ObjectKey: key}, err
	}
	res.ObjectKey = key
	if out.ContentLength != nil {
		res.SizeBytes = *out.ContentLength
	}
	return res, nil
}
