package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type fileWrite struct {
	path string
	data []byte
}

type stagedFileWrite struct {
	path       string
	tempPath   string
	backupPath string
	existed    bool
	committed  bool
}

// writeFilesTransactionally stages every file beside its destination before
// replacing any destination. If a later rename fails, already-replaced files
// are restored from same-directory backups.
func writeFilesTransactionally(writes []fileWrite) error {
	if len(writes) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(writes))
	staged := make([]stagedFileWrite, 0, len(writes))
	cleanup := func() {
		for _, item := range staged {
			if item.tempPath != "" {
				_ = os.Remove(item.tempPath)
			}
			if item.backupPath != "" {
				_ = os.Remove(item.backupPath)
			}
		}
	}

	for _, write := range writes {
		if write.path == "" {
			cleanup()
			return fmt.Errorf("configuration destination path is empty")
		}
		if _, ok := seen[write.path]; ok {
			cleanup()
			return fmt.Errorf("duplicate configuration destination %q", write.path)
		}
		seen[write.path] = struct{}{}

		dir := filepath.Dir(write.path)
		temp, err := os.CreateTemp(dir, "."+filepath.Base(write.path)+".*.tmp")
		if err != nil {
			cleanup()
			return fmt.Errorf("stage %q: %w", write.path, err)
		}
		tempPath := temp.Name()
		mode := os.FileMode(0644)
		if info, statErr := os.Stat(write.path); statErr == nil {
			mode = info.Mode().Perm()
		} else if !os.IsNotExist(statErr) {
			_ = temp.Close()
			_ = os.Remove(tempPath)
			cleanup()
			return fmt.Errorf("inspect %q: %w", write.path, statErr)
		}
		if err := temp.Chmod(mode); err != nil {
			_ = temp.Close()
			_ = os.Remove(tempPath)
			cleanup()
			return fmt.Errorf("set permissions for %q: %w", write.path, err)
		}
		if _, err := temp.Write(write.data); err != nil {
			_ = temp.Close()
			_ = os.Remove(tempPath)
			cleanup()
			return fmt.Errorf("write staged %q: %w", write.path, err)
		}
		if err := temp.Sync(); err != nil {
			_ = temp.Close()
			_ = os.Remove(tempPath)
			cleanup()
			return fmt.Errorf("sync staged %q: %w", write.path, err)
		}
		if err := temp.Close(); err != nil {
			_ = os.Remove(tempPath)
			cleanup()
			return fmt.Errorf("close staged %q: %w", write.path, err)
		}
		staged = append(staged, stagedFileWrite{path: write.path, tempPath: tempPath})
	}

	rollback := func(cause error) error {
		var rollbackErr error
		for i := len(staged) - 1; i >= 0; i-- {
			item := &staged[i]
			if item.committed {
				if err := os.Remove(item.path); err != nil && !os.IsNotExist(err) && rollbackErr == nil {
					rollbackErr = fmt.Errorf("remove partial destination %q: %w", item.path, err)
				}
			}
			if item.existed && item.backupPath != "" {
				if err := os.Rename(item.backupPath, item.path); err != nil {
					if rollbackErr == nil {
						rollbackErr = fmt.Errorf("restore destination %q (backup retained at %q): %w", item.path, item.backupPath, err)
					}
					// Preserve the backup for operator recovery; cleanup must not
					// delete the only remaining copy of the original configuration.
					item.backupPath = ""
				} else {
					item.backupPath = ""
				}
			}
		}
		cleanup()
		if rollbackErr != nil {
			return fmt.Errorf("%w (rollback also failed: %v)", cause, rollbackErr)
		}
		return cause
	}

	// Preserve every original before installing any new file.
	for i := range staged {
		item := &staged[i]
		if _, err := os.Stat(item.path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return rollback(fmt.Errorf("inspect destination %q: %w", item.path, err))
		}
		backup, err := os.CreateTemp(filepath.Dir(item.path), "."+filepath.Base(item.path)+".*.bak")
		if err != nil {
			return rollback(fmt.Errorf("prepare backup for %q: %w", item.path, err))
		}
		item.backupPath = backup.Name()
		if err := backup.Close(); err != nil {
			return rollback(fmt.Errorf("close backup placeholder for %q: %w", item.path, err))
		}
		if err := os.Remove(item.backupPath); err != nil {
			return rollback(fmt.Errorf("prepare backup path for %q: %w", item.path, err))
		}
		if err := os.Rename(item.path, item.backupPath); err != nil {
			item.backupPath = ""
			return rollback(fmt.Errorf("backup destination %q: %w", item.path, err))
		}
		item.existed = true
	}

	for i := range staged {
		item := &staged[i]
		if err := os.Rename(item.tempPath, item.path); err != nil {
			return rollback(fmt.Errorf("install destination %q: %w", item.path, err))
		}
		item.tempPath = ""
		item.committed = true
	}

	cleanup()
	return nil
}
