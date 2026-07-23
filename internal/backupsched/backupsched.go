package backupsched

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/steveokay/janus-secrets/internal/store"
)

// backupExt is the object suffix; matches the on-demand backup's .jsonl format.
const backupExt = ".jsonl"

// dumper produces the key-preserving backup artifact. *store.Store satisfies it
// via WriteBackup; tests substitute a fake.
type dumper interface {
	WriteBackup(ctx context.Context, w io.Writer, janusVersion string) error
}

// runRepo records each backup attempt (value-free). *store.BackupRunRepo
// satisfies it; tests substitute a fake.
type runRepo interface {
	InsertRun(ctx context.Context, in store.BackupRunInput) error
}

// verifier checks that a downloaded backup artifact restores — WITHOUT touching
// the live instance. Implemented by rehearseVerify (validates header + row
// structure + wrapped-material decryptability against the current keyring), but
// abstracted so the engine stays testable without a full crypto stack.
type verifier interface {
	Verify(ctx context.Context, r io.Reader) (RehearsalResult, error)
}

// Config is the resolved scheduled-S3-backup configuration.
type Config struct {
	S3 S3Config
	// Tick is the scheduler interval. Zero disables the scheduler entirely.
	Tick time.Duration
	// Retention keeps the N most recent objects under the prefix after each
	// successful upload; older ones are pruned. Zero/negative disables pruning.
	Retention int
	// Version is the janus build version stamped into the backup header.
	Version string
}

// Enabled reports whether the scheduled-backup engine is configured to run
// (a bucket and a positive tick).
func (c Config) Enabled() bool {
	return c.Tick > 0 && strings.TrimSpace(c.S3.Bucket) != ""
}

// Service is the scheduled-S3-backup engine.
type Service struct {
	cfg      Config
	dump     dumper
	runs     runRepo
	logger   *slog.Logger
	sealed   func() bool
	verify   verifier
	now      func() time.Time
	tickHook func()

	// newClient builds an s3API from the config (overridable in tests).
	newClient func(ctx context.Context, c S3Config) (s3API, error)
}

// New wires the engine against the real store + a sealed-check.
func New(cfg Config, st *store.Store, sealed func() bool, v verifier, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		cfg:       cfg,
		dump:      st,
		runs:      store.NewBackupRunRepo(st),
		logger:    logger,
		sealed:    sealed,
		verify:    v,
		now:       func() time.Time { return time.Now().UTC() },
		newClient: defaultS3Client,
	}
}

// SetTickHook registers a callback invoked once per scheduler tick (used to
// stamp the shared scheduler last-tick tracker).
func (s *Service) SetTickHook(h func()) { s.tickHook = h }

// objectKey builds the timestamped destination key under the prefix.
func (s *Service) objectKey(t time.Time) string {
	name := "janus-backup-" + t.Format("20060102T150405Z") + backupExt
	prefix := strings.Trim(s.cfg.S3.Prefix, "/")
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

// prefixDir returns the list prefix (trailing slash), or "" for the root.
func (s *Service) prefixDir() string {
	prefix := strings.Trim(s.cfg.S3.Prefix, "/")
	if prefix == "" {
		return ""
	}
	return prefix + "/"
}

// RunBackup produces one backup artifact, uploads it to S3, applies retention,
// and records the attempt (success or failure) in backup_runs. It is a no-op
// while sealed (the dump needs no key material, but a sealed instance is
// mid-lifecycle; skip rather than race). Every failure path is sanitized: the
// recorded error is a category string, never an SDK message that could carry a
// bucket name, ARN, or account id, and creds are never logged.
func (s *Service) RunBackup(ctx context.Context) error {
	if s.sealed != nil && s.sealed() {
		return nil
	}
	started := s.now()

	// Produce the artifact in memory. The dump is wrapped material only; a Janus
	// instance's full logical dump is modest and this keeps upload atomic (S3
	// PutObject needs a seekable/sized body). WriteBackup pre-flights the schema.
	var buf bytes.Buffer
	if err := s.dump.WriteBackup(ctx, &buf, s.cfg.Version); err != nil {
		s.recordFailure(ctx, started, "dump_failed")
		s.logger.Warn("scheduled backup dump failed", "err", err)
		return fmt.Errorf("backup dump: %w", err)
	}
	size := int64(buf.Len())

	// Note: S3-boundary errors are NEVER logged with their raw SDK message —
	// that text can carry a bucket name, ARN, or account id. Only the sanitized
	// category is logged/recorded (mirrors the sync providers' hygiene).
	cl, err := s.newClient(ctx, s.cfg.S3)
	if err != nil {
		s.recordFailure(ctx, started, "client_failed")
		s.logger.Warn("scheduled backup s3 client init failed", "reason", "client_failed")
		return errors.New("backup: s3 client init failed")
	}

	key := s.objectKey(started)
	if _, err := cl.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.cfg.S3.Bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(buf.Bytes()),
		ContentLength: aws.Int64(size),
		ContentType:   aws.String("application/x-ndjson"),
	}); err != nil {
		s.recordFailure(ctx, started, "upload_failed")
		s.logger.Warn("scheduled backup upload failed", "reason", "upload_failed")
		return errors.New("backup: upload failed")
	}

	// Retention: prune older objects, best-effort. A prune failure does NOT fail
	// the backup — the upload already succeeded — but is logged (sanitized).
	pruned, perr := s.prune(ctx, cl)
	if perr != nil {
		s.logger.Warn("scheduled backup retention prune failed", "reason", "prune_failed")
	} else if pruned > 0 {
		s.logger.Info("scheduled backup pruned old objects", "count", pruned)
	}

	// Record success. object_key + size are value-free (path + byte count).
	if err := s.runs.InsertRun(ctx, store.BackupRunInput{
		StartedAt:  started,
		FinishedAt: s.now(),
		Status:     "success",
		ObjectKey:  &key,
		SizeBytes:  &size,
	}); err != nil {
		s.logger.Warn("scheduled backup run record failed (upload succeeded)", "err", err)
		return fmt.Errorf("backup: record run: %w", err)
	}
	s.logger.Info("scheduled backup uploaded", "key", key, "bytes", size)
	return nil
}

