# Time Tracking Glossary

## Worklog

A completed record of time spent on a Jira issue. Jira is the authoritative source for worklogs. A worklog may have an optional description.

## Worklog contribution

The portion of a Worklog that falls within both a Report window and one local calendar day. It retains the Worklog's Jira identity, issue, and description. Report totals sum contributions independently, including contributions from overlapping Worklogs.

## Running timer

An in-progress record of work on one Jira issue with an optional description. A running timer belongs to one machine and ends when stopped. It has no automatic expiry; age alone never invalidates it. A successful stop creates a worklog; a failed Jira write leaves no retained entry after its details are displayed. At most one running timer may exist.

## Hourly rate

The single current amount earned per hour in CZK. It applies to every earnings calculation, including historical ranges.

## Jira identity

The authenticated Atlassian account on the configured Jira Cloud site. Its email authenticates API requests, while its stable Jira account ID identifies authored Worklogs. The site is identified by both its canonical URL and Jira Cloud ID.

## Earnings

A value calculated from reported worklog time and the current hourly rate. Earnings are displayed on demand and are not recorded.

## Report window

A half-open span of local time used to select Worklog time for a report. Calendar presets use the machine's local timezone: today is the current calendar day, last week is the previous Monday-through-Sunday week, and last month is the previous calendar month. An explicit window requires an inclusive start and exclusive end in local time; a date without a time denotes local midnight.

## Duration

A positive amount of tracked time. Manual input uses compact hours and minutes, such as `2h30m`; days and weeks are not accepted. A stopped Running timer preserves its elapsed whole seconds.
