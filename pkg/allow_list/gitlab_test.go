package allow_list

import (
	"os"
	"testing"
)

func TestIsGitlabProjectAllowed(t *testing.T) {
	type args struct {
		projectWithNamespace string
		allowEnv             string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "groupies/repros_allowed",
			args: args{
				projectWithNamespace: "groupies/repros",
				allowEnv:             "groupies,other_groupies",
			},
			want: true,
		},
		{
			name: "other_groupies/repro2_allowed",
			args: args{
				projectWithNamespace: "groupies/repro2",
				allowEnv:             "groupies,other_groupies",
			},
			want: true,
		},
		{
			name: "group/repo_denied",
			args: args{
				projectWithNamespace: "group/repo",
				allowEnv:             "groupies,other_groupies",
			},
			want: false,
		},
		{
			name: "env not set",
			args: args{
				projectWithNamespace: "group/repo",
				allowEnv:             "",
			},
			want: false,
		},
		{
			name: "env single space",
			args: args{
				projectWithNamespace: "group/repo",
				allowEnv:             " ",
			},
			want: false,
		},
	}
	defer os.Unsetenv(GitlabProjectAllowListEnv)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv(GitlabProjectAllowListEnv, tt.args.allowEnv)
			if got := IsGitlabProjectAllowed(tt.args.projectWithNamespace); got != tt.want {
				t.Errorf("IsGitlabProjectAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}
