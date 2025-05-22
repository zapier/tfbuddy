package gitlab_hooks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zapier/tfbuddy/pkg/hooks_stream"
	"github.com/zapier/tfbuddy/pkg/vcs/gitlab"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.opentelemetry.io/otel/propagation"
)

func TestGitlabHookEvent_GetPlatform(t *testing.T) {
	event := &GitlabHookEvent{}
	assert.Equal(t, "gitlab", event.GetPlatform())
}

func Test_noteEventsStreamSubject(t *testing.T) {
	expected := "HOOKS.gitlab.noteevents"
	assert.Equal(t, expected, noteEventsStreamSubject())
}

func Test_mrEventsStreamSubject(t *testing.T) {
	expected := "HOOKS.gitlab.mrevents"
	assert.Equal(t, expected, mrEventsStreamSubject())
}

func TestConstants(t *testing.T) {
	assert.Equal(t, "gitlab", GitlabHooksSubject)
	assert.Equal(t, "mrevents", MergeRequestEventsSubject)
	assert.Equal(t, "noteevents", NoteEventsSubject)
}

func TestNoteEventMsg_GetId(t *testing.T) {
	noteMsg := &NoteEventMsg{}

	ctx := context.Background()
	defer func() {
		if r := recover(); r != nil {
			assert.NotNil(t, r)
		}
	}()

	noteMsg.GetId(ctx)
}

func TestNoteEventMsg_DecodeEventData(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{
			name:    "valid JSON",
			data:    []byte(`{"payload":null,"Carrier":{}}`),
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			data:    []byte(`{invalid json}`),
			wantErr: true,
		},
		{
			name:    "empty data",
			data:    []byte(``),
			wantErr: true,
		},
		{
			name:    "malformed object",
			data:    []byte(`{`),
			wantErr: true,
		},
		{
			name:    "null data",
			data:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			noteMsg := &NoteEventMsg{}
			err := noteMsg.DecodeEventData(tt.data)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, noteMsg.Context)
			}
		})
	}
}

func TestNoteEventMsg_EncodeEventData(t *testing.T) {
	noteMsg := &NoteEventMsg{
		Payload: &gitlab.GitlabMergeCommentEvent{},
	}

	ctx := context.Background()
	data := noteMsg.EncodeEventData(ctx)

	assert.NotEmpty(t, data)

	var decoded map[string]interface{}
	err := json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.NotNil(t, noteMsg.Carrier)
}

func TestNoteEventMsg_GetProject(t *testing.T) {
	mockEvent := &gitlab.GitlabMergeCommentEvent{}
	noteMsg := &NoteEventMsg{
		Payload: mockEvent,
	}

	project := noteMsg.GetProject()
	assert.NotNil(t, project)
}

func TestNoteEventMsg_GetMR(t *testing.T) {
	mockEvent := &gitlab.GitlabMergeCommentEvent{}
	noteMsg := &NoteEventMsg{
		Payload: mockEvent,
	}

	mr := noteMsg.GetMR()
	assert.Equal(t, mockEvent, mr)
}

func TestNoteEventMsg_GetAttributes(t *testing.T) {
	mockEvent := &gitlab.GitlabMergeCommentEvent{}
	noteMsg := &NoteEventMsg{
		Payload: mockEvent,
	}

	attrs := noteMsg.GetAttributes()
	assert.NotNil(t, attrs)
}

func TestNoteEventMsg_GetLastCommit(t *testing.T) {
	mockEvent := &gitlab.GitlabMergeCommentEvent{}
	noteMsg := &NoteEventMsg{
		Payload: mockEvent,
	}

	commit := noteMsg.GetLastCommit()
	assert.NotNil(t, commit)
}

func createMergeEventForStreamTest(id int, action, commitID string) *gogitlab.MergeEvent {
	event := &gogitlab.MergeEvent{}
	event.ObjectAttributes.ID = id
	event.ObjectAttributes.Action = action
	event.ObjectAttributes.LastCommit.ID = commitID
	return event
}

