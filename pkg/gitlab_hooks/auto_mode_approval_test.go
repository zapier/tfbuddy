package gitlab_hooks

import (
	"context"
	"strings"
	"testing"

	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.uber.org/mock/gomock"
)

// autoModeSourceConfig is the MR's own .tfbuddy.yaml: one workspace that every
// change triggers.
const autoModeSourceConfig = `
workspaces:
  - name: svc-a
    organization: zapier-test
    mode: apply-before-merge
    autoMerge: true
    triggerDirs: ["**"]
`

const autoModeTargetConfig = `
autoMode:
  authors:
    - username: tags-bot
      commitEmails: [tags-bot@example.com]
  classes:
    - name: tags
      authors: [tags-bot]
      paths: ["**/tags.auto.tfvars.json"]
      workspaces: ["svc-*"]
`

func TestPlanStatusBlocker(t *testing.T) {
	green := []vcs.CommitJobStatus{
		fakeJobStatus{name: "TFC/plan/svc-a", status: "success"},
		fakeJobStatus{name: "TFC/plan/svc-b", status: "success"},
		fakeJobStatus{name: "TFC/apply/svc-a", status: "pending"},
		fakeJobStatus{name: vcs.ClassStatusName, status: "success"},
	}
	if got := planStatusBlocker(green, tfbuddyUsername); got != "" {
		t.Fatalf("expected green plans and class to pass, got %q", got)
	}
	// GitLab usernames are case-insensitive.
	if got := planStatusBlocker(green, "TFBuddy"); got != "" {
		t.Fatalf("expected TFBuddy's statuses to count whatever the case, got %q", got)
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
		"green statuses from someone else are ignored": {
			statuses: []vcs.CommitJobStatus{
				fakeJobStatus{name: "TFC/plan/svc-a", status: "success", author: "mallory"},
				fakeJobStatus{name: vcs.ClassStatusName, status: "success", author: "mallory"},
			},
			want: "TFC/class, TFC/plan/svc-a on the head commit was not posted by TFBuddy",
		},
		"a foreign green class does not replace a missing one": {
			statuses: []vcs.CommitJobStatus{
				fakeJobStatus{name: "TFC/plan/svc-a", status: "success"},
				fakeJobStatus{name: vcs.ClassStatusName, status: "success", author: "mallory"},
			},
			want: "TFC/class on the head commit was not posted by TFBuddy",
		},
		"a foreign plan for a workspace TFBuddy never planned": {
			statuses: []vcs.CommitJobStatus{
				fakeJobStatus{name: "TFC/plan/svc-a", status: "success"},
				fakeJobStatus{name: "TFC/plan/svc-b", status: "success", author: "mallory"},
				fakeJobStatus{name: vcs.ClassStatusName, status: "success"},
			},
			want: "TFC/plan/svc-b on the head commit was not posted by TFBuddy",
		},
		"TFBuddy's account is unknown": {
			statuses: green,
			want:     "account is unknown",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			account := tfbuddyUsername
			if name == "TFBuddy's account is unknown" {
				account = ""
			}
			if got := planStatusBlocker(tt.statuses, account); !strings.Contains(got, tt.want) || got == "" {
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
	stream  *mocks.MockStreamClient
	// changedFiles are the files the MR changes. They fit the tags class
	// unless a test sets others.
	changedFiles []string
	// targetConfig is the default branch's .tfbuddy.yaml; empty means
	// autoModeTargetConfig, which requires no CI job.
	targetConfig string
	// created holds the options of every trigger the worker built.
	created []*tfc_trigger.TFCTriggerOptions
}

func newAutoModeFixture(t *testing.T, autoModeEnabled bool) *autoModeFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := &autoModeFixture{
		ctrl:         ctrl,
		client:       mocks.NewMockGitClient(ctrl),
		mr:           mocks.NewMockDetailedMR(ctrl),
		trigger:      mocks.NewMockTrigger(ctrl),
		stream:       mocks.NewMockStreamClient(ctrl),
		changedFiles: []string{"services/api/prod/main-us-east-1/tags.auto.tfvars.json"},
	}
	cfg := config.C
	cfg.AutoModeEnabled = autoModeEnabled
	cfg.GitlabProjectAllowList = []string{"zapier/"}
	f.worker = &GitlabEventWorker{
		cfg:       cfg,
		gl:        f.client,
		runstream: f.stream,
		triggerCreation: func(_ config.Config, _ vcs.GitClient, _ tfc_api.ApiClient, _ runstream.StreamClient, opts *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
			f.created = append(f.created, opts)
			return f.trigger
		},
	}
	return f
}

// applies is the apply triggers the worker built. The MR event handler builds
// a plan trigger for every GitLab action before it looks at the action, so a
// built trigger alone does not mean anything ran.
func (f *autoModeFixture) applies() []*tfc_trigger.TFCTriggerOptions {
	var applies []*tfc_trigger.TFCTriggerOptions
	for _, opts := range f.created {
		if opts.Action == tfc_trigger.ApplyAction {
			applies = append(applies, opts)
		}
	}
	return applies
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
	f.mr.EXPECT().GetHeadSHA().Return(gateCommitSHA).AnyTimes()
	f.client.EXPECT().GetMergeRequest(gomock.Any(), gateMRIID, gateProject).Return(f.mr, nil)
	// An MR that fits no class says so in TFC/class.
	f.client.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	f.client.EXPECT().GetProjectSettings(gomock.Any(), gateProject).Return(fakeProjectSettings{}, nil).AnyTimes()
	target := f.targetConfig
	if target == "" {
		target = autoModeTargetConfig
	}
	f.client.EXPECT().GetRepoFile(gomock.Any(), gateProject, ".tfbuddy.yaml", "main").Return([]byte(target), nil).AnyTimes()
	f.client.EXPECT().GetRepoFile(gomock.Any(), gateProject, ".tfbuddy.yaml", "tags-bot/tags").Return([]byte(autoModeSourceConfig), nil).AnyTimes()
	f.client.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), gateMRIID, gateProject).Return(f.changedFiles, nil).AnyTimes()
	// Not in Manual Mode yet, and a fallback finds it already marked, unless
	// a test registered other answers first.
	f.stream.EXPECT().IsAutoModeManual(gomock.Any()).Return(false, "", nil).AnyTimes()
	f.stream.EXPECT().MarkAutoModeManual(gomock.Any(), gomock.Any()).Return(false, nil).AnyTimes()
	f.client.EXPECT().GetMergeRequestCommits(gomock.Any(), gateMRIID, gateProject).Return([]vcs.MRCommit{
		{SHA: gateCommitSHA, AuthorEmail: "tags-bot@example.com", CommitterEmail: "tags-bot@example.com"},
	}, nil).AnyTimes()
}

