package hooks

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// eventValue stands in for content of a webhook event (a PR body, a
// comment). It must never reach the logs.
const eventValue = "event-value-that-must-not-be-logged"

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	prevLevel := zerolog.GlobalLevel()
	prevLogger := log.Logger
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(prevLevel)
		log.Logger = prevLogger
	})
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	buf := &bytes.Buffer{}
	log.Logger = zerolog.New(buf)
	return buf
}

func TestDecodeEventData_LogsIdentifiersOnly(t *testing.T) {
	tests := []struct {
		name string
		msg  interface{ DecodeEventData([]byte) error }
		data string
	}{
		{
			name: "pull request",
			msg:  &PullRequestEventMsg{},
			data: `{"payload": {"action": "opened", "number": 7,
				"pull_request": {"url": "https://api.github.com/repos/example-org/example-repo/pulls/7", "body": "` + eventValue + `"},
				"repository": {"full_name": "example-org/example-repo"}}}`,
		},
		{
			name: "issue comment",
			msg:  &GithubIssueCommentEventMsg{},
			data: `{"payload": {"action": "created",
				"comment": {"id": 42, "body": "` + eventValue + `"},
				"issue": {"number": 7, "body": "` + eventValue + `"},
				"repository": {"full_name": "example-org/example-repo"}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)

			if err := tt.msg.DecodeEventData([]byte(tt.data)); err != nil {
				t.Fatalf("DecodeEventData() error = %v", err)
			}

			out := logs.String()
			if strings.Contains(out, eventValue) {
				t.Error("the event body was written to the logs")
			}
			if !strings.Contains(out, "example-org/example-repo") {
				t.Errorf("expected a line naming the repository, got:\n%s", out)
			}
		})
	}
}
