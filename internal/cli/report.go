package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	appreport "github.com/JirakLu/clock/internal/app/report"
	"github.com/JirakLu/clock/internal/report"
	"github.com/JirakLu/clock/internal/worklog"
	"github.com/spf13/cobra"
)

func newReportCommand(options RootOptions) *cobra.Command {
	var (
		fromRaw         string
		toRaw           string
		includeEarnings bool
		asJSON          bool
	)
	command := &cobra.Command{
		Use:   "report",
		Short: "Report accessible authored Jira Worklogs",
		Long: `Report accessible Worklogs authored by the configured Jira identity.

Presets use machine-local calendar windows. Explicit --from and --to bounds are
required together and form an inclusive-start, exclusive-end Report window.
A bound accepts YYYY-MM-DD, local YYYY-MM-DDTHH:MM, or offset-bearing
YYYY-MM-DDTHH:MM+02:00. Ambiguous local times require an explicit offset.`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runReport(command, options, report.Explicit, fromRaw, toRaw, includeEarnings, asJSON)
		},
	}
	command.PersistentFlags().StringVar(&fromRaw, "from", "", "inclusive Report bound")
	command.PersistentFlags().StringVar(&toRaw, "to", "", "exclusive Report bound")
	command.PersistentFlags().BoolVar(&includeEarnings, "earnings", false, "include CZK Earnings aggregates")
	command.PersistentFlags().BoolVar(&asJSON, "json", false, "emit the clock.report.v1 JSON contract")
	for _, preset := range []report.Selector{report.Today, report.LastWeek, report.LastMonth} {
		selector := preset
		command.AddCommand(&cobra.Command{
			Use:   selector.Value(),
			Short: "Report " + strings.ReplaceAll(selector.Value(), "-", " "),
			Args:  cobra.NoArgs,
			RunE: func(child *cobra.Command, _ []string) error {
				return runReport(child, options, selector, fromRaw, toRaw, includeEarnings, asJSON)
			},
		})
	}
	return command
}

func runReport(
	command *cobra.Command,
	options RootOptions,
	selector report.Selector,
	fromRaw string,
	toRaw string,
	includeEarnings bool,
	asJSON bool,
) error {
	if options.Report == nil {
		return errors.New("report command is unavailable")
	}
	fromSet := command.Flags().Changed("from")
	toSet := command.Flags().Changed("to")
	input := appreport.Input{Selector: selector, Earnings: includeEarnings}
	if selector == report.Explicit {
		if fromSet != toSet {
			return errors.New("--from and --to are required together")
		}
		if !fromSet {
			return errors.New("clock report requires a preset or both --from and --to")
		}
		location := options.Location
		if location == nil {
			location = time.Local
		}
		var err error
		input.From, err = parseReportBound(fromRaw, location)
		if err != nil {
			return err
		}
		input.To, err = parseReportBound(toRaw, location)
		if err != nil {
			return err
		}
		if !input.To.After(input.From) {
			return errors.New("Report window --to must be after --from")
		}
	} else if fromSet || toSet {
		return errors.New("Report presets conflict with --from and --to")
	}

	result, err := options.Report.Run(command.Context(), input)
	if err != nil {
		return err
	}
	if asJSON {
		return renderReportJSON(command.OutOrStdout(), result)
	}
	return renderReportTerminal(command.OutOrStdout(), result)
}

