package experimental

import (
	"fmt"

	"github.com/ventd/ventd/internal/hwdiag"
)

// Publish writes one hwdiag.Entry per active flag into store under
// ComponentExperimental. Inactive flags are not written. Callers should invoke
// this after Merge so the store always reflects the resolved active set.
func Publish(store *hwdiag.Store, flags Flags) {
	for _, name := range flags.Active() {
		p := Check(name)
		summary := fmt.Sprintf("experimental feature enabled: %s", name)
		detail := p.Detail
		store.Set(hwdiag.Entry{
			ID:        "experimental." + name,
			Component: hwdiag.ComponentExperimental,
			Severity:  hwdiag.SeverityInfo,
			Summary:   summary,
			Detail:    detail,
		})
	}
}
