package terraform_plan

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/stretchr/testify/assert"
)

var updateGolden = false

func init() {
	updateGoldenEnv := os.Getenv("TFBUDDY_TEST_UPDATE_GOLDEN")
	if updateGoldenEnv == "true" {
		updateGolden = true
	}
}

func Test_parseJSONPlan(t *testing.T) {

	tests := []struct {
		name    string
		tfplan  string
		want    *tfjson.Plan
		wantErr bool
	}{
		{
			name:    "basic",
			tfplan:  "testdata/TestPresentPlanChangesAsMarkdown/basic.tfplan.json",
			want:    nil,
			wantErr: false,
		},
		{
			name:    "destroy-create",
			tfplan:  "testdata/TestPresentPlanChangesAsMarkdown/destroy-create.tfplan.json",
			want:    nil,
			wantErr: false,
		},
		{
			name:    "import",
			tfplan:  "testdata/TestPresentPlanChangesAsMarkdown/import.tfplan.json",
			want:    nil,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			planFile := testLoadFile(t, tt.tfplan)
			_, err := parseJSONPlan(planFile)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseJSONPlan() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

		})
	}
}

func TestPresentPlanChangesAsMarkdown(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "basic",
		},
		{
			name: "destroy-create",
		},
		{
			name: "replace",
		},
		{
			name: "import",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := testLoadTestData(t, ".tfplan.json")
			got := PresentPlanChangesAsMarkdown(plan, "http://app.terraform.io/x/y/z")
			if updateGolden {
				testWriteTestData(t, ".md", []byte(got))
			}
			want := string(testLoadTestData(t, ".md"))
			assert.Equal(t, want, got, "")
		})
	}
}

func testLoadTestData(t *testing.T, suffix string) []byte {
	filename := fmt.Sprintf("testdata/%s%s", t.Name(), suffix)
	return testLoadFile(t, filename)
}

func testLoadFile(t *testing.T, filename string) []byte {
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("could not read test file (%s): %v", filename, err)
	}
	return data
}

func testWriteTestData(t *testing.T, suffix string, b []byte) {
	filename := fmt.Sprintf("testdata/%s%s", t.Name(), suffix)
	err := os.MkdirAll(filepath.Dir(filename), 0755)
	if err != nil {
		t.Fatalf("could not create dir (%s): %v", filename, err)
	}
	err = os.WriteFile(filename, b, 0644)
	if err != nil {
		t.Fatalf("could not write testdata file (%s): %v", filename, err)
	}
}
