package comment_formatter

import (
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/go-tfe"
	"github.com/stretchr/testify/assert"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"go.uber.org/mock/gomock"
)

func TestGetProperApplyText(t *testing.T) {
	testCases := []struct {
		name      string
		autoMerge bool
		workspace string
	}{
		{
			name:      "with auto merge",
			autoMerge: true,
			workspace: "test-workspace",
		},
		{
			name:      "without auto merge",
			autoMerge: false,
			workspace: "test-workspace",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockRunMeta := mocks.NewMockRunMetadata(ctrl)
			mockRunMeta.EXPECT().GetAutoMerge().Return(tc.autoMerge)

			result := getProperApplyText(mockRunMeta, tc.workspace)

			// Verify the result contains the workspace name
			assert.Contains(t, result, tc.workspace)

			// Verify the correct merge snippet is included based on autoMerge
			if tc.autoMerge {
				assert.Contains(t, result, autoMRMergeSnippet)
			} else {
				assert.Contains(t, result, manualMRMergeSnippet)
			}
		})
	}
}

func TestGetProperTargetedApplyText(t *testing.T) {
	testCases := []struct {
		name      string
		autoMerge bool
		workspace string
		targets   []string
	}{
		{
			name:      "with auto merge single target",
			autoMerge: true,
			workspace: "test-workspace",
			targets:   []string{"module.test"},
		},
		{
			name:      "without auto merge single target",
			autoMerge: false,
			workspace: "test-workspace",
			targets:   []string{"module.test"},
		},
		{
			name:      "with multiple targets",
			autoMerge: true,
			workspace: "test-workspace",
			targets:   []string{"module.test1", "module.test2"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockRunMeta := mocks.NewMockRunMetadata(ctrl)
			mockRunMeta.EXPECT().GetAutoMerge().Return(tc.autoMerge)

			run := &tfe.Run{
				TargetAddrs: tc.targets,
			}

			result := getProperTargetedApplyText(mockRunMeta, run, tc.workspace)

			// Verify the result contains the workspace name
			assert.Contains(t, result, tc.workspace)

			// Verify the targets are included in the result
			targetsJoined := strings.Join(tc.targets, ",")
			assert.Contains(t, result, targetsJoined)

			// Verify the correct merge snippet is included based on autoMerge
			if tc.autoMerge {
				assert.Contains(t, result, autoMRMergeSnippet)
			} else {
				assert.Contains(t, result, manualMRMergeSnippet)
			}
		})
	}
}