// recordFailure inserts a failure run with a sanitized category, swallowing (but
// logging) a record error so the caller's original failure surfaces.
func (s *Service) recordFailure(ctx context.Context, started time.Time, category string) {
	cat := category
	if err := s.runs.InsertRun(ctx, store.BackupRunInput{
		StartedAt:  started,
		FinishedAt: s.now(),
		Status:     "failure",
		Error:      &cat,
	}); err != nil {
		s.logger.Warn("scheduled backup failure-run record failed", "err", err)
	}
}

// listBackups lists Janus backup objects under the prefix, newest-first by the
// timestamp embedded in the key (LastModified is authoritative but the key's
// timestamp is monotonic with it and needs no extra field). Only objects whose
// name matches the janus-backup-*.jsonl shape are considered, so unrelated
// objects sharing the prefix are never pruned.
func (s *Service) listBackups(ctx context.Context, cl s3API) ([]string, error) {
	dir := s.prefixDir()
	var keys []string
	var token *string
	for {
		out, err := cl.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.cfg.S3.Bucket),
			Prefix:            aws.String(dir),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, errors.New("list objects failed")
		}
		for _, o := range out.Contents {
			if o.Key == nil {
				continue
			}
			base := *o.Key
			if i := strings.LastIndex(base, "/"); i >= 0 {
				base = base[i+1:]
			}
			if strings.HasPrefix(base, "janus-backup-") && strings.HasSuffix(base, backupExt) {
				keys = append(keys, *o.Key)
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		token = out.NextContinuationToken
	}
	// Sort newest-first. The key's UTC timestamp sorts lexicographically in the
	// same order as chronologically, so a plain reverse string sort works.
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	return keys, nil
}

// prune keeps the Retention most recent backup objects under the prefix and
// deletes the rest. Retention<=0 disables pruning. Returns the number deleted.
func (s *Service) prune(ctx context.Context, cl s3API) (int, error) {
	if s.cfg.Retention <= 0 {
		return 0, nil
	}
	keys, err := s.listBackups(ctx, cl)
	if err != nil {
		return 0, err
	}
	if len(keys) <= s.cfg.Retention {
		return 0, nil
	}
	deleted := 0
	for _, k := range keys[s.cfg.Retention:] {
		if _, err := cl.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.cfg.S3.Bucket),
			Key:    aws.String(k),
		}); err != nil {
			return deleted, errors.New("delete object failed")
		}
		deleted++
	}
	return deleted, nil
}

// RunScheduler ticks every Config.Tick and runs a backup until ctx is done.
// A zero tick disables the scheduler (returns immediately). Ties to the server
// shutdown context so it stops cleanly on SIGTERM.
func (s *Service) RunScheduler(ctx context.Context) {
	if s.cfg.Tick <= 0 {
		return
	}
	t := time.NewTicker(s.cfg.Tick)
	defer t.Stop()
	s.logger.Info("backup scheduler started", "tick", s.cfg.Tick.String(),
		"bucket", s.cfg.S3.Bucket, "retention", s.cfg.Retention)
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("backup scheduler stopping")
			return
		case <-t.C:
			if s.tickHook != nil {
				s.tickHook()
			}
			if err := s.RunBackup(ctx); err != nil {
				// RunBackup already recorded + logged; keep the loop alive.
				s.logger.Warn("scheduled backup attempt failed", "err", err)
			}
		}
	}
}
