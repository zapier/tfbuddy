package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/go-tfe"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.uber.org/mock/gomock"
)

func TestSetClassStatusPostsTFCClass(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.RawPath
		if path == "" {
			path = r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(path, "/pipelines"):
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 900, "source": "merge_request_event", "status": "running"},
			})
		case strings.Contains(path, "/statuses/"):
			json.NewDecoder(r.Body).Decode(&got)
			json.NewEncoder(w).Encode(map[string]any{"id": 1, "name": "TFC/class", "sha": "abc123", "author": map[string]any{"username": "tfbuddy"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	err := newTestClient(t, srv.URL).SetClassStatus(context.Background(), vcs.ClassStatus{
		Project: testProject, CommitSHA: "abc123", MergeRequestIID: 101,
		State: vcs.CommitStateSuccess, Description: "class tags: every plan passed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["name"] != "TFC/class" || got["state"] != "success" || got["pipeline_id"] != float64(900) {
		t.Errorf("unexpected status posted: %v", got)
	}
}

// classPlanRef is the class plans of the commit these tests plan.
var classPlanRef = runstream.ClassPlanRef{VcsProvider: "gitlab", Project: testProject, MergeRequest: 101, CommitSHA: "abc123"}

// currentClassPlans makes the report find that the class plans it reports
// are still the commit's current ones, begun with no trigger.
func currentClassPlans(rs *mocks.MockStreamClient) {
	rs.EXPECT().GetClassPlans(classPlanRef).Return(&runstream.ClassPlanState{}, nil).AnyTimes()
}

func classPlanRun(ws, runID string) *runstream.TFRunMetadata {
	return &runstream.TFRunMetadata{
		Action: runstream.PlanAction, Workspace: ws, RunID: runID, CommitSHA: "abc123",
		MergeRequestProjectNameWithNamespace: testProject, MergeRequestIID: 101,
		ChangeClass: "tags", ClassWorkspaces: []string{"a", "b"},
	}
}

// The plan that finishes the class reports it: TFC/class and one summary
// comment with the class, the commit and each workspace's run and result.
func TestUpdateClassStatusAfterLastPlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockGitClient(ctrl)
	rs := mocks.NewMockStreamClient(ctrl)
	r := &RunStatusUpdater{cfg: config.C, client: client, rs: rs}
	currentClassPlans(rs)

	// A run that has not finished, a person's run and an apply run make no calls.
	r.updateClassStatus(context.Background(), &tfe.Run{Status: tfe.RunPlanning}, classPlanRun("a", "run-a"))
	r.updateClassStatus(context.Background(), &tfe.Run{Status: tfe.RunPlannedAndFinished}, &runstream.TFRunMetadata{Action: runstream.PlanAction})
	r.updateClassStatus(context.Background(), &tfe.Run{Status: tfe.RunPlannedAndFinished}, &runstream.TFRunMetadata{Action: runstream.ApplyAction, ChangeClass: "tags"})

	// The first plan to pass does not finish the class.
	rs.EXPECT().RecordClassPlanResult(classPlanRef, "a", "run-a", "", true, "planned_and_finished").Return(nil, false, nil)
	r.updateClassStatus(context.Background(), &tfe.Run{Status: tfe.RunPlannedAndFinished}, classPlanRun("a", "run-a"))

	finished := &runstream.ClassPlanState{
		VcsProvider: "gitlab", Project: testProject, MergeRequest: 101, CommitSHA: "abc123", Class: "tags", Finished: true,
		Workspaces: map[string]*runstream.ClassPlanWorkspace{
			"a": {RunID: "run-a", Outcome: runstream.ClassPlanPassed, Detail: "planned_and_finished"},
			"b": {RunID: "run-b", Outcome: runstream.ClassPlanPassed, Detail: "planned_and_finished"},
		},
	}
	rs.EXPECT().RecordClassPlanResult(classPlanRef, "b", "run-b", "", true, "planned_and_finished").Return(finished, true, nil)
	mrRef := runstream.AutoModeMRRef{VcsProvider: "gitlab", Project: testProject, MergeRequest: 101}
	rs.EXPECT().IsAutoModeManual(mrRef).Return(false, "", nil)
	client.EXPECT().SetClassStatus(gomock.Any(), vcs.ClassStatus{
		Project: testProject, CommitSHA: "abc123", MergeRequestIID: 101,
		State: vcs.CommitStateSuccess, Description: "class tags: every plan passed",
	}).Return(nil)
	// An existing summary comment is updated in place.
	rs.EXPECT().GetAutoModeSummaryNote(mrRef).Return(int64(55), nil)
	client.EXPECT().UpdateMergeRequestComment(gomock.Any(), 101, int64(55), testProject, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int, _ int64, _, body string) error {
			for _, want := range []string{"class `tags`", "head commit `abc123`", "TFC/class` **passed**", "| `a` | `run-a` | passed |", "| `b` | `run-b` | passed |"} {
				if !strings.Contains(body, want) {
					t.Errorf("summary %q does not contain %q", body, want)
				}
			}
			return nil
		})
	rs.EXPECT().MarkClassPlansReported(classPlanRef, "").Return(nil)
	if err := r.updateClassStatus(context.Background(), &tfe.Run{Status: tfe.RunPlannedAndFinished}, classPlanRun("b", "run-b")); err != nil {
		t.Fatal(err)
	}
}

// An Auto Mode plan that soft-fails a policy fails, whatever
// fail-ci-on-sentinel-soft-fail says, so that its class fails too.
func TestAutoModeSoftFailFailsThePlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockGitClient(ctrl)
	r := &RunStatusUpdater{cfg: config.C, client: client}
	var got vcs.WorkspaceStatus
	client.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s vcs.WorkspaceStatus) error {
		got = s
		return nil
	})

	err := r.updateCommitStatusForRun(context.Background(), &tfe.Run{Status: tfe.RunPolicySoftFailed}, &runstream.TFRunMetadata{
		Action: runstream.PlanAction, Workspace: "a", ChangeClass: "tags",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Action != "plan" || got.State != vcs.CommitStateFailed {
		t.Errorf("status = %+v, want a failed plan", got)
	}
}

// A failed class check puts the MR in Manual Mode for good: TFC/class fails
// with that message, the MR is told once, and only its head commit is planned
// again.
func TestUpdateClassStatusFailureFallsBackToManualMode(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockGitClient(ctrl)
	rs := mocks.NewMockStreamClient(ctrl)
	replanner := &fakeReplanner{}
	r := &RunStatusUpdater{cfg: config.C, client: client, rs: rs, replanner: replanner}
	mrRef := runstream.AutoModeMRRef{VcsProvider: "gitlab", Project: testProject, MergeRequest: 101}

	// a failed; b has not finished, so the class is not reported yet.
	rs.EXPECT().RecordClassPlanResult(classPlanRef, "a", "run-a", "", false, "errored").Return(nil, false, nil)
	rs.EXPECT().MarkAutoModeManual(mrRef, "the plan for a did not pass (errored)").Return(true, nil)
	client.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s vcs.ClassStatus) error {
		if s.State != vcs.CommitStateSkipped || s.Description != "Manual Mode: the plan for a did not pass (errored)" {
			t.Errorf("unexpected class status %+v", s)
		}
		return nil
	})
	client.EXPECT().CreateMergeRequestComment(gomock.Any(), 101, testProject, gomock.Any()).DoAndReturn(func(_ context.Context, _ int, _, msg string) error {
		if !strings.Contains(msg, "now runs in Manual Mode because the plan for a did not pass") {
			t.Errorf("unexpected comment %q", msg)
		}
		return nil
	})
	// No GetMergeRequest or run: the callback only asks for the re-plan.

	if err := r.updateClassStatus(context.Background(), &tfe.Run{Status: tfe.RunErrored}, classPlanRun("a", "run-a")); err != nil {
		t.Fatal(err)
	}
	if len(replanner.requests) != 1 || replanner.requests[0] != testProject+"!101@abc123" {
		t.Errorf("re-plan requests = %q, want one for the failed commit", replanner.requests)
	}
}

