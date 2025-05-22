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
		}, nil, "apply command"},
		{"tfc plan", &CommentOpts{
			TriggerOpts: &tfc_trigger.TFCTriggerOptions{
				Action: tfc_trigger.PlanAction,
			},
			Args: CommentArgs{
				Agent:   "tfc",
				Command: "plan",
			},
		}, nil, "plan command"},
		{"tfc plan -w -v 1.1.8", nil, ErrPermanent, "malformed command"},
		{"tfc apply -k", nil, ErrPermanent, "invalid flag"},
		{"terraform apply", nil, ErrOtherTFTool, "terraform agent"},
		{"atlantis plan", nil, ErrOtherTFTool, "atlantis agent"},
		{"tfc invalid_action", nil, ErrInvalidAction, "invalid action"},
		{"some_tool do_something", nil, ErrNotTFCCommand, "non-tfc agent"},
		{"TFC ApPlY", &CommentOpts{
			TriggerOpts: &tfc_trigger.TFCTriggerOptions{
				Action: tfc_trigger.ApplyAction,
			},
			Args: CommentArgs{
				Agent:   "tfc",
				Command: "apply",
			},
		}, nil, "mixed case input"},
		{"   tfc   plan   -w   workspace1   ", &CommentOpts{
			TriggerOpts: &tfc_trigger.TFCTriggerOptions{
				Workspace: "workspace1",
				Action:    tfc_trigger.PlanAction,
			},
			Args: CommentArgs{
				Agent:   "tfc",
				Command: "plan",
			},
		}, nil, "additional whitespace"},
		{"tfc plan -w workspace1", &CommentOpts{
			TriggerOpts: &tfc_trigger.TFCTriggerOptions{
				Workspace: "workspace1",
				Action:    tfc_trigger.PlanAction,
			},
			Args: CommentArgs{
				Agent:   "tfc",
				Command: "plan",
			},
		}, nil, "workspace option"},
		{"tfc apply -v 1.1.7", &CommentOpts{
			TriggerOpts: &tfc_trigger.TFCTriggerOptions{
				TFVersion: "1.1.7",
				Action:    tfc_trigger.ApplyAction,
			},
			Args: CommentArgs{
				Agent:   "tfc",
				Command: "apply",
			},
		}, nil, "version option"},
		{"tfc apply -e", &CommentOpts{
			TriggerOpts: &tfc_trigger.TFCTriggerOptions{
				Action:        tfc_trigger.ApplyAction,
				AllowEmptyRun: true,
			},
			Args: CommentArgs{
				Agent:   "tfc",
				Command: "apply",
			},
		}, nil, "empty run option short"},
		{"tfc apply --allow_empty_run", &CommentOpts{
			TriggerOpts: &tfc_trigger.TFCTriggerOptions{
				Action:        tfc_trigger.ApplyAction,
				AllowEmptyRun: true,
			},
			Args: CommentArgs{
				Agent:   "tfc",
				Command: "apply",
			},
		}, nil, "empty run option long"},
		{"tfc refresh", &CommentOpts{
			TriggerOpts: &tfc_trigger.TFCTriggerOptions{
				Action: tfc_trigger.RefreshAction,
			},
			Args: CommentArgs{
				Agent:   "tfc",
				Command: "refresh",
			},
		}, nil, "refresh command"},
		{"tfc apply -t module.resource", &CommentOpts{
			TriggerOpts: &tfc_trigger.TFCTriggerOptions{
				Action: tfc_trigger.ApplyAction,
				Target: "module.resource",
			},
			Args: CommentArgs{
				Agent:   "tfc",
				Command: "apply",
			},
		}, nil, "target option"},
	}

	for _, tc := range tcs {
		t.Run(tc.testName, func(t *testing.T) {
			opts, err := ParseCommentCommand(tc.noteBody)

			// Check error first
			if tc.e != nil {
				assert.Error(t, err)
				assert.ErrorIs(t, err, tc.e)
				assert.Nil(t, opts)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, opts)

				// Check if structures match
				assert.Equal(t, tc.expectedOpts.Args.Agent, opts.Args.Agent)
				assert.Equal(t, tc.expectedOpts.Args.Command, opts.Args.Command)

				// Check TriggerOpts
				assert.Equal(t, tc.expectedOpts.TriggerOpts.Action, opts.TriggerOpts.Action)

				if tc.expectedOpts.TriggerOpts.Workspace != "" {
					assert.Equal(t, tc.expectedOpts.TriggerOpts.Workspace, opts.TriggerOpts.Workspace)
				}

				if tc.expectedOpts.TriggerOpts.TFVersion != "" {
					assert.Equal(t, tc.expectedOpts.TriggerOpts.TFVersion, opts.TriggerOpts.TFVersion)
				}

				if tc.expectedOpts.TriggerOpts.AllowEmptyRun {
					assert.True(t, opts.TriggerOpts.AllowEmptyRun)
				}

				if tc.expectedOpts.TriggerOpts.Target != "" {
					assert.Equal(t, tc.expectedOpts.TriggerOpts.Target, opts.TriggerOpts.Target)
				}
			}
		})
	}
}