// expectGates answers every gate the approval checks.
func (f *autoModeFixture) expectGates(approved bool, buildStatus string) {
	f.client.EXPECT().GetAuthenticatedUsername(gomock.Any()).Return(tfbuddyUsername, nil).AnyTimes()
	approvals := mocks.NewMockMRApproved(f.ctrl)
	approvals.EXPECT().IsApproved().Return(approved).AnyTimes()
	f.client.EXPECT().GetMergeRequestApprovals(gomock.Any(), gateMRIID, gateProject).Return(approvals, nil)
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
	f.expectGates(true, "success")
	f.trigger.EXPECT().TriggerTFCEvents(gomock.Any()).Return(&tfc_trigger.TriggeredTFCWorkspaces{Executed: []string{"svc-a"}}, nil)

	if _, err := f.worker.processMergeRequestEvent(mergeEventMsg("approved")); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 1 {
		t.Fatalf("expected one trigger, got %d", len(f.created))
	}
	opts := f.created[0]
	if opts.Action != tfc_trigger.ApplyAction || opts.CommitSHA != gateCommitSHA || opts.DeliveryID != "delivery-1" ||
		opts.MergeRequestIID != gateMRIID || opts.VcsProvider != "gitlab" || !opts.AutoModeApproval {
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
			name:            "an MR that fits no class falls back to Manual Mode",
			autoModeEnabled: true,
			action:          "approved",
			setup: func(f *autoModeFixture) {
				f.changedFiles = []string{"services/api/prod/main-us-east-1/main.tf"}
				f.expectMR("tags-bot")
			},
		},
		{
			name:            "a single approval, not the MR's full approval",
			autoModeEnabled: true,
			action:          "approval",
			setup:           func(f *autoModeFixture) {},
		},
		{
			name:            "no longer approved when the event is handled",
			autoModeEnabled: true,
			action:          "approved",
			setup: func(f *autoModeFixture) {
				f.expectMR("tags-bot")
				f.expectGates(false, "success")
			},
			wantComment: "the MR is not approved",
		},
		{
			name:            "the pipeline has not succeeded",
			autoModeEnabled: true,
			action:          "approved",
			setup: func(f *autoModeFixture) {
				f.expectMR("tags-bot")
				f.expectGates(true, "running")
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
			if applies := f.applies(); len(applies) != 0 {
				t.Fatalf("expected no apply, but an apply trigger was built: %+v", applies[0])
			}
		})
	}
}