// fakeReplanner records the Manual Mode re-plans it is asked for.
type fakeReplanner struct{ requests []string }

func (f *fakeReplanner) RequestManualModeReplan(_ context.Context, project string, mrIID int, commitSHA string) error {
	f.requests = append(f.requests, fmt.Sprintf("%s!%d@%s", project, mrIID, commitSHA))
	return nil
}

// When the last plan finishes a class that failed, the summary says so and
// says the MR is in Manual Mode; a summary without a comment yet posts one.
func TestUpdateClassStatusReportsAFailedClass(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockGitClient(ctrl)
	rs := mocks.NewMockStreamClient(ctrl)
	r := &RunStatusUpdater{cfg: config.C, client: client, rs: rs}
	mrRef := runstream.AutoModeMRRef{VcsProvider: "gitlab", Project: testProject, MergeRequest: 101}
	currentClassPlans(rs)

	finished := &runstream.ClassPlanState{
		VcsProvider: "gitlab", Project: testProject, MergeRequest: 101, CommitSHA: "abc123", Class: "tags", Finished: true,
		Workspaces: map[string]*runstream.ClassPlanWorkspace{
			"a": {Outcome: runstream.ClassPlanFailed, Detail: "did not plan: TFC is down"},
			"b": {RunID: "run-b", Outcome: runstream.ClassPlanPassed, Detail: "planned_and_finished"},
		},
	}
	rs.EXPECT().RecordClassPlanResult(classPlanRef, "b", "run-b", "", true, "planned_and_finished").Return(finished, true, nil)
	rs.EXPECT().IsAutoModeManual(mrRef).Return(true, "a plan failed", nil)
	client.EXPECT().SetClassStatus(gomock.Any(), vcs.ClassStatus{
		Project: testProject, CommitSHA: "abc123", MergeRequestIID: 101,
		State: vcs.CommitStateSkipped, Description: "Manual Mode: a plan failed",
	}).Return(nil)
	rs.EXPECT().GetAutoModeSummaryNote(mrRef).Return(int64(0), nil)
	client.EXPECT().CreateMergeRequestCommentWithID(gomock.Any(), 101, testProject, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int, _, body string) (int64, error) {
			for _, want := range []string{"TFC/class` **skipped**", "in Manual Mode because a plan failed", "| `a` | none | failed: did not plan: TFC is down |", "| `b` | `run-b` | passed |"} {
				if !strings.Contains(body, want) {
					t.Errorf("summary %q does not contain %q", body, want)
				}
			}
			return 66, nil
		})
	rs.EXPECT().SetAutoModeSummaryNote(mrRef, int64(66)).Return(nil)
	rs.EXPECT().MarkClassPlansReported(classPlanRef, "").Return(nil)

	if err := r.updateClassStatus(context.Background(), &tfe.Run{Status: tfe.RunPlannedAndFinished}, classPlanRun("b", "run-b")); err != nil {
		t.Fatal(err)
	}
}

