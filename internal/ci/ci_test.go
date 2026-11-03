package ci

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetect(
	t *testing.T,
) {
	testCases := []struct {
		name string
		env  map[string]string
		want Env
	}{
		{
			name: "github pull request",
			env: map[string]string{
				"GITHUB_ACTIONS": "true", "GITHUB_SHA": "abc", "GITHUB_HEAD_REF": "feature",
				"GITHUB_REF_NAME": "12/merge", "GITHUB_BASE_REF": "main", "GITHUB_STEP_SUMMARY": "/tmp/s",
				"GITHUB_SERVER_URL": "https://github.com", "GITHUB_REPOSITORY": "acme/api", "GITHUB_RUN_ID": "7",
			},
			want: Env{Provider: GitHub, Commit: "abc", Branch: "feature", BaseBranch: "main",
				StepSummary: "/tmp/s", BuildURL: "https://github.com/acme/api/actions/runs/7"},
		},
		{
			name: "github tag push",
			env:  map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_REF_NAME": "v1.2.0", "GITHUB_REF_TYPE": "tag"},
			want: Env{Provider: GitHub, Branch: "v1.2.0", Tag: "v1.2.0"},
		},
		{
			name: "gitlab merge request",
			env: map[string]string{
				"GITLAB_CI": "true", "CI_COMMIT_SHA": "def", "CI_MERGE_REQUEST_SOURCE_BRANCH_NAME": "feature",
				"CI_MERGE_REQUEST_TARGET_BRANCH_NAME": "develop", "CI_PIPELINE_URL": "https://gitlab.com/p/1",
			},
			want: Env{Provider: GitLab, Commit: "def", Branch: "feature", BaseBranch: "develop", BuildURL: "https://gitlab.com/p/1"},
		},
		{
			name: "bitbucket pull request",
			env: map[string]string{
				"BITBUCKET_BUILD_NUMBER": "3", "BITBUCKET_COMMIT": "123", "BITBUCKET_BRANCH": "feature",
				"BITBUCKET_PR_DESTINATION_BRANCH": "main", "BITBUCKET_GIT_HTTP_ORIGIN": "https://bitbucket.org/acme/api",
			},
			want: Env{Provider: Bitbucket, Commit: "123", Branch: "feature", BaseBranch: "main",
				BuildURL: "https://bitbucket.org/acme/api/pipelines/results/3"},
		},
		{
			name: "azure pull request",
			env: map[string]string{
				"TF_BUILD": "True", "BUILD_SOURCEVERSION": "456", "SYSTEM_PULLREQUEST_SOURCEBRANCH": "refs/heads/feature",
				"SYSTEM_PULLREQUEST_TARGETBRANCH": "refs/heads/main", "BUILD_SOURCEBRANCH": "refs/pull/4/merge",
				"SYSTEM_COLLECTIONURI": "https://dev.azure.com/acme/", "SYSTEM_TEAMPROJECT": "api", "BUILD_BUILDID": "9",
			},
			want: Env{Provider: Azure, Commit: "456", Branch: "feature", BaseBranch: "main",
				BuildURL: "https://dev.azure.com/acme/api/_build/results?buildId=9"},
		},
		{name: "other CI", env: map[string]string{"CI": "1"}, want: Env{Provider: Generic}},
		{name: "a laptop", env: map[string]string{}, want: Env{}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := Detect(func(k string) string { return testCase.env[k] })
			assert.Equal(t, testCase.want, got)
		})
	}

	assert.Equal(t, "origin/main", Env{BaseBranch: "main"}.BaseRef())
	assert.Equal(t, "", Env{}.BaseRef())
}
