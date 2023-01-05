package gitlab_hooks

import (
	"encoding/json"
	"fmt"

	gogitlab "github.com/xanzy/go-gitlab"
	"github.com/zapier/tfbuddy/pkg/gitlab"
	"github.com/zapier/tfbuddy/pkg/hooks_stream"
	"github.com/zapier/tfbuddy/pkg/vcs"
)

const GitlabHooksSubject = "gitlab"
const MergeRequestEventsSubject = "mrevents"
const NoteEventsSubject = "noteevents"

func noteEventsStreamSubject() string {
	return fmt.Sprintf("%s.%s.%s", hooks_stream.HooksStreamName, GitlabHooksSubject, NoteEventsSubject)
}

// GitlabHookEvent represents all types of Gitlab Hooks events to be processed.
type GitlabHookEvent struct {
}

func (e *GitlabHookEvent) GetPlatform() string {
	return "gitlab"
}

// ----------------------------------------------

type NoteEventMsg struct {
	GitlabHookEvent

	payload *gitlab.GitlabMergeCommentEvent
}

func (e *NoteEventMsg) GetId() string {
	return e.payload.GetDiscussionID()
}

func (e *NoteEventMsg) DecodeEventData(b []byte) error {
	d := &gitlab.GitlabMergeCommentEvent{}
	err := json.Unmarshal(b, d)
	if err != nil {
		return err
	}
	e.payload = d
	return nil
}

func (e *NoteEventMsg) EncodeEventData() []byte {
	b, _ := json.Marshal(e.payload)
	return b
}

func (e *NoteEventMsg) GetProject() vcs.Project {
	return e.payload.GetProject()
}

func (e *NoteEventMsg) GetMR() vcs.MR {
	return e.payload
}

func (e *NoteEventMsg) GetAttributes() vcs.MRAttributes {
	return e.payload.GetAttributes()
}

func (e *NoteEventMsg) GetLastCommit() vcs.Commit {
	return e.payload.GetLastCommit()
}

// ----------------------------------------------

func mrEventsStreamSubject() string {
	return fmt.Sprintf("%s.%s.%s", hooks_stream.HooksStreamName, GitlabHooksSubject, MergeRequestEventsSubject)
}

type MergeRequestEventMsg struct {
	GitlabHookEvent

	payload *gogitlab.MergeEvent
}

func (e *MergeRequestEventMsg) GetId() string {
	return fmt.Sprintf("%d-%s", e.payload.ObjectAttributes.ID, e.payload.ObjectAttributes.Action)
}

func (e *MergeRequestEventMsg) DecodeEventData(b []byte) error {
	d := &gogitlab.MergeEvent{}
	err := json.Unmarshal(b, d)
	if err != nil {
		return err
	}
	e.payload = d
	return nil
}

func (e *MergeRequestEventMsg) EncodeEventData() []byte {
	b, _ := json.Marshal(e.payload)
	return b
}

func (e *MergeRequestEventMsg) GetType() string {
	return "MergeRequestEventMsg"
}

func (e *MergeRequestEventMsg) GetPayload() interface{} {
	return *e.payload
}