// Every plan passed, but the MR fell back to Manual Mode meanwhile (here a
// `tfc plan -w` run with the class failed): TFC/class does not pass.
func TestReportClassPlansFailsAManualModeMR(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockGitClient(ctrl)
	rs := mocks.NewMockStreamClient(ctrl)
	mrRef := runstream.AutoModeMRRef{VcsProvider: "gitlab", Project: testProject, MergeRequest: 101}
	currentClassPlans(rs)
	state := &runstream.ClassPlanState{
		VcsProvider: "gitlab", Project: testProject, MergeRequest: 101, CommitSHA: "abc123", Class: "tags", Finished: true,
		Workspaces: map[string]*runstream.ClassPlanWorkspace{"a": {RunID: "run-a", Outcome: runstream.ClassPlanPassed}},
	}
	rs.EXPECT().IsAutoModeManual(mrRef).Return(true, "a plan failed", nil)
	client.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s vcs.ClassStatus) error {
		if s.State != vcs.CommitStateSkipped || s.Description != "Manual Mode: a plan failed" {
			t.Errorf("unexpected class status %+v", s)
		}
		return nil
	})
	rs.EXPECT().GetAutoModeSummaryNote(mrRef).Return(int64(0), nil)
	client.EXPECT().CreateMergeRequestCommentWithID(gomock.Any(), 101, testProject, gomock.Any()).Return(int64(1), nil)
	rs.EXPECT().SetAutoModeSummaryNote(mrRef, int64(1)).Return(nil)
	rs.EXPECT().MarkClassPlansReported(classPlanRef, "").Return(nil)

	if err := tfc_trigger.ReportClassPlans(context.Background(), client, rs, state); err != nil {
		t.Fatal(err)
	}
}

