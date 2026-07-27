package recording

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/JirakLu/clock/internal/jiraidentity"
	"github.com/JirakLu/clock/internal/secret"
	"github.com/JirakLu/clock/internal/worklog"
)

var (
	ErrFutureWorklog      = errors.New("Worklog must not end in the future")
	ErrNonPositiveWorklog = errors.New("Worklog Duration must be at least one whole second")
	ErrOverlap            = errors.New("proposed Worklog overlaps an existing authored Worklog")
	ErrNoLatestWorklog    = errors.New("no eligible authored Worklog starts today; use --at or a Duration ending now")
)

type TimingMode uint8

const (
	EndingNow TimingMode = iota + 1
	AtStart
	AfterLast
)

type Timing struct {
	Mode     TimingMode
	Duration worklog.Duration
	Start    time.Time
}

type Request struct {
	Issue       worklog.IssueKey
	Timing      Timing
	Description string
}

type Auth struct {
	Identity jiraidentity.Reference
	Token    secret.Token
}

type Status uint8

const (
	Submitted Status = iota + 1
	Rejected
	Uncertain
)

type Result struct {
	Status  Status
	Worklog worklog.Worklog
	Attempt worklog.Draft
	Cause   error
}

type Gateway interface {
	VerifyIdentity(context.Context, Auth) error
	ListAuthoredWorklogs(
		context.Context,
		Auth,
		time.Time,
		time.Time,
	) ([]worklog.Worklog, error)
	CreateWorklog(
		context.Context,
		Auth,
		worklog.Draft,
	) (worklog.Worklog, error)
}

type Service struct {
	gateway Gateway
}

func New(gateway Gateway) *Service {
	return &Service{gateway: gateway}
}

func (s *Service) Record(
	ctx context.Context,
	auth Auth,
	request Request,
	now time.Time,
) (Result, error) {
	if s.gateway == nil {
		return Result{}, errors.New("Jira recording capability is unavailable")
	}
	if err := s.gateway.VerifyIdentity(ctx, auth); err != nil {
		return Result{}, fmt.Errorf("verify configured Jira identity: %w", err)
	}

	draft, existing, err := s.resolveDraft(ctx, auth, request, now)
	if err != nil {
		return Result{}, err
	}
	if draft.Interval.End().After(now) {
		return Result{}, ErrFutureWorklog
	}
	if draft.Interval.Seconds() <= 0 {
		return Result{}, ErrNonPositiveWorklog
	}
	if existing == nil {
		existing, err = s.gateway.ListAuthoredWorklogs(
			ctx,
			auth,
			draft.Interval.Start(),
			draft.Interval.End(),
		)
		if err != nil {
			return Result{}, fmt.Errorf("check authored Worklog overlap: %w", err)
		}
	}
	for _, candidate := range existing {
		if draft.Interval.Overlaps(candidate.Interval) {
			return Result{}, fmt.Errorf(
				"%w %q from %s to %s",
				ErrOverlap,
				candidate.ID,
				candidate.Interval.Start().Format(time.RFC3339),
				candidate.Interval.End().Format(time.RFC3339),
			)
		}
	}

	created, err := s.gateway.CreateWorklog(ctx, auth, draft)
	if err != nil {
		status := Rejected
		var classified interface{ Uncertain() bool }
		if errors.As(err, &classified) && classified.Uncertain() {
			status = Uncertain
		}
		return Result{Status: status, Attempt: draft, Cause: err}, nil
	}
	return Result{Status: Submitted, Worklog: created}, nil
}

func (s *Service) resolveDraft(
	ctx context.Context,
	auth Auth,
	request Request,
	now time.Time,
) (worklog.Draft, []worklog.Worklog, error) {
	var (
		start    time.Time
		end      time.Time
		existing []worklog.Worklog
	)
	if (request.Timing.Mode == EndingNow || request.Timing.Mode == AtStart) &&
		!request.Timing.Duration.Valid() {
		return worklog.Draft{}, nil, errors.New("manual Worklog requires a positive Duration")
	}
	switch request.Timing.Mode {
	case EndingNow:
		start = now.Add(-request.Timing.Duration.Duration())
		end = now
	case AtStart:
		if request.Timing.Start.IsZero() {
			return worklog.Draft{}, nil, errors.New("explicit Worklog start must not be empty")
		}
		start = request.Timing.Start
		end = start.Add(request.Timing.Duration.Duration())
	case AfterLast:
		todayStart := time.Date(
			now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location(),
		)
		tomorrowStart := todayStart.AddDate(0, 0, 1)
		var err error
		existing, err = s.gateway.ListAuthoredWorklogs(
			ctx,
			auth,
			todayStart,
			tomorrowStart,
		)
		if err != nil {
			return worklog.Draft{}, nil, fmt.Errorf("find today's latest authored Worklog: %w", err)
		}
		for _, candidate := range existing {
			if candidate.Interval.Start().Before(todayStart) ||
				!candidate.Interval.Start().Before(tomorrowStart) {
				continue
			}
			if start.IsZero() || candidate.Interval.End().After(start) {
				start = candidate.Interval.End()
			}
		}
		if start.IsZero() {
			return worklog.Draft{}, nil, ErrNoLatestWorklog
		}
		if !start.Before(now) {
			return worklog.Draft{}, nil, errors.New(
				"today's latest authored Worklog ends now or in the future; use --at or a Duration ending now",
			)
		}
		end = now
	default:
		return worklog.Draft{}, nil, errors.New("unsupported Worklog timing mode")
	}
	interval, err := worklog.NewInterval(start, end)
	if err != nil {
		return worklog.Draft{}, nil, err
	}
	return worklog.Draft{
		Issue: request.Issue, Interval: interval, Description: request.Description,
	}, existing, nil
}
