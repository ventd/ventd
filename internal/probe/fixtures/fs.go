// Package fixtures provides synthetic fs.FS trees for probe package tests.
// All fixtures are pure in-memory testing/fstest.MapFS values; no real /sys
// or /proc paths are accessed (RULE-PROBE-01).
package fixtures

import (
	"testing/fstest"
)

// KVMRoot is a synthetic root FS that looks like a KVM guest.
func KVMRoot() fstest.MapFS {
	return fstest.MapFS{
		"sys/class/dmi/id/sys_vendor":   {Data: []byte("KVM\n")},
		"sys/class/dmi/id/product_name": {Data: []byte("Standard PC (Q35 + ICH9, 2009)\n")},
	}
}

// VMwareRoot is a synthetic root FS that looks like a VMware guest.
func VMwareRoot() fstest.MapFS {
	return fstest.MapFS{
		"sys/class/dmi/id/sys_vendor":   {Data: []byte("VMware, Inc.\n")},
		"sys/class/dmi/id/product_name": {Data: []byte("VMware Virtual Platform\n")},
	}
}

// HyperVRoot is a synthetic root FS that looks like a Hyper-V guest.
func HyperVRoot() fstest.MapFS {
	return fstest.MapFS{
		"sys/class/dmi/id/sys_vendor":   {Data: []byte("Microsoft Corporation\n")},
		"sys/class/dmi/id/product_name": {Data: []byte("Virtual Machine\n")},
	}
}

// DockerRoot is a synthetic root FS that looks like a Docker container.
// /.dockerenv exists; /proc/1/cgroup mentions docker.
func DockerRoot() fstest.MapFS {
	return fstest.MapFS{
		".dockerenv":    {Data: []byte{}},
		"proc/1/cgroup": {Data: []byte("12:memory:/docker/abc123\n11:cpuset:/docker/abc123\n")},
	}
}

// LXCRoot is a synthetic root FS for an LXC container.
func LXCRoot() fstest.MapFS {
	return fstest.MapFS{
		"proc/1/cgroup": {Data: []byte("1:name=systemd:/lxc/mycontainer\n")},
	}
}

// BareMetalRoot is a synthetic root FS for a bare-metal system with no virt
// indicators. Has enough DMI data for ReadDMI to succeed.
func BareMetalRoot() fstest.MapFS {
	return fstest.MapFS{
		"sys/class/dmi/id/sys_vendor":    {Data: []byte("ASUSTeK COMPUTER INC.\n")},
		"sys/class/dmi/id/product_name":  {Data: []byte("PRIME Z790-A WIFI\n")},
		"sys/class/dmi/id/board_vendor":  {Data: []byte("ASUSTeK COMPUTER INC.\n")},
		"sys/class/dmi/id/board_name":    {Data: []byte("PRIME Z790-A WIFI\n")},
		"sys/class/dmi/id/board_version": {Data: []byte("Rev 1.xx\n")},
		"sys/class/dmi/id/bios_version":  {Data: []byte("1801\n")},
		"proc/cpuinfo": {Data: []byte(
			"processor\t: 0\nmodel name\t: Intel(R) Core(TM) i9-13900K\n\n" +
				"processor\t: 1\nmodel name\t: Intel(R) Core(TM) i9-13900K\n\n",
		)},
	}
}

// SysWithThermal returns a synthetic /sys subtree with one hwmon device that
// has one temperature sensor and no PWM channels.
func SysWithThermal() fstest.MapFS {
	return fstest.MapFS{
		"class/hwmon/hwmon0/name":        {Data: []byte("coretemp\n")},
		"class/hwmon/hwmon0/temp1_input": {Data: []byte("45000\n")},
		"class/hwmon/hwmon0/temp1_label": {Data: []byte("Package id 0\n")},
	}
}

// SysWithThermalAndPWM returns a /sys subtree with one thermal source and one
// controllable PWM channel (for OutcomeControl scenario).
func SysWithThermalAndPWM() fstest.MapFS {
	return fstest.MapFS{
		"class/hwmon/hwmon0/name":        {Data: []byte("nct6798\n")},
		"class/hwmon/hwmon0/temp1_input": {Data: []byte("38000\n")},
		"class/hwmon/hwmon0/temp1_label": {Data: []byte("SYSTIN\n")},
		"class/hwmon/hwmon0/pwm1":        {Data: []byte("128\n")},
		"class/hwmon/hwmon0/pwm1_enable": {Data: []byte("1\n")},
		"class/hwmon/hwmon0/fan1_input":  {Data: []byte("1200\n")},
	}
}

// SysWithThermalOnly returns a /sys subtree with one thermal source but no
// controllable PWM channels (OutcomeMonitorOnly scenario).
func SysWithThermalOnly() fstest.MapFS {
	return fstest.MapFS{
		"class/hwmon/hwmon0/name":        {Data: []byte("k10temp\n")},
		"class/hwmon/hwmon0/temp1_input": {Data: []byte("55000\n")},
	}
}

// SysEmpty returns an empty /sys subtree — no thermal sources, no channels.
// Combined with a non-virt root, results in OutcomeRefuse.
func SysEmpty() fstest.MapFS {
	return fstest.MapFS{}
}

// ProcForBareMetal is a synthetic /proc subtree for a bare-metal system with
// no container indicators.
func ProcForBareMetal() fstest.MapFS {
	return fstest.MapFS{
		"1/cgroup": {Data: []byte("0::/init.scope\n")},
		"cpuinfo": {Data: []byte(
			"processor\t: 0\nmodel name\t: Intel(R) Core(TM) i9-13900K\n\n",
		)},
	}
}
