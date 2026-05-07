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

	got := C.WorkspaceAllowList
	want := []string{"org-a/ws-a", "ws-b", "org-c/ws-c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("C.WorkspaceAllowList = %v, want %v", got, want)
	}
}

func TestStringListReturnsNilForEmptyValues(t *testing.T) {
	t.Setenv("TFBUDDY_WORKSPACE_DENY_LIST", " , , ")
	resetViperForTest(t)

	if got := C.WorkspaceDenyList; got != nil {
		t.Fatalf("C.WorkspaceDenyList = %v, want nil", got)
	}
}

func TestAutoMergeDefaultsToTrue(t *testing.T) {
	resetViperForTest(t)

	if !C.AllowAutoMerge {
		t.Fatal("C.AllowAutoMerge = false, want true")
	}
}

func TestDeleteOldCommentsDefaultsToFalse(t *testing.T) {
	resetViperForTest(t)

	if C.DeleteOldComments {
		t.Fatal("C.DeleteOldComments = true, want false")
	}
}

func TestDeleteOldCommentsCanBeEnabledFromEnv(t *testing.T) {
	t.Setenv("TFBUDDY_DELETE_OLD_COMMENTS", "true")
	resetViperForTest(t)

	if !C.DeleteOldComments {
		t.Fatal("C.DeleteOldComments = false, want true")
	}
}

func TestSentinelSoftFailDefaultsToFalse(t *testing.T) {
	resetViperForTest(t)

	if C.FailCIOnSentinelSoftFail {
		t.Fatal("C.FailCIOnSentinelSoftFail = true, want false")
	}
}

func TestDevModeDefaultsToFalse(t *testing.T) {
	resetViperForTest(t)

	if C.DevMode {
		t.Fatal("C.DevMode = true, want false")
	}
}

func TestStringAccessorsReadConfiguredValues(t *testing.T) {
	t.Setenv("TFBUDDY_LOG_LEVEL", "debug")
	t.Setenv("TFBUDDY_NATS_SERVICE_URL", "nats://example:4222")
	t.Setenv("TFBUDDY_DEFAULT_TFC_ORGANIZATION", "zapier")
	resetViperForTest(t)

	if got := C.LogLevel; got != "debug" {
		t.Fatalf("C.LogLevel = %q, want %q", got, "debug")
	}
	if got := C.NATSServiceURL; got != "nats://example:4222" {
		t.Fatalf("C.NATSServiceURL = %q, want %q", got, "nats://example:4222")
	}
	if got := C.DefaultTFCOrganization; got != "zapier" {
		t.Fatalf("C.DefaultTFCOrganization = %q, want %q", got, "zapier")
	}
}
