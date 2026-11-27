package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFilesTransactionallyReplacesAllFiles(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "config.yaml")
	second := filepath.Join(dir, "services.yaml")
	if err := os.WriteFile(first, []byte("old-config"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("old-services"), 0640); err != nil {
		t.Fatal(err)
	}

	err := writeFilesTransactionally([]fileWrite{
		{path: first, data: []byte("new-config")},
		{path: second, data: []byte("new-services")},
	})
	if err != nil {
		t.Fatalf("writeFilesTransactionally() error = %v", err)
	}

	for path, want := range map[string]string{first: "new-config", second: "new-services"} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	info, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("existing file mode = %o, want 600", info.Mode().Perm())
	}
}

func TestWriteFilesTransactionallyStagesBeforeReplacing(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(first, []byte("known-good"), 0600); err != nil {
		t.Fatal(err)
	}

	err := writeFilesTransactionally([]fileWrite{
		{path: first, data: []byte("candidate")},
		{path: filepath.Join(dir, "missing", "services.yaml"), data: []byte("services")},
	})
	if err == nil {
		t.Fatal("expected staging failure")
	}
	got, readErr := os.ReadFile(first)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "known-good" {
		t.Fatalf("existing config changed after staging failure: %q", got)
	}
}

func TestWriteFilesTransactionallyRejectsDuplicatePaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := writeFilesTransactionally([]fileWrite{
		{path: path, data: []byte("first")},
		{path: path, data: []byte("second")},
	})
	if err == nil {
		t.Fatal("expected duplicate destination error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("duplicate destination unexpectedly wrote file: %v", err)
	}
}
