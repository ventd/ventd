package main

import (
	"strings"
	"testing"
)

func runCheck(t *testing.T, yaml string) (int, string) {
	t.Helper()
	var buf strings.Builder
	n := check("<test>", strings.NewReader(yaml), &buf)
	return n, buf.String()
}

func TestActionPinCheck_RefFormat(t *testing.T) {
	goodSHA := strings.Repeat("a", 40)
	tests := []struct {
		name     string
		yaml     string
		wantViol int
		wantRule string
	}{
		{
			name: "sha40_with_comment_passes",
			yaml: "steps:\n  - uses: actions/checkout@" + goodSHA + " # v4.1.7\n",
		},
		{
			name: "vtag_first_party_passes",
			yaml: "steps:\n  - uses: actions/checkout@v4\n",
		},
		{
			name:     "branch_main_fails",
			yaml:     "steps:\n  - uses: some/action@main\n",
			wantViol: 1, wantRule: "RULE-CI-01",
		},
		{
			name:     "branch_master_fails",
			yaml:     "steps:\n  - uses: some/action@master\n",
			wantViol: 1, wantRule: "RULE-CI-01",
		},
		{
			name:     "short_sha_7_fails",
			yaml:     "steps:\n  - uses: some/action@abc1234\n",
			wantViol: 1, wantRule: "RULE-CI-01",
		},
		{
			name:     "sha39_fails",
			yaml:     "steps:\n  - uses: some/action@" + strings.Repeat("a", 39) + "\n",
			wantViol: 1, wantRule: "RULE-CI-01",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, out := runCheck(t, tc.yaml)
			if got != tc.wantViol {
				t.Errorf("violations: got %d, want %d\noutput:\n%s", got, tc.wantViol, out)
			}
			if tc.wantRule != "" && !strings.Contains(out, tc.wantRule) {
				t.Errorf("expected %q in output, got:\n%s", tc.wantRule, out)
			}
		})
	}
}

func TestActionPinCheck_SHAHasVersionComment(t *testing.T) {
	sha := strings.Repeat("b", 40)
	tests := []struct {
		name     string
		yaml     string
		wantViol int
		wantRule string
	}{
		{
			name: "sha_with_semver_comment_passes",
			yaml: "steps:\n  - uses: third/party@" + sha + " # v1.2.3\n",
		},
		{
			name: "sha_with_prerelease_passes",
			yaml: "steps:\n  - uses: third/party@" + sha + " # v1.2.3-rc.1\n",
		},
		{
			name: "sha_with_extra_text_passes",
			yaml: "steps:\n  - uses: peter-evans/pr@" + sha + " # v7.0.11 extra info\n",
		},
		{
			name:     "sha_without_comment_fails",
			yaml:     "steps:\n  - uses: third/party@" + sha + "\n",
			wantViol: 1, wantRule: "RULE-CI-02",
		},
		{
			name:     "sha_major_only_comment_fails",
			yaml:     "steps:\n  - uses: third/party@" + sha + " # v4\n",
			wantViol: 1, wantRule: "RULE-CI-02",
		},
		{
			name:     "sha_major_minor_comment_fails",
			yaml:     "steps:\n  - uses: third/party@" + sha + " # v4.2\n",
			wantViol: 1, wantRule: "RULE-CI-02",
		},
		{
			name:     "sha_non_version_comment_fails",
			yaml:     "steps:\n  - uses: third/party@" + sha + " # some-tool\n",
			wantViol: 1, wantRule: "RULE-CI-02",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, out := runCheck(t, tc.yaml)
			if got != tc.wantViol {
				t.Errorf("violations: got %d, want %d\noutput:\n%s", got, tc.wantViol, out)
			}
			if tc.wantRule != "" && !strings.Contains(out, tc.wantRule) {
				t.Errorf("expected %q in output, got:\n%s", tc.wantRule, out)
			}
		})
	}
}

func TestActionPinCheck_AllowlistBoundary(t *testing.T) {
	sha := strings.Repeat("c", 40)
	tests := []struct {
		name     string
		yaml     string
		wantViol int
		wantRule string
	}{
		{
			name: "actions_vtag_passes",
			yaml: "steps:\n  - uses: actions/checkout@v4\n",
		},
		{
			name: "github_vtag_passes",
			yaml: "steps:\n  - uses: github/codeql-action@v3\n",
		},
		{
			name: "docker_vtag_passes",
			yaml: "steps:\n  - uses: docker/build-push-action@v6\n",
		},
		{
			name:     "third_party_vtag_fails",
			yaml:     "steps:\n  - uses: taiki-e/install-action@v2\n",
			wantViol: 1, wantRule: "RULE-CI-03",
		},
		{
			name:     "goreleaser_vtag_fails",
			yaml:     "steps:\n  - uses: goreleaser/goreleaser-action@v7\n",
			wantViol: 1, wantRule: "RULE-CI-03",
		},
		{
			name: "third_party_sha_passes",
			yaml: "steps:\n  - uses: goreleaser/goreleaser-action@" + sha + " # v7.0.0\n",
		},
		{
			name: "actions_v_major_minor_passes",
			yaml: "steps:\n  - uses: actions/setup-go@v5.1\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, out := runCheck(t, tc.yaml)
			if got != tc.wantViol {
				t.Errorf("violations: got %d, want %d\noutput:\n%s", got, tc.wantViol, out)
			}
			if tc.wantRule != "" && !strings.Contains(out, tc.wantRule) {
				t.Errorf("expected %q in output, got:\n%s", tc.wantRule, out)
			}
		})
	}
}

func TestActionPinCheck_EdgeCases(t *testing.T) {
	sha := strings.Repeat("d", 40)

	t.Run("matrix_template_skipped", func(t *testing.T) {
		got, _ := runCheck(t, "steps:\n  - uses: ${{ matrix.action }}\n")
		if got != 0 {
			t.Errorf("matrix template should be skipped, got %d violations", got)
		}
	})

	t.Run("local_action_skipped", func(t *testing.T) {
		got, _ := runCheck(t, "steps:\n  - uses: ./.github/actions/my-action\n")
		if got != 0 {
			t.Errorf("local action (no @) should be skipped, got %d violations", got)
		}
	})

	t.Run("uses_inside_run_block_skipped", func(t *testing.T) {
		yaml := "steps:\n  - name: Example\n    run: |\n      uses: some/action@main\n  - uses: actions/checkout@" + sha + " # v4.1.7\n"
		got, _ := runCheck(t, yaml)
		if got != 0 {
			t.Errorf("uses inside run: | block should be skipped, got %d violations", got)
		}
	})

	t.Run("reusable_workflow_with_path_and_sha", func(t *testing.T) {
		yaml := "jobs:\n  call:\n    uses: org/repo/.github/workflows/wf.yml@" + sha + " # v1.0.0\n"
		got, _ := runCheck(t, yaml)
		if got != 0 {
			t.Errorf("reusable workflow with SHA+comment should pass, got %d violations", got)
		}
	})

	t.Run("multiple_violations_counted", func(t *testing.T) {
		yaml := "steps:\n  - uses: third/a@main\n  - uses: third/b@main\n"
		got, _ := runCheck(t, yaml)
		if got != 2 {
			t.Errorf("want 2 violations, got %d", got)
		}
	})
}
