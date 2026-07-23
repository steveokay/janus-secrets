package backupsched

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/steveokay/janus-secrets/internal/store"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---- fakes -----------------------------------------------------------------

// fakeS3 is an in-memory S3 with obviously-fake objects. No live AWS.
type fakeS3 struct {
	objects  map[string][]byte
	putErr   error
	listErr  error
	delErr   error
	getErr   error
	putCalls int
	delCalls int
}

func newFakeS3() *fakeS3 { return &fakeS3{objects: map[string][]byte{}} }

func (f *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.putCalls++
	if f.putErr != nil {
		return nil, f.putErr
	}
	body, _ := io.ReadAll(in.Body)
	f.objects[*in.Key] = body
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	prefix := ""
	if in.Prefix != nil {
		prefix = *in.Prefix
	}
	var keys []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := &s3.ListObjectsV2Output{IsTruncated: aws.Bool(false)}
	for _, k := range keys {
		kk := k
		out.Contents = append(out.Contents, s3types.Object{Key: aws.String(kk)})
	}
	return out, nil
}

func (f *fakeS3) DeleteObject(_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	f.delCalls++
	if f.delErr != nil {
		return nil, f.delErr
	}
	delete(f.objects, *in.Key)
	return &s3.DeleteObjectOutput{}, nil
}

func (f *fakeS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	body, ok := f.objects[*in.Key]
	if !ok {
		return nil, errors.New("not found")
	}
	n := int64(len(body))
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(body)), ContentLength: &n}, nil
}

// fakeDump writes a fixed, obviously-fake backup artifact.
type fakeDump struct {
	err  error
	body string
}

func (d fakeDump) WriteBackup(_ context.Context, w io.Writer, _ string) error {
	if d.err != nil {
		return d.err
	}
	_, err := io.WriteString(w, d.body)
	return err
}

// fakeRuns captures recorded runs in memory.
type fakeRuns struct {
	inserted []store.BackupRunInput
	err      error
}

func (r *fakeRuns) InsertRun(_ context.Context, in store.BackupRunInput) error {
	if r.err != nil {
		return r.err
	}
	r.inserted = append(r.inserted, in)
	return nil
}

// fakeVerifier records that Verify ran and returns a canned result.
type fakeVerifier struct {
	called int
	res    RehearsalResult
	err    error
	sawLen int
}

func (v *fakeVerifier) Verify(_ context.Context, r io.Reader) (RehearsalResult, error) {
	v.called++
	b, _ := io.ReadAll(r)
	v.sawLen = len(b)
	return v.res, v.err
}

// fixed low-entropy fixture backup (header + a couple of value-free rows).
const fixtureBackup = `{"janus_backup":1,"migration_version":35,"janus_version":"test","created_at":"2020-01-01T00:00:00Z"}
{"table":"projects","row":{"id":"proj-fake","wrapped_kek":"AAAA"}}
{"table":"environments","row":{"id":"env-fake"}}
`

func newTestService(t *testing.T, cfg Config, dump dumper, runs runRepo, v verifier, cl *fakeS3) *Service {
	t.Helper()
	s := &Service{
		cfg:       cfg,
		dump:      dump,
		runs:      runs,
		logger:    testLogger(),
		sealed:    func() bool { return false },
		verify:    v,
		now:       func() time.Time { return time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC) },
		newClient: func(_ context.Context, _ S3Config) (s3API, error) { return cl, nil },
	}
	return s
}

func baseCfg() Config {
	return Config{
		S3: S3Config{Bucket: "fake-bucket", Prefix: "backups", Region: "us-fake-1",
			AccessKeyID: "AKIAFAKE", SecretAccessKey: "fakesecret"},
		Tick:      time.Minute,
		Retention: 3,
		Version:   "test",
	}
}

// ---- tests -----------------------------------------------------------------

func TestRunBackup_UploadsAndRecordsSuccess(t *testing.T) {
	cl := newFakeS3()
	runs := &fakeRuns{}
	s := newTestService(t, baseCfg(), fakeDump{body: fixtureBackup}, runs, nil, cl)

	if err := s.RunBackup(context.Background()); err != nil {
		t.Fatalf("RunBackup: %v", err)
	}
	if cl.putCalls != 1 {
		t.Fatalf("want 1 PutObject, got %d", cl.putCalls)
	}
	wantKey := "backups/janus-backup-20200102T030405Z.jsonl"
	if _, ok := cl.objects[wantKey]; !ok {
		t.Fatalf("object %q not uploaded; have %v", wantKey, keysOf(cl))
	}
	// The uploaded bytes are exactly the sealed artifact the dumper produced.
	if string(cl.objects[wantKey]) != fixtureBackup {
		t.Fatalf("uploaded bytes differ from dump artifact")
	}
	if len(runs.inserted) != 1 || runs.inserted[0].Status != "success" {
		t.Fatalf("want 1 success run, got %+v", runs.inserted)
	}
	if runs.inserted[0].ObjectKey == nil || *runs.inserted[0].ObjectKey != wantKey {
		t.Fatalf("run object_key wrong: %+v", runs.inserted[0].ObjectKey)
	}
	if runs.inserted[0].SizeBytes == nil || *runs.inserted[0].SizeBytes != int64(len(fixtureBackup)) {
		t.Fatalf("run size wrong: %+v", runs.inserted[0].SizeBytes)
	}
}

