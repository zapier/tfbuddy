package gitlab_hooks

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"go.uber.org/mock/gomock"
)

func TestCheckNotDraft(t *testing.T) {
	tests := []struct {
		name string
		// blockApplyOnDraft is the TFBuddy config flag under test.
		blockApplyOnDraft bool
		// draft is what the API reports. The MR is only fetched with the flag on.
		draft  bool
		getErr error
		want   bool
		// wantComment is the message the gate must post to the MR, or "" when it
		// must stay silent.
		wantComment string
	}{
		{
			name:              "flag disabled allows apply without calling the API",
			blockApplyOnDraft: false,
			want:              true,
		},
		{
			name:              "ready MR allows apply",
			blockApplyOnDraft: true,
			draft:             false,
			want:              true,
		},
		{
			name:              "draft MR blocks apply",
			blockApplyOnDraft: true,
			draft:             true,
			want:              false,
			wantComment:       draftApplyBlockedMessage,
		},
		{
			name:              "lookup error blocks apply",
			blockApplyOnDraft: true,
			getErr:            errors.New("boom"),
			want:              false,
			wantComment:       ":fire: <br> Error: could not get MergeRequest from GitlabAPI: boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			f := newGateFixture(t, ctrl, false)
			f.worker.cfg.BlockApplyOnDraft = tt.blockApplyOnDraft

			switch {
			case !tt.blockApplyOnDraft:
				// No expectations: the MR must not be fetched.
			case tt.getErr != nil:
				f.client.EXPECT().GetMergeRequest(gomock.Any(), gateMRIID, gateProject).Return(nil, tt.getErr)
			default:
				detailedMR := mocks.NewMockDetailedMR(ctrl)
				detailedMR.EXPECT().IsDraft().Return(tt.draft)
				f.client.EXPECT().GetMergeRequest(gomock.Any(), gateMRIID, gateProject).Return(detailedMR, nil)
			}
			if tt.wantComment != "" {
				f.client.EXPECT().
					CreateMergeRequestComment(gomock.Any(), gateMRIID, gateProject, tt.wantComment).
					Return(nil)
			}

			got := f.worker.checkNotDraft(context.Background(), f.event)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestProcessNoteEventDraftGate checks the gate is wired into the apply command
// path only, and that it changes nothing while the flag is off.
func TestProcessNoteEventDraftGate(t *testing.T) {
	tests := []struct {
		name              string
		blockApplyOnDraft bool
		note              string
		// expect registers the GitLab calls the command must make. Any call not
		// registered fails the test, which is how the cases prove the gate does
		// not consult the Draft state when it should not.
		expect func(ctrl *gomock.Controller, gl *mocks.MockGitClient)
		// wantTrigger is whether the command must reach TFC.
		wantTrigger bool
	}{
		{
			name:              "apply on a draft is blocked and the reason posted",
			blockApplyOnDraft: true,
			note:              "tfc apply -w service-tf-buddy",
			expect: func(ctrl *gomock.Controller, gl *mocks.MockGitClient) {
				detailedMR := mocks.NewMockDetailedMR(ctrl)
				detailedMR.EXPECT().IsDraft().Return(true)
				gl.EXPECT().GetMergeRequest(gomock.Any(), gateMRIID, gateProject).Return(detailedMR, nil)
				gl.EXPECT().
					CreateMergeRequestComment(gomock.Any(), gateMRIID, gateProject, draftApplyBlockedMessage).
					Return(nil)
			},
			wantTrigger: false,
		},
		{
			name:              "plan on a draft is allowed without looking up the MR",
			blockApplyOnDraft: true,
			note:              "tfc plan -w service-tf-buddy",
			expect:            func(*gomock.Controller, *mocks.MockGitClient) {},
			wantTrigger:       true,
		},
		{
			name:              "apply on a ready MR proceeds to the existing checks",
			blockApplyOnDraft: true,
			note:              "tfc apply -w service-tf-buddy",
			expect: func(ctrl *gomock.Controller, gl *mocks.MockGitClient) {
				readyMR := mocks.NewMockDetailedMR(ctrl)
				readyMR.EXPECT().IsDraft().Return(false)
				readyMR.EXPECT().HasConflicts().Return(false)
				// Once for the draft gate, once for the merge-conflict check.
				gl.EXPECT().GetMergeRequest(gomock.Any(), gateMRIID, gateProject).Return(readyMR, nil).Times(2)
				approvals := mocks.NewMockMRApproved(ctrl)
				approvals.EXPECT().IsApproved().Return(true)
				gl.EXPECT().GetMergeRequestApprovals(gomock.Any(), gateMRIID, gateProject).Return(approvals, nil)
			},
			wantTrigger: true,
		},
		{
			name:              "flag off leaves apply on a draft unchanged",
			blockApplyOnDraft: false,
			note:              "tfc apply -w service-tf-buddy",
			expect: func(ctrl *gomock.Controller, gl *mocks.MockGitClient) {
				// No IsDraft expectation: with the flag off the Draft state is
				// never read, so a Draft MR applies exactly as before.
				draftMR := mocks.NewMockDetailedMR(ctrl)
				draftMR.EXPECT().HasConflicts().Return(false)
				gl.EXPECT().GetMergeRequest(gomock.Any(), gateMRIID, gateProject).Return(draftMR, nil)
				approvals := mocks.NewMockMRApproved(ctrl)
				approvals.EXPECT().IsApproved().Return(true)
				gl.EXPECT().GetMergeRequestApprovals(gomock.Any(), gateMRIID, gateProject).Return(approvals, nil)
			},
			wantTrigger: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			gl := mocks.NewMockGitClient(ctrl)
			tt.expect(ctrl, gl)

			project := mocks.NewMockProject(ctrl)
			project.EXPECT().GetPathWithNamespace().Return(gateProject).AnyTimes()

			lastCommit := mocks.NewMockCommit(ctrl)
			lastCommit.EXPECT().GetSHA().Return(gateCommitSHA)

			attributes := mocks.NewMockMRAttributes(ctrl)
			attributes.EXPECT().GetNote().Return(tt.note)
			attributes.EXPECT().GetType().Return("SomeNote")

			mr := mocks.NewMockMR(ctrl)
			mr.EXPECT().GetSourceBranch().Return("DTA-2009")
			mr.EXPECT().GetInternalID().Return(gateMRIID).AnyTimes()

			event := mocks.NewMockMRCommentEvent(ctrl)
			event.EXPECT().GetProject().Return(project).AnyTimes()
			event.EXPECT().GetAttributes().Return(attributes).Times(2)
			event.EXPECT().GetLastCommit().Return(lastCommit)
			event.EXPECT().GetMR().Return(mr).AnyTimes()

			// Without a TriggerTFCEvents expectation, reaching TFC fails the test.
			trigger := mocks.NewMockTrigger(ctrl)
			if tt.wantTrigger {
				trigger.EXPECT().TriggerTFCEvents(gomock.Any()).Return(&tfc_trigger.TriggeredTFCWorkspaces{
					Executed: []string{"service-tf-buddy"},
				}, nil)
			}

			cfg := config.C
			cfg.GitlabProjectAllowList = []string{"zapier/"}
			cfg.RequirePipelineSuccess = false
			cfg.BlockApplyOnDraft = tt.blockApplyOnDraft

			worker := &GitlabEventWorker{
				cfg:       cfg,
				gl:        gl,
				tfc:       mocks.NewMockApiClient(ctrl),
				runstream: mocks.NewMockStreamClient(ctrl),
				triggerCreation: func(appCfg config.Config, gl vcs.GitClient, tfc tfc_api.ApiClient, rs runstream.StreamClient, cfg *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
					return trigger
				},
			}

			proj, err := worker.processNoteEvent(context.Background(), event)
			assert.NoError(t, err)
			assert.Equal(t, gateProject, proj)
		})
	}
}
