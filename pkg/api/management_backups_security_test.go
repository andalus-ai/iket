package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bhangun/iket/pkg/config"
)

func TestSnapshotIdentifiersRejectPathTraversal(t *testing.T) {
	for _, id := range []string{
		"../outside",
		"rev-../../outside",
		"rev-20261128-101010.123456789/../../outside",
		"",
	} {
		if validRevisionID(id) {
			t.Errorf("validRevisionID(%q) unexpectedly accepted", id)
		}
	}
	for _, id := range []string{
		"backup-20261128-101010",
		"backup-20261128-101010.123456789",
	} {
		if !validBackupID(id) {
			t.Errorf("validBackupID(%q) rejected valid backup ID", id)
		}
	}
	if validBackupID("../outside") {
		t.Fatal("backup path traversal ID was accepted")
	}
}

func TestSaveAndLoadConfigRevisionVerifiesIntegrity(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil { t.Fatal(err) }
	workDir := t.TempDir()
	if err := os.Chdir(workDir); err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	cfg := &config.Config{Server: config.ServerConfig{Port: 8080}}
	id, err := saveConfigRevision("test", "test snapshot", "note", "change-123", nil, cfg)
	if err != nil { t.Fatal(err) }
	if !validRevisionID(id) { t.Fatalf("generated invalid revision ID %q", id) }

	record, err := loadConfigRevision(id)
	if err != nil { t.Fatal(err) }
	if record.ConfigHash == "" { t.Fatal("revision has no configuration hash") }

	path := filepath.Join(revisionsDir(), id+".json")
	data, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	var stored configRevisionRecord
	if err := json.Unmarshal(data, &stored); err != nil { t.Fatal(err) }
	stored.Config.Server.Port = 9090
	corrupted, err := json.Marshal(stored)
	if err != nil { t.Fatal(err) }
	if err := os.WriteFile(path, corrupted, 0600); err != nil { t.Fatal(err) }
	if _, err := loadConfigRevision(id); err == nil {
		t.Fatal("expected tampered snapshot to fail integrity verification")
	}
}

func TestWriteSnapshotFileUsesOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "snapshot.json")
	if err := writeSnapshotFile(path, []byte(`{"ok":true}`)); err != nil { t.Fatal(err) }
	info, err := os.Stat(path)
	if err != nil { t.Fatal(err) }
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("snapshot permissions = %04o, want 0600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	if string(data) != `{"ok":true}` { t.Fatalf("unexpected snapshot content: %s", data) }
}
