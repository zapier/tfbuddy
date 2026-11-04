package runstream

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaimWorkspaceDispatch(t *testing.T) {
	_, url := startTestNATS(t)
	nc := testConnect(t, url)
	defer nc.Close()
	js := testGetJetstreamContext(t, nc)

	kv, err := configureWorkspaceDispatchKVStore(js)
	require.NoError(t, err)
	s := &Stream{js: js, dispatchKV: kv}

	claim := WorkspaceDispatchClaim{
		VcsProvider:  "gitlab",
		Project:      "zapier/terraform-services",
		MergeRequest: 5391,
		CommitSHA:    "abc123",
		Action:       "plan",
		Trigger:      "delivery:d-1",
		Organization: "zapier",
		Workspace:    "ws-a",
	}

	claimed, err := s.ClaimWorkspaceDispatch(claim)
	require.NoError(t, err)
	require.True(t, claimed, "first claim")

	claimed, err = s.ClaimWorkspaceDispatch(claim)
	require.NoError(t, err)
	require.False(t, claimed, "a redelivery of the same trigger must not claim the workspace again")

	otherWorkspace := claim
	otherWorkspace.Workspace = "ws-b"
	claimed, err = s.ClaimWorkspaceDispatch(otherWorkspace)
	require.NoError(t, err)
	require.True(t, claimed, "another workspace of the same trigger")

	nextTrigger := claim
	nextTrigger.Trigger = "note:42"
	claimed, err = s.ClaimWorkspaceDispatch(nextTrigger)
	require.NoError(t, err)
	require.True(t, claimed, "a new trigger on the same commit, such as a tfc plan comment")

	// Another replica starting up binds to the same bucket and sees the claims.
	kv2, err := configureWorkspaceDispatchKVStore(js)
	require.NoError(t, err)
	claimed, err = (&Stream{js: js, dispatchKV: kv2}).ClaimWorkspaceDispatch(claim)
	require.NoError(t, err)
	require.False(t, claimed)
}

func TestClaimWorkspaceDispatchWithoutStore(t *testing.T) {
	claimed, err := (&Stream{}).ClaimWorkspaceDispatch(WorkspaceDispatchClaim{Workspace: "ws-a"})
	require.Error(t, err)
	require.False(t, claimed)
}
