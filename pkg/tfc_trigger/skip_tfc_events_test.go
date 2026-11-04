package tfc_trigger_test

import (
	"context"
	"testing"

	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.uber.org/mock/gomock"
)

const skipPlanTestReason = "skipped: merge request has a skip-auto-plan label"

// captureAllStatuses records every status the trigger posts, in call order.
// Unlike captureStatuses (workspace_status_test.go), which keys by workspace
// name alone, this keeps every call: SkipTFCEvents posts two statuses (plan
// and apply) for the same workspace, which a workspace-keyed map would
// collapse into one. Declared before InitTestSuite for the same reason
// captureStatuses is: gomock returns the earliest declared non-exhausted
// expectation, so InitTestSuite's AnyTimes() default would otherwise swallow
// every call.
func captureAllStatuses(ts *mocks.TestSuite) *[]vcs.WorkspaceStatus {
	var seen []vcs.WorkspaceStatus
	ts.MockGitClient.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s vcs.WorkspaceStatus) error {
			seen = append(seen, s)
			return nil
		}).AnyTimes()
	return &seen
}

// A skip-plan label must not let a brand-new workspace merge in having never
// been planned: SkipTFCEvents has to discover the same workspaces
// TriggerTFCEvents would have, and post a pending status for both the plan
// and apply actions, without ever creating a run.
func TestSkipTFCEvents_PostsPendingPlanAndApplyPerWorkspace(t *testing.T) {
	ws := &tfc_trigger.ProjectConfig{Workspaces: []*tfc_trigger.TFCWorkspace{{
		Name: "service-tfbuddy", Organization: "zapier-test", Mode: "apply-before-merge",
	}}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)
	seen := captureAllStatuses(testSuite)
	// SkipTFCEvents must never clone the repo: cloning exists to run
	// Terraform locally, and this path never does.
	testSuite.MockGitClient.EXPECT().CloneMergeRequest(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	testSuite.InitTestSuite()
	// MockApiClient has no CreateRunFromSource expectation configured
	// anywhere in this test: a call to it fails the test, proving
	// SkipTFCEvents never creates a Terraform Cloud run.

	trigger := planTrigger(t, testSuite, tfc_trigger.PlanAction)
	result, err := trigger.SkipTFCEvents(context.Background(), skipPlanTestReason)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errored) > 0 {
		t.Fatalf("unexpected errored workspaces: %+v", result.Errored)
	}
	if len(result.Executed) != 1 || result.Executed[0] != "service-tfbuddy" {
		t.Fatalf("expected service-tfbuddy to be recorded as executed, got %+v", result.Executed)
	}

	byAction := map[string]vcs.WorkspaceStatus{}
	for _, s := range *seen {
		byAction[s.Action] = s
	}
	plan, ok := byAction["plan"]
	if !ok {
		t.Fatal("expected a plan status, got none")
	}
	if plan.State != vcs.CommitStatePending || plan.Workspace != "service-tfbuddy" || plan.Description != skipPlanTestReason {
		t.Errorf("plan status = %+v, want pending for service-tfbuddy with the skip reason", plan)
	}
	apply, ok := byAction["apply"]
	if !ok {
		t.Fatal("expected an apply status, got none")
	}
	if apply.State != vcs.CommitStatePending || apply.Workspace != "service-tfbuddy" || apply.Description != skipPlanTestReason {
		t.Errorf("apply status = %+v, want pending for service-tfbuddy with the skip reason", apply)
	}
}

// A workspace excluded by the allow/deny list must stay excluded even when
// skipping: it gets the same terminal "skipped: excluded" status the
// TriggerTFCEvents path already posts for it, not a pending plan/apply pair
// that implies it will eventually run.
func TestSkipTFCEvents_DeniedWorkspaceStaysExcluded(t *testing.T) {
	denyWorkspace(t, "zapier-test/service-tfbuddy")

	ws := &tfc_trigger.ProjectConfig{Workspaces: []*tfc_trigger.TFCWorkspace{{
		Name: "service-tfbuddy", Organization: "zapier-test", Mode: "apply-before-merge",
	}}}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	testSuite := mocks.CreateTestSuite(mockCtrl, mocks.TestOverrides{ProjectConfig: ws}, t)
	seen := captureAllStatuses(testSuite)
	testSuite.InitTestSuite()

	trigger := planTrigger(t, testSuite, tfc_trigger.PlanAction)
	result, err := trigger.SkipTFCEvents(context.Background(), skipPlanTestReason)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Executed) != 0 {
		t.Fatalf("denied workspace must not be recorded as executed, got %+v", result.Executed)
	}
	if len(result.Errored) != 1 {
		t.Fatalf("expected the denied workspace to be recorded as errored, got %+v", result.Errored)
	}

	if len(*seen) != 1 {
		t.Fatalf("expected exactly one status for the denied workspace, got %+v", *seen)
	}
	if got := (*seen)[0]; got.State != vcs.CommitStateSkipped {
		t.Errorf("state = %q, want skipped", got.State)
	}
}