func TestHasChanges(t *testing.T) {
	testCases := []struct {
		name     string
		plan     *tfe.Plan
		expected bool
	}{
		{
			name: "with additions",
			plan: &tfe.Plan{
				ResourceAdditions:    1,
				ResourceChanges:      0,
				ResourceDestructions: 0,
			},
			expected: true,
		},
		{
			name: "with changes",
			plan: &tfe.Plan{
				ResourceAdditions:    0,
				ResourceChanges:      1,
				ResourceDestructions: 0,
			},
			expected: true,
		},
		{
			name: "with destructions",
			plan: &tfe.Plan{
				ResourceAdditions:    0,
				ResourceChanges:      0,
				ResourceDestructions: 1,
			},
			expected: true,
		},
		{
			name: "with no changes",
			plan: &tfe.Plan{
				ResourceAdditions:    0,
				ResourceChanges:      0,
				ResourceDestructions: 0,
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := hasChanges(tc.plan)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestFormatRunStatusCommentBody(t *testing.T) {
	testCases := []struct {
		name                 string
		runStatus            tfe.RunStatus
		targetAddrs          []string
		autoApply            bool
		runAction            string
		autoMerge            bool
		planOutput           []byte
		planOutputErr        error
		expectedResolve      bool
		expectedExtraInfo    string
		positionInQueue      int
		resourceAdditions    int
		resourceChanges      int
		resourceDestructions int
		resourceImports      int
	}{
		{
			name:              "Pending with position in queue",
			runStatus:         tfe.RunPending,
			positionInQueue:   3,
			runAction:         "plan",
			expectedResolve:   false,
			expectedExtraInfo: "Position in Queue: 3",
		},
		{
			name:              "Applying",
			runStatus:         tfe.RunApplying,
			runAction:         "apply",
			expectedResolve:   false,
			expectedExtraInfo: "",
		},
		{
			name:                 "Applied with target addresses",
			runStatus:            tfe.RunApplied,
			targetAddrs:          []string{"module.resource"},
			autoMerge:            true,
			runAction:            "apply",
			expectedResolve:      false,
			resourceAdditions:    1,
			resourceChanges:      2,
			resourceDestructions: 3,
			resourceImports:      4,
			expectedExtraInfo:    "**Need to Apply Full Workspace Before Merging**",
		},
		{
			name:                 "Applied without target addresses",
			runStatus:            tfe.RunApplied,
			targetAddrs:          []string{},
			autoMerge:            false,
			runAction:            "apply",
			expectedResolve:      true,
			resourceAdditions:    1,
			resourceChanges:      2,
			resourceDestructions: 3,
			resourceImports:      4,
		},
		{
			name:              "Discarded",
			runStatus:         tfe.RunDiscarded,
			runAction:         "plan",
			expectedResolve:   false,
			expectedExtraInfo: "",
		},
		{
			name:              "Errored with plan action",
			runStatus:         tfe.RunErrored,
			runAction:         runstream.PlanAction,
			expectedResolve:   false,
			expectedExtraInfo: failedPlanSummaryFormat,
		},
		{
			name:              "Planning with auto apply",
			runStatus:         tfe.RunPlanning,
			autoApply:         true,
			runAction:         "plan",
			expectedResolve:   false,
			expectedExtraInfo: "Auto Apply Enabled - plan will automatically Apply if it passes policy checks.",
		},
		{
			name:                 "Planned with auto apply",
			runStatus:            tfe.RunPlanned,
			autoApply:            true,
			runAction:            "plan",
			expectedResolve:      false,
			resourceAdditions:    1,
			resourceChanges:      2,
			resourceDestructions: 3,
			resourceImports:      4,
		},
		{
			name:                 "Planned without auto apply and with targets",
			runStatus:            tfe.RunPlanned,
			autoApply:            false,
			targetAddrs:          []string{"module.resource"},
			autoMerge:            false,
			runAction:            "plan",
			expectedResolve:      false,
			resourceAdditions:    1,
			resourceChanges:      2,
			resourceDestructions: 3,
			resourceImports:      4,
			expectedExtraInfo:    "module.resource",
		},
		{
			name:                 "Planned and Finished with changes and targets",
			runStatus:            tfe.RunPlannedAndFinished,
			targetAddrs:          []string{"module.resource"},
			planOutput:           []byte(`{"json": "data"}`),
			planOutputErr:        nil,
			autoMerge:            true,
			runAction:            "plan",
			expectedResolve:      false,
			resourceAdditions:    1,
			resourceChanges:      0,
			resourceDestructions: 0,
		},
		{
			name:                 "Planned and Finished without changes and with targets",
			runStatus:            tfe.RunPlannedAndFinished,
			targetAddrs:          []string{"module.resource"},
			planOutput:           []byte(`{"json": "data"}`),
			planOutputErr:        nil,
			autoMerge:            false,
			runAction:            "plan",
			expectedResolve:      false,
			resourceAdditions:    0,
			resourceChanges:      0,
			resourceDestructions: 0,
			expectedExtraInfo:    "**Need to Apply Full Workspace Before Merging**",
		},
		{
			name:                 "Planned and Finished without changes and without targets",
			runStatus:            tfe.RunPlannedAndFinished,
			targetAddrs:          []string{},
			planOutput:           []byte(`{"json": "data"}`),
			planOutputErr:        nil,
			autoMerge:            false,
			runAction:            "plan",
			expectedResolve:      true,
			resourceAdditions:    0,
			resourceChanges:      0,
			resourceDestructions: 0,
		},
		{
			name:                 "Planned and Finished with error getting plan output",
			runStatus:            tfe.RunPlannedAndFinished,
			planOutput:           nil,
			planOutputErr:        errors.New("failed to get plan"),
			autoMerge:            false,
			runAction:            "plan",
			expectedResolve:      false,
			resourceAdditions:    1,
			resourceChanges:      0,
			resourceDestructions: 0,
		},
		{
			name:              "Policy Soft Failed",
			runStatus:         tfe.RunPolicySoftFailed,
			runAction:         "plan",
			expectedResolve:   false,
			expectedExtraInfo: "The plan has soft failed policy checks, please open TFC URL to approve.",
		},
		{
			name:              "Policy Checked without auto apply",
			runStatus:         tfe.RunPolicyChecked,
			autoApply:         false,
			runAction:         "plan",
			expectedResolve:   false,
			expectedExtraInfo: "Plan requires confirmation through the Terraform Cloud console.",
		},
		{
			name:              "Unknown status",
			runStatus:         "unknown-status",
			runAction:         "plan",
			expectedResolve:   false,
			expectedExtraInfo: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockApiClient := mocks.NewMockApiClient(ctrl)
			mockRunMeta := mocks.NewMockRunMetadata(ctrl)

			// Set up expectations for all methods that might be called
			mockRunMeta.EXPECT().GetAction().Return(tc.runAction).AnyTimes()
			mockRunMeta.EXPECT().GetAutoMerge().Return(tc.autoMerge).AnyTimes()

			if tc.runStatus == tfe.RunPlannedAndFinished {
				mockApiClient.EXPECT().GetPlanOutput(gomock.Any()).Return(tc.planOutput, tc.planOutputErr).AnyTimes()
			}

			// Setup the run object with all necessary fields
			run := &tfe.Run{
				ID:              "run-id",
				Status:          tc.runStatus,
				TargetAddrs:     tc.targetAddrs,
				AutoApply:       tc.autoApply,
				PositionInQueue: tc.positionInQueue,
				Workspace: &tfe.Workspace{
					Name: "test-workspace",
					Organization: &tfe.Organization{
						Name: "test-org",
					},
				},
			}

			// Setup plan and apply objects if needed
			if tc.runStatus == tfe.RunPlanned || tc.runStatus == tfe.RunApplied {
				run.Plan = &tfe.Plan{
					ID:                   "plan-id",
					ResourceAdditions:    tc.resourceAdditions,
					ResourceChanges:      tc.resourceChanges,
					ResourceDestructions: tc.resourceDestructions,
				}
				run.Apply = &tfe.Apply{
					ResourceImports:      tc.resourceImports,
					ResourceAdditions:    tc.resourceAdditions,
					ResourceChanges:      tc.resourceChanges,
					ResourceDestructions: tc.resourceDestructions,
				}
			}

			if tc.runStatus == tfe.RunPlannedAndFinished {
				run.Plan = &tfe.Plan{
					ID:                   "plan-id",
					ResourceAdditions:    tc.resourceAdditions,
					ResourceChanges:      tc.resourceChanges,
					ResourceDestructions: tc.resourceDestructions,
				}
			}

			extraInfo, topLevel, resolve := FormatRunStatusCommentBody(mockApiClient, run, mockRunMeta)

			// Verify the expected outcomes
			if tc.runStatus != "unknown-status" {
				assert.Contains(t, topLevel, "test-workspace")
				assert.Contains(t, topLevel, "Run URL")
				assert.Contains(t, topLevel, string(run.Status))
			}

			if tc.expectedExtraInfo != "" {
				assert.Contains(t, extraInfo, tc.expectedExtraInfo)
			}

			assert.Equal(t, tc.expectedResolve, resolve)
		})
	}
}
