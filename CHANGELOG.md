# Changelog

## v0.3.0

This release adds opt-in sanitisation and intentionally changes logger
configuration ownership and removes the package-level configuration API.
Applications must migrate as described below.

### Added

- `Handler.SetLevel` and `Handler.GetLevel` expose instance-level control.
- `WithContextExtractors` supplies initial logger-local extractors. Handler-level
  registration, lookup and removal methods support live registry updates.
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
- Filter expiry values are copied on configuration input and output, preventing
  indirect changes through caller-owned pointers.
- Output-level transformation avoids copying record attributes.
- Sanitisation reuses unchanged strings, groups and record attribute storage.
  Matching-heavy workloads allocate less but may do extra regex work; benchmarks
  are provided for measuring application-relevant tradeoffs.

### Breaking behaviour and migration from v0.2.x

- Go 1.22 remains the minimum version. No external runtime dependencies added.
- `New` creates an independent logger and does not change global state.
- Removed package-level `SetLevel`, `GetLevel`, `SetFilters`, `GetFilters`,
  `AddFilter`, `RemoveFilter`, `ClearFilters`, `RegisterContextExtractor`,
  `UnregisterContextExtractor`, `GetContextExtractor`, `ClearContextExtractors`
  and `ContextExtractorKeys`. Use the corresponding methods on
  `logger.Handler().(*logfilter.Handler)`.
- Removed `GetHandler`; retain the handler from the constructed logger instead.
- Removed `SetDefault`; construct with `New(options...)`, then explicitly call
  `slog.SetDefault(logger)`. There is no separate package-global registry.
- API checks allow only these documented removals against v0.2.1; unexpected
  source incompatibilities still fail.
- `With` and `WithGroup` now share live filter and extractor updates, as well as
  levels. Updates through a child also reach parents and siblings. Use separate
  constructors when independent configuration is required.
- `NewHandler` starts with an empty context registry and does not read the global
  registry. Use its `RegisterContextExtractor` method instead. Supplying the same
  `LevelVar` to separate handlers still shares only the level explicitly.
- Sanitisers remain immutable and opt-in; log output without sanitisation is
  unchanged when the same effective configuration is used.
- Future default sanitiser policy changes will be described in release notes.
  Supply explicit rules when log-output stability requires a fixed policy.