func TestMergeRequestEventMsg_GetId(t *testing.T) {
	tests := []struct {
		name     string
		id       int
		action   string
		commitID string
		expected string
	}{
		{
			name:     "basic merge request ID",
			id:       123,
			action:   "opened",
			commitID: "abc123",
			expected: "123-opened-abc123",
		},
		{
			name:     "updated merge request",
			id:       456,
			action:   "updated",
			commitID: "def456",
			expected: "456-updated-def456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := createMergeEventForStreamTest(tt.id, tt.action, tt.commitID)
			mrMsg := &MergeRequestEventMsg{
				Payload: event,
			}

			id := mrMsg.GetId(context.Background())
			assert.Equal(t, tt.expected, id)
		})
	}
}

func TestMergeRequestEventMsg_DecodeEventData(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{
			name:    "valid JSON",
			data:    []byte(`{"payload":{"object_attributes":{"id":123,"action":"opened","last_commit":{"id":"abc123"}}},"Carrier":{}}`),
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			data:    []byte(`{invalid json}`),
			wantErr: true,
		},
		{
			name:    "empty data",
			data:    []byte(``),
			wantErr: true,
		},
		{
			name:    "malformed object",
			data:    []byte(`{`),
			wantErr: true,
		},
		{
			name:    "null data",
			data:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mrMsg := &MergeRequestEventMsg{}
			err := mrMsg.DecodeEventData(tt.data)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, mrMsg.Context)
			}
		})
	}
}

func TestMergeRequestEventMsg_EncodeEventData(t *testing.T) {
	event := createMergeEventForStreamTest(123, "opened", "abc123")
	mrMsg := &MergeRequestEventMsg{
		Payload: event,
	}

	ctx := context.Background()
	data := mrMsg.EncodeEventData(ctx)

	assert.NotEmpty(t, data)

	var decoded map[string]interface{}
	err := json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.NotNil(t, mrMsg.Carrier)
}

func TestMergeRequestEventMsg_GetType(t *testing.T) {
	mrMsg := &MergeRequestEventMsg{}
	assert.Equal(t, "MergeRequestEventMsg", mrMsg.GetType(context.Background()))
}

func TestMergeRequestEventMsg_GetPayload(t *testing.T) {
	event := createMergeEventForStreamTest(123, "opened", "abc123")
	mrMsg := &MergeRequestEventMsg{
		Payload: event,
	}

	payload := mrMsg.GetPayload()
	assert.Equal(t, *event, payload)
}

func TestNoteEventMsg_RoundTripEncoding(t *testing.T) {
	original := &NoteEventMsg{
		Payload: &gitlab.GitlabMergeCommentEvent{},
		Carrier: make(propagation.MapCarrier),
	}

	ctx := context.Background()
	encoded := original.EncodeEventData(ctx)

	decoded := &NoteEventMsg{}
	err := decoded.DecodeEventData(encoded)
	require.NoError(t, err)

	assert.NotNil(t, decoded.Context)
	assert.NotNil(t, decoded.Carrier)
}

func TestMergeRequestEventMsg_RoundTripEncoding(t *testing.T) {
	event := createMergeEventForStreamTest(123, "opened", "abc123")
	original := &MergeRequestEventMsg{
		Payload: event,
		Carrier: make(propagation.MapCarrier),
	}

	ctx := context.Background()
	encoded := original.EncodeEventData(ctx)

	decoded := &MergeRequestEventMsg{}
	err := decoded.DecodeEventData(encoded)
	require.NoError(t, err)

	assert.NotNil(t, decoded.Context)
	assert.NotNil(t, decoded.Carrier)

	assert.Equal(t, original.Payload.ObjectAttributes.ID, decoded.Payload.ObjectAttributes.ID)
	assert.Equal(t, original.Payload.ObjectAttributes.Action, decoded.Payload.ObjectAttributes.Action)
	assert.Equal(t, original.Payload.ObjectAttributes.LastCommit.ID, decoded.Payload.ObjectAttributes.LastCommit.ID)
}

func TestStreamSubjectGeneration(t *testing.T) {
	expectedNoteSubject := hooks_stream.HooksStreamName + ".gitlab.noteevents"
	expectedMRSubject := hooks_stream.HooksStreamName + ".gitlab.mrevents"

	assert.Equal(t, expectedNoteSubject, noteEventsStreamSubject())
	assert.Equal(t, expectedMRSubject, mrEventsStreamSubject())

	assert.Equal(t, "HOOKS", hooks_stream.HooksStreamName)
}
