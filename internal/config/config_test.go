package config

import (
	"reflect"
	"testing"

	"github.com/spf13/viper"
)

func resetViperForTest(t *testing.T) {
	t.Helper()
	viper.Reset()
	Init()
}

func TestStringListParsesCommaSeparatedEnv(t *testing.T) {
	t.Setenv("TFBUDDY_WORKSPACE_ALLOW_LIST", " org-a/ws-a,ws-b,, org-c/ws-c ")
	resetViperForTest(t)

	got := StringList(KeyWorkspaceAllowList)
	want := []string{"org-a/ws-a", "ws-b", "org-c/ws-c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("StringList(%q) = %v, want %v", KeyWorkspaceAllowList, got, want)
	}
}

func TestStringListReturnsNilForEmptyValues(t *testing.T) {
	t.Setenv("TFBUDDY_WORKSPACE_DENY_LIST", " , , ")
	resetViperForTest(t)

	if got := StringList(KeyWorkspaceDenyList); got != nil {
		t.Fatalf("StringList(%q) = %v, want nil", KeyWorkspaceDenyList, got)
	}
}

func TestAutoMergeDefaultsToTrue(t *testing.T) {
	resetViperForTest(t)

	if !AutoMergeEnabled() {
		t.Fatal("AutoMergeEnabled() = false, want true")
	}
}

func TestDeleteOldCommentsDefaultsToFalse(t *testing.T) {
	resetViperForTest(t)

	if DeleteOldCommentsEnabled() {
		t.Fatal("DeleteOldCommentsEnabled() = true, want false")
	}
}

func TestDeleteOldCommentsCanBeEnabledFromEnv(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")
	resetViperForTest(t)

	if !DeleteOldCommentsEnabled() {
		t.Fatal("DeleteOldCommentsEnabled() = false, want true")
	}
}

func TestSentinelSoftFailDefaultsToFalse(t *testing.T) {
	resetViperForTest(t)

	if FailCIOnSentinelSoftFail() {
		t.Fatal("FailCIOnSentinelSoftFail() = true, want false")
	}
}

func TestDevModeDefaultsToFalse(t *testing.T) {
	resetViperForTest(t)

	if DevModeEnabled() {
		t.Fatal("DevModeEnabled() = true, want false")
	}
}

func TestStringAccessorsReadConfiguredValues(t *testing.T) {
	t.Setenv("TFBUDDY_LOG_LEVEL", "debug")
	t.Setenv("TFBUDDY_NATS_SERVICE_URL", "nats://example:4222")
	t.Setenv("TFBUDDY_DEFAULT_TFC_ORGANIZATION", "zapier")
	resetViperForTest(t)

	if got := LogLevel(); got != "debug" {
		t.Fatalf("LogLevel() = %q, want %q", got, "debug")
	}
	if got := NATSServiceURL(); got != "nats://example:4222" {
		t.Fatalf("NATSServiceURL() = %q, want %q", got, "nats://example:4222")
	}
	if got := DefaultTFCOrganization(); got != "zapier" {
		t.Fatalf("DefaultTFCOrganization() = %q, want %q", got, "zapier")
	}
}