func renderReportTerminal(writer io.Writer, result appreport.Result) error {
	value := result.Report
	location := value.Window.Location()
	if _, err := fmt.Fprintf(
		writer, "%s · [%s, %s) · %s\n\n",
		strings.ToUpper(strings.ReplaceAll(value.Window.Selector.Value(), "-", " ")),
		value.Window.From.In(location).Format(time.RFC3339),
		value.Window.To.In(location).Format(time.RFC3339),
		value.Window.Timezone,
	); err != nil {
		return err
	}
	switch {
	case value.Window.Selector == report.Today || len(value.DailyTotals) == 1:
		if err := renderDayReport(writer, result); err != nil {
			return err
		}
	case value.Window.Selector == report.LastWeek || len(value.DailyTotals) <= 7:
		if err := renderWeekReport(writer, result); err != nil {
			return err
		}
	default:
		if err := renderMonthReport(writer, result); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(writer, "\nTOTAL  %s%s\n", formatSeconds(value.TotalSeconds), earningsSuffix(result, value.TotalSeconds))
	if err != nil {
		return err
	}
	if value.Window.Selector == report.Today || len(value.DailyTotals) == 1 {
		_, err = fmt.Fprintln(writer, "\nNote: overlapping Worklogs are counted independently.")
	} else if value.Window.Selector == report.LastMonth || len(value.DailyTotals) > 7 {
		_, err = fmt.Fprintln(writer, "\nUse JSON output when individual Worklog contributions are needed.")
	}
	return err
}

func renderDayReport(writer io.Writer, result appreport.Result) error {
	value := result.Report
	if len(value.DailyTotals) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(
		writer,
		strings.ToUpper(value.DailyTotals[0].Date.Format("Mon 02 Jan")),
	); err != nil {
		return err
	}
	for _, contribution := range value.Contributions {
		from := contribution.From.In(value.Window.Location()).Format("15:04")
		to := contribution.To.In(value.Window.Location()).Format("15:04")
		if contribution.To.In(value.Window.Location()).Hour() == 0 &&
			contribution.To.In(value.Window.Location()).Minute() == 0 &&
			contribution.To.In(value.Window.Location()).YearDay() !=
				contribution.From.In(value.Window.Location()).YearDay() {
			to = "24:00"
		}
		if _, err := fmt.Fprintf(
			writer, "%s ┌ %s — %s\n",
			from, contribution.Issue, contribution.Summary,
		); err != nil {
			return err
		}
		if contribution.Description != "" {
			if _, err := fmt.Fprintf(writer, "      │ %s\n", contribution.Description); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(
			writer, "%s └ %s\n\n", to, formatSeconds(contribution.Seconds),
		); err != nil {
			return err
		}
	}
	total := value.DailyTotals[0].Seconds
	_, err := fmt.Fprintf(writer, "DAY TOTAL  %s%s\n", formatSeconds(total), earningsSuffix(result, total))
	return err
}

func renderWeekReport(writer io.Writer, result appreport.Result) error {
	if _, err := fmt.Fprintln(writer, "DATE        TIME         DURATION  ISSUE  DESCRIPTION"); err != nil {
		return err
	}
	byDate := make(map[string][]report.Contribution)
	for _, contribution := range result.Report.Contributions {
		byDate[contribution.Date.String()] = append(
			byDate[contribution.Date.String()],
			contribution,
		)
	}
	for _, daily := range result.Report.DailyTotals {
		label := daily.Date.Format("Mon 02 Jan")
		contributions := byDate[daily.Date.String()]
		if len(contributions) == 0 {
			if _, err := fmt.Fprintf(
				writer, "%s  DAY TOTAL       %s%s\n",
				label, formatSeconds(daily.Seconds), earningsSuffix(result, daily.Seconds),
			); err != nil {
				return err
			}
			continue
		}
		for index, contribution := range contributions {
			rowDate := "           "
			if index == 0 {
				rowDate = label
			}
			description := contribution.Description
			if description == "" {
				description = "—"
			}
			if _, err := fmt.Fprintf(
				writer, "%s  %-11s  %8s  %s — %s  %s\n",
				rowDate, formatContributionTime(contribution, result.Report.Window.Location()),
				formatSeconds(contribution.Seconds), contribution.Issue, contribution.Summary, description,
			); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(
			writer, "            DAY TOTAL       %s%s\n",
			formatSeconds(daily.Seconds), earningsSuffix(result, daily.Seconds),
		); err != nil {
			return err
		}
	}
	return nil
}

func renderMonthReport(writer io.Writer, result appreport.Result) error {
	header := "DAILY TOTALS\nDATE        DURATION"
	if result.IncludeEarnings {
		header += "  EARNINGS"
	}
	if _, err := fmt.Fprintln(writer, header); err != nil {
		return err
	}
	for _, daily := range result.Report.DailyTotals {
		if _, err := fmt.Fprintf(
			writer, "%s  %-10s%s\n",
			daily.Date.Format("Mon 02 Jan"),
			formatSeconds(daily.Seconds),
			monthEarnings(result, daily.Seconds),
		); err != nil {
			return err
		}
	}
	type issueTotal struct {
		key     worklog.IssueKey
		summary string
		seconds int64
	}
	byIssue := make(map[worklog.IssueKey]*issueTotal)
	for _, contribution := range result.Report.Contributions {
		total := byIssue[contribution.Issue]
		if total == nil {
			total = &issueTotal{key: contribution.Issue, summary: contribution.Summary}
			byIssue[contribution.Issue] = total
		}
		total.seconds += contribution.Seconds
	}
	issues := make([]*issueTotal, 0, len(byIssue))
	for _, total := range byIssue {
		issues = append(issues, total)
	}
	sort.Slice(issues, func(left, right int) bool { return issues[left].key < issues[right].key })
	if _, err := fmt.Fprintln(writer, "\nBY ISSUE"); err != nil {
		return err
	}
	for _, issue := range issues {
		if _, err := fmt.Fprintf(writer, "%s — %s  %s\n", issue.key, issue.summary, formatSeconds(issue.seconds)); err != nil {
			return err
		}
	}
	return nil
}

func formatContributionTime(contribution report.Contribution, location *time.Location) string {
	from := contribution.From.In(location)
	to := contribution.To.In(location)
	end := to.Format("15:04")
	if to.Hour() == 0 && to.Minute() == 0 &&
		to.YearDay() != from.YearDay() {
		end = "24:00"
	}
	return from.Format("15:04") + "–" + end
}

func formatSeconds(seconds int64) string {
	if seconds == 0 {
		return "0m"
	}
	duration, err := worklog.DurationFromSeconds(seconds)
	if err != nil {
		return "0m"
	}
	return duration.String()
}

func earningsSuffix(result appreport.Result, seconds int64) string {
	if !result.IncludeEarnings {
		return ""
	}
	return " · " + result.HourlyRate.FormatAggregate(seconds) + " CZK"
}

func monthEarnings(result appreport.Result, seconds int64) string {
	if !result.IncludeEarnings {
		return ""
	}
	return result.HourlyRate.FormatAggregate(seconds) + " CZK"
}

type reportJSON struct {
	Schema        string             `json:"schema"`
	Selector      selectorJSON       `json:"selector"`
	Window        windowJSON         `json:"window"`
	Contributions []contributionJSON `json:"contributions"`
	DailyTotals   []aggregateJSON    `json:"daily_totals"`
	Total         aggregateJSON      `json:"total"`
}

type selectorJSON struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type windowJSON struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Timezone string `json:"timezone"`
}

type contributionJSON struct {
	WorklogID   string    `json:"worklog_id"`
	Issue       issueJSON `json:"issue"`
	Description string    `json:"description,omitempty"`
	From        string    `json:"from"`
	To          string    `json:"to"`
	Seconds     int64     `json:"seconds"`
}

type issueJSON struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
}

type aggregateJSON struct {
	Date        string `json:"date,omitempty"`
	Seconds     int64  `json:"seconds"`
	EarningsCZK string `json:"earnings_czk,omitempty"`
}

func renderReportJSON(writer io.Writer, result appreport.Result) error {
	value := result.Report
	document := reportJSON{
		Schema: "clock.report.v1",
		Selector: selectorJSON{
			Type: value.Window.Selector.JSONType(), Value: value.Window.Selector.JSONValue(),
		},
		Window: windowJSON{
			From:     value.Window.From.In(value.Window.Location()).Format(time.RFC3339),
			To:       value.Window.To.In(value.Window.Location()).Format(time.RFC3339),
			Timezone: value.Window.Timezone,
		},
		Contributions: make([]contributionJSON, 0, len(value.Contributions)),
		DailyTotals:   make([]aggregateJSON, 0, len(value.DailyTotals)),
		Total:         aggregateJSON{Seconds: value.TotalSeconds},
	}
	for _, contribution := range value.Contributions {
		document.Contributions = append(document.Contributions, contributionJSON{
			WorklogID:   contribution.WorklogID,
			Issue:       issueJSON{Key: contribution.Issue.String(), Summary: contribution.Summary},
			Description: contribution.Description,
			From:        contribution.From.In(value.Window.Location()).Format(time.RFC3339),
			To:          contribution.To.In(value.Window.Location()).Format(time.RFC3339),
			Seconds:     contribution.Seconds,
		})
	}
	for _, daily := range value.DailyTotals {
		aggregate := aggregateJSON{Date: daily.Date.String(), Seconds: daily.Seconds}
		if result.IncludeEarnings {
			aggregate.EarningsCZK = result.HourlyRate.FormatAggregate(daily.Seconds)
		}
		document.DailyTotals = append(document.DailyTotals, aggregate)
	}
	if result.IncludeEarnings {
		document.Total.EarningsCZK = result.HourlyRate.FormatAggregate(value.TotalSeconds)
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(document)
}
