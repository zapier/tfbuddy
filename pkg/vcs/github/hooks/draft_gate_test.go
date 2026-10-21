package hooks

import (
	"context"
	"testing"

	gogithub "github.com/google/go-github/v69/github"
	"github.com/stretchr/testify/assert"
	"github.com/zapier/tfbuddy/internal/config"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/vcs"
	"github.com/zapier/tfbuddy/pkg/vcs/github"
	"go.uber.org/mock/gomock"
)

const (
	draftRepo = "zapier/service-tf-buddy"
	draftPR   = 101
)

// TestProcessIssueCommentDraftGate checks the GitHub apply path honours
// BlockApplyOnDraft the same way the GitLab one does.
func TestProcessIssueCommentDraftGate(t *testing.T) {
	tests := []struct {
		name              string
		blockApplyOnDraft bool
		body              string
		// wantComment is the message that must be posted, or "" for none.
		wantComment string
		// wantTrigger is whether the command must reach TFC.
		wantTrigger bool
	}{
		{
			name:              "apply on a draft is blocked and the reason posted",
			blockApplyOnDraft: true,
			body:              "tfc apply -w service-tf-buddy",
			wantComment:       draftApplyBlockedMessage,
		},
		{
			name:              "plan on a draft is allowed",
			blockApplyOnDraft: true,
			body:              "tfc plan -w service-tf-buddy",
			wantTrigger:       true,
		},
		{
			name:              "flag off leaves apply on a draft unchanged",
			blockApplyOnDraft: false,
			body:              "tfc apply -w service-tf-buddy",
			wantTrigger:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			// An approved, mergeable Draft, so only the draft gate can refuse it.
			pr := &github.GithubPR{PullRequest: &gogithub.PullRequest{
				Number:         gogithub.Ptr(draftPR),
				Draft:          gogithub.Ptr(true),
				Mergeable:      gogithub.Ptr(true),
				MergeableState: gogithub.Ptr("clean"),
				Head:           &gogithub.PullRequestBranch{Ref: gogithub.Ptr("DTA-2009")},
				Base:           &gogithub.PullRequestBranch{SHA: gogithub.Ptr("abvc12345")},
			}}

			gh := mocks.NewMockGitClient(ctrl)
			gh.EXPECT().GetMergeRequest(gomock.Any(), draftPR, draftRepo).Return(pr, nil)
			if tt.wantComment != "" {
				gh.EXPECT().CreateMergeRequestComment(gomock.Any(), draftPR, draftRepo, tt.wantComment).Return(nil)
			}

			// Without a TriggerTFCEvents expectation, reaching TFC fails the test.
			trigger := mocks.NewMockTrigger(ctrl)
			if tt.wantTrigger {
				trigger.EXPECT().TriggerTFCEvents(gomock.Any()).Return(&tfc_trigger.TriggeredTFCWorkspaces{
					Executed: []string{"service-tf-buddy"},
				}, nil)
			}

			cfg := config.C
			cfg.GithubRepoAllowList = []string{"zapier/"}
			cfg.BlockApplyOnDraft = tt.blockApplyOnDraft

			h := &GithubHooksHandler{
				cfg:       cfg,
				vcs:       gh,
				tfc:       mocks.NewMockApiClient(ctrl),
				runstream: mocks.NewMockStreamClient(ctrl),
				triggerCreation: func(config.Config, vcs.GitClient, tfc_api.ApiClient, runstream.StreamClient, *tfc_trigger.TFCTriggerOptions) tfc_trigger.Trigger {
					return trigger
				},
			}

			msg := &GithubIssueCommentEventMsg{Payload: &gogithub.IssueCommentEvent{
				Repo:    &gogithub.Repository{FullName: gogithub.Ptr(draftRepo)},
				Issue:   &gogithub.Issue{Number: gogithub.Ptr(draftPR)},
				Comment: &gogithub.IssueComment{Body: gogithub.Ptr(tt.body)},
			}}

			err := h.processIssueComment(context.Background(), msg)
			assert.NoError(t, err)
		})
	}
}
