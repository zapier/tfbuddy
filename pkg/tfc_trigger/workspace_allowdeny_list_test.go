package tfc_trigger

import (
	"reflect"
	"testing"

	"github.com/spf13/viper"
	"github.com/zapier/tfbuddy/internal/config"
)

func TestGetWorkspaceAllowDenyListReadsViper(t *testing.T) {
	viper.Reset()
	config.Init()
	t.Cleanup(func() {
		viper.Reset()
		config.Init()
	})
	viper.Set(config.KeyDefaultTFCOrganization, "zapier")
	viper.Set(config.KeyWorkspaceAllowList, "svc-a, org-b/svc-b")
	viper.Set(config.KeyWorkspaceDenyList, "svc-c")
	config.Reload()

	allow, deny := getWorkspaceAllowDenyList(config.C)

	wantAllow := []string{"zapier/svc-a", "org-b/svc-b"}
	wantDeny := []string{"zapier/svc-c"}
	if !reflect.DeepEqual(allow, wantAllow) {
		t.Fatalf("allow = %v, want %v", allow, wantAllow)
	}
	if !reflect.DeepEqual(deny, wantDeny) {
		t.Fatalf("deny = %v, want %v", deny, wantDeny)
	}
}
