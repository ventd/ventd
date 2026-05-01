// Package smartmode holds cross-spec integration tests that validate the
// design-of-record in specs/spec-smart-mode.md. Per-rule unit tests live
// in their owning package (probe, envelope, idle, observation, …); this
// package asserts the §16 success criteria that span multiple packages
// and cannot be expressed in a single rule binding.
//
// The package is test-only — there is no production code. The single
// non-test file is this doc.go so the directory is a valid Go package.
package smartmode
