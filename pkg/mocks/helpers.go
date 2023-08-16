package mocks

import (
	"fmt"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	gomock "github.com/golang/mock/gomock"
	tfe "github.com/hashicorp/go-tfe"
	"github.com/stretchr/testify/assert"
	tfc_trigger "github.com/zapier/tfbuddy/pkg/tfc_trigger"
	vcs "github.com/zapier/tfbuddy/pkg/vcs"
	"gopkg.in/yaml.v2"
)

type TestGitRepo struct {
	Repo *git.Repository
}

// CreateFileOnCurrentBranch creates a file on the current active branch and commits it
func (t *TestGitRepo) CreateCommitFileOnCurrentBranch(fileName, commitMessage string) (string, error) {
	wt, err := t.Repo.Worktree()
	if err != nil {
		return "", err
	}
	path := filepath.Dir(fileName)
	err = wt.Filesystem.MkdirAll(path, 0666)
	if err != nil {
		return "", err
	}
	file, err := wt.Filesystem.Create(fileName)
	if err != nil {
		return "", err
	}
	_, err = file.Write([]byte("test data"))
	if err != nil {
		return "", err
	}
	file.Close()
	_, err = wt.Add(file.Name())
	if err != nil {
		return "", err
	}

	hash, err := wt.Commit(commitMessage, &git.CommitOptions{All: true, Author: &object.Signature{Name: "tester", Email: "test@zapier.com"}})
	return hash.String(), err
}

