// Package backupsched is Janus's scheduled encrypted-backup engine: on a
// configured interval it runs the existing key-preserving instance dump and
// uploads the resulting sealed artifact to S3-compatible object storage, applies
// retention (keep N most recent, prune the rest), records each attempt in
// backup_runs (value-free), and offers a restore-rehearsal that verifies the
// latest (or a named) backup restores WITHOUT touching the live instance.
//
// The uploaded object is byte-identical to what GET /v1/sys/backup produces: a
// key-preserving logical dump in which wrapped KEKs and ciphertext stay wrapped.
// It contains no plaintext secret and no unseal material, and is useless without
// the origin's Shamir shares / KMS key. Operators must still protect the bucket.
package backupsched

import (
	"context"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// s3API is the subset of the S3 SDK client this engine uses. *s3.Client
// satisfies it; tests substitute a fake so no live AWS call is ever made.
type s3API interface {
	PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObject(ctx context.Context, in *s3.DeleteObjectInput, opts ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// S3Config is the operator-supplied S3 destination + credentials. Static creds
// only: a backup destination's identity is explicit and never silently borrows
// the host's ambient AWS identity. Endpoint is optional (empty ⇒ real AWS S3);
// set it for S3-compatible stores (MinIO, Cloudflare R2, Backblaze B2, …).
type S3Config struct {
	Bucket          string
	Prefix          string
	Region          string
	Endpoint        string // optional: custom BaseEndpoint for S3-compatible storage
	AccessKeyID     string
	SecretAccessKey string
}

// defaultS3Client builds an S3 client from STATIC credentials only, honoring an
// optional custom endpoint (MinIO/R2/etc.) via BaseEndpoint + path-style
// addressing (compatible with most non-AWS stores).
func defaultS3Client(ctx context.Context, c S3Config) (s3API, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(c.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			c.AccessKeyID, c.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
			o.UsePathStyle = true
		}
	}), nil
}

// drainAndClose fully drains r then closes it (S3 SDK response bodies want both
// to release the connection). Errors are ignored: the caller already has the
// data or the failure it cares about.
func drainAndClose(r io.ReadCloser) {
	if r == nil {
		return
	}
	_, _ = io.Copy(io.Discard, r)
	_ = r.Close()
}
