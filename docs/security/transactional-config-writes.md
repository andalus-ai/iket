# Transactional file configuration writes

The file-backed provider validates a candidate configuration before writing it.
When a separate services file is configured, the provider now stages both YAML
outputs before replacing either destination.

## Guarantees

- Writes through one FileProvider instance are serialized.
- Every destination is staged in its own directory, flushed, and closed before
  any existing destination is moved.
- Existing file permissions are retained; new files use mode 0644.
- Existing destinations are moved to same-directory backup paths before staged
  files are installed.
- If an installation step fails, already-installed candidates are removed and
  original files are restored from backups.
- Duplicate destination paths are rejected.

## Boundaries

This is a best-effort rollback-capable filesystem transaction, not a portable
multi-file atomic commit: readers outside the provider can observe the brief
interval between individual renames, and a process or machine crash during the
commit sequence can require operator recovery from leftover backup files.
The mutex protects only calls made through the same FileProvider instance; it
does not provide cross-process locking or compare-and-swap semantics between
multiple Iket processes.

For multi-instance deployments, use a transactional database-backed provider
or a future revision/CAS contract rather than sharing writable YAML paths.
External writers should continue to publish files using same-directory
temporary files and atomic rename.
