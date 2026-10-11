# Developing and releasing the library

The public module supports Go 1.22 and later and has no external runtime
dependencies. Preserve source compatibility and documented behaviour, including
logging output and configuration ownership. Intentional API or behavioural breaks
require explicit agreement, regression tests and migration notes, as in the v0.3.0
ownership change. Do not silently change constructor semantics. Sanitisation is opt-in.

## Checks

Run these with both Go 1.22.x and the latest stable Go toolchain:

```sh
go version
go test -race ./...
go vet ./...
```

The examples in `*_example_test.go` are executable GoDoc and run with the test
suite. `go test -run Example ./...` runs only examples. CI sets `GOTOOLCHAIN=local`
so the minimum-version job cannot silently switch to a newer compiler. Check
formatting with `gofmt -l .` and allocations with:

```sh
go test -run '^$' -bench . -benchmem
```

Using a current stable Go toolchain, compare the module's exported API:

```sh
bash scripts/check-api.sh v0.2.1
```

Without an argument the script selects the latest reachable version tag before
`HEAD`, so release-tag builds compare against the preceding release. Fetch full
history and tags first when working in a shallow clone. It uses the Go project's
[`apidiff`](https://pkg.go.dev/golang.org/x/exp/cmd/apidiff) tool at a pinned revision,
installed in a temporary directory. Its dependencies are development-only; the
script does not change the library's `go.mod`. An incompatible report fails the
check unless it exactly matches the reviewed, baseline-specific report in
`scripts/api-breaks/`. The v0.2.1 report records the deliberate v0.3 removals;
review any changes to this file as public API changes. This is a source-API check,
not a proof of behavioural compatibility; retain regression tests for output, configuration ownership and sanitiser policies.

## Release process

1. Update `CHANGELOG.md` with API additions, fixes, minimum Go changes and any
   changes to default redaction patterns, replacements or validation.
2. Run the checks above and review the CI matrix. Compare APIs against the
   preceding release, and review any behavioural changes separately.
3. Commit the reviewed changes. Create an annotated version tag on that commit,
   for example `git tag -a v0.3.0 -m 'Release v0.3.0'`.
4. When publishing is intended, push the reviewed branch and tag. Use the
   changelog entry as the release notes. Tags are immutable once published.
5. Verify consumption from outside the repository with
   `go list -m github.com/jmylchreest/slog-logfilter@v0.3.0` and check the generated
   package documentation on pkg.go.dev after publication.

The module version comes from Git tags; do not add a version constant or change
the module path for minor releases. This is a source library, so releases need
no application binaries or deployment artefacts. See the
[Go module release workflow](https://go.dev/doc/modules/release-workflow).

Despite the v0 version, this project aims to preserve existing callers across
minor releases. Additive API changes receive a minor bump, fixes a patch bump;
incompatible changes require an explicit migration plan and release discussion.

Only `.aide/shared/` exports are eligible for version control. Local AIDE state,
configuration and binaries are ignored.
