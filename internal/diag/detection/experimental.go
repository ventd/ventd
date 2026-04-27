package detection

import (
	"encoding/json"

	"github.com/ventd/ventd/internal/experimental"
)

// CollectExperimental encodes the experimental feature snapshot into a single
// JSON item for inclusion in the diagnostic bundle.
func CollectExperimental(flags experimental.Flags) CollectResult {
	snap := experimental.Snapshot(flags)
	data, _ := json.MarshalIndent(snap, "", "  ")
	return CollectResult{
		Items: []Item{{
			Path:    "experimental-flags.json",
			Content: data,
			Schema:  "application/json",
		}},
	}
}
