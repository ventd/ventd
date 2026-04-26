package hwdb

import (
	"bytes"
	"errors"
	"fmt"
	"sync/atomic"

	"gopkg.in/yaml.v3"

	"github.com/ventd/ventd/internal/diag/redactor"
)

// ErrAnonymise is the sentinel returned when anonymisation fails.
var ErrAnonymise = errors.New("anonymise")

// atomicAnonymiseFn stores the active anonymise implementation. An
// atomic.Pointer is used so test injection is race-safe under t.Parallel.
var atomicAnonymiseFn atomic.Pointer[func(*Profile) error]

func init() {
	fn := func(p *Profile) error { return anonymiseProfile(p) }
	atomicAnonymiseFn.Store(&fn)
}

// callAnonymise dispatches through atomicAnonymiseFn for injectability.
func callAnonymise(p *Profile) error {
	return (*atomicAnonymiseFn.Load())(p)
}

// Anonymise applies the profile-class anonymisation pipeline to profile,
// mutating it in place. The function:
//  1. Forces contributed_by="anonymous" and verified=false.
//  2. Clears user-set text fields (fan labels, sensor trust reasons) that
//     could contain arbitrary user PII.
//  3. Applies text-level redaction (hostname, MAC, IP, path, USB-physical)
//     via internal/diag/redactor to vendor-attributed identification fields.
//  4. Validates via a strict YAML round-trip (KnownFields) to enforce
//     RULE-HWDB-CAPTURE-03.
//
// Fails closed: returns a non-nil error if the round-trip fails.
// RULE-HWDB-CAPTURE-02, RULE-HWDB-CAPTURE-03.
func Anonymise(p *Profile) error {
	return anonymiseProfile(p)
}

// anonymiseProfile is the concrete implementation (split so the init() fn
// literal doesn't reference Anonymise before it is defined).
func anonymiseProfile(profile *Profile) error {
	profile.ContributedBy = "anonymous"
	profile.Verified = false

	// Clear user-set text fields. Fan labels and sensor trust reasons are
	// chosen by the contributor and are not needed for catalog matching.
	// Clearing them prevents PII leaks even when the redactor doesn't know
	// the specific username or hostname used as the label.
	for i := range profile.Hardware.Fans {
		profile.Hardware.Fans[i].Label = ""
	}
	for i := range profile.SensorTrust {
		profile.SensorTrust[i].Reason = ""
	}

	// Apply text-level primitives (P1 hostname, P3 MAC, P4 IP, P5 username,
	// P6 path, P7 USB-physical) to vendor-attributed identification fields.
	// We operate on individual string values, not on full YAML, so P9UserLabel
	// (which replaces with [REDACTED] — invalid YAML when decoded into a string
	// field) is not needed: user-controlled labels are cleared above.
	r := newProfileRedactor()
	redactStr := func(s string) string {
		return string(r.Apply([]byte(s)))
	}
	profile.Fingerprint.DMISysVendor = redactStr(profile.Fingerprint.DMISysVendor)
	profile.Fingerprint.DMIProductName = redactStr(profile.Fingerprint.DMIProductName)
	profile.Fingerprint.DMIBoardVendor = redactStr(profile.Fingerprint.DMIBoardVendor)
	profile.Fingerprint.DMIBoardName = redactStr(profile.Fingerprint.DMIBoardName)

	// Strict YAML round-trip: KnownFields(true) rejects any field that was
	// added to the serialised form but is not in the v1 Profile struct.
	// This satisfies RULE-HWDB-CAPTURE-03 (allowlisted fields only).
	data, err := yaml.Marshal(profile)
	if err != nil {
		return fmt.Errorf("%w: marshal: %v", ErrAnonymise, err)
	}
	var out Profile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&out); err != nil {
		return fmt.Errorf("%w: re-decode (schema violation or unknown field): %v", ErrAnonymise, err)
	}
	*profile = out
	return nil
}

// newProfileRedactor builds a Redactor configured for profile-class PII.
// Uses default-conservative profile which activates P1–P10 primitives.
// A nil MappingStore causes New to allocate a fresh in-memory store.
func newProfileRedactor() *redactor.Redactor {
	return redactor.New(redactor.Config{
		Profile: redactor.ProfileConservative,
	}, nil)
}
