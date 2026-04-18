package ipmi

// Compile-time references to test-only constructors that are declared but not
// yet called by any test. Prevents unused lint without a nolint suppression.
var (
	_ = (*Backend)(nil).withDMI
	_ = (*Backend)(nil).withVendor
)
