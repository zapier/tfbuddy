package allow_list

import (
	"testing"
)

func TestIsGithubRepoAllowed(t *testing.T) {
	tests := []struct {
		name string
		args struct {
			fullName string
			allowEnv string
		}
		want bool
	}{
		{
			name: "org/repo_allowed",
			args: {
				fullName: "org/repo",
				allowEnv: "org,other_org",
			},
			want: true,
		},
		{
			name: "org/other_repo_allowed",
			args: args{
				fullName: "org/other_repo",
				allowEnv: "org,other_org",
			},
			want: true,
		},
		{
			name: "different_org/repo_denied",
			args: args{
				fullName: "different_org/repo",
				allowEnv: "org,other_org",
			},
			want: false,
		},
		{
			name: "env not set",
			args: args{
				fullName: "org/repo",
				allowEnv: "",
			},
			want: false,
		},
		{
			name: "env single space",
			args: args{
				fullName: "org/repo",
				allowEnv: " ",
			},
			want: false,
		},
		{
			name: "specific repo allowed",
			args: args{
				fullName: "org/specific-repo",
				allowEnv: "org/specific-repo,other_org",
			},
			want: true,
		},
		{
			name: "repo not in specific list",
			args: args{
				fullName: "org/other-repo",
				allowEnv: "org/specific-repo,other_org/repo",
			},
			want: false,
		},
		{
			name: "partial match allowed with prefix",
			args: args{
				fullName: "orgprefix/repo",
				allowEnv: "org,other_org",
			},
			want: true,
		},
		{
			name: "spaces in allow list",
			args: args{
				fullName: "org/repo",
				allowEnv: "org, other_org",
			},
			want: true,
		},
		{
			name: "trailing spaces",
			args: args{
				fullName: "other_org/repo",
				allowEnv: "org,other_org ",
			},
			want: true,
		},
		{
			name: "case sensitivity",
			args: args{
				fullName: "Org/Repo",
				allowEnv: "org",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(githubRepoAllowListEnv, tt.args.allowEnv)

			if got := IsGithubRepoAllowed(tt.args.fullName); got != tt.want {
				t.Errorf("IsGithubRepoAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}
