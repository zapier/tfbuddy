package gitlab_hooks

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.opentelemetry.io/otel/propagation"
)

func createMergeEvent(action, projectPath, sourceBranch, commitSHA, oldRev string, mrIID int) *gogitlab.MergeEvent {
	event := &gogitlab.MergeEvent{}
	event.ObjectAttributes.Action = action
	event.ObjectAttributes.IID = mrIID
	event.ObjectAttributes.SourceBranch = sourceBranch
	event.ObjectAttributes.LastCommit.ID = commitSHA
	event.ObjectAttributes.OldRev = oldRev
	event.ObjectAttributes.Source = &gogitlab.Repository{
		PathWithNamespace: projectPath,
	}
	event.Project.Name = "project"
	event.Project.PathWithNamespace = projectPath
	return event
}

func TestGitlabEventWorker_processMergeRequestEvent(t *testing.T) {

	type args struct {
		msg *MergeRequestEventMsg
	}
	tests := []struct {
		name            string
		args            args
		allowList       string
		wantProjectName string
		wantErr         assert.ErrorAssertionFunc
	}{
		{
			name: "project not in allow list returns early without error",
			args: args{
				msg: &MergeRequestEventMsg{
					Payload: createMergeEvent("open", "unauthorized/project", "test-branch", "abc123", "", 101),
					Context: context.Background(),
					Carrier: propagation.MapCarrier{},
				},
			},
			allowList:       "zapier/tfbuddy",
			wantProjectName: "unauthorized/project",
			wantErr:         assert.NoError,
		},
		{
			name: "update with same commit returns early without error",
			args: args{
				msg: &MergeRequestEventMsg{
					Payload: createMergeEvent("update", "zapier/tfbuddy", "test-branch", "abc123", "abc123", 101),
					Context: context.Background(),
					Carrier: propagation.MapCarrier{},
				},
			},
			allowList:       "zapier/tfbuddy",
			wantProjectName: "zapier/tfbuddy",
			wantErr:         assert.NoError,
		},
		{
			name: "update with empty old rev returns early without error",
			args: args{
				msg: &MergeRequestEventMsg{
					Payload: createMergeEvent("update", "zapier/tfbuddy", "test-branch", "abc123", "", 101),
					Context: context.Background(),
					Carrier: propagation.MapCarrier{},
				},
			},
			allowList:       "zapier/tfbuddy",
			wantProjectName: "zapier/tfbuddy",
			wantErr:         assert.NoError,
		},
		{
			name: "unknown action returns early without error",
			args: args{
				msg: &MergeRequestEventMsg{
					Payload: createMergeEvent("unknown", "zapier/tfbuddy", "test-branch", "abc123", "", 101),
					Context: context.Background(),
					Carrier: propagation.MapCarrier{},
				},
			},
			allowList:       "zapier/tfbuddy",
			wantProjectName: "zapier/tfbuddy",
			wantErr:         assert.NoError,
		},
		{
			name: "returns correct project name from event",
			args: args{
				msg: &MergeRequestEventMsg{
					Payload: createMergeEvent("unknown", "different/project", "test-branch", "abc123", "", 101),
					Context: context.Background(),
					Carrier: propagation.MapCarrier{},
				},
			},
			allowList:       "different/project",
			wantProjectName: "different/project",
			wantErr:         assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TFBUDDY_GITLAB_PROJECT_ALLOW_LIST", tt.allowList)

			w := &GitlabEventWorker{
				tfc:       nil,
				gl:        nil,
				runstream: nil,
			}

			gotProjectName, err := w.processMergeRequestEvent(tt.args.msg)
			if !tt.wantErr(t, err, fmt.Sprintf("processMergeRequestEvent(%v)", tt.args.msg)) {
				return
			}
			assert.Equalf(t, tt.wantProjectName, gotProjectName, "processMergeRequestEvent(%v)", tt.args.msg)
		})
	}
}
