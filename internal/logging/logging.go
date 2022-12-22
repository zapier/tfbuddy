package logging

import (
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	colorBlack = iota + 30
	colorRed
	colorGreen
	colorYellow
	colorBlue
	colorMagenta
	colorCyan
	colorWhite

	colorBold     = 1
	colorDarkGray = 90
)

func SetupLogOutput(level zerolog.Level) {
	// Setup Human friendly Console Output for dev mode
	devMode := os.Getenv("TFBUDDY_DEV_MODE")
	if devMode != "" {
		output := zerolog.ConsoleWriter{Out: os.Stdout}
		output.FormatLevel = formatLevel(false)
		output.FormatMessage = func(i interface{}) string {
			return fmt.Sprintf("| %-60s ", i)
		}
		log.Logger = log.Output(output)
	}

	// Default level is info, unless debug flag is present
	zerolog.SetGlobalLevel(level)
	log.Debug().Msg("Debug level logging enabled.")
	log.Trace().Msg("Trace level logging enabled.")
	log.Info().Msg("Initialized logger.")
}

func formatLevel(noColor bool) zerolog.Formatter {
	return func(i interface{}) string {
		var l string

		if i == nil {
			return colorize("HTTP", colorMagenta, noColor)
		}

		if lvl, ok := i.(zerolog.Level); ok {
			i = lvl.String()
		}

		if ll, ok := i.(string); ok {
			switch strings.ToLower(ll) {
			case "trace":
				l = colorize("Trace", colorBlue, noColor)
			case "debug":
				l = colorize("Debug", colorYellow, noColor)
			case "info":
				l = colorize("Info", colorGreen, noColor)
			case "warn":
				l = colorize("Warn", colorRed, noColor)
			case "error":
				l = colorize(colorize("Error", colorRed, noColor), colorBold, noColor)
			case "fatal":
				l = colorize(colorize("Fatal", colorRed, noColor), colorBold, noColor)
			case "panic":
				l = colorize(colorize("Panic", colorRed, noColor), colorBold, noColor)
			default:
				l = colorize("???", colorBold, noColor)
			}
		} else {
			l = strings.ToUpper(fmt.Sprintf("%v", i))
			if len(l) > 10 {
				l = l[0:9]
			}
		}
		return l
	}
}

// colorize returns the string s wrapped in ANSI code c, unless disabled is true.
func colorize(s interface{}, c int, disabled bool) string {
	if disabled {
		return fmt.Sprintf("%s", s)
	}
	return fmt.Sprintf("\x1b[%dm%v\x1b[0m", c, s)
}