// A TFC/class write that fails leaves the finished class unreported, and the
// event is handled again; its redelivery reports the class, once.
func TestUpdateClassStatusReportsAgainAfterAFailedReport(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockGitClient(ctrl)
	rs := mocks.NewMockStreamClient(ctrl)
	r := &RunStatusUpdater{cfg: config.C, client: client, rs: rs}
	mrRef := runstream.AutoModeMRRef{VcsProvider: "gitlab", Project: testProject, MergeRequest: 101}
	currentClassPlans(rs)
	state := func(reported bool) *runstream.ClassPlanState {
		return &runstream.ClassPlanState{
			VcsProvider: "gitlab", Project: testProject, MergeRequest: 101, CommitSHA: "abc123", Class: "tags",
			Finished: true, Reported: reported,
			Workspaces: map[string]*runstream.ClassPlanWorkspace{"a": {RunID: "run-a", Outcome: runstream.ClassPlanPassed}},
		}
	}
	success := vcs.ClassStatus{
		Project: testProject, CommitSHA: "abc123", MergeRequestIID: 101,
		State: vcs.CommitStateSuccess, Description: "class tags: every plan passed",
	}
	rs.EXPECT().IsAutoModeManual(mrRef).Return(false, "", nil).Times(2)
	gomock.InOrder(
		// The plan finishes the class, but GitLab refuses the status.
		rs.EXPECT().RecordClassPlanResult(classPlanRef, "a", "run-a", "", true, "planned_and_finished").Return(state(false), true, nil),
		client.EXPECT().SetClassStatus(gomock.Any(), success).Return(errors.New("502 Bad Gateway")),
		// The redelivery changes no outcome, and finds the class unreported.
		rs.EXPECT().RecordClassPlanResult(classPlanRef, "a", "run-a", "", true, "planned_and_finished").Return(state(false), false, nil),
		client.EXPECT().SetClassStatus(gomock.Any(), success).Return(nil),
		rs.EXPECT().GetAutoModeSummaryNote(mrRef).Return(int64(0), nil),
		client.EXPECT().CreateMergeRequestCommentWithID(gomock.Any(), 101, testProject, gomock.Any()).Return(int64(3), nil),
		rs.EXPECT().SetAutoModeSummaryNote(mrRef, int64(3)).Return(nil),
		rs.EXPECT().MarkClassPlansReported(classPlanRef, "").Return(nil),
		// Once reported, a later event reports nothing.
		rs.EXPECT().RecordClassPlanResult(classPlanRef, "a", "run-a", "", true, "planned_and_finished").Return(state(true), false, nil),
	)

	run := &tfe.Run{Status: tfe.RunPlannedAndFinished}
	if err := r.updateClassStatus(context.Background(), run, classPlanRun("a", "run-a")); err == nil {
		t.Fatal("a TFC/class write that failed must be retried")
	}
	for i := range 2 {
		if err := r.updateClassStatus(context.Background(), run, classPlanRun("a", "run-a")); err != nil {
			t.Fatalf("event %d: %v", i+2, err)
		}
	}
}

// When NATS cannot record a class plan's outcome, the run event is not acked,
// so that the outcome is recorded when it is handled again.
func TestEventStreamCallbackRetriesAnUnrecordedClassPlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	tfc := mocks.NewMockApiClient(ctrl)
	tfc.EXPECT().GetRun(gomock.Any(), "run-123").Return(testRun(tfe.RunErrored), nil)
	client := mocks.NewMockGitClient(ctrl)
	client.EXPECT().GetOldRunUrls(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	client.EXPECT().UpdateMergeRequestDiscussionNote(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	client.EXPECT().AddMergeRequestDiscussionReply(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	client.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).Return(nil)
	// The MR was already marked, so the fallback only sets TFC/class.
	client.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).Return(nil)
	rs := mocks.NewMockStreamClient(ctrl)
	ref := runstream.ClassPlanRef{VcsProvider: "gitlab", Project: "zapier/tfbuddy", MergeRequest: 101, CommitSHA: "commit-123"}
	rs.EXPECT().RecordClassPlanResult(ref, "service-tfbuddy", "run-123", "", false, "errored").Return(nil, false, errors.New("nats: timeout"))
	rs.EXPECT().MarkAutoModeManual(gomock.Any(), gomock.Any()).Return(false, nil)
	// No RecordRunFinished: the event is not done.
	r := &RunStatusUpdater{cfg: config.C, tfc: tfc, client: client, rs: rs}
	re := testPlanRunEvent(tfe.RunErrored)
	re.Metadata.(*runstream.TFRunMetadata).ChangeClass = "tags"

	if r.eventStreamCallback(re) {
		t.Fatal("an event whose class plan outcome was not recorded must be retried")
	}
}

