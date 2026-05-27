package aggregator

import (
	"testing"

	"github.com/ventd/ventd/internal/marginal"
)

// TestConfCFromMarginal binds RULE-AGG-SIG-COLLAPSE-01 from the
// production side: conf_C is the active-signature shard's product term
// (ResidualTerm·CovarianceTerm·SampleCountTerm) when the saturation gate
// admits, and 0 otherwise — nil snapshot, still-warming shard, or gate
// closed. The sibling TestAggregator_ActiveSignatureCollapse covers the
// consumption side (conf_C = 0 ⇒ the LPF rides w_pred down); this covers
// the collapse arithmetic the caller must perform before Tick.
func TestConfCFromMarginal(t *testing.T) {
	t.Parallel()

	const wantProduct = 0.5 * 0.4 * 0.25
	admitted := marginal.ConfidenceComponents{
		SaturationAdmit: true,
		ResidualTerm:    0.5,
		CovarianceTerm:  0.4,
		SampleCountTerm: 0.25,
	}

	cases := []struct {
		name string
		snap *marginal.Snapshot
		want float64
	}{
		{"nil snapshot", nil, 0},
		{"warming up", &marginal.Snapshot{WarmingUp: true, Confidence: admitted}, 0},
		{"gate closed", &marginal.Snapshot{Confidence: marginal.ConfidenceComponents{
			SaturationAdmit: false, ResidualTerm: 0.5, CovarianceTerm: 0.4, SampleCountTerm: 0.25,
		}}, 0},
		{"admitted product", &marginal.Snapshot{Confidence: admitted}, wantProduct},
	}
	for _, tc := range cases {
		if got := ConfCFromMarginal(tc.snap); got != tc.want {
			t.Errorf("ConfCFromMarginal(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
