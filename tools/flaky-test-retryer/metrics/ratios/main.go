package main

import (
	"context"
	"log"
	"time"
	"flag"
	"fmt"
	"sort"
	"strings"
	"io/ioutil"

	"knative.dev/test-infra/shared/ghutil"
	"github.com/google/go-github/github"
)
const (
	org = "knative"
	repo = "serving"
	deploymentDate = "2019-08-02"
)

var (
	botIDs = []int64{48565599, 41213312} // knative robot user IDs
	ctx = context.Background()
)

type Client struct {
	*ghutil.GithubClient
	StartTime *time.Time
}

type Data map[string]*struct{
	prs, retries int
}

type Status struct {
	State string `json:"state"`
	Context string `json:"context"`
}

// get a client to handle our API requests
func getClient(githubToken, timeString string) *Client {
	github, err := ghutil.NewGithubClient(githubToken)
	if err != nil {
		log.Fatalf("GitHub auth error: %v", err)
	}
	startTime, err := parseTime(timeString)
	if err != nil {
		log.Fatalf("Unable to parse time: %v", err)
	}
	log.Printf("Scanning PRs since %s", startTime.Format("2 Jan 2006 15:04:05"))
	return &Client{github, startTime}
}

// determine start time, either via a date or a time period to look back
func parseTime(timeString string) (*time.Time, error) {
	// try to parse as time
	startTime, err := time.Parse("2006-01-02", timeString)
	if err != nil {
		// try to parse as duration
		duration, err := time.ParseDuration("-"+timeString)
		if err != nil {
			return nil, err
		}
		startTime = time.Now().Add(duration)
	}
	return &startTime, nil
}

// generate a new data struct with zero values for each day from startTime to now
func (c *Client) initData() Data {
	data := Data{}
	date := *c.StartTime
	for date.Before(time.Now()) {
		data[date.Format("2006-01-02")] = &struct{prs, retries int}{}
		date = date.AddDate(0, 0, 1)
	}
	return data
}

// get pull requests merged after the specified start time
func (c *Client) getPullRequests() ([]*github.PullRequest, error) {
	prs, err := c.ListPullRequests(org, repo, "", "")
	if err != nil {
		return nil, err
	}
	var filteredPRs []*github.PullRequest
	for _, pr := range prs {
		if !pr.GetMergedAt().IsZero() && pr.GetMergedAt().After(*c.StartTime) {
			filteredPRs = append(filteredPRs, pr)
		}
	}
	return filteredPRs, nil
}
// get comments with /test strings made by real people
func (c *Client) getComments(pr *github.PullRequest) ([]*github.IssueComment, error){
	comments, err := c.ListComments(org, repo, pr.GetNumber())
	if err != nil {
		return nil, err
	}
	return filterComments(comments), nil
}
// filter out bot comments
func filterComments(comments []*github.IssueComment) []*github.IssueComment {
	var filteredComments []*github.IssueComment
	for _, comment := range comments {
		userID := comment.GetUser().GetID()
		cmtBody := comment.GetBody()
		if strings.Contains(cmtBody, "/test pull-knative-") && userID != botIDs[0] && userID != botIDs[1] {
			filteredComments = append(filteredComments, comment)
		}
	}
	return filteredComments
}

// write data formatted to CSV
func writeToCSV(data Data) error {
	fileData := "date,retries,prs,ratio,ratio\n"
	// sort keys so we write dates in order
	dates := []string{}
	for k := range data {
		dates = append(dates, k)
	}
	sort.Strings(dates)
	for _, date := range dates {
		ratio := float64(data[date].retries) / float64(data[date].prs)
		fileData += fmt.Sprintf("%s,%d,%d,", date, data[date].retries, data[date].prs)
		if date <= deploymentDate {
			fileData += fmt.Sprintf("%0.3f", ratio)
		}
		if date >= deploymentDate {
			fileData += fmt.Sprintf(",%0.3f", ratio)
		}
		fileData += "\n"
	}
	ioutil.WriteFile("data.csv", []byte(fileData), 0777)
	return nil
}

func main() {
	githubToken := flag.String("github-account", "", "Github token file")
	timeRange := flag.String("since", "2019-07-01", "date to collect data after, or time range to collect data from (e.g. 24h)")
	flag.Parse()

	client := getClient(*githubToken, *timeRange)
	data := client.initData()

	log.Printf("Querying PR API...\n")
	prs, err := client.getPullRequests()
	if err != nil {
		log.Fatalf("Could not get pull requests: %v", err)
	}
	log.Printf("Done querying PR API\n")

	log.Printf("Querying Comment API\n")
	for _, pr := range prs {
		comments, err := client.getComments(pr)
		if err != nil {
			log.Printf("Could not get comments: %v", err)
			continue
		}
		data[pr.GetMergedAt().Format("2006-01-02")].prs++
		data[pr.GetMergedAt().Format("2006-01-02")].retries += len(comments)
	}
	log.Printf("Done querying Comment API\n")
	writeToCSV(data)
}