// TFBuddy never overrides a policy for Auto Mode: a soft-failed Auto Mode
// apply is discarded at once, its apply status fails, the MR falls back to
// Manual Mode, and the MR says why. TFC reports the soft failure of a run that
// would apply as policy_override; both statuses are handled.
func TestAutoModeSoftFailedApplyIsDiscarded(t *testing.T) {
	for _, status := range []tfe.RunStatus{tfe.RunPolicyOverride, tfe.RunPolicySoftFailed} {
		for _, discardFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/discard fails %v", status, discardFails), func(t *testing.T) {
				ctrl := gomock.NewController(t)
				client := mocks.NewMockGitClient(ctrl)
				rs := mocks.NewMockStreamClient(ctrl)
				tfc := mocks.NewMockApiClient(ctrl)
				replanner := &fakeReplanner{}
				r := &RunStatusUpdater{cfg: config.C, client: client, rs: rs, tfc: tfc, replanner: replanner}
				rmd := &runstream.TFRunMetadata{
					Action: runstream.ApplyAction, Workspace: "a", RunID: "run-a", CommitSHA: "abc123", Organization: "zapier",
					MergeRequestProjectNameWithNamespace: testProject, MergeRequestIID: 101, ChangeClass: "tags",
				}
				var discardErr error
				if discardFails {
					discardErr = errors.New("409 transition not allowed")
				}
				tfc.EXPECT().DiscardRun(gomock.Any(), "run-a", gomock.Any()).Return(discardErr)
				client.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s vcs.WorkspaceStatus) error {
					if s.Action != "apply" || s.State != vcs.CommitStateFailed || s.Workspace != "a" {
						t.Errorf("status = %+v, want a failed apply", s)
					}
					return nil
				})
				rs.EXPECT().MarkAutoModeManual(runstream.AutoModeMRRef{VcsProvider: "gitlab", Project: testProject, MergeRequest: 101}, gomock.Any()).
					Return(true, nil)
				var comments []string
				client.EXPECT().CreateMergeRequestComment(gomock.Any(), 101, testProject, gomock.Any()).
					DoAndReturn(func(_ context.Context, _ int, _, msg string) error {
						comments = append(comments, msg)
						return nil
					}).Times(2)
				// TFC/class says why there is no class, without blocking the merge.
				client.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s vcs.ClassStatus) error {
					if s.State != vcs.CommitStateSkipped || !strings.HasPrefix(s.Description, "Manual Mode: its apply of `a` soft-failed") {
						t.Errorf("unexpected class status %+v", s)
					}
					return nil
				})

				if err := r.updateCommitStatusForRun(context.Background(), &tfe.Run{ID: "run-a", Status: status}, rmd); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(comments[0], "now runs in Manual Mode because its apply of `a` soft-failed a Sentinel policy") ||
					!strings.Contains(comments[0], "planning it again without a change class") {
					t.Errorf("unexpected fallback comment %q", comments[0])
				}
				// The same fallback as a failed class check: plan the head again.
				if len(replanner.requests) != 1 || replanner.requests[0] != testProject+"!101@abc123" {
					t.Errorf("re-plan requests = %q, want one for the commit", replanner.requests)
				}
				want := "discarded the apply of `a` (run `run-a`)"
				if discardFails {
					want = "could not discard the run (409 transition not allowed)"
				}
				if !strings.Contains(comments[1], want) || !strings.Contains(comments[1], "never overrides a policy") {
					t.Errorf("comment = %q, want it to contain %q", comments[1], want)
				}
			})
		}
	}
}

