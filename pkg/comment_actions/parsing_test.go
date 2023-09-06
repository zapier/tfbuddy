package comment_actions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
)

func TestParseCommentCommand(t *testing.T) {
	tcs := []struct {
		noteBody     string
		expectedOpts *CommentOpts
		e            error
		testName     string
	}{
		{"", nil, ErrNoNotePassed, "empty test"},
		{"tfc apply", &CommentOpts{
			TriggerOpts: &tfc_trigger.TFCTriggerOptions{
				Action: tfc_trigger.ApplyAction,
			},
			Args: CommentArgs{
				Agent:   "tfc",
				Command: "apply",
			},
		}, nil, "simple plan"},
		{"tfc plan -w fake_space", &CommentOpts{
			TriggerOpts: &tfc_trigger.TFCTriggerOptions{
				Workspace: "fake_space",
				Action:    tfc_trigger.PlanAction,
			},
			Args: CommentArgs{
				Agent:   "tfc",
				Command: "plan",
			},
		}, nil, "simple plan with workspace"},
		{"tfc apply -w fake_space -v 1.1.7", &CommentOpts{
			TriggerOpts: &tfc_trigger.TFCTriggerOptions{
				Workspace: "fake_space",
				TFVersion: "1.1.7",
				Action:    tfc_trigger.ApplyAction,
			},
			Args: CommentArgs{
				Agent:   "tfc",
				Command: "apply",
			},
		}, nil, "simple plan with workspace and version"},
		{"tfc plan -v 1.1.8", &CommentOpts{
			TriggerOpts: &tfc_trigger.TFCTriggerOptions{
				TFVersion: "1.1.8",
				Action:    tfc_trigger.PlanAction,
			},
			Args: CommentArgs{
				Agent:   "tfc",
				Command: "plan",
			},
		}, nil, "simple plan with version"},
		{"tfc plan -w -v 1.1.8",
			nil,
			ErrPermanent,
			"not a valid command",
		},
		{"tfc apply -e", &CommentOpts{
			TriggerOpts: &tfc_trigger.TFCTriggerOptions{
				Action:        tfc_trigger.ApplyAction,
				AllowEmptyRun: true,
			},
			Args: CommentArgs{
				Agent:   "tfc",
				Command: "apply",
			},
		}, nil, "short flag for allow empty run"},
		{"tfc apply --allow_empty_run", &CommentOpts{
			TriggerOpts: &tfc_trigger.TFCTriggerOptions{
				Action:        tfc_trigger.ApplyAction,
				AllowEmptyRun: true,
			},
			Args: CommentArgs{
				Agent:   "tfc",
				Command: "apply",
			},
		}, nil, "long flag for allow empty run"},
	}

	for _, tc := range tcs {
		t.Run(tc.testName, func(t *testing.T) {
			opts, err := ParseCommentCommand(tc.noteBody)
			assert.Equal(t, tc.expectedOpts, opts, tc.testName)
			assert.ErrorIs(t, err, tc.e, tc.testName)
		})
	}
}