// Green TFC/* statuses that someone other than TFBuddy posted do not start an
// apply.
func TestAutoModeApproval_ForeignStatusesDoNotApply(t *testing.T) {
	f := newAutoModeFixture(t, true)
	f.expectMR("tags-bot")
	approvals := mocks.NewMockMRApproved(f.ctrl)
	approvals.EXPECT().IsApproved().Return(true).AnyTimes()
	f.client.EXPECT().GetMergeRequestApprovals(gomock.Any(), gateMRIID, gateProject).Return(approvals, nil)
	f.client.EXPECT().GetAuthenticatedUsername(gomock.Any()).Return(tfbuddyUsername, nil)
	f.client.EXPECT().GetCommitJobStatuses(gomock.Any(), gateProject, gateCommitSHA).Return([]vcs.CommitJobStatus{
		fakeJobStatus{name: "TFC/plan/svc-a", status: "success", pipelineID: 900, author: "a-developer"},
		fakeJobStatus{name: vcs.ClassStatusName, status: "success", pipelineID: 900, author: "a-developer"},
		fakeJobStatus{name: "aa:code-review", status: "success", pipelineID: 900, author: "a-developer"},
	}, nil)
	f.client.EXPECT().CreateMergeRequestComment(gomock.Any(), gateMRIID, gateProject, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int, _ string, msg string) error {
			if !strings.Contains(msg, "was not posted by TFBuddy") {
				t.Errorf("comment = %q, want it to say the statuses are not TFBuddy's", msg)
			}
			return nil
		})

	if _, err := f.worker.processMergeRequestEvent(mergeEventMsg("approved")); err != nil {
		t.Fatal(err)
	}
	if applies := f.applies(); len(applies) != 0 {
		t.Fatalf("expected no apply, but an apply trigger was built: %+v", applies[0])
	}
}