// A Manual Mode apply that soft-fails keeps today's behavior: a person
// decides in TFC.
func TestManualModeSoftFailedApplyIsUnchanged(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockGitClient(ctrl)
	r := &RunStatusUpdater{cfg: config.C, client: client}
	rmd := &runstream.TFRunMetadata{Action: runstream.ApplyAction, Workspace: "a", RunID: "run-a"}

	// policy_override was, and is, ignored.
	if err := r.updateCommitStatusForRun(context.Background(), &tfe.Run{Status: tfe.RunPolicyOverride}, rmd); err != nil {
		t.Fatal(err)
	}
	var got vcs.WorkspaceStatus
	client.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s vcs.WorkspaceStatus) error {
		got = s
		return nil
	})
	if err := r.updateCommitStatusForRun(context.Background(), &tfe.Run{Status: tfe.RunPolicySoftFailed}, rmd); err != nil {
		t.Fatal(err)
	}
	if got.Action != "apply" || got.State != vcs.CommitStateSuccess {
		t.Errorf("status = %+v, want today's successful apply status", got)
	}
}

// An Auto Mode apply that errors, for whatever reason, takes the same fallback
// as a failed class check; a Manual Mode apply that errors does not.
func TestErroredAutoModeApplyFallsBackToManualMode(t *testing.T) {
	for _, class := range []string{"tags", ""} {
		t.Run("class "+class, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			client := mocks.NewMockGitClient(ctrl)
			rs := mocks.NewMockStreamClient(ctrl)
			replanner := &fakeReplanner{}
			r := &RunStatusUpdater{cfg: config.C, client: client, rs: rs, replanner: replanner}
			rmd := &runstream.TFRunMetadata{
				Action: runstream.ApplyAction, Workspace: "a", RunID: "run-a", CommitSHA: "abc123", Organization: "zapier",
				MergeRequestProjectNameWithNamespace: testProject, MergeRequestIID: 101, ChangeClass: class,
			}
			client.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s vcs.WorkspaceStatus) error {
				if s.Action != "apply" || s.State != vcs.CommitStateFailed {
					t.Errorf("status = %+v, want a failed apply", s)
				}
				return nil
			})
			if class != "" {
				rs.EXPECT().MarkAutoModeManual(runstream.AutoModeMRRef{VcsProvider: "gitlab", Project: testProject, MergeRequest: 101},
					"its apply of `a` errored (run `run-a`)").Return(true, nil)
				client.EXPECT().SetClassStatus(gomock.Any(), vcs.ClassStatus{
					Project: testProject, CommitSHA: "abc123", MergeRequestIID: 101,
					State: vcs.CommitStateSkipped, Description: "Manual Mode: its apply of `a` errored (run `run-a`)",
				}).Return(nil)
				client.EXPECT().CreateMergeRequestComment(gomock.Any(), 101, testProject, gomock.Any()).Return(nil)
			}

			if err := r.updateCommitStatusForRun(context.Background(), &tfe.Run{ID: "run-a", Status: tfe.RunErrored}, rmd); err != nil {
				t.Fatal(err)
			}
			if want := map[bool]int{true: 1, false: 0}[class != ""]; len(replanner.requests) != want {
				t.Errorf("re-plan requests = %q, want %d", replanner.requests, want)
			}
		})
	}
}

// When the MR's mode cannot be read, TFC/class fails closed, and the report is
// not recorded, so that a retry can report the real result.
func TestReportClassPlansRetriesAnUnreadableMode(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockGitClient(ctrl)
	rs := mocks.NewMockStreamClient(ctrl)
	currentClassPlans(rs)
	state := &runstream.ClassPlanState{
		VcsProvider: "gitlab", Project: testProject, MergeRequest: 101, CommitSHA: "abc123", Class: "tags", Finished: true,
		Workspaces: map[string]*runstream.ClassPlanWorkspace{"a": {RunID: "run-a", Outcome: runstream.ClassPlanPassed}},
	}
	rs.EXPECT().IsAutoModeManual(gomock.Any()).Return(false, "", errors.New("nats: timeout"))
	client.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s vcs.ClassStatus) error {
		if s.State != vcs.CommitStateFailed {
			t.Errorf("TFC/class = %+v, want failed until the mode can be read", s)
		}
		return nil
	})
	// No summary and no MarkClassPlansReported.

	if err := tfc_trigger.ReportClassPlans(context.Background(), client, rs, state); err == nil {
		t.Fatal("a report that could not read the MR's mode must be retried")
	}
}

