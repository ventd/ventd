package detection

import (
	"context"
)

// CollectJournal gathers ventd journal entries and filtered kernel logs (§12.7).
// Per §15.5: two separate files — dmesg-filtered.txt and kernel-ring.txt.
func CollectJournal(ctx context.Context) CollectResult {
	var res CollectResult
	add := func(item Item) { res.Items = append(res.Items, item) }
	miss := func(m *MissingTool) {
		if m != nil {
			res.MissingTools = append(res.MissingTools, *m)
		}
	}

	// ventd unit journal — last 24h as NDJSON.
	if out, m := runCmd(ctx, "journalctl",
		"-u", "ventd.service",
		"--since", "24 hours ago",
		"-o", "json",
		"--no-pager",
	); m != nil {
		miss(m)
	} else {
		add(Item{
			Path:    "commands/journal/ventd-tail.ndjson",
			Content: out,
			Schema:  "ventd-journal-v1",
		})
		add(symlinkItem("journal", "commands/journal/ventd-tail.ndjson"))
	}

	// dmesg filtered to error/warn — requires CAP_SYSLOG on many distros.
	if out, m := runCmd(ctx, "dmesg", "--ctime", "--level=err,warn,crit,alert,emerg"); m != nil {
		// Graceful skip — EPERM is expected for unprivileged users.
		res.MissingTools = append(res.MissingTools, MissingTool{
			Name:   "dmesg-unprivileged",
			Reason: m.Reason,
		})
	} else {
		add(textItem("commands/journal/dmesg-filtered.txt", filterDmesg(out)))
	}

	// journalctl -k — persisted kernel copy (works without CAP_SYSLOG).
	if out, m := runCmd(ctx, "journalctl", "-k", "-b", "0",
		"--since", "24 hours ago",
		"--no-pager",
	); m != nil {
		miss(m)
	} else {
		add(textItem("commands/journal/kernel-ring.txt", out))
	}

	return res
}

// filterDmesg keeps only lines mentioning fan-controller-relevant modules.
func filterDmesg(raw []byte) []byte {
	keywords := []string{
		"nct", "it87", "fintek", "hwmon", "fan", "thermal",
		"amdgpu", "nvidia", "corsair", "ventd",
	}
	var out []byte
	for _, line := range splitLines(raw) {
		lower := toLower(line)
		for _, kw := range keywords {
			if contains(lower, kw) {
				out = append(out, line...)
				out = append(out, '\n')
				break
			}
		}
	}
	return out
}