func TestRunBackup_RetentionPrunesKeepingN(t *testing.T) {
	cl := newFakeS3()
	// Seed 5 older backups; newest-first the retention (3) keeps the 3 latest.
	for _, ts := range []string{
		"20200101T000001Z", "20200101T000002Z", "20200101T000003Z",
		"20200101T000004Z", "20200101T000005Z",
	} {
		cl.objects["backups/janus-backup-"+ts+".jsonl"] = []byte("old")
	}
	// An unrelated object under the prefix must never be pruned.
	cl.objects["backups/unrelated.txt"] = []byte("keep me")

	runs := &fakeRuns{}
	cfg := baseCfg() // Retention 3
	s := newTestService(t, cfg, fakeDump{body: fixtureBackup}, runs, nil, cl)

	if err := s.RunBackup(context.Background()); err != nil {
		t.Fatalf("RunBackup: %v", err)
	}
	// After upload there are 6 janus backups; retention keeps 3, so 3 deleted.
	if cl.delCalls != 3 {
		t.Fatalf("want 3 deletes, got %d", cl.delCalls)
	}
	remaining := backupKeysOf(cl)
	if len(remaining) != 3 {
		t.Fatalf("want 3 backups retained, got %d: %v", len(remaining), remaining)
	}
	// The freshly-uploaded one (newest) must survive.
	newest := "backups/janus-backup-20200102T030405Z.jsonl"
	if _, ok := cl.objects[newest]; !ok {
		t.Fatalf("newest backup was pruned")
	}
	// The unrelated object is untouched.
	if _, ok := cl.objects["backups/unrelated.txt"]; !ok {
		t.Fatalf("unrelated object was pruned")
	}
}

func TestRunBackup_RetentionDisabledKeepsAll(t *testing.T) {
	cl := newFakeS3()
	for _, ts := range []string{"20200101T000001Z", "20200101T000002Z"} {
		cl.objects["backups/janus-backup-"+ts+".jsonl"] = []byte("old")
	}
	cfg := baseCfg()
	cfg.Retention = 0 // disabled
	s := newTestService(t, cfg, fakeDump{body: fixtureBackup}, &fakeRuns{}, nil, cl)
	if err := s.RunBackup(context.Background()); err != nil {
		t.Fatalf("RunBackup: %v", err)
	}
	if cl.delCalls != 0 {
		t.Fatalf("retention disabled must not delete; got %d", cl.delCalls)
	}
	if len(backupKeysOf(cl)) != 3 {
		t.Fatalf("want all 3 backups kept, got %d", len(backupKeysOf(cl)))
	}
}

func TestRunBackup_UploadFailureRecordsFailure(t *testing.T) {
	cl := newFakeS3()
	cl.putErr = errors.New("s3://fake-bucket AccessDenied arn:aws:iam::123456789012:user/x") // must NOT leak
	runs := &fakeRuns{}
	s := newTestService(t, baseCfg(), fakeDump{body: fixtureBackup}, runs, nil, cl)

	err := s.RunBackup(context.Background())
	if err == nil {
		t.Fatal("want error on upload failure")
	}
	if strings.Contains(err.Error(), "AccessDenied") || strings.Contains(err.Error(), "arn:aws") {
		t.Fatalf("upload error leaked SDK detail: %v", err)
	}
	if len(runs.inserted) != 1 || runs.inserted[0].Status != "failure" {
		t.Fatalf("want 1 failure run, got %+v", runs.inserted)
	}
	if runs.inserted[0].Error == nil || *runs.inserted[0].Error != "upload_failed" {
		t.Fatalf("want sanitized category upload_failed, got %+v", runs.inserted[0].Error)
	}
	if runs.inserted[0].ObjectKey != nil {
		t.Fatalf("failure run must not record an object key")
	}
}

func TestRunBackup_DumpFailureRecordsFailure(t *testing.T) {
	cl := newFakeS3()
	runs := &fakeRuns{}
	s := newTestService(t, baseCfg(), fakeDump{err: errors.New("schema inconsistent: 2 tables missing")}, runs, nil, cl)
	err := s.RunBackup(context.Background())
	if err == nil {
		t.Fatal("want error on dump failure")
	}
	if cl.putCalls != 0 {
		t.Fatalf("must not upload after dump failure; got %d puts", cl.putCalls)
	}
	if len(runs.inserted) != 1 || *runs.inserted[0].Error != "dump_failed" {
		t.Fatalf("want dump_failed failure run, got %+v", runs.inserted)
	}
}

