# Changelog

## v0.3.0

This release adds opt-in sanitisation and isolated logger configuration while
preserving the public API and default logging behaviour of v0.2.1.

### Added

- `NewIsolated` creates a logger with an independent level, filters and context
  extractor registry, without changing package globals or `slog.Default`.
- `Handler.SetLevel` and `Handler.GetLevel` expose instance-level control.
- `WithContextExtractors` supplies an immutable, logger-local registry. It can
  also be used with the existing `New` constructor.
- `NewSanitiser`, `SanitiserRule`, `WithSanitiser`, `WithSanitisers` and
  `NewSanitisingHandler` support reusable, ordered sanitisation configurations.
- Rules support field-name and value regexes, literal replacement text and
  `ReplaceFunc` callbacks for custom validation and masking.
- GoDoc examples, race and compatibility tests, allocation benchmarks, a CI
  matrix for Go 1.22 and stable Go, and a repeatable API comparison script.

### Default sanitisation policy

- Disabled unless explicitly configured. `DefaultSanitiserRules()` is opt-in.
- Redacts named credential fields, labelled credentials, Bearer tokens and
  hyphenated UUIDs. Quoted credentials support escaped quotes.
- Card-like strings must have 13–19 digits and a valid Luhn checksum; explicit
  card fields are redacted even when their values are invalid. Issuers are not
  checked. Other numeric IDs with valid checksums may also be redacted.
- No explicit phone, email, IP-address or provider-specific API-key detection.
- Applies to messages, bound attributes, nested slog groups and resolved
  LogValuer values. Opaque maps, structs, slices and errors require whole-field
  rules or conversion to slog groups. Attribute names, source metadata, context
  contents and data added by downstream handlers are not scanned.
- Callbacks are concurrent user code, may return the input unchanged or remove
  matches with an empty result, and propagate panics. JSON does not preserve them;
  reattach functions after loading configuration, including default Luhn checking.

### Fixed and optimised

- Filter updates no longer race with logging or handler derivation.
- Output-level transformation avoids copying record attributes.
- Sanitisation reuses unchanged strings, groups and record attribute storage.
  Matching-heavy workloads allocate less but may do extra regex work; benchmarks
  are provided for measuring application-relevant tradeoffs.

### Compatibility and migration

- Go 1.22 remains the minimum version. No external runtime dependencies added.
- All v0.2.1 public APIs remain available. `New` still shares a global level, and
  package-level filter setters still target the most recently created handler.
  `SetDefault` still sets the default slog logger.
- Derived loggers share levels but retain filter snapshots. Parent filter changes
  do not update existing children. This is documented and tested, not changed.
- To isolate a logger, replace `New` with `NewIsolated`, move global level/filter
  setters to its `*Handler`, and supply context extractors with
  `WithContextExtractors`. Package-level registrations do not affect isolated loggers.
- Default sanitiser policy changes will be described in future release notes.
  Supply explicit rules when log-output stability requires a fixed policy.
