package allow_list

import (
	"reflect"
	"testing"
)

func TestGetAllowList(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "multiple values",
			input: []string{"prefix1", "prefix2", "prefix3"},
			want:  []string{"prefix1", "prefix2", "prefix3"},
		},
		{
			name:  "single value",
			input: []string{"prefix1"},
			want:  []string{"prefix1"},
		},
		{
			name:  "values with spaces",
			input: []string{" prefix1 ", " prefix2 ", " prefix3 "},
			want:  []string{"prefix1", "prefix2", "prefix3"},
		},
		{
			name:  "empty env",
			input: nil,
			want:  nil,
		},
		{
			name:  "only spaces",
			input: []string{"   "},
			want:  nil,
		},
		{
			name:  "empty strings in list",
			input: []string{"prefix1", "", "prefix3"},
			want:  []string{"prefix1", "prefix3"},
		},
		{
			name:  "trailing comma",
			input: []string{"prefix1", "prefix2", ""},
			want:  []string{"prefix1", "prefix2"},
		},
		{
			name:  "leading comma",
			input: []string{"", "prefix1", "prefix2"},
			want:  []string{"prefix1", "prefix2"},
		},
		{
			name:  "multiple consecutive commas",
			input: []string{"prefix1", "", "", "prefix2"},
			want:  []string{"prefix1", "prefix2"},
		},
		{
			name:  "only commas",
			input: []string{"", "", "", ""},
			want:  nil,
		},
		{
			name:  "values with forward slashes",
			input: []string{"zapier/", "github/org", "gitlab/project"},
			want:  []string{"zapier/", "github/org", "gitlab/project"},
		},
		{
			name:  "values with special characters",
			input: []string{"org-name", "project_name", "repo.name", "user@domain"},
			want:  []string{"org-name", "project_name", "repo.name", "user@domain"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getAllowList(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getAllowList() = %v, want %v", got, tt.want)
			}
		})
	}
}
