// Package ci recognizes the CI system the CLI runs in, so commands can pick
// the right base branch, commit and report format without flags.
package ci

import "strings"

// Provider names a CI system.
type Provider string

const (
	None      Provider = ""
	GitHub    Provider = "github"
	GitLab    Provider = "gitlab"
	Bitbucket Provider = "bitbucket"
	Azure     Provider = "azure"
	Generic   Provider = "generic"
)

// Env is what the CI tells us about the build.
type Env struct {
	Provider Provider
	// Branch is the branch being built (the head of a pull request).
	Branch string
	// Commit is the commit being built.
	Commit string
	// BaseBranch is the branch a pull/merge request targets, empty on a push.
	BaseBranch string
	// BuildURL links back to the pipeline run.
	BuildURL string
	// StepSummary is GitHub's job summary file.
	StepSummary string
	// Tag is the tag being built, if any.
	Tag string
}

// Detect reads the environment through getenv.
func Detect(getenv func(string) string) Env {
	switch {
	case getenv("GITHUB_ACTIONS") == "true":
		env := Env{
			Provider:    GitHub,
			Commit:      getenv("GITHUB_SHA"),
			BaseBranch:  getenv("GITHUB_BASE_REF"),
			StepSummary: getenv("GITHUB_STEP_SUMMARY"),
			Branch:      firstNonEmpty(getenv("GITHUB_HEAD_REF"), getenv("GITHUB_REF_NAME")),
		}
		if getenv("GITHUB_REF_TYPE") == "tag" {
			env.Tag = getenv("GITHUB_REF_NAME")
		}
		if server, repo, run := getenv("GITHUB_SERVER_URL"), getenv("GITHUB_REPOSITORY"), getenv("GITHUB_RUN_ID"); server != "" && repo != "" && run != "" {
			env.BuildURL = server + "/" + repo + "/actions/runs/" + run
		}
		return env
	case getenv("GITLAB_CI") == "true":
		return Env{
			Provider:   GitLab,
			Commit:     getenv("CI_COMMIT_SHA"),
			Branch:     firstNonEmpty(getenv("CI_MERGE_REQUEST_SOURCE_BRANCH_NAME"), getenv("CI_COMMIT_BRANCH")),
			BaseBranch: getenv("CI_MERGE_REQUEST_TARGET_BRANCH_NAME"),
			BuildURL:   getenv("CI_PIPELINE_URL"),
			Tag:        getenv("CI_COMMIT_TAG"),
		}
	case getenv("BITBUCKET_BUILD_NUMBER") != "":
		env := Env{
			Provider:   Bitbucket,
			Commit:     getenv("BITBUCKET_COMMIT"),
			Branch:     getenv("BITBUCKET_BRANCH"),
			BaseBranch: getenv("BITBUCKET_PR_DESTINATION_BRANCH"),
			Tag:        getenv("BITBUCKET_TAG"),
		}
		if origin := getenv("BITBUCKET_GIT_HTTP_ORIGIN"); origin != "" {
			env.BuildURL = origin + "/pipelines/results/" + getenv("BITBUCKET_BUILD_NUMBER")
		}
		return env
	case getenv("TF_BUILD") == "True":
		env := Env{
			Provider:   Azure,
			Commit:     getenv("BUILD_SOURCEVERSION"),
			Branch:     strings.TrimPrefix(firstNonEmpty(getenv("SYSTEM_PULLREQUEST_SOURCEBRANCH"), getenv("BUILD_SOURCEBRANCH")), "refs/heads/"),
			BaseBranch: strings.TrimPrefix(getenv("SYSTEM_PULLREQUEST_TARGETBRANCH"), "refs/heads/"),
		}
		if ref := getenv("BUILD_SOURCEBRANCH"); strings.HasPrefix(ref, "refs/tags/") {
			env.Tag = strings.TrimPrefix(ref, "refs/tags/")
		}
		if collection, project, id := getenv("SYSTEM_COLLECTIONURI"), getenv("SYSTEM_TEAMPROJECT"), getenv("BUILD_BUILDID"); collection != "" && id != "" {
			env.BuildURL = strings.TrimSuffix(collection, "/") + "/" + project + "/_build/results?buildId=" + id
		}
		return env
	case getenv("CI") != "":
		return Env{Provider: Generic}
	}
	return Env{}
}

// IsCI reports whether a CI system was recognised.
func (e Env) IsCI() bool {
	return e.Provider != None
}

// BaseRef is the git ref to compare with: the remote copy of the target
// branch, since CI checkouts rarely have a local one.
func (e Env) BaseRef() string {
	if e.BaseBranch == "" {
		return ""
	}
	return "origin/" + e.BaseBranch
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