// A re-plan TFBuddy asked for arrives as an MR event, and the MR event worker
// plans the head commit, in Manual Mode, like a push.
func TestManualModeReplanEvent(t *testing.T) {
	for _, tt := range []struct {
		name, state, head string
		wantPlan          bool
	}{
		{name: "the head commit is planned", state: "opened", head: gateCommitSHA, wantPlan: true},
		{name: "an old commit is not", state: "opened", head: "newer-sha"},
		{name: "a merged MR is not", state: "merged", head: gateCommitSHA},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newAutoModeFixture(t, true)
			f.mr.EXPECT().GetState().Return(tt.state).AnyTimes()
			f.mr.EXPECT().GetHeadSHA().Return(tt.head).AnyTimes()
			f.mr.EXPECT().GetSourceBranch().Return("tags-bot/tags").AnyTimes()
			f.client.EXPECT().GetMergeRequest(gomock.Any(), gateMRIID, gateProject).Return(f.mr, nil)
			if tt.wantPlan {
				f.trigger.EXPECT().TriggerTFCEvents(gomock.Any()).Return(&tfc_trigger.TriggeredTFCWorkspaces{}, nil)
			}

			msg := manualModeReplanMsg(gateProject, gateMRIID, gateCommitSHA)
			msg.Context = context.Background()
			if !strings.Contains(msg.GetId(context.Background()), gateCommitSHA) {
				t.Errorf("message ID %q must name the commit, so a duplicate request is dropped", msg.GetId(context.Background()))
			}
			if _, err := f.worker.processMergeRequestEvent(msg); err != nil {
				t.Fatal(err)
			}
			if !tt.wantPlan {
				if len(f.created) != 0 {
					t.Fatalf("expected no plan, got %+v", f.created[0])
				}
				return
			}
			opts := f.created[0]
			if opts.Action != tfc_trigger.PlanAction || opts.CommitSHA != gateCommitSHA || opts.Branch != "tags-bot/tags" ||
				opts.DeliveryID != "tfbuddy-manual-mode-replan/"+gateCommitSHA || opts.AutoModeApproval {
				t.Errorf("unexpected plan options %+v", opts)
			}
		})
	}
}

type fakePipelineJob struct {
	name, status string
}

func (f fakePipelineJob) GetID() int        { return 1 }
func (f fakePipelineJob) GetName() string   { return f.name }
func (f fakePipelineJob) GetStatus() string { return f.status }

// A required job must be in the head pipeline and its latest run must have
// succeeded. The pipeline gate lets manual, skipped and allowed-to-fail jobs
// through; a required job gets no such exemption.
func TestAutoModeApproval_RequiredJobs(t *testing.T) {
	withRequired := strings.Replace(autoModeTargetConfig, "  classes:", "  requiredJobs: [\"aa:code-review\"]\n  classes:", 1)
	for _, tt := range []struct {
		name        string
		jobs        []vcs.PipelineJob
		wantComment string
	}{
		{name: "the job succeeded", jobs: []vcs.PipelineJob{fakePipelineJob{"lint", "success"}, fakePipelineJob{"aa:code-review", "success"}}},
		{name: "the job is missing", jobs: []vcs.PipelineJob{fakePipelineJob{"lint", "success"}}, wantComment: "has no required job aa:code-review"},
		{name: "the job is manual", jobs: []vcs.PipelineJob{fakePipelineJob{"aa:code-review", "manual"}}, wantComment: "has not succeeded: aa:code-review (manual)"},
		{name: "the job was skipped", jobs: []vcs.PipelineJob{fakePipelineJob{"aa:code-review", "skipped"}}, wantComment: "aa:code-review (skipped)"},
		{name: "the job failed but may", jobs: []vcs.PipelineJob{fakePipelineJob{"aa:code-review", "failed"}}, wantComment: "aa:code-review (failed)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newAutoModeFixture(t, true)
			f.targetConfig = withRequired
			f.expectMR("tags-bot")
			// The job's commit status is green, as a manual job's or an
			// allowed failure's is to the pipeline gate.
			f.expectGates(true, "success")
			f.client.EXPECT().GetPipelineJobs(gomock.Any(), gateProject, 900).Return(tt.jobs, nil)
			if tt.wantComment == "" {
				f.trigger.EXPECT().TriggerTFCEvents(gomock.Any()).Return(&tfc_trigger.TriggeredTFCWorkspaces{}, nil)
			} else {
				f.client.EXPECT().CreateMergeRequestComment(gomock.Any(), gateMRIID, gateProject, gomock.Any()).
					DoAndReturn(func(_ context.Context, _ int, _ string, msg string) error {
						if !strings.Contains(msg, tt.wantComment) {
							t.Errorf("comment = %q, want it to mention %q", msg, tt.wantComment)
						}
						return nil
					})
			}

			if _, err := f.worker.processMergeRequestEvent(mergeEventMsg("approved")); err != nil {
				t.Fatal(err)
			}
			if applied := len(f.created) == 1; applied != (tt.wantComment == "") {
				t.Fatalf("applied = %v, want %v", applied, tt.wantComment == "")
			}
		})
	}
}
