# Configuration file reload governance

The file-backed provider treats the main configuration file and the optional
service configuration file as one logical configuration input.

## Reload behavior

- Both configured paths are polled by the file watcher (5-second interval).
- A change to either file triggers a full provider load, environment expansion,
  default normalization, and configuration validation before callbacks run.
- A parse or validation failure leaves the last successfully observed file
  timestamps unchanged. The watcher retries on the next poll; consumers keep
  using their last successfully applied configuration.
- Watcher callbacks are copied while holding the provider read lock and invoked
  after releasing it. This avoids holding the watcher registry lock while
  arbitrary reload code executes.
- A callback failure is logged and retried on a later poll.

## Operational guidance

External editors and deployment tooling should write each file through a
same-directory temporary file followed by an atomic rename. Avoid in-place
partial writes. The watcher validates the complete loaded configuration before
notifying consumers, but the two-file format is not a cross-file filesystem
transaction: deployments that update both files should publish them together
as closely as possible and should use a coordinated rollout mechanism where
strict cross-file atomicity is required.

The provider does not claim that a configuration is active merely because a
file changed. The callback is the integration point that must successfully
apply the validated configuration to the running gateway.
