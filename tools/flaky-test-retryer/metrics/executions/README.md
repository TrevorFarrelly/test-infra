#Flaky Test Retryer Execution Data

This tool queries GitHub PRs for flaky-test-retryer comments, and writes data to a csv, easily
importable into your favorite spreadsheet software.

##Flags

* `--github-account` path to your GitHub OAuth file
* `--since` Date to search forward from. Can be in the form YYYY-MM-DD or a string parseable by golang's
`time.Duration` type, e.g. `24h`. Default 2019-08-02, the deployment date of the retryer.
