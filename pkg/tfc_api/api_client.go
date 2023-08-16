package tfc_api

import (
	"context"
	"fmt"
	"os"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

//go:generate mockgen -source api_client.go -destination=../mocks/mock_tfc_api.go -package=mocks github.com/zapier/tfbuddy/pkg/tfc_api
type ApiClient interface {
	GetPlanOutput(id string) ([]byte, error)
	GetRun(ctx context.Context, id string) (*tfe.Run, error)
	GetWorkspaceByName(ctx context.Context, org, name string) (*tfe.Workspace, error)
	GetWorkspaceById(ctx context.Context, id string) (*tfe.Workspace, error)
	CreateRunFromSource(ctx context.Context, opts *ApiRunOptions) (*tfe.Run, error)
	LockUnlockWorkspace(ctx context.Context, workspace string, reason string, tag string, lock bool) error
	AddTags(ctx context.Context, workspace string, prefix string, value string) error
	RemoveTagsByQuery(ctx context.Context, workspace string, query string) error
	GetTagsByQuery(ctx context.Context, workspace string, query string) ([]string, error)
}

type TFCClient struct {
	Client *tfe.Client
}

func NewTFCClient() ApiClient {
	token := os.Getenv("TFC_TOKEN")
	if token == "" {
		log.Fatal().Msg("TFC_TOKEN not set")
	}

	config := &tfe.Config{
		Token: token,
	}

	var err error
	tfcClient, err := tfe.NewClient(config)
	if err != nil {
		log.Fatal().Err(err)
	}

	return &TFCClient{Client: tfcClient}
}

func (t *TFCClient) GetRun(ctx context.Context, id string) (*tfe.Run, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "GetTFRun", trace.WithAttributes(attribute.String("run_id", id)))
	defer span.End()

	run, err := t.Client.Runs.ReadWithOptions(
		ctx,
		id,
		&tfe.RunReadOptions{
			Include: []tfe.RunIncludeOpt{tfe.RunPlan, tfe.RunWorkspace, tfe.RunConfigVer, tfe.RunApply},
		},
	)
	if err != nil {
		//log.Error().Err(err)
		return nil, err
	}

	return run, nil
}

func (t *TFCClient) GetPlanOutput(id string) ([]byte, error) {
	b, err := t.Client.Plans.ReadJSONOutput(
		context.Background(),
		id,
	)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (t *TFCClient) GetWorkspaceById(ctx context.Context, id string) (*tfe.Workspace, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "GetTFWorkspaceById", trace.WithAttributes(attribute.String("run_id", id)))
	defer span.End()
	return t.Client.Workspaces.ReadByID(ctx, id)
}

func (t *TFCClient) GetWorkspaceByName(ctx context.Context, org, name string) (*tfe.Workspace, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "GetTFWorkspaceByName", trace.WithAttributes(attribute.String("name", name), attribute.String("org", org)))
	defer span.End()
	return t.Client.Workspaces.ReadWithOptions(
		ctx,
		org,
		name,
		&tfe.WorkspaceReadOptions{Include: []tfe.WSIncludeOpt{tfe.WSOrganization}},
	)
}

func (t *TFCClient) LockUnlockWorkspace(ctx context.Context, workspaceID string, reason string, tag string, lock bool) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "LockUnlockWorkspace", trace.WithAttributes(
		attribute.String("workspaceID", workspaceID),
		attribute.Bool("lock", lock),
	))
	defer span.End()
	LockOptions := tfe.WorkspaceLockOptions{Reason: &reason}
	TagPrefix := "gl-lock"

	if lock {
		_, err := t.Client.Workspaces.Lock(
			ctx,
			workspaceID,
			LockOptions,
		)
		log.Info().Msgf("locking workspace")
		if err != nil {
			log.Error().Err(err)
			return err
		}
		err = t.AddTags(ctx, workspaceID, TagPrefix, tag)
		if err != nil {
			log.Error().Err(err)
			return err
		}
	} else {
		_, err := t.Client.Workspaces.Unlock(
			ctx,
			workspaceID,
		)
		log.Info().Msgf("unlocking workspace")
		if err != nil {
			log.Error().Err(err)
			return err
		}
		err = t.RemoveTagsByQuery(ctx, workspaceID, TagPrefix)
		if err != nil {
			log.Error().Err(err)
			return err
		}
	}
	return nil
}

// AddTags Adds a tag to a named terraform workspace. The function returns an error if there's an error generated while trying to add the tags.
// The tags take the format of prefix dash value, which is just a convention and not required by terraform cloud for naming format.
// The tag, however, will be lowercased by terraform cloud, and in any retrieval operations.
func (t *TFCClient) AddTags(ctx context.Context, workspace string, prefix string, value string) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "AddTags", trace.WithAttributes(
		attribute.String("workspace", workspace),
	))
	defer span.End()
	LockTag := &tfe.Tag{
		Name: fmt.Sprintf("%s-%s", prefix, value),
	}
	span.SetAttributes(attribute.String("tag", LockTag.Name))

	AddTagsOptions := tfe.WorkspaceAddTagsOptions{
		Tags: []*tfe.Tag{LockTag},
	}

	err := t.Client.Workspaces.AddTags(
		ctx,
		workspace,
		AddTagsOptions,
	)
	return err

}

// RemoveTagsByQuery removes all tags matching a query from a terraform cloud workspace.  It returns an error if one is returned fom searching or removing tags.// Note: the query will match anywhere in the tag, so common substrings should be avoided.
func (t *TFCClient) RemoveTagsByQuery(ctx context.Context, workspace string, query string) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "RemoveTagsByQuery", trace.WithAttributes(
		attribute.String("workspace", workspace),
	))
	defer span.End()

	taglist, err := t.GetTagsByQuery(ctx, workspace, query)
	if err != nil {
		log.Error().Err(err)
		return err
	}
	var removeTags []*tfe.Tag
	for _, tag := range taglist {
		retag := tfe.Tag{Name: tag}
		removeTags = append(removeTags, &retag)
	}

	if len(removeTags) == 0 {
		return nil
	}
	RemoveTagsOptions := tfe.WorkspaceRemoveTagsOptions{
		Tags: removeTags,
	}
	err = t.Client.Workspaces.RemoveTags(
		ctx,
		workspace,
		RemoveTagsOptions,
	)
	if err != nil {
		log.Error().Err(err)
		return err
	}
	return nil
}

// GetTagsByQuery returns a list of values of tags on a terraform workspace matching the query string.
// It operates on strings reporesenting the value of the tag and internally converts it to and from the upstreams tag struct as needed.  Attempting to query tags based on their tag ID will not match the tag.
func (t *TFCClient) GetTagsByQuery(ctx context.Context, workspace string, query string) ([]string, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "GetTagsByQuery", trace.WithAttributes(
		attribute.String("workspace", workspace),
	))
	defer span.End()

	ListTagOptions := &tfe.WorkspaceTagListOptions{
		Query: &query,
	}
	var tags []string
	TagList, err := t.Client.Workspaces.ListTags(
		ctx,
		workspace,
		ListTagOptions,
	)
	if err != nil {
		log.Error().Err(err)
		return tags, err
	}

	for _, tag := range TagList.Items {
		tags = append(tags, tag.Name)
	}
	return tags, err
}
