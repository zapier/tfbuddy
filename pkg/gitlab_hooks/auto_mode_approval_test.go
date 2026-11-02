package gitlab_hooks

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.uber.org/mock/gomock"
)

const autoModeTargetConfig = `
autoMode:
  authors:
    - username: tags-bot
      commitEmails: [tags-bot@example.com]
  classes:
    - name: tags
      authors: [tags-bot]
      paths: ["**/tags.auto.tfvars.json"]
`

var (
	headPushedAt = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	afterPush    = headPushedAt.Add(time.Minute)
	beforePush   = headPushedAt.Add(-time.Minute)
)

func approvedBy(at *time.Time) *vcs.MRApprovalState {
	return &vcs.MRApprovalState{Approved: true, ApprovedBy: []vcs.MRApproval{{Username: "approver-bot", ApprovedAt: at}}}
}

func headVersions() []vcs.MRVersion {
	return []vcs.MRVersion{
		{HeadSHA: gateCommitSHA, CreatedAt: headPushedAt},
		{HeadSHA: "older", CreatedAt: headPushedAt.Add(-time.Hour)},
	}
}

func TestApprovalAfterPushBlocker(t *testing.T) {
	tests := []struct {
		name      string
		approvals *vcs.MRApprovalState
		versions  []vcs.MRVersion
		want      string
	}{
		{name: "approved after the head was pushed", approvals: approvedBy(&afterPush), versions: headVersions()},
		{
			name:      "versions in any order",
			approvals: approvedBy(&afterPush),
			versions:  []vcs.MRVersion{headVersions()[1], headVersions()[0]},
		},
		{name: "approved before the head was pushed", approvals: approvedBy(&beforePush), versions: headVersions(), want: "before the head commit was pushed"},
		{name: "approved in the same instant as the push", approvals: approvedBy(&headPushedAt), versions: headVersions(), want: "before the head commit was pushed"},
		{name: "no approval time", approvals: approvedBy(nil), versions: headVersions(), want: "before the head commit was pushed"},
		{
			name: "one stale approval among fresh ones",
			approvals: &vcs.MRApprovalState{Approved: true, ApprovedBy: []vcs.MRApproval{
				{Username: "fresh", ApprovedAt: &afterPush}, {Username: "stale", ApprovedAt: &beforePush},
			}},
			versions: headVersions(),
			want:     "approval by stale",
		},
		{
			name:      "a newer push than the approved commit",
			approvals: approvedBy(&afterPush),
			versions:  append(headVersions(), vcs.MRVersion{HeadSHA: "newer", CreatedAt: headPushedAt.Add(30 * time.Second)}),
			want:      "newer push",
		},
		{name: "not approved", approvals: &vcs.MRApprovalState{}, versions: headVersions(), want: "not approved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := approvalAfterPushBlocker(tt.approvals, tt.versions, gateCommitSHA)
			if tt.want == "" && got != "" {
				t.Fatalf("expected no blocker, got %q", got)
			}
			if !strings.Contains(got, tt.want) {
				t.Fatalf("blocker = %q, want it to mention %q", got, tt.want)
			}
		})
	}
}

func TestPlanStatusBlocker(t *testing.T) {
	green := []vcs.CommitJobStatus{
		fakeJobStatus{name: "TFC/plan/svc-a", status: "success"},
		fakeJobStatus{name: "TFC/plan/svc-b", status: "success"},
		fakeJobStatus{name: "TFC/apply/svc-a", status: "pending"},
		fakeJobStatus{name: vcs.ClassStatusName, status: "success"},
	}
	if got := planStatusBlocker(green); got != "" {
		t.Fatalf("expected green plans and class to pass, got %q", got)
	}

	tests := map[string]struct {
		statuses []vcs.CommitJobStatus
		want     string
	}{
		"no plans": {
			statuses: []vcs.CommitJobStatus{fakeJobStatus{name: vcs.ClassStatusName, status: "success"}},
			want:     "no plans",
		},
		"a plan still running": {
			statuses: []vcs.CommitJobStatus{
				fakeJobStatus{name: "TFC/plan/svc-a", status: "running"},
				fakeJobStatus{name: vcs.ClassStatusName, status: "success"},
			},
			want: "TFC/plan/svc-a (running)",
		},
		"class not set": {
			statuses: []vcs.CommitJobStatus{fakeJobStatus{name: "TFC/plan/svc-a", status: "success"}},
			want:     "TFC/class is not set",
		},
		"class failed": {
			statuses: []vcs.CommitJobStatus{
				fakeJobStatus{name: "TFC/plan/svc-a", status: "success"},
				fakeJobStatus{name: vcs.ClassStatusName, status: "failed"},
			},
			want: "TFC/class is failed",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := planStatusBlocker(tt.statuses); !strings.Contains(got, tt.want) || got == "" {
				t.Fatalf("blocker = %q, want it to mention %q", got, tt.want)
			}
		})
	}
}

