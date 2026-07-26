# Jira Cloud integration contract

## Decision

`clock` integrates with Jira Cloud REST API v3. Jira is the source of truth for every completed **Worklog**. The CLI may keep only the one active **Timer** locally; Jira has no native running-worklog representation.

For this personal v1, authenticate the user's Atlassian account with a scoped API token. Use the classic scopes `read:jira-user`, `read:jira-work`, and `write:jira-work`. They cover current-user lookup, issue/worklog reads and searches, and worklog creation; Jira permissions still constrain the token. Atlassian recommends scoped tokens, requires them to use the `api.atlassian.com/ex/jira/{cloudId}` gateway, and gives new tokens a configurable lifetime of 1–365 days. The Cloud ID can be discovered from `https://deepvision.atlassian.net/_edge/tenant_info`. [API-token guidance](https://support.atlassian.com/atlassian-account/docs/manage-api-tokens-for-your-atlassian-account/) [Jira scopes](https://developer.atlassian.com/cloud/jira/platform/scopes-for-oauth-2-3LO-and-forge-apps/) [Cloud ID lookup](https://support.atlassian.com/jira/kb/retrieve-my-atlassian-sites-cloud-id/)

Send HTTP Basic authentication using the Atlassian account email and API token (`Authorization: Basic base64(email:token)`). Atlassian explicitly positions this scheme for scripts and manual REST calls; passwords are deprecated. Never put the token in command arguments, checked-in configuration, logs, or JSON output. [Basic authentication](https://developer.atlassian.com/cloud/jira/platform/basic-auth-for-rest-apis/)

## REST endpoints

All paths below are relative to `https://api.atlassian.com/ex/jira/{cloudId}` when using a scoped token.

| Purpose | Request | Contract |
| --- | --- | --- |
| Validate credentials and identify the Worklog author | `GET /rest/api/3/myself` | Capture `accountId` as the stable author identity. `displayName`, email visibility, and other profile fields are not reliable identities. Requires Jira access; classic scope `read:jira-user`. [Current user](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-myself/#api-rest-api-3-myself-get) |
| Find candidate issues for a report range | `POST /rest/api/3/search/jql` | Use enhanced search, not the deprecated `/search` operations. Request only `summary`; follow `nextPageToken` until `isLast`. Results include only issues the user may browse. [Enhanced JQL search](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-search/#api-rest-api-3-search-jql-post) |
| Read an issue's Worklogs | `GET /rest/api/3/issue/{issueIdOrKey}/worklog` | Supply `startedAfter`, `startedBefore`, `startAt`, and `maxResults`; continue until `startAt + worklogs.length >= total`. Jira orders these Worklogs by creation time, not start time. [Issue Worklogs](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-worklogs/#api-rest-api-3-issue-issueidorkey-worklog-get) |
| Enumerate all visible Worklogs by update time | `GET /rest/api/3/worklog/updated?since=...` | Follow `nextPage`/`until` until `lastPage`; pages contain at most 1,000 IDs. This is a change feed keyed by update time, not a started-time range query, and excludes the minute immediately preceding the request. [Updated Worklogs](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-worklogs/#api-rest-api-3-worklog-updated-get) |
| Resolve Worklog IDs | `POST /rest/api/3/worklog/list` | Submit at most 1,000 IDs per request. The returned Worklog contains `issueId`, author, start, duration, optional comment, and optional visibility. [Bulk Worklog retrieval](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-worklogs/#api-rest-api-3-worklog-list-post) |
| Resolve issue IDs to current keys and summaries | `POST /rest/api/3/issue/bulkfetch` | Submit up to 100 distinct issue IDs per request with `fields: ["summary"]`. The top-level issue object supplies the current `key`; `fields.summary` is the task name shown by `clock`. Handle partial per-issue errors. [Bulk issue retrieval](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/#api-rest-api-3-issue-bulkfetch-post) |
| Create a completed Worklog | `POST /rest/api/3/issue/{issueIdOrKey}/worklog?adjustEstimate=leave` | A success is `201 Created`. Do not use update/delete endpoints in v1. [Add Worklog](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-worklogs/#api-rest-api-3-issue-issueidorkey-worklog-post) |

## Reporting algorithm

Interpret every user range as a half-open local-time interval `[from, to)`, using the machine timezone. Convert its boundaries to instants. A Worklog belongs to the report when its parsed `started` instant is within that interval and `author.accountId` equals the `accountId` returned by `/myself`. Derive its end instant as `started + timeSpentSeconds`; Jira stores a start and duration, not an explicit end.

The practical range query is:

1. Search candidate issues with JQL equivalent to:

   ```text
   worklogAuthor = currentUser()
   AND worklogDate >= "YYYY-MM-DD"
   AND worklogDate <= "YYYY-MM-DD"
   ```

   Expand the JQL dates by one local calendar day on each edge so timezone conversion and the inclusive, date-only JQL predicates cannot discard a boundary Worklog. Request `summary`, paginate `nextPageToken`, and deduplicate issue IDs.
2. For every candidate issue, request all pages of issue Worklogs with epoch-millisecond `startedAfter`/`startedBefore`.
3. Client-filter by the exact half-open instants and `/myself` account ID. Never use `updateAuthor`: it identifies the last editor, not the person who logged the work.
4. Resolve/display the current issue `key` and `summary`, sort by `started`, and calculate totals from `timeSpentSeconds`.

This is the efficient v1 path, but Jira Cloud documents no direct “all Worklogs by this author and started-time range” endpoint. Therefore, validate the candidate strategy against the real site before promising mathematical completeness on unusually high-volume issues. Atlassian documents a 100-most-recent-worklogs JQL indexing safeguard for Data Center; it is not Cloud documentation, so it is a warning rather than evidence of identical Cloud behavior. [Atlassian Data Center limitation](https://support.atlassian.com/jira/kb/jql-queries-involving-the-worklogauthor-function-are-missing-some-jira-issues/)

If the product requirement is strict retrieval of **all** visible Worklogs without relying on JQL candidacy, use the documented global feed instead: page `/worklog/updated` from `since=0`, bulk-resolve every ID through `/worklog/list`, then apply the author and started-time filters. Supplement it with the candidate-issue path for the feed's documented one-minute blind window and deduplicate by Worklog ID. This is stateless and complete within what the authenticated user may see, but potentially expensive because it scans site-wide history on each report. A later local index could make it efficient, but that would be a cache—not the source of truth—and is outside the agreed v1.

Batch requests and honor Jira's `429 Too Many Requests` response and `Retry-After` header with bounded exponential backoff and jitter. [Jira rate limits](https://developer.atlassian.com/cloud/jira/platform/rate-limiting/)

## Create-Worklog mapping

Use this v3 payload:

```json
{
  "started": "2026-07-26T09:30:00.000+0200",
  "timeSpentSeconds": 5400,
  "comment": {
    "type": "doc",
    "version": 1,
    "content": [
      {
        "type": "paragraph",
        "content": [
          {
            "type": "text",
            "text": "Optional description"
          }
        ]
      }
    ]
  }
}
```

- `issueIdOrKey` is the user-supplied Jira task ID.
- `started` is the Timer/manual entry's local wall-clock start with the machine's numeric UTC offset. Preserve the instant returned by Jira rather than comparing timestamp strings.
- Parse the accepted `h`/`m` CLI duration syntax locally and send the positive integer `timeSpentSeconds`. This avoids Jira's administrator-configured default unit and day/week conversions. Jira also accepts `timeSpent`, but the CLI does not need to delegate this small grammar.
- Omit `comment` entirely when no description is supplied. When present, v3 expects Atlassian Document Format (ADF); a root `doc` containing one `paragraph` and one plain `text` node is sufficient. [ADF structure](https://developer.atlassian.com/cloud/jira/platform/apis/document/structure/)
- Omit `visibility`; `clock` does not create group/role-restricted Worklogs. Existing restricted Worklogs may still be absent from reports when the authenticated user lacks the required group or project role.
- Set `adjustEstimate=leave` explicitly so logging time does not alter the issue's remaining estimate.
- Do not set `overrideEditableFlag`, properties, or notifications in v1.

On `201`, the returned Worklog is authoritative. On `400`, show Jira's validation message. Treat `401` as invalid/expired credentials, while a `404` may mean either an unknown issue or insufficient visibility. A stopped Timer is cleared even if creation fails, per the product decision; print its issue key, start, attempted stop, duration, and optional description so the user can recover manually. The create endpoint can also return `413`. Jira can reject logging when workflow properties make the issue non-editable. [Worklog endpoint and permissions](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-worklogs/#api-rest-api-3-issue-issueidorkey-worklog-post) [Workflow restrictions](https://support.atlassian.com/jira-cloud-administration/docs/use-workflow-properties/)

## Permissions and visibility

The token acts with the same Jira permissions as its account; scopes never grant permissions the user lacks. Reading issues and Worklogs requires **Browse projects** plus any applicable issue-security access. Reading a visibility-restricted Worklog additionally requires membership in its selected group or project role. Creating requires **Browse projects**, **Work on issues**, and applicable issue-security access. Time tracking must be enabled or the Worklog operations fail. No edit/delete Worklog permissions are needed for v1. [Worklog REST permissions](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-worklogs/) [Time-tracking permissions](https://support.atlassian.com/jira-cloud-administration/docs/time-tracking-permissions/)

Because Jira filters inaccessible issues and restricted Worklogs, “all my Worklogs” means all Worklogs authored by the current account that the current account can still browse. A report must not silently label partial transport failures as complete: fail the report if any required page or issue batch fails.

## Running Timer conclusion

A Jira Worklog is a completed record requiring a duration (`timeSpent` or `timeSpentSeconds`) and a `started` timestamp. The API has create/read/update/delete operations and change feeds, but no start/stop operation, running state, or end timestamp. Jira's own workflow asks users to log time after work and presents accumulated time spent. [Worklog resource](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-worklogs/) [Jira time logging](https://support.atlassian.com/jira-software-cloud/docs/log-time-on-an-issue/)

Representing the active Timer as a provisional Jira Worklog would require repeated edits, would inflate Jira's time-spent aggregates while running, would need edit permissions excluded from v1, and would expose an incomplete record to other Jira users. Therefore the sole active Timer is local-only. On stop, calculate its duration once and create exactly one completed Jira Worklog.
