package tfc_trigger

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_isWorkspaceAllowed(t *testing.T) {
	type args struct {
		workspace string
		org       string
		allowList []string
		denyList  []string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "ws-denied",
			args: args{
				workspace: "service-foo-prod",
				allowList: []string{"service-foo-prod", "service-foo-staging"},
				denyList:  []string{"service-foo-prod"},
			},
			want: false,
		},
		{
			name: "ws-allowed",
			args: args{
				workspace: "service-foo-prod",
				org:       "foocorp",
				allowList: []string{"foocorp/service-foo-prod", "foocorp/service-foo-staging"},
				denyList:  []string{"foocorp/service-foo-staging"},
			},
			want: true,
		},
		{
			name: "ws-allowList-not-set",
			args: args{
				workspace: "service-foo-prod",
				allowList: []string{},
				denyList:  []string{},
			},
			want: true,
		},
		{
			name: "ws-allowList-with-org",
			args: args{
				workspace: "service-foo-prod",
				org:       "foo-org",
				allowList: []string{"foo-org/service-foo-prod"},
				denyList:  []string{},
			},
			want: true,
		},
		{
			name: "ws-allowList-no-org",
			args: args{
				workspace: "service-foo-prod",
				org:       "",
				allowList: []string{"/service-foo-prod"},
				denyList:  []string{},
			},
			want: true,
		},
		{
			name: "ws-denied-by-omission",
			args: args{
				workspace: "service-foo-prod",
				allowList: []string{"service-foo-staging"},
				denyList:  []string{},
			},
			want: false,
		},
		{
			name: "ws-denied-wrong-org",
			args: args{
				workspace: "acmecorp/service-foo-prod",
				org:       "acmecorp",
				allowList: []string{"foocorp/service-foo-prod"},
				denyList:  []string{},
			},
			want: false,
		},
	}
	defer os.Unsetenv("TFBUDDY_WORKSPACE_ALLOW_LIST")
	defer os.Unsetenv("TFBUDDY_WORKSPACE_DENY_LIST")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("TFBUDDY_WORKSPACE_ALLOW_LIST", strings.Join(tt.args.allowList, ","))
			os.Setenv("TFBUDDY_WORKSPACE_DENY_LIST", strings.Join(tt.args.denyList, ","))
			assert.Equalf(t, tt.want, isWorkspaceAllowed(tt.args.workspace, tt.args.org), "isWorkspaceAllowed(%v)", tt.args.workspace)
		})
	}
}
