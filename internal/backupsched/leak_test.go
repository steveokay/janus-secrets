package backupsched

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestNoCredsOrSDKDetailInLogsOrErrors captures the engine's log output and the
// errors it returns across every failure path, and asserts they never contain
// the static S3 credentials nor the SDK's raw (bucket/ARN/account-bearing)
// error text. The recorded run's Error field is likewise a sanitized category.
func TestNoCredsOrSDKDetailInLogsOrErrors(t *testing.T) {
	const (
		secretKey = "SUPER-SECRET-KEY-fixture-9f3a"
		accessKey = "AKIA-FIXTURE-0000"
		sdkLeak   = "arn:aws:iam::123456789012:user/backup AccessDenied bucket=janus-fake"
	)
	var logbuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logbuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg := baseCfg()
	cfg.S3.AccessKeyID = accessKey
	cfg.S3.SecretAccessKey = secretKey

	// Exercise upload failure (SDK error carries the leaky string).
	cl := newFakeS3()
	cl.putErr = errors.New(sdkLeak)
	runs := &fakeRuns{}
	s := &Service{
		cfg: cfg, dump: fakeDump{body: fixtureBackup}, runs: runs, logger: logger,
		sealed:    func() bool { return false },
		now:       func() time.Time { return time.Unix(0, 0).UTC() },
		newClient: func(_ context.Context, _ S3Config) (s3API, error) { return cl, nil },
	}
	var errs []string
	if err := s.RunBackup(context.Background()); err != nil {
		errs = append(errs, err.Error())
	}

	// Exercise list/prune + delete failure too (retention path).
	cl2 := newFakeS3()
	cl2.objects["backups/janus-backup-20200101T000001Z.jsonl"] = []byte("x")
	cl2.objects["backups/janus-backup-20200101T000002Z.jsonl"] = []byte("x")
	cl2.objects["backups/janus-backup-20200101T000003Z.jsonl"] = []byte("x")
	cl2.objects["backups/janus-backup-20200101T000004Z.jsonl"] = []byte("x")
	cl2.delErr = errors.New(sdkLeak)
	cfg2 := cfg
	cfg2.Retention = 1
	s2 := &Service{
		cfg: cfg2, dump: fakeDump{body: fixtureBackup}, runs: &fakeRuns{}, logger: logger,
		sealed:    func() bool { return false },
		now:       func() time.Time { return time.Unix(0, 0).UTC() },
		newClient: func(_ context.Context, _ S3Config) (s3API, error) { return cl2, nil },
	}
	if err := s2.RunBackup(context.Background()); err != nil {
		errs = append(errs, err.Error())
	}

	forbidden := []string{secretKey, accessKey, "arn:aws:iam", "AccessDenied", "janus-fake"}
	haystacks := append([]string{logbuf.String()}, errs...)
	for _, in := range runs.inserted {
		if in.Error != nil {
			haystacks = append(haystacks, *in.Error)
		}
	}
	for _, h := range haystacks {
		for _, bad := range forbidden {
			if strings.Contains(h, bad) {
				t.Errorf("leaked %q in output: %s", bad, h)
			}
		}
	}
	// Sanity: the failure run WAS recorded with a category, not empty.
	if len(runs.inserted) != 1 || runs.inserted[0].Error == nil || *runs.inserted[0].Error != "upload_failed" {
		t.Fatalf("want one upload_failed run, got %+v", runs.inserted)
	}
}