// autoModeFixture drives processMergeRequestEvent with an "approved" event.
type autoModeFixture struct {
	ctrl    *gomock.Controller
	worker  *GitlabEventWorker
	client  *mocks.MockGitClient
	mr      *mocks.MockDetailedMR
	trigger *mocks.MockTrigger
	// created holds the options of every trigger the worker built.
	created []*tfc_trigger.TFCTriggerOptions
}

func newAutoModeFixture(t *testing.T, autoModeEnabled bool) *autoModeFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := &autoModeFixture{
		ctrl:    ctrl,
		client:  mocks.NewMockGitClient(ctrl),
		mr:      mocks.NewMockDetailedMR(ctrl),
		trigger: mocks.NewMockTrigger(ctrl),
	}
	cfg := config.C
	cfg.AutoModeEnabled = autoModeEnabled
	cfg.GitlabProjectAllowList = []string{"zapier/"}
	f.worker = &GitlabEventWorker{
		cfg: cfg,
		gl:  f.client,
		triggerCreation: func(_ config.Config, _ vcs.GitClient, _ tfc_api.ApiClient, _ runstream.StreamClient, opts *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
			f.created = append(f.created, opts)
			return f.trigger
		},
	}
	return f
}

// expectMR makes the API's MR an open, mergeable one by author, with the
// Auto Mode config on its target branch.
func (f *autoModeFixture) expectMR(author string) {
	a := mocks.NewMockMRAuthor(f.ctrl)
	a.EXPECT().GetUsername().Return(author).AnyTimes()
	f.mr.EXPECT().GetAuthor().Return(a).AnyTimes()
	f.mr.EXPECT().GetTargetBranch().Return("main").AnyTimes()
	f.mr.EXPECT().GetSourceBranch().Return("tags-bot/tags").AnyTimes()
	f.mr.EXPECT().GetInternalID().Return(gateMRIID).AnyTimes()
	f.mr.EXPECT().GetState().Return("opened").AnyTimes()
	f.mr.EXPECT().IsDraft().Return(false).AnyTimes()
	f.mr.EXPECT().HasConflicts().Return(false).AnyTimes()
	f.client.EXPECT().GetMergeRequest(gomock.Any(), gateMRIID, gateProject).Return(f.mr, nil)
	f.client.EXPECT().GetRepoFile(gomock.Any(), gateProject, ".tfbuddy.yaml", "main").Return([]byte(autoModeTargetConfig), nil)
}

// expectGates answers every gate the approval checks.
func (f *autoModeFixture) expectGates(approvedAt time.Time, buildStatus string) {
	f.client.EXPECT().GetMergeRequestApprovalState(gomock.Any(), gateMRIID, gateProject).Return(approvedBy(&approvedAt), nil)
	f.client.EXPECT().GetMergeRequestVersions(gomock.Any(), gateMRIID, gateProject).Return(headVersions(), nil)
	f.client.EXPECT().GetCommitJobStatuses(gomock.Any(), gateProject, gateCommitSHA).Return([]vcs.CommitJobStatus{
		fakeJobStatus{name: "TFC/plan/svc-a", status: "success", pipelineID: 900},
		fakeJobStatus{name: vcs.ClassStatusName, status: "success", pipelineID: 900},
		fakeJobStatus{name: "aa:code-review", status: buildStatus, pipelineID: 900},
	}, nil).AnyTimes()
	f.client.EXPECT().GetPipelinesForCommit(gomock.Any(), gateProject, gateCommitSHA).Return([]vcs.ProjectPipeline{
		fakePipeline{id: 900, source: vcs.PipelineSourceMergeRequestEvent, status: "running"},
	}, nil).AnyTimes()
}

