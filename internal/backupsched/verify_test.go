package backupsched

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var knownTables = map[string]bool{
	"seal_config": true, "projects": true, "environments": true, "users": true,
}

const goodBackup = `{"janus_backup":1,"migration_version":35,"janus_version":"test","created_at":"2020-01-01T00:00:00Z"}
{"table":"seal_config","row":{"id":1}}
{"table":"projects","row":{"id":"proj-fake","wrapped_kek":"AAECAwQ="}}
{"table":"environments","row":{"id":"env-fake"}}
`

func TestVerify_GoodBackupDecryptable(t *testing.T) {
	var unwrapped []byte
	var unwrappedID string
	v := StructVerifier{
		KnownTables:   knownTables,
		SchemaVersion: 35,
		UnwrapKEK: func(wrapped []byte, projectID string) error {
			unwrapped = wrapped
			unwrappedID = projectID
			return nil // decrypts
		},
	}
	res, err := v.Verify(context.Background(), strings.NewReader(goodBackup))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !res.Verified || !res.Decryptable {
		t.Fatalf("want verified+decryptable, got %+v", res)
	}
	if res.Records != 3 || res.Tables != 3 {
		t.Fatalf("want 3 records/3 tables, got %d/%d", res.Records, res.Tables)
	}
	if res.SchemaVer != 35 {
		t.Fatalf("want schema 35, got %d", res.SchemaVer)
	}
	// The probe unwrapped the fixture project's KEK bytes ([0,1,2,3,4]).
	if unwrappedID != "proj-fake" || len(unwrapped) != 5 {
		t.Fatalf("unwrap probe wrong: id=%q bytes=%v", unwrappedID, unwrapped)
	}
}

func TestVerify_ForeignInstanceNotDecryptableButNotFatal(t *testing.T) {
	v := StructVerifier{
		KnownTables:   knownTables,
		SchemaVersion: 35,
		UnwrapKEK:     func([]byte, string) error { return errors.New("decrypt failed") },
	}
	res, err := v.Verify(context.Background(), strings.NewReader(goodBackup))
	if err != nil {
		t.Fatalf("foreign-instance verify must not error: %v", err)
	}
	if res.Decryptable {
		t.Fatal("want decryptable=false for foreign backup")
	}
	if res.Verified {
		t.Fatal("verified must be false when material does not decrypt")
	}
	if res.Records != 3 {
		t.Fatalf("structure still parsed: want 3 records, got %d", res.Records)
	}
	if res.Note == "" {
		t.Fatal("want an explanatory note")
	}
}

func TestVerify_StructureOnlyWhenNoUnwrapper(t *testing.T) {
	v := StructVerifier{KnownTables: knownTables, SchemaVersion: 35}
	res, err := v.Verify(context.Background(), strings.NewReader(goodBackup))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// No probe → decryptable stays true (unprobed) and structure verifies.
	if !res.Verified {
		t.Fatalf("structure-only verify should pass, got %+v", res)
	}
}

func TestVerify_SchemaMismatch(t *testing.T) {
	v := StructVerifier{KnownTables: knownTables, SchemaVersion: 99}
	res, err := v.Verify(context.Background(), strings.NewReader(goodBackup))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Verified {
		t.Fatal("schema mismatch must not verify")
	}
	if !strings.Contains(res.Note, "schema_version mismatch") {
		t.Fatalf("want schema mismatch note, got %q", res.Note)
	}
}

func TestVerify_RejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"empty":            ``,
		"bad header":       `not json` + "\n",
		"wrong version":    `{"janus_backup":2,"migration_version":35}` + "\n",
		"unknown table":    "{\"janus_backup\":1,\"migration_version\":35}\n{\"table\":\"evil\",\"row\":{}}\n",
		"malformed record": "{\"janus_backup\":1,\"migration_version\":35}\nnot json\n",
		"header only":      `{"janus_backup":1,"migration_version":35}` + "\n",
	}
	v := StructVerifier{KnownTables: knownTables}
	for name, in := range cases {
		_, err := v.Verify(context.Background(), strings.NewReader(in))
		if err == nil {
			t.Errorf("%s: want error, got nil", name)
		}
	}
}
