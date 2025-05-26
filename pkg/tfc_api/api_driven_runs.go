package tfc_api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
)

type ApiRunOptions struct {
	// IsApply = true if this run will auto apply
	IsApply bool
	// Path is the path to the directory where a repo source has been cloned
	Path string
	// Message is the Terraform Cloud run title.
	Message string
	// Organization is the Terraform Cloud organization name
	Organization string
	// Workspace is the Terraform Cloud workspace name
	Workspace string
	// Terraform Version
	TFVersion string
	// Terraform Target
	Target string
	// Terraform AllowEmptyApply
	AllowEmptyRun bool
}

// CreateRunFromSource creates a new Terraform Cloud run from source files
func (c *TFCClient) CreateRunFromSource(ctx context.Context, opts *ApiRunOptions) (*tfe.Run, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "CreateRunFromSource")
	defer span.End()

	log := log.With().Str("workspace", opts.Workspace).Logger()

	ws, err := c.GetWorkspaceByName(ctx, opts.Organization, opts.Workspace)
	if err != nil {
		log.Error().Msg("could not get workspace")
		return nil, err
	}

	cv, err := c.createConfigurationVersion(opts, ctx, ws)
	if err != nil {
		return nil, err
	}

	// TODO: Clean this up maybe check for valid Versions from TFCloud
	var tfVersion *string = nil
	var tfPlanOnly *bool = nil
	var tfAllowEmptyApply = tfe.Bool(false)
	var tfTarget []string

	if opts.TFVersion != "" && !opts.IsApply {
		log.Debug().Str("version", opts.TFVersion).Msg("setting tf version")
		tfVersion = tfe.String(opts.TFVersion)
		tfPlanOnly = tfe.Bool(true)
	}

	if opts.AllowEmptyRun {
		tfAllowEmptyApply = tfe.Bool(true)
	}

	if opts.Target != "" {
		tfTarget = append(tfTarget, strings.Split(opts.Target, ",")...)
	}

	// create run for new CV
	run, err := c.Client.Runs.Create(ctx, tfe.RunCreateOptions{
		Message:              tfe.String(opts.Message),
		ConfigurationVersion: cv,
		Workspace:            ws,
		AutoApply:            tfe.Bool(opts.IsApply),
		TargetAddrs:          tfTarget,
		PlanOnly:             tfPlanOnly,
		TerraformVersion:     tfVersion,
		AllowEmptyApply:      tfAllowEmptyApply,
	})
	if err != nil {
		log.Error().Err(err).Msg("could create run")
		return nil, err
	}

	run.Workspace = ws
	// TFC API is weird, it doesn't return the correct value for Speculative, so we override here.
	run.ConfigurationVersion.Speculative = !opts.IsApply

	return run, nil
}

func (c *TFCClient) createConfigurationVersion(opts *ApiRunOptions, ctx context.Context, ws *tfe.Workspace) (*tfe.ConfigurationVersion, error) {
	cfgOpts := tfe.ConfigurationVersionCreateOptions{
		AutoQueueRuns: tfe.Bool(false),
		Speculative:   tfe.Bool(!opts.IsApply),
	}
	cv, err := c.Client.ConfigurationVersions.Create(ctx, ws.ID, cfgOpts)
	if err != nil {
		log.Error().Err(err).Msg("could not create TFC configuration version")
		return cv, err
	}
	log.Debug().Interface("CV", cv).Msg("Created new CV")

	err = c.Client.ConfigurationVersions.Upload(ctx, cv.UploadURL, opts.Path)
	if err != nil {
		log.Error().Err(err).Msg("could not upload config")
		return cv, err
	}

	cv2, err := c.pollCVWhilePending(ctx, cv)
	if err != nil {
		log.Error().Err(err).Msg("could not read configuration version")
		return nil, err
	}
	log.Debug().Interface("CV", cv2).Msg("Uploaded source to CV")
	return cv2, err
}

func (c *TFCClient) pollCVWhilePending(ctx context.Context, cv *tfe.ConfigurationVersion) (*tfe.ConfigurationVersion, error) {
	for i := 0; i < 30; i++ {
		cv, err := c.Client.ConfigurationVersions.Read(ctx, cv.ID)
		if err != nil {
			return nil, err
		}
		if cv.Status != tfe.ConfigurationPending {
			return cv, nil
		}
		time.Sleep(1 * time.Second)
	}
	return nil, fmt.Errorf("timed out waiting for CV to move from pending")
}
