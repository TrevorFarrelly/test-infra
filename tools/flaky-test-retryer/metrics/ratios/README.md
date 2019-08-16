#Def-Triggered Retry Data

This tool queries knative/serving PRs for integration presubmit job reruns triggered by devs, via `/test` commands, and lists daily data in a csv file, easily importable into your favorite spreadsheet software

##Flags

* `--github-account` path to your GitHub OAuth file
* `--since` Date to search forward from. Can be in the form YYYY-MM-DD or a string parseable by golang's
`time.Duration` type, e.g. `24h`. Default 2019-07-01, the deployment date of the retryer.
