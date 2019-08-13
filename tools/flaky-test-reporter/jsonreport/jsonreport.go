/*
Copyright 2019 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package jsonreport

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path"
	"sort"
	"strings"
	"time"

	"knative.dev/test-infra/shared/common"
	"knative.dev/test-infra/shared/prow"
)

const (
	filename = "flaky-tests.json"
	jobName  = "ci-knative-flakes-reporter" // flaky-test-reporter's Prow job name
	maxAge   = 4                            // maximum age in days that JSON data is valid
)

// Report contains concise information about current flaky tests in a given repo
type Report struct {
	Repo  string   `json:"repo"`
	Flaky []string `json:"flaky"`
}

// JSONClient contains the set of operations a JSON reporter needs
type Client interface {
	CreateReportForRepo(repo string, flaky []string, writeFile bool) (*Report, error)
	ParseFlakyLog(repo string, buildID int, f func(report Report, result *[]string)) ([]Report, []string, error)
	GetFlakyTestReport(repo string, buildID int) ([]Report, error)
}

// Client is simply a way to call methods, it does not contain any data itself
type JSONClient struct{}

// Initialize wraps prow's init, which must be called before any other prow functions are used.
func Initialize(serviceAccount string) (Client, error) {
	return NewClient(), prow.Initialize(serviceAccount)
}

// NewClient gives us a new Client struct, without initializing Prow
func NewClient() *JSONClient {
	return &JSONClient{}
}

// writeToArtifactsDir writes the flaky test data for this repo to disk.
func (c *JSONClient) writeToArtifactsDir(r *Report) error {
	artifactsDir := prow.GetLocalArtifactsDir()
	if err := common.CreateDir(path.Join(artifactsDir, r.Repo)); nil != err {
		return err
	}
	outFilePath := path.Join(artifactsDir, r.Repo, filename)
	contents, err := json.Marshal(r)
	if nil != err {
		return err
	}
	return ioutil.WriteFile(outFilePath, contents, 0644)
}

// ParseFlakyLog reads the latest flaky test report and returns filtered results based
// on the function the caller passes in.
func (c *JSONClient) ParseFlakyLog(repo string, buildID int, f func(report Report, result *[]string)) ([]Report, []string, error) {
	var parsedResults []string
	results, err := c.GetFlakyTestReport(repo, buildID)
	if err != nil || len(results) == 0 {
		return results, parsedResults, err
	}
	for _, r := range results {
		f(r, &parsedResults)
	}
	return results, parsedResults, err
}

// GetFlakyTestReport collects flaky test reports from the given buildID and repo.
// Use repo = "" to get reports from all repositories, and buildID = -1 to get the
// most recent report
func (c *JSONClient) GetFlakyTestReport(repo string, buildID int) ([]Report, error) {
	job := prow.NewJob(jobName, prow.PeriodicJob, "", 0)
	var err error
	if buildID == -1 {
		buildID, err = c.getLatestValidBuild(job, repo)
		if err != nil {
			return nil, err
		}
	}
	build := job.NewBuild(buildID)
	var reports []Report
	for _, filepath := range c.getReportPaths(build, repo) {
		report, err := c.readJSONReport(build, filepath)
		if err != nil {
			return nil, err
		}
		reports = append(reports, *report)
	}
	return reports, nil
}

// getLatestValidBuild inexpensively sorts and finds the most recent JSON report.
// Assumes sequential build IDs are sequential in time.
func (c *JSONClient) getLatestValidBuild(job *prow.Job, repo string) (int, error) {
	// check latest build first, before looking to older builds
	if buildID, err := job.GetLatestBuildNumber(); err == nil {
		build := job.NewBuild(buildID)
		if reports := c.getReportPaths(build, repo); len(reports) != 0 {
			return buildID, nil
		}
	}
	// look at older builds
	maxElapsedTime, _ := time.ParseDuration(fmt.Sprintf("%dh", maxAge*24))
	buildIDs := job.GetBuildIDs()
	sort.Sort(sort.Reverse(sort.IntSlice(buildIDs)))
	for _, buildID := range buildIDs {
		build := job.NewBuild(buildID)
		// check if reports exist for this build
		if reports := c.getReportPaths(build, repo); len(reports) == 0 {
			continue
		}
		// check if this report is too old
		startTimeInt, err := build.GetStartTime()
		if err != nil {
			continue
		}
		startTime := time.Unix(startTimeInt, 0)
		if time.Since(startTime) < maxElapsedTime {
			return buildID, nil
		}
		return 0, fmt.Errorf("latest JSON log is outdated: %.2f days old", time.Since(startTime).Hours()/24)
	}
	return 0, fmt.Errorf("no JSON logs found in recent builds")
}

// getReportPaths searches build artifacts for reports from the given repo, returning
// the path to any matching files. Use repo = "" to get all reports from all repos.
func (c *JSONClient) getReportPaths(build *prow.Build, repo string) []string {
	var matches []string
	suffix := path.Join(repo, filename)
	for _, artifact := range build.GetArtifacts() {
		if strings.HasSuffix(artifact, suffix) {
			matches = append(matches, strings.TrimPrefix(artifact, build.StoragePath))
		}
	}
	return matches
}

// readJSONReport builds a repo-specific report object from a given json file path.
func (c *JSONClient) readJSONReport(build *prow.Build, filename string) (*Report, error) {
	report := &Report{}
	contents, err := build.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(contents, report); err != nil {
		return nil, err
	}
	return report, nil
}

// CreateReportForRepo generates a flaky report for a given repository, and optionally
// writes it to disk.
func (c *JSONClient) CreateReportForRepo(repo string, flaky []string, writeFile bool) (*Report, error) {
	report := &Report{
		Repo:  repo,
		Flaky: flaky,
	}
	if writeFile {
		return report, c.writeToArtifactsDir(report)
	}
	return report, nil
}