// Plans that another trigger has begun again for the commit do not report:
// the new plans report themselves.
func TestReportClassPlansSkipsSupersededPlans(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockGitClient(ctrl)
	rs := mocks.NewMockStreamClient(ctrl)
	rs.EXPECT().GetClassPlans(classPlanRef).Return(&runstream.ClassPlanState{Trigger: "note:2"}, nil)
	state := &runstream.ClassPlanState{
		VcsProvider: "gitlab", Project: testProject, MergeRequest: 101, CommitSHA: "abc123", Class: "tags", Finished: true,
		Trigger:    "delivery:1",
		Workspaces: map[string]*runstream.ClassPlanWorkspace{"a": {RunID: "run-a", Outcome: runstream.ClassPlanPassed}},
	}
	// No status, summary or mark: any call fails the test.

	if err := tfc_trigger.ReportClassPlans(context.Background(), client, rs, state); err != nil {
		t.Fatal(err)
	}
}

// A soft-failed Auto Mode apply whose fallback NATS cannot record is not
// discarded yet: once discarded, its events are stale and no retry could
// record the fallback. The error makes the event retry.
func TestAutoModeSoftFailedApplyRecordsTheFallbackFirst(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockGitClient(ctrl)
	rs := mocks.NewMockStreamClient(ctrl)
	tfc := mocks.NewMockApiClient(ctrl)
	r := &RunStatusUpdater{cfg: config.C, client: client, rs: rs, tfc: tfc, replanner: &fakeReplanner{}}
	rmd := &runstream.TFRunMetadata{
		Action: runstream.ApplyAction, Workspace: "a", RunID: "run-a", CommitSHA: "abc123", Organization: "zapier",
		MergeRequestProjectNameWithNamespace: testProject, MergeRequestIID: 101, ChangeClass: "tags",
	}
	rs.EXPECT().MarkAutoModeManual(gomock.Any(), gomock.Any()).Return(false, errors.New("nats: timeout")).MinTimes(1)
	client.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s vcs.ClassStatus) error {
		if s.State != vcs.CommitStateFailed {
			t.Errorf("TFC/class = %+v, want failed while the fallback is not recorded", s)
		}
		return nil
	})
	// No DiscardRun, apply status or comment.

	if err := r.updateCommitStatusForRun(context.Background(), &tfe.Run{ID: "run-a", Status: tfe.RunPolicyOverride}, rmd); err == nil {
		t.Fatal("a soft-failed apply whose fallback was not recorded must be retried")
	}
}

// An errored Auto Mode apply whose fallback NATS cannot record is retried, so
// that the MR does not stay in Auto Mode.
func TestErroredAutoModeApplyRetriesAnUnrecordedFallback(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockGitClient(ctrl)
	rs := mocks.NewMockStreamClient(ctrl)
	r := &RunStatusUpdater{cfg: config.C, client: client, rs: rs, replanner: &fakeReplanner{}}
	rmd := &runstream.TFRunMetadata{
		Action: runstream.ApplyAction, Workspace: "a", RunID: "run-a", CommitSHA: "abc123", Organization: "zapier",
		MergeRequestProjectNameWithNamespace: testProject, MergeRequestIID: 101, ChangeClass: "tags",
	}
	client.EXPECT().SetWorkspaceStatus(gomock.Any(), gomock.Any()).Return(nil)
	rs.EXPECT().MarkAutoModeManual(gomock.Any(), gomock.Any()).Return(false, errors.New("nats: timeout")).MinTimes(1)
	client.EXPECT().SetClassStatus(gomock.Any(), gomock.Any()).Return(nil)

	if err := r.updateCommitStatusForRun(context.Background(), &tfe.Run{ID: "run-a", Status: tfe.RunErrored}, rmd); err == nil {
		t.Fatal("an errored apply whose fallback was not recorded must be retried")
	}
}
