package idle

import (
	"bufio"
	"os"
	"strings"
)

// PSIAvailable returns true when /proc/pressure/cpu exists and begins with
// "some " — the kernel-PSI format indicator.
func PSIAvailable(procRoot string) bool {
	path := psiPath(procRoot, "cpu")
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.HasPrefix(string(data), "some ")
}

func psiPath(procRoot, resource string) string {
	if procRoot == "" {
		return "/proc/pressure/" + resource
	}
	return procRoot + "/pressure/" + resource
}

// capturePSI reads /proc/pressure/{cpu,io,memory} and returns parsed averages.
// Zero values are returned for any file that cannot be read.
func capturePSI(procRoot string) PSIReadings {
	var r PSIReadings
	parsePSIFile(psiPath(procRoot, "cpu"), func(kind string, avg10, avg60, avg300 float64) {
		if kind == "some" {
			r.CPUSomeAvg10 = avg10
			r.CPUSomeAvg60 = avg60
			r.CPUSomeAvg300 = avg300
		}
	})
	parsePSIFile(psiPath(procRoot, "io"), func(kind string, avg10, avg60, avg300 float64) {
		if kind == "some" {
			r.IOSomeAvg10 = avg10
			r.IOSomeAvg60 = avg60
			r.IOSomeAvg300 = avg300
		}
	})
	parsePSIFile(psiPath(procRoot, "memory"), func(kind string, avg10, avg60, avg300 float64) {
		if kind == "full" {
			r.MemFullAvg10 = avg10
			r.MemFullAvg60 = avg60
		}
	})
	return r
}

// parsePSIFile parses a /proc/pressure/<resource> file, calling fn for each
// "some" or "full" line with its avg10/avg60/avg300 values.
//
// Format:
//
//	some avg10=0.00 avg60=0.00 avg300=0.00 total=...
//	full avg10=0.00 avg60=0.00 avg300=0.00 total=...
func parsePSIFile(path string, fn func(kind string, avg10, avg60, avg300 float64)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		kind := fields[0]
		var avg10, avg60, avg300 float64
		for _, kv := range fields[1:] {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 {
				continue
			}
			v, err := parseFloat64(parts[1])
			if err != nil {
				continue
			}
			switch parts[0] {
			case "avg10":
				avg10 = v
			case "avg60":
				avg60 = v
			case "avg300":
				avg300 = v
			}
		}
		fn(kind, avg10, avg60, avg300)
	}
}