func TestRunBackup_SkippedWhileSealed(t *testing.T) {
	cl := newFakeS3()
	runs := &fakeRuns{}
	s := newTestService(t, baseCfg(), fakeDump{body: fixtureBackup}, runs, nil, cl)
	s.sealed = func() bool { return true }
	if err := s.RunBackup(context.Background()); err != nil {
		t.Fatalf("sealed RunBackup should be a no-op nil, got %v", err)
	}
	if cl.putCalls != 0 || len(runs.inserted) != 0 {
		t.Fatalf("sealed backup must do nothing; puts=%d runs=%d", cl.putCalls, len(runs.inserted))
	}
}

func TestRehearse_VerifiesLatestWithoutClobber(t *testing.T) {
	cl := newFakeS3()
	// Two backups; rehearse must pick the newest.
	cl.objects["backups/janus-backup-20200101T000001Z.jsonl"] = []byte("older")
	cl.objects["backups/janus-backup-20200101T000009Z.jsonl"] = []byte(fixtureBackup)
	v := &fakeVerifier{res: RehearsalResult{Verified: true, Records: 2, Tables: 2, Decryptable: true}}
	runs := &fakeRuns{}
	s := newTestService(t, baseCfg(), fakeDump{body: fixtureBackup}, runs, v, cl)

	res, err := s.Rehearse(context.Background(), "")
	if err != nil {
		t.Fatalf("Rehearse: %v", err)
	}
	if !res.Verified {
		t.Fatalf("want verified result, got %+v", res)
	}
	if res.ObjectKey != "backups/janus-backup-20200101T000009Z.jsonl" {
		t.Fatalf("rehearse picked wrong object: %q", res.ObjectKey)
	}
	if v.called != 1 || v.sawLen != len(fixtureBackup) {
		t.Fatalf("verifier not fed the full artifact: called=%d len=%d", v.called, v.sawLen)
	}
	// Non-clobber: no puts, no deletes, no run rows written by a rehearsal.
	if cl.putCalls != 0 || cl.delCalls != 0 {
		t.Fatalf("rehearsal must not write/delete S3; puts=%d dels=%d", cl.putCalls, cl.delCalls)
	}
	if len(runs.inserted) != 0 {
		t.Fatalf("rehearsal must not record runs; got %d", len(runs.inserted))
	}
	// Both objects still present (nothing removed).
	if len(cl.objects) != 2 {
		t.Fatalf("rehearsal changed object set: %v", keysOf(cl))
	}
}

func TestRehearse_NamedObject(t *testing.T) {
	cl := newFakeS3()
	cl.objects["backups/janus-backup-20200101T000001Z.jsonl"] = []byte("older")
	cl.objects["backups/janus-backup-20200101T000009Z.jsonl"] = []byte(fixtureBackup)
	v := &fakeVerifier{res: RehearsalResult{Verified: true}}
	s := newTestService(t, baseCfg(), fakeDump{body: fixtureBackup}, &fakeRuns{}, v, cl)

	res, err := s.Rehearse(context.Background(), "backups/janus-backup-20200101T000001Z.jsonl")
	if err != nil {
		t.Fatalf("Rehearse named: %v", err)
	}
	if res.ObjectKey != "backups/janus-backup-20200101T000001Z.jsonl" {
		t.Fatalf("named rehearse used wrong object: %q", res.ObjectKey)
	}
}

func TestRehearse_NoBackups(t *testing.T) {
	cl := newFakeS3()
	v := &fakeVerifier{}
	s := newTestService(t, baseCfg(), fakeDump{}, &fakeRuns{}, v, cl)
	_, err := s.Rehearse(context.Background(), "")
	if !errors.Is(err, ErrNoBackups) {
		t.Fatalf("want ErrNoBackups, got %v", err)
	}
	if v.called != 0 {
		t.Fatal("verifier must not run when there is nothing to verify")
	}
}

func TestConfig_Enabled(t *testing.T) {
	if (Config{}).Enabled() {
		t.Fatal("zero config must be disabled")
	}
	if (Config{Tick: time.Minute}).Enabled() {
		t.Fatal("no bucket → disabled")
	}
	if !(Config{Tick: time.Minute, S3: S3Config{Bucket: "b"}}).Enabled() {
		t.Fatal("tick+bucket → enabled")
	}
}

// ---- helpers ---------------------------------------------------------------

func keysOf(f *fakeS3) []string {
	var out []string
	for k := range f.objects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func backupKeysOf(f *fakeS3) []string {
	var out []string
	for k := range f.objects {
		if strings.Contains(k, "janus-backup-") {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
