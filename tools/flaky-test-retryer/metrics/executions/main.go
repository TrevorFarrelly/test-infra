package main

import (
	"context"
	"log"
	"time"
	"flag"
	"net/http"
	"fmt"
	"regexp"
	"strings"
	"strconv"
	"io/ioutil"

	"knative.dev/test-infra/shared/ghutil"
	"github.com/google/go-github/github"
)
const (
	org = "knative"
	repo = "serving"
	botID = 48565599 // knative test reporter robot user ID
)
var ctx = context.Background()

type Client struct {
	*ghutil.GithubClient
	StartTime *time.Time
}

type Data struct {
	PR *github.PullRequest
	Comment *github.IssueComment
}

type Status struct {
	State string `json:"state"`
	Context string `json:"context"`
}

// get a client to handle our API requests
func getClient(githubToken, timeRange string) *Client {
	github, err := ghutil.NewGithubClient(githubToken)
	if err != nil {
		log.Fatalf("GitHub auth error: %v", err)
	}
	startTime, err := parseTime(timeRange)
	if err != nil {
		log.Fatalf("Unable to parse time range: %v", err)
	}
	log.Printf("Scanning PRs since %s", startTime.Format("2 Jan 2006 15:04:05"))
	return &Client{github, startTime}
}
// determine start time based on a duration of time to look back from now
func parseTime(timeRange string) (*time.Time, error) {
	duration, err := time.ParseDuration("-"+timeRange)
	if err != nil {
		return nil, err
	}
	startTime := time.Now().Add(duration)
	return &startTime, nil
}

// get pull requests created after the start time specified in the client
func (c *Client) getPullRequests() ([]*github.PullRequest, error) {
	prs, err := c.ListPullRequests(org, repo, "", "")
	if err != nil {
		return nil, err
	}
	var filteredPRs []*github.PullRequest
	for _, pr := range prs {
		if pr.GetCreatedAt().After(*c.StartTime) || (!pr.GetClosedAt().IsZero() && pr.GetClosedAt().After(*c.StartTime)) {
			filteredPRs = append(filteredPRs, pr)
		}
	}
	return filteredPRs, nil
}
// get retryer comment, if it exists, from PR
func (c *Client) getRetryerComment(pr *github.PullRequest) (*github.IssueComment, error){
	comments, err := c.ListComments(org, repo, pr.GetNumber())
	if err != nil {
		return nil, err
	}
	return filterComments(comments), nil
}
// find bot comment in list of comments on PR
func filterComments(comments []*github.IssueComment) *github.IssueComment {
	for _, comment := range comments {
		if comment.GetUser().GetID() == botID {
			return comment
		}
	}
	return nil
}

// Go Github API does not have an easy way to get PR statuses, so we manually query the URL.
// return the status of integration tests, the overall status of the PR, and any errors
func (c *Client) getPRStatus(url string) (string, string, error) {
	// get statuses from API
	statuses := []*Status{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", "", err
	}
	if _, err = c.Client.Do(ctx, req, &statuses); err != nil {
		return "", "", err
	}
	// filter out old statuses
	var integrationStatus string
	var overallStatus string
	for job, status := range filterStatuses(statuses) {
		// consider errors as failures
		if status == "error" {
			status = "failure"
		}
		// do not consider CLAs or Tide in determining status
		if job == "cla/google" || job == "tide" {
			continue
		}
		// set integration to whatever it is
		if job == "pull-knative-serving-integration-tests" {
			integrationStatus = status
		}
		// if overall is empty or success, anything replaces it
		if overallStatus == "" || overallStatus == "success" {
			overallStatus = status
			// if overall is pending, anything but success replaces it
		} else if overallStatus == "pending" && status != "success" {
			overallStatus = status
		}
	}
	return integrationStatus, overallStatus, nil
}
// filter stale statuses from the list. API returns statuses in reverse chronological order,
// so no need to sort manually.
func filterStatuses(statuses []*Status) map[string]string {
	filteredStatuses := map[string]string{}
	for _, status := range statuses {
		if _, ok := filteredStatuses[status.Context]; !ok {
			filteredStatuses[status.Context] = status.State
		}
	}
	return filteredStatuses
}
// get state of a pull request, either open, closed, or merged
func getPRState(pr *github.PullRequest) string {
	state := pr.GetState()
	if state == "closed" && !pr.GetMergedAt().IsZero() {
		state = "merged"
	}
	return state
}
// parse number of attempts from the retryers comment body
func getRetryAttempts(comment *github.IssueComment) int {
	if comment == nil {
		return 0
	}
	re := regexp.MustCompile("\\d\\/\\d")
	attemptString := strings.Split(string(re.Find([]byte(comment.GetBody()))), "/")[0]
	attempts, _ := strconv.Atoi(attemptString)
	return attempts
}
// get state of retryer and number of retries based on comment data
func getRetryerState(comment *github.IssueComment) string {
	if comment == nil {
		return "nil"
	}
	if strings.Contains(comment.GetBody(), "/test") {
		return "retried"
	}
	if strings.Contains(comment.GetBody(), "non-flaky") {
		return "blocked"
	}
	if strings.Contains(comment.GetBody(), "expended") {
		return "expended"
	}
	return "nil"
}

// write data formatted to CSV
func (c *Client) writeToCSV(data []*Data) error {
	fileData := "retries,retryer status,job status,overall status,pr state,url\n"
	for _, d := range data {
		prState := getPRState(d.PR)
		retryerState := getRetryerState(d.Comment)
		retryAttempts := getRetryAttempts(d.Comment)
		integrationStatus, overallStatus, err := c.getPRStatus(d.PR.GetStatusesURL())
		if err != nil {
			return err
		}
		if retryerState == "nil" {
			continue
		}
		fileData += fmt.Sprintf("%d,%s,%s,%s,%s,%s\n", retryAttempts, retryerState, integrationStatus, overallStatus, prState, d.PR.GetHTMLURL())
	}
	ioutil.WriteFile("data.csv", []byte(fileData), 0777)
	return nil
}

func main() {
	githubToken := flag.String("github-account", "", "Github token file")
	timeRange := flag.String("range", "24h", "amount of time to show results from")
	flag.Parse()

	client := getClient(*githubToken, *timeRange)
	data := []*Data{}

	log.Printf("Querying PR API...\n")
	prs, err := client.getPullRequests()
	if err != nil {
		log.Fatalf("Could not get pull requests: %v", err)
	}
	log.Printf("Done querying PR API\n")

	log.Printf("Querying Comment API\n")
	for _, pr := range prs {
		comment, err := client.getRetryerComment(pr)
		if err != nil {
			log.Printf("Could not get comments: %v", err)
			continue
		}
		data = append(data, &Data{pr, comment})
	}
	log.Printf("Done querying Comment API\n")
	client.writeToCSV(data)
}
