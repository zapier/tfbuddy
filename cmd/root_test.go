package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// runnableCommands returns every command under root that runs something,
// with the arguments that select it (e.g. ["tfc", "handler"]).
func runnableCommands(c *cobra.Command) [][]string {
	var out [][]string
	for _, sub := range c.Commands() {
		for _, args := range runnableCommands(sub) {
			out = append(out, append([]string{sub.Name()}, args...))
		}
	}
	if c.Runnable() && c != rootCmd {
		out = append(out, []string{})
	}
	return out
}

func findCommand(t *testing.T, args []string) *cobra.Command {
	t.Helper()
	c, _, err := rootCmd.Find(args)
	if err != nil {
		t.Fatalf("could not find command %v: %v", args, err)
	}
	return c
}

// TestCommandsHonourLogLevel runs each command (its own run and pre-run
// replaced by a stub that logs at every level) with the log level set to
// info, and checks that no trace or debug lines are written. `tfc handler`
// is the server, so it is the case that matters most.
func TestCommandsHonourLogLevel(t *testing.T) {
	commands := runnableCommands(rootCmd)

	var sawHandler bool
	for _, args := range commands {
		if strings.Join(args, " ") == "tfc handler" {
			sawHandler = true
		}
	}
	if !sawHandler {
		t.Fatalf("`tfc handler` not found among commands %v", commands)
	}

	for _, args := range commands {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			lines := runWithLogLevel(t, "info", args)

			var sawInfo bool
			for _, l := range lines {
				switch l.Level {
				case zerolog.TraceLevel.String(), zerolog.DebugLevel.String():
					t.Errorf("`tfbuddy %s` with log level info wrote a %s line: %q", strings.Join(args, " "), l.Level, l.Message)
				case zerolog.InfoLevel.String():
					if l.Message == "command ran" {
						sawInfo = true
					}
				}
			}
			if !sawInfo {
				t.Errorf("`tfbuddy %s` did not run, or its info line was dropped", strings.Join(args, " "))
			}
		})
	}
}

// TestCommandsApplyTraceLevel checks the other direction: the configured
// level is applied, not just defaulted to info.
func TestCommandsApplyTraceLevel(t *testing.T) {
	lines := runWithLogLevel(t, "trace", []string{"tfc", "handler"})

	var sawTrace bool
	for _, l := range lines {
		if l.Level == zerolog.TraceLevel.String() && l.Message == "command ran" {
			sawTrace = true
		}
	}
	if !sawTrace {
		t.Error("`tfbuddy tfc handler` with log level trace did not write its trace line")
	}
}

type logLine struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// runWithLogLevel executes `tfbuddy <args>` with TFBUDDY_LOG_LEVEL set and
// returns what was logged. The command's own pre-run and run are replaced by
// a stub that writes one line at each level, so nothing else (servers, API
// calls) runs.
func runWithLogLevel(t *testing.T, level string, args []string) []logLine {
	t.Helper()

	// zerolog's package init sets the global level to trace; start from
	// there, the way the process does.
	prevLevel := zerolog.GlobalLevel()
	prevLogger := log.Logger
	prevToken := tfcToken
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(prevLevel)
		log.Logger = prevLogger
		tfcToken = prevToken
		rootCmd.SetArgs(nil)
	})
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf)

	t.Setenv("TFBUDDY_LOG_LEVEL", level)
	t.Setenv("TFBUDDY_DEV_MODE", "false")
	t.Setenv("TFC_TOKEN", "not-a-real-token")
	tfcToken = ""

	c := findCommand(t, args)
	prevRun, prevRunE, prevPreRun, prevPreRunE := c.Run, c.RunE, c.PreRun, c.PreRunE
	t.Cleanup(func() {
		c.Run, c.RunE, c.PreRun, c.PreRunE = prevRun, prevRunE, prevPreRun, prevPreRunE
	})
	c.PreRun, c.PreRunE, c.RunE = nil, nil, nil
	c.Run = func(cmd *cobra.Command, args []string) {
		log.Trace().Msg("command ran")
		log.Debug().Msg("command ran")
		log.Info().Msg("command ran")
	}

	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("`tfbuddy %s` failed: %v", strings.Join(args, " "), err)
	}

	var lines []logLine
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		var l logLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("could not parse log line %q: %v", sc.Text(), err)
		}
		lines = append(lines, l)
	}
	return lines
}
