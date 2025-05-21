package allow_list

import (
	"os"
	"reflect"
	"testing"
)

func TestGetAllowList(t *testing.T) {
	const testEnvVar = "TEST_ALLOW_LIST_ENV"

	originalValue, originalExists := os.LookupEnv(testEnvVar)

	t.Cleanup(func() {
		if originalExists {
			os.Setenv(testEnvVar, originalValue)
		} else {
			os.Unsetenv(testEnvVar)
		}
	})

	tests := []struct {
		name   string
		envVal string
		want   []string
	}{
		{
			name:   "multiple values",
			envVal: "prefix1,prefix2,prefix3",
			want:   []string{"prefix1", "prefix2", "prefix3"},
		},
		{
			name:   "single value",
			envVal: "prefix1",
			want:   []string{"prefix1"},
		},
		{
			name:   "values with spaces",
			envVal: " prefix1 , prefix2 , prefix3 ",
			want:   []string{"prefix1", "prefix2", "prefix3"},
		},
		{
			name:   "empty env",
			envVal: "",
			want:   nil,
		},
		{
			name:   "only spaces",
			envVal: "   ",
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv(testEnvVar, tt.envVal)

			t.Cleanup(func() {
				os.Unsetenv(testEnvVar)
			})

			if got := getAllowList(testEnvVar); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getAllowList() = %v, want %v", got, tt.want)
			}
		})
	}
}
