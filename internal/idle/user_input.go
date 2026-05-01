package idle

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// inputIRQClassifier matches IRQ action labels that v0.5.5 OpportunisticGate
// considers human-input. The list mirrors typical Linux input subsystems:
// PS/2 (i8042), USB host controllers (xhci/ehci/uhci/ohci), USB HID class
// drivers (usbhid), and explicit kbd/mouse drivers.
//
// Classification reads /sys/kernel/irq/<irq>/actions, which kernel ≥4.10
// exposes as a comma-separated list of driver names attached to the IRQ.
// Older kernels expose the same names through /proc/interrupts itself
// (the trailing label column); IRQActions also falls back to that column.
var inputIRQClassifierKeywords = []string{
	"i8042",
	"xhci_hcd",
	"ehci_hcd",
	"uhci_hcd",
	"ohci_hcd",
	"hid",
	"usbhid",
	"kbd",
	"mouse",
	"synaptics",
	"elan",
}

// IRQCounters maps IRQ identifier (e.g. "1", "12", "NMI") -> total count
// summed across CPUs. Captured by ReadIRQCounters and compared between
// snapshots to detect input activity since the last sample.
type IRQCounters map[string]uint64

// ReadIRQCounters reads /proc/interrupts under procRoot and returns the
// per-IRQ summed-across-CPUs counter. The caller is responsible for
// holding two snapshots and comparing for delta.
//
// Returns an error if /proc/interrupts is unreadable or unparseable.
// Conservative behaviour: if the format is unrecognised, the error
// surfaces; caller should treat that as "input activity unknown" and
// refuse the gate.
func ReadIRQCounters(procRoot string) (IRQCounters, error) {
	path := filepath.Join(procRoot, "proc", "interrupts")
	if procRoot == "" || procRoot == "/" {
		path = "/proc/interrupts"
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	out := make(IRQCounters)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	// First line is the CPU header — skip.
	if !scanner.Scan() {
		return out, nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// First field is the IRQ identifier with a trailing colon.
		irq := strings.TrimSuffix(fields[0], ":")
		if irq == fields[0] {
			// No colon — malformed line, skip.
			continue
		}
		var total uint64
		for _, f := range fields[1:] {
			n, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				// Non-numeric field — start of label column. Stop summing.
				break
			}
			total += n
		}
		out[irq] = total
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return out, nil
}

// IsInputIRQ reports whether the IRQ named by id is human-input. Reads
// /sys/kernel/irq/<id>/actions when available; falls back to the trailing
// label column of /proc/interrupts when it's not.
func IsInputIRQ(sysRoot, procRoot, id string) bool {
	candidates := readIRQActions(sysRoot, id)
	if len(candidates) == 0 {
		candidates = readIRQLabelFromInterrupts(procRoot, id)
	}
	for _, c := range candidates {
		c = strings.ToLower(c)
		for _, kw := range inputIRQClassifierKeywords {
			if strings.Contains(c, kw) {
				return true
			}
		}
	}
	return false
}

// readIRQActions reads /sys/kernel/irq/<id>/actions and returns the
// comma-separated driver names. Empty slice on error or missing file.
func readIRQActions(sysRoot, id string) []string {
	path := filepath.Join(sysRoot, "sys", "kernel", "irq", id, "actions")
	if sysRoot == "" || sysRoot == "/" {
		path = filepath.Join("/sys/kernel/irq", id, "actions")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// readIRQLabelFromInterrupts reads /proc/interrupts and extracts the
// trailing label column for the given IRQ id. Used as a fallback when
// /sys/kernel/irq/<id>/actions is missing.
func readIRQLabelFromInterrupts(procRoot, id string) []string {
	path := filepath.Join(procRoot, "proc", "interrupts")
	if procRoot == "" || procRoot == "/" {
		path = "/proc/interrupts"
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if !scanner.Scan() {
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, id+":") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil
		}
		// Walk past the numeric per-CPU columns; the rest is the label.
		labelIdx := 1
		for ; labelIdx < len(fields); labelIdx++ {
			if _, err := strconv.ParseUint(fields[labelIdx], 10, 64); err != nil {
				break
			}
		}
		if labelIdx >= len(fields) {
			return nil
		}
		return strings.Fields(strings.Join(fields[labelIdx:], " "))
	}
	return nil
}

// HasRecentInputActivity compares two IRQ counter snapshots and returns
// true when any input-classified IRQ has a non-zero delta. Used by
// OpportunisticGate to refuse on /proc/interrupts evidence of recent
// keyboard / mouse / USB input activity (RULE-OPP-IDLE-02).
func HasRecentInputActivity(prev, cur IRQCounters, sysRoot, procRoot string) (bool, string) {
	for id, n := range cur {
		p, ok := prev[id]
		if !ok {
			continue
		}
		if n <= p {
			continue
		}
		if IsInputIRQ(sysRoot, procRoot, id) {
			return true, id
		}
	}
	return false, ""
}

// SSHSession describes a single loginctl session row that's relevant to
// the OpportunisticGate decision.
type SSHSession struct {
	ID          string
	Remote      bool
	Active      bool
	IdleSeconds int64 // -1 when unknown
}

// loginctlExec runs `loginctl list-sessions --output=json` and returns
// the raw stdout. Tests inject a fake by passing their own output via
// HasRecentSSHActivityFromOutput.
func loginctlExec() (string, error) {
	if _, err := exec.LookPath("loginctl"); err != nil {
		return "", errors.New("loginctl not on PATH")
	}
	cmd := exec.Command("loginctl", "list-sessions", "--output=json")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("loginctl list-sessions: %w", err)
	}
	return string(out), nil
}

// HasRecentSSHActivity returns true when any current loginctl session is
// Remote=yes, Active=yes, and idle for less than idleThresholdSeconds.
// Long-idle SSH sessions (e.g. tmux attach left running) do NOT trigger
// the gate (RULE-OPP-IDLE-03).
//
// On loginctl-binary-missing or unparseable output, returns false +
// nil error: a desktop without systemd-logind is presumed not to have
// active SSH sessions, and the gate is otherwise responsible for its
// other refusal signals.
func HasRecentSSHActivity(idleThresholdSeconds int64) (bool, string) {
	out, err := loginctlExec()
	if err != nil {
		return false, ""
	}
	return parseLoginctlForActiveRemote(out, idleThresholdSeconds)
}

// HasRecentSSHActivityFromOutput is the test seam: parses canned
// loginctl JSON output for the same predicate.
func HasRecentSSHActivityFromOutput(output string, idleThresholdSeconds int64) (bool, string) {
	return parseLoginctlForActiveRemote(output, idleThresholdSeconds)
}

// parseLoginctlForActiveRemote walks a loginctl `--output=json` array.
// The schema is loosely versioned by systemd; we look for the keys we
// need and tolerate missing fields.
func parseLoginctlForActiveRemote(output string, idleThresholdSeconds int64) (bool, string) {
	// Hand-rolled JSON walking to avoid pulling in encoding/json's reflection
	// path for what is a tiny, well-shaped fixed schema. loginctl emits an
	// array of objects with stable lowercase keys.
	//
	// Example:
	//   [{"session":"3","uid":1000,"user":"phoenix","seat":null,"tty":null,"idle":false,"state":"active","remote":true,"idle-since":null}]
	dec := strings.NewReader(output)
	scanner := bufio.NewScanner(dec)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var blob strings.Builder
	for scanner.Scan() {
		blob.WriteString(scanner.Text())
	}
	body := blob.String()
	if !strings.Contains(body, "{") {
		return false, ""
	}
	// Split into per-object substrings on `},{` boundaries. Crude but
	// adequate for loginctl's flat schema.
	body = strings.TrimSpace(body)
	body = strings.TrimPrefix(body, "[")
	body = strings.TrimSuffix(body, "]")
	objs := splitJSONObjects(body)
	for _, o := range objs {
		s := parseLoginctlObject(o)
		if !s.Remote || !s.Active {
			continue
		}
		if s.IdleSeconds >= 0 && s.IdleSeconds > idleThresholdSeconds {
			continue
		}
		return true, s.ID
	}
	return false, ""
}

// splitJSONObjects splits a flat array body (without outer brackets)
// into substrings each containing one object. Tolerates objects without
// nested arrays (loginctl's schema is flat).
func splitJSONObjects(body string) []string {
	var out []string
	depth := 0
	start := -1
	for i, c := range body {
		switch c {
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			depth--
			if depth == 0 && start >= 0 {
				out = append(out, body[start:i+1])
				start = -1
			}
		}
	}
	return out
}

// parseLoginctlObject pulls the fields of interest out of a single
// loginctl JSON object substring.
func parseLoginctlObject(obj string) SSHSession {
	s := SSHSession{IdleSeconds: -1}
	s.ID = jsonStringField(obj, "session")
	s.Active = jsonStringField(obj, "state") == "active"
	s.Remote = jsonBoolField(obj, "remote")
	// idle-since is RFC3339 or null; we don't compute exact deltas here —
	// the kernel-cgroup-pressure path already debounces; loginctl's idle
	// flag is enough for the boolean predicate.
	if jsonBoolField(obj, "idle") {
		s.IdleSeconds = idleThresholdInfinity
	} else {
		s.IdleSeconds = 0
	}
	return s
}

// idleThresholdInfinity represents "loginctl reports session as idle"
// — large enough that it always exceeds any threshold a caller passes.
const idleThresholdInfinity int64 = 1 << 30

func jsonStringField(obj, key string) string {
	tag := fmt.Sprintf(`"%s":"`, key)
	idx := strings.Index(obj, tag)
	if idx < 0 {
		return ""
	}
	rest := obj[idx+len(tag):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func jsonBoolField(obj, key string) bool {
	tag := fmt.Sprintf(`"%s":`, key)
	idx := strings.Index(obj, tag)
	if idx < 0 {
		return false
	}
	rest := obj[idx+len(tag):]
	rest = strings.TrimLeft(rest, " ")
	return strings.HasPrefix(rest, "true")
}
