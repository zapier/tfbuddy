package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/zapier/tfbuddy/internal/config"
)

const (
	beginMarker = "<!-- BEGIN GENERATED CONFIGURATION -->"
	endMarker   = "<!-- END GENERATED CONFIGURATION -->"
)

//go:embed templates/usage_config.tmpl
var usageTemplate string

type templateOption struct {
	Env         string
	Flag        string
	Description string
	Default     string
}

type templateData struct {
	Options []templateOption
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	tmpl, err := template.New("usage").Parse(usageTemplate)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}

	var rendered bytes.Buffer
	data := templateData{Options: make([]templateOption, 0, len(config.DocumentationOptions()))}
	for _, option := range config.DocumentationOptions() {
		data.Options = append(data.Options, templateOption{
			Env:         strings.Join(option.EnvVars, "<br>"),
			Flag:        option.Flag,
			Description: option.Description,
			Default:     option.DefaultValue,
		})
	}
	if err := tmpl.Execute(&rendered, data); err != nil {
		return fmt.Errorf("render template: %w", err)
	}

	usagePath, err := findUsagePath()
	if err != nil {
		return err
	}
	current, err := os.ReadFile(usagePath)
	if err != nil {
		return fmt.Errorf("read usage doc: %w", err)
	}

	updated, err := replaceGeneratedBlock(string(current), rendered.String())
	if err != nil {
		return err
	}

	if err := os.WriteFile(usagePath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write usage doc: %w", err)
	}
	return nil
}

func findUsagePath() (string, error) {
	candidates := []string{
		filepath.Join("docs", "usage.md"),
		filepath.Join("..", "..", "docs", "usage.md"),
	}
	for _, candidate := range candidates {
		path := filepath.Clean(candidate)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("could not locate docs/usage.md from current working directory")
}

func replaceGeneratedBlock(current string, generated string) (string, error) {
	start := strings.Index(current, beginMarker)
	if start == -1 {
		return "", fmt.Errorf("missing begin marker %q", beginMarker)
	}
	end := strings.Index(current, endMarker)
	if end == -1 {
		return "", fmt.Errorf("missing end marker %q", endMarker)
	}
	if end < start {
		return "", fmt.Errorf("configuration markers out of order")
	}

	var output strings.Builder
	output.WriteString(current[:start+len(beginMarker)])
	output.WriteString("\n")
	output.WriteString(strings.TrimRight(generated, "\n"))
	output.WriteString("\n")
	output.WriteString(current[end:])
	return output.String(), nil
}
