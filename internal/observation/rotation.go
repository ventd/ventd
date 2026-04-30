package observation

// This file is the home for rotation-related constants and helpers.
// DefaultRotationPolicy (in schema.go) defines the retention values.
// Writer.Rotate() implements the midnight-crossing and 50 MB triggers.
// Rotation tests live in rotation_test.go and exercise Writer via
// clock injection.
