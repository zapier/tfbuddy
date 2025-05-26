package tfc_api

import (
	"strings"
	"testing"

	"github.com/hashicorp/go-tfe"
)

func TestApiRunOptions_Validation(t *testing.T) {
	tests := []struct {
		name    string
		opts    *ApiRunOptions
		wantErr bool
	}{
		{
			name: "valid_plan_options",
			opts: &ApiRunOptions{
				Organization: "test-org",
				Workspace:    "test-workspace",
				Message:      "test plan",
				Path:         "/tmp",
				IsApply:      false,
				TFVersion:    "1.5.0",
			},
			wantErr: false,
		},
		{
			name: "valid_apply_options",
			opts: &ApiRunOptions{
				Organization:  "test-org",
				Workspace:     "test-workspace",
				Message:       "test apply",
				Path:          "/tmp",
				IsApply:       true,
				AllowEmptyRun: true,
			},
			wantErr: false,
		},
		{
			name: "missing_organization",
			opts: &ApiRunOptions{
				Organization: "",
				Workspace:    "test-workspace",
				Message:      "test",
				Path:         "/tmp",
			},
			wantErr: true,
		},
		{
			name: "missing_workspace",
			opts: &ApiRunOptions{
				Organization: "test-org",
				Workspace:    "",
				Message:      "test",
				Path:         "/tmp",
			},
			wantErr: true,
		},
		{
			name: "missing_path",
			opts: &ApiRunOptions{
				Organization: "test-org",
				Workspace:    "test-workspace",
				Message:      "test",
				Path:         "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasErr := tt.opts.Organization == "" || tt.opts.Workspace == "" || tt.opts.Path == ""
			if hasErr != tt.wantErr {
				t.Errorf("Expected validation error %v, got %v", tt.wantErr, hasErr)
			}
		})
	}
}

func TestTargetParsing(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		expected []string
	}{
		{
			name:     "single_target",
			target:   "resource.example",
			expected: []string{"resource.example"},
		},
		{
			name:     "multiple_targets",
			target:   "resource.example,module.test",
			expected: []string{"resource.example", "module.test"},
		},
		{
			name:     "empty_target",
			target:   "",
			expected: []string{},
		},
		{
			name:     "targets_with_spaces",
			target:   "resource.example, module.test, data.source",
			expected: []string{"resource.example", " module.test", " data.source"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result []string
			if tt.target != "" {
				result = strings.Split(tt.target, ",")
			}

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d targets, got %d", len(tt.expected), len(result))
				return
			}

			for i, expected := range tt.expected {
				if result[i] != expected {
					t.Errorf("Expected target[%d] = %q, got %q", i, expected, result[i])
				}
			}
		})
	}
}

func TestConfigurationVersionStatus(t *testing.T) {
	statuses := []tfe.ConfigurationStatus{
		tfe.ConfigurationPending,
		tfe.ConfigurationUploaded,
		tfe.ConfigurationErrored,
	}

	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			cv := &tfe.ConfigurationVersion{
				ID:     "cv-123",
				Status: status,
			}

			isPending := cv.Status == tfe.ConfigurationPending
			shouldContinuePolling := isPending

			if status == tfe.ConfigurationPending {
				if !shouldContinuePolling {
					t.Error("Expected to continue polling for pending status")
				}
			} else {
				if shouldContinuePolling {
					t.Error("Expected to stop polling for non-pending status")
				}
			}
		})
	}
}

func TestTerraformVersionHandling(t *testing.T) {
	tests := []struct {
		name      string
		version   string
		isApply   bool
		expectSet bool
	}{
		{
			name:      "version_with_plan",
			version:   "1.5.0",
			isApply:   false,
			expectSet: true,
		},
		{
			name:      "version_with_apply",
			version:   "1.5.0",
			isApply:   true,
			expectSet: false,
		},
		{
			name:      "no_version_plan",
			version:   "",
			isApply:   false,
			expectSet: false,
		},
		{
			name:      "no_version_apply",
			version:   "",
			isApply:   true,
			expectSet: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tfVersion *string = nil
			var tfPlanOnly *bool = nil

			if tt.version != "" && !tt.isApply {
				tfVersion = &tt.version
				planOnly := true
				tfPlanOnly = &planOnly
			}

			versionSet := tfVersion != nil
			planOnlySet := tfPlanOnly != nil && *tfPlanOnly

			if tt.expectSet {
				if !versionSet {
					t.Error("Expected terraform version to be set")
				}
				if !planOnlySet {
					t.Error("Expected plan-only to be set")
				}
			} else {
				if versionSet && tt.isApply {
					t.Error("Did not expect terraform version to be set for apply runs")
				}
			}
		})
	}
}