func mergeEventMsg(action string) *MergeRequestEventMsg {
	event := &gogitlab.MergeEvent{}
	event.Project.PathWithNamespace = gateProject
	event.ObjectAttributes.IID = gateMRIID
	event.ObjectAttributes.Action = action
	event.ObjectAttributes.SourceBranch = "tags-bot/tags"
	event.ObjectAttributes.LastCommit.ID = gateCommitSHA
	event.ObjectAttributes.Source = &gogitlab.Repository{PathWithNamespace: gateProject}
	return &MergeRequestEventMsg{Payload: event, DeliveryID: "delivery-1", Context: context.Background()}
}

func TestAutoModeApproval_StartsApplyForAutoModeMR(t *testing.T) {
	f := newAutoModeFixture(t, true)
	f.expectMR("tags-bot")
	f.expectGates(afterPush, "success")
	f.trigger.EXPECT().TriggerTFCEvents(gomock.Any()).Return(&tfc_trigger.TriggeredTFCWorkspaces{Executed: []string{"svc-a"}}, nil)

	if _, err := f.worker.processMergeRequestEvent(mergeEventMsg("approved")); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 1 {
		t.Fatalf("expected one trigger, got %d", len(f.created))
	}
	opts := f.created[0]
	if opts.Action != tfc_trigger.ApplyAction || opts.CommitSHA != gateCommitSHA || opts.DeliveryID != "delivery-1" ||
		opts.MergeRequestIID != gateMRIID || opts.VcsProvider != "gitlab" {
		t.Errorf("unexpected trigger options: %+v", opts)
	}
}

func TestAutoModeApproval_DoesNotApply(t *testing.T) {
	tests := []struct {
		name            string
		autoModeEnabled bool
		action          string
		// setup registers the only GitLab calls the case may make.
		setup       func(f *autoModeFixture)
		wantComment string
	}{
		{
			name:            "Auto Mode is off",
			autoModeEnabled: false,
			action:          "approved",
			setup:           func(f *autoModeFixture) {},
		},
		{
			name:            "a person's MR",
			autoModeEnabled: true,
			action:          "approved",
			setup:           func(f *autoModeFixture) { f.expectMR("a-person") },
		},
		{
			name:            "a single approval, not the MR's full approval",
			autoModeEnabled: true,
			action:          "approval",
			setup:           func(f *autoModeFixture) {},
		},
		{
			name:            "approved before the head was pushed",
			autoModeEnabled: true,
			action:          "approved",
			setup: func(f *autoModeFixture) {
				f.expectMR("tags-bot")
				f.expectGates(beforePush, "success")
			},
			wantComment: "before the head commit was pushed",
		},
		{
			name:            "the pipeline has not succeeded",
			autoModeEnabled: true,
			action:          "approved",
			setup: func(f *autoModeFixture) {
				f.expectMR("tags-bot")
				f.expectGates(afterPush, "running")
			},
			wantComment: "still waiting on: aa:code-review (running)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAutoModeFixture(t, tt.autoModeEnabled)
			tt.setup(f)
			if tt.wantComment != "" {
				f.client.EXPECT().CreateMergeRequestComment(gomock.Any(), gateMRIID, gateProject, gomock.Any()).
					DoAndReturn(func(_ context.Context, _ int, _ string, msg string) error {
						if !strings.Contains(msg, tt.wantComment) {
							t.Errorf("comment = %q, want it to mention %q", msg, tt.wantComment)
						}
						return nil
					})
			}

			if _, err := f.worker.processMergeRequestEvent(mergeEventMsg(tt.action)); err != nil {
				t.Fatal(err)
			}
			if len(f.created) != 0 {
				t.Fatalf("expected no apply, but a trigger was built: %+v", f.created[0])
			}
		})
	}
}