// SwitchToBranch will switch to a new branch and create it if needed
func (t *TestGitRepo) SwitchToBranch(branch string) error {
	branches, err := t.Repo.Branches()
	if err != nil {
		return err
	}
	wt, err := t.Repo.Worktree()
	if err != nil {
		return err
	}
	foundMatchingBranch := false
	err = branches.ForEach(func(r *plumbing.Reference) error {
		if r.Name().String() == fmt.Sprintf("refs/heads/%s", branch) {
			foundMatchingBranch = true
			return wt.Checkout(&git.CheckoutOptions{Branch: r.Name()})
		}
		return nil
	})
	if foundMatchingBranch {
		return err
	}
	headRef, err := t.Repo.Head()
	if err != nil {
		return err
	}
	ref := plumbing.NewHashReference(plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", branch)), headRef.Hash())

	// The created reference is saved in the storage.
	err = t.Repo.Storer.SetReference(ref)
	if err != nil {
		return err
	}
	err = wt.Checkout(&git.CheckoutOptions{Branch: ref.Name()})
	return err
}

// InitGitTestRepo create an in memory git repo and returns itself and the initial commit hash
func InitGitTestRepo(t *testing.T) (*TestGitRepo, string) {
	store := memory.NewStorage()
	fs := memfs.New()
	repo, err := git.Init(store, fs)
	assert.Equal(t, nil, err)
	wt, err := repo.Worktree()
	assert.Equal(t, nil, err)
	file, err := wt.Filesystem.Create("main.tf")
	assert.Equal(t, nil, err)
	file.Write([]byte("test data"))
	file.Close()
	wt.Add(file.Name())
	hash, err := wt.Commit("init commit", &git.CommitOptions{All: true, Author: &object.Signature{Name: "tester", Email: "test@zapier.com"}})
	assert.Equal(t, nil, err)
	assert.NotEqual(t, hash, "")

	return &TestGitRepo{
		Repo: repo,
	}, hash.String()
}

type TestMetaData struct {
	MRIID         int
	ProjectNameNS string
	CommonSHA     string
	TargetBranch  string
	SourceBranch  string
	TFBuddyConfig []byte
}
type TestSuite struct {
	MockGitClient    *MockGitClient
	MockGitMR        *MockDetailedMR
	MockGitRepo      *MockGitRepo
	MockGitDisc      *MockMRDiscussionNotes
	MockMRNote       *MockMRNote
	MockApiClient    *MockApiClient
	MockStreamClient *MockStreamClient
	MockProject      *MockProject
	MetaData         *TestMetaData
}
type TestOverrides struct {
	ProjectConfig *tfc_trigger.ProjectConfig
}
type RegexMatcher struct {
	regex *regexp.Regexp
}

func (r *RegexMatcher) Matches(x interface{}) bool {
	if _, ok := x.(string); !ok {
		return false
	}
	val := x.(string)
	return r.regex.MatchString(val)
}

// String describes what the matcher matches.
func (r *RegexMatcher) String() string {
	return "checks if a value matches a regex"
}

func (ts *TestSuite) InitTestSuite() {
	ts.MockGitRepo.EXPECT().FetchUpstreamBranch(ts.MetaData.TargetBranch).Return(nil).AnyTimes()
	ts.MockGitRepo.EXPECT().GetMergeBase(ts.MetaData.SourceBranch, ts.MetaData.TargetBranch).Return(ts.MetaData.CommonSHA, nil).AnyTimes()
	ts.MockGitRepo.EXPECT().GetLocalDirectory().Return("/tmp/local").AnyTimes()
	ts.MockGitRepo.EXPECT().GetModifiedFileNamesBetweenCommits(ts.MetaData.CommonSHA, ts.MetaData.TargetBranch).Return([]string{}, nil).AnyTimes()

	ts.MockGitMR.EXPECT().GetInternalID().Return(ts.MetaData.MRIID).AnyTimes()
	ts.MockGitMR.EXPECT().GetTargetBranch().Return(ts.MetaData.TargetBranch).AnyTimes()
	ts.MockGitMR.EXPECT().GetSourceBranch().Return(ts.MetaData.SourceBranch).AnyTimes()
	ts.MockGitMR.EXPECT().GetTitle().Return("MR Title").AnyTimes()

	ts.MockMRNote.EXPECT().GetNoteID().Return(int64(301)).AnyTimes()
	ts.MockGitDisc.EXPECT().GetDiscussionID().Return("201").AnyTimes()
	ts.MockGitDisc.EXPECT().GetMRNotes().Return([]vcs.MRNote{ts.MockMRNote}).AnyTimes()

	ts.MockGitClient.EXPECT().GetMergeRequest(gomock.Any(), ts.MetaData.MRIID, ts.MetaData.ProjectNameNS).Return(ts.MockGitMR, nil).AnyTimes()
	ts.MockGitClient.EXPECT().GetMergeRequestModifiedFiles(gomock.Any(), ts.MetaData.MRIID, ts.MetaData.ProjectNameNS).Return([]string{"main.tf"}, nil).AnyTimes()
	ts.MockGitClient.EXPECT().GetRepoFile(gomock.Any(), ts.MetaData.ProjectNameNS, ".tfbuddy.yaml", ts.MetaData.SourceBranch).Return(ts.MetaData.TFBuddyConfig, nil).AnyTimes()
	ts.MockGitClient.EXPECT().CloneMergeRequest(gomock.Any(), ts.MetaData.ProjectNameNS, gomock.Any(), gomock.Any()).Return(ts.MockGitRepo, nil).AnyTimes()
	ts.MockGitClient.EXPECT().CreateMergeRequestDiscussion(gomock.Any(), ts.MetaData.MRIID, ts.MetaData.ProjectNameNS, &RegexMatcher{regex: regexp.MustCompile("Starting TFC apply for Workspace: `([A-z\\-]){1,}/([A-z\\-]){1,}`.")}).Return(ts.MockGitDisc, nil).AnyTimes()

	ts.MockApiClient.EXPECT().GetWorkspaceByName(gomock.Any(), gomock.Any(), gomock.Any()).Return(&tfe.Workspace{ID: "service-tfbuddy"}, nil).AnyTimes()
	ts.MockApiClient.EXPECT().GetTagsByQuery(gomock.Any(), gomock.Any(), "tfbuddylock").AnyTimes()
	ts.MockApiClient.EXPECT().AddTags(gomock.Any(), gomock.Any(), "tfbuddylock", "101").AnyTimes()

	ts.MockStreamClient.EXPECT().AddRunMeta(gomock.Any()).AnyTimes()

	ts.MockProject.EXPECT().GetPathWithNamespace().Return(ts.MetaData.ProjectNameNS).AnyTimes()

}

const TF_WORKSPACE_NAME = "service-tfbuddy"
const TF_ORGANIZATION_NAME = "zapier-test"

func CreateTestSuite(mockCtrl *gomock.Controller, overrides TestOverrides, t *testing.T) *TestSuite {
	projectNameNS := "zapier/tfbuddy"
	mrIID := 101
	srcBranch := "test-branch"
	targetBranch := "main"

	ws := &tfc_trigger.ProjectConfig{
		Workspaces: []*tfc_trigger.TFCWorkspace{{
			Name:         TF_WORKSPACE_NAME,
			Organization: TF_ORGANIZATION_NAME,
			Mode:         "apply-before-merge",
		}}}
	if overrides.ProjectConfig != nil {
		ws = overrides.ProjectConfig
	}
	data, err := yaml.Marshal(&ws)
	if err != nil {
		t.Fatal(err)
	}

	commonSha := "commonsha1234"
	mockGitClient := NewMockGitClient(mockCtrl)
	mockGitMR := NewMockDetailedMR(mockCtrl)
	mockGitRepo := NewMockGitRepo(mockCtrl)
	mockGitDisc := NewMockMRDiscussionNotes(mockCtrl)
	mockMRNote := NewMockMRNote(mockCtrl)

	mockApiClient := NewMockApiClient(mockCtrl)

	mockStreamClient := NewMockStreamClient(mockCtrl)

	mockProject := NewMockProject(mockCtrl)

	return &TestSuite{
		MockGitClient:    mockGitClient,
		MockGitMR:        mockGitMR,
		MockGitRepo:      mockGitRepo,
		MockGitDisc:      mockGitDisc,
		MockMRNote:       mockMRNote,
		MockApiClient:    mockApiClient,
		MockStreamClient: mockStreamClient,
		MockProject:      mockProject,
		MetaData: &TestMetaData{
			MRIID:         mrIID,
			ProjectNameNS: projectNameNS,
			CommonSHA:     commonSha,
			TargetBranch:  targetBranch,
			TFBuddyConfig: data,
			SourceBranch:  srcBranch,
		},
	}
}
