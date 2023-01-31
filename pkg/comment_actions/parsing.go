package comment_actions

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jessevdk/go-flags"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/tfc_trigger"
	"github.com/zapier/tfbuddy/pkg/utils"
)

var (
	ErrNotTFCCommand = errors.New("not a TFC command")
	ErrOtherTFTool   = errors.New("use 'tfc' to interact with tfbuddy")
	ErrNoNotePassed  = errors.New("no notes passed in note block")
	ErrInvalidAction = errors.New("invalid tfc action")
)

type CommentOpts struct {
	TriggerOpts *tfc_trigger.TFCTriggerOptions
	Args        CommentArgs `positional-args:"yes" required:"yes"`
}

type CommentArgs struct {
	Agent   string
	Command string
	Rest    []string
}

func ParseCommentCommand(noteBody string) (*CommentOpts, error) {
	comment := strings.TrimSpace(strings.ToLower(noteBody))
	words := strings.Fields(comment)

	if len(words) == 0 {
		return nil, ErrNoNotePassed
	}

	if len(words)%2 != 0 {
		log.Debug().Str("comment", comment[0:10]).Msg("not a tfc command")
		return nil, ErrNotTFCCommand
	}

	opts := &CommentOpts{
		TriggerOpts: &tfc_trigger.TFCTriggerOptions{},
	}
	_, err := flags.ParseArgs(opts, words)
	if err != nil {
		log.Error().Err(err).Msg("error parsing comment as command")
		return nil, fmt.Errorf("could not parse comment as command. %w", utils.ErrPermanent)
	}

	if opts.Args.Agent == "terraform" || opts.Args.Agent == "atlantis" {
		log.Debug().Str("comment", opts.Args.Agent).Msg("Use tfc to interact with tfbuddy")
		return nil, ErrOtherTFTool
	}
	if opts.Args.Agent != "tfc" {
		return nil, ErrNotTFCCommand
	}

	opts.TriggerOpts.Action = tfc_trigger.CheckTriggerAction(opts.Args.Command)
	if opts.TriggerOpts.Action == tfc_trigger.InvalidAction {
		return nil, ErrInvalidAction
	}

	return opts, nil
}
