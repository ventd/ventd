package ipmi

import (
	"log/slog"

	"github.com/ventd/ventd/internal/hal"
)

// NewBackendNonServer creates a Backend whose DMI reports a desktop chassis.
// Enumerate will return empty without opening /dev/ipmi0.
func NewBackendNonServer(logger *slog.Logger) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	b := &Backend{
		device:  "/dev/ipmi0",
		logger:  logger,
		readDMI: readDMIFromSysfs,
		fd:      -1,
	}
	b.dmi = dmiInfo{chassisType: 3, sysVendor: "ASUS"}
	b.vendor = detectVendorFromString(b.dmi.sysVendor)
	return b
}

// NewBackendForTest creates a Backend with the given vendor string injected
// directly (bypassing DMI and device access).  Used to test the Write and
// Restore vendor-dispatch paths without a real BMC.
func NewBackendForTest(logger *slog.Logger, vendor string) *Backend {
	if logger == nil {
		logger = slog.Default()
	}
	return &Backend{
		device:  "/dev/ipmi0",
		logger:  logger,
		readDMI: readDMIFromSysfs,
		dmi:     dmiInfo{chassisType: 23, sysVendor: "test"},
		vendor:  vendor,
		fd:      -1,
	}
}

// MakeTestChannel constructs a hal.Channel with IPMI State opaque for use in
// unit tests that exercise Write / Read / Restore without a real device.
func MakeTestChannel(sensorNum uint8, name string) hal.Channel {
	return hal.Channel{
		ID:   "sensor" + string(rune('0'+sensorNum)),
		Role: hal.RoleAIOFan,
		Caps: hal.CapRead | hal.CapWritePWM | hal.CapRestore,
		Opaque: State{
			SensorNumber: sensorNum,
			SensorName:   name,
		},
	}
}

// DetectVendorFromString exposes detectVendorFromString for table-driven tests.
var DetectVendorFromString = detectVendorFromString

// PWMToPercent exposes pwmToPercent for conversion tests.
var PWMToPercent = pwmToPercent
