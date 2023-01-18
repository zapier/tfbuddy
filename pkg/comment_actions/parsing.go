package comment_actions

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jessevdk/go-flags"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/utils"
)

var (
	ErrNotTFCCommand = errors.New("not a TFC command")
	ErrOtherTFTool   = errors.New("Use 'tfc' to interact with tfbuddy.")
)

type CommentOpts struct {
	Args      CommentArgs `positional-args:"yes" required:"yes"`
	Workspace string      `short:"w" long:"workspace" description:"A specific terraform Workspace to use" required:"false"`
}

type CommentArgs struct {
	Agent   string
	Command string
	Rest    []string
}

func ParseCommentCommand(noteBody string) (*CommentOpts, error) {
	comment := strings.TrimSpace(strings.ToLower(noteBody))

	words := strings.Fields(comment)
	if len(words) < 2 || len(words) > 4 {
		log.Debug().Str("comment", comment[0:10]).Msg("not a tfc command")
		return nil, ErrNotTFCCommand
	}

	opts := &CommentOpts{}
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

	return opts, nil
}
