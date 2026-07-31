package runningtimer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/JirakLu/clock/internal/jiraidentity"
	"github.com/JirakLu/clock/internal/worklog"
)

var ErrNoTimer = errors.New("No Running timer.")

type Timer struct {
	Issue       worklog.IssueKey
	StartedAt   time.Time
	Description string
	CloudID     jiraidentity.CloudID
	AccountID   jiraidentity.AccountID
}

type Inspection struct {
	Timer Timer
}

type Store struct {
	path        string
	stagingPath string
	lockPath    string
}

func NewStore(userConfigDir string) *Store {
	path := filepath.Join(userConfigDir, "clock", "state.json")
	return &Store{
		path: path, stagingPath: path + ".staging", lockPath: path + ".lock",
	}
}

func (s *Store) Path() string        { return s.path }
func (s *Store) StagingPath() string { return s.stagingPath }

func (s *Store) Inspect(now time.Time) (Inspection, error) {
	var result Inspection
	err := s.withLock(func() error {
		var err error
		result, err = s.inspectLocked(now)
		return err
	})
	return result, err
}

func (s *Store) Create(timer Timer, now time.Time) error {
	if err := validateTimer(timer, now); err != nil {
		return fmt.Errorf("validate Running timer: %w", err)
	}
	return s.withLock(func() error {
		_, err := s.inspectLocked(now)
		if err == nil {
			return errors.New("a Running timer is already active")
		}
		if !errors.Is(err, ErrNoTimer) {
			return err
		}
		return s.writeLocked(timer)
	})
}

func (s *Store) Consume(expected Timer, now time.Time) error {
	return s.removeValid(expected, now, "consume")
}

func (s *Store) Discard(expected Timer, now time.Time) error {
	return s.removeValid(expected, now, "discard")
}

func (s *Store) removeValid(expected Timer, now time.Time, operation string) error {
	return s.withLock(func() error {
		inspection, err := s.inspectLocked(now)
		if err != nil {
			return err
		}
		if !timersEqual(inspection.Timer, expected) {
			return fmt.Errorf("Running timer changed while attempting to %s it", operation)
		}
		if err := os.Remove(s.path); err != nil {
			return fmt.Errorf("%s Running timer state %q: %w", operation, s.path, err)
		}
		// Removal is the commit point. A later directory-sync failure must not be
		// reported as a preserving failure after the timer is already gone.
		_ = syncDirectory(filepath.Dir(s.path))
		return nil
	})
}

func timersEqual(left, right Timer) bool {
	return left.Issue == right.Issue && left.StartedAt.Equal(right.StartedAt) &&
		left.Description == right.Description && left.CloudID == right.CloudID &&
		left.AccountID == right.AccountID
}

func (s *Store) inspectLocked(now time.Time) (Inspection, error) {
	stagingExists, err := exists(s.stagingPath)
	if err != nil {
		return Inspection{}, fmt.Errorf("inspect Running timer staging state %q: %w", s.stagingPath, err)
	}
	if stagingExists {
		return Inspection{}, fmt.Errorf("incomplete atomic Running timer write at %q", s.stagingPath)
	}
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return Inspection{}, ErrNoTimer
	}
	if err != nil {
		return Inspection{}, fmt.Errorf("read Running timer state %q: %w", s.path, err)
	}
	if info, err := os.Stat(s.path); err != nil {
		return Inspection{}, fmt.Errorf("inspect Running timer state %q: %w", s.path, err)
	} else if info.Mode().Perm() != 0o600 {
		return Inspection{}, fmt.Errorf("invalid Running timer state %q: permissions are %o, want 600", s.path, info.Mode().Perm())
	}
	timer, err := decodeTimer(data, now)
	if err != nil {
		return Inspection{}, fmt.Errorf("invalid Running timer state %q: %w", s.path, err)
	}
	return Inspection{Timer: timer}, nil
}

func (s *Store) writeLocked(timer Timer) error {
	directory := filepath.Dir(s.path)
	document := struct {
		SchemaVersion int                    `json:"schema_version"`
		IssueKey      worklog.IssueKey       `json:"issue_key"`
		StartedAt     string                 `json:"started_at"`
		Description   string                 `json:"description,omitempty"`
		CloudID       jiraidentity.CloudID   `json:"jira_cloud_id"`
		AccountID     jiraidentity.AccountID `json:"jira_account_id"`
	}{
		SchemaVersion: 1, IssueKey: timer.Issue,
		StartedAt: timer.StartedAt.Format(time.RFC3339Nano), Description: timer.Description,
		CloudID: timer.CloudID, AccountID: timer.AccountID,
	}
	data, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode Running timer state: %w", err)
	}
	data = append(data, '\n')
	file, err := os.OpenFile(s.stagingPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create Running timer staging state %q: %w", s.stagingPath, err)
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(s.stagingPath)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write Running timer staging state %q: %w", s.stagingPath, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync Running timer staging state %q: %w", s.stagingPath, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Running timer staging state %q: %w", s.stagingPath, err)
	}
	if err := os.Rename(s.stagingPath, s.path); err != nil {
		return fmt.Errorf("atomically create Running timer state %q: %w", s.path, err)
	}
	committed = true
	// Rename is the commit point. Do not report a committed timer as absent if
	// the best-effort directory sync is unavailable.
	_ = syncDirectory(directory)
	return nil
}

func (s *Store) withLock(operation func() error) error {
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create Running timer directory %q: %w", directory, err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("inspect Running timer directory %q: %w", directory, err)
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("Running timer directory %q has permissions %o; set them to 700", directory, info.Mode().Perm())
	}
	lock, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open Running timer lock %q: %w", s.lockPath, err)
	}
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return fmt.Errorf("secure Running timer lock %q: %w", s.lockPath, err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock Running timer state %q: %w", s.path, err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return operation()
}

func decodeTimer(data []byte, now time.Time) (Timer, error) {
	fields, err := strictObject(data)
	if err != nil {
		return Timer{}, err
	}
	allowed := map[string]bool{
		"schema_version": true, "issue_key": true, "started_at": true,
		"description": true, "jira_cloud_id": true, "jira_account_id": true,
	}
	for name := range fields {
		if !allowed[name] {
			return Timer{}, fmt.Errorf("unknown field %q", name)
		}
	}
	for _, name := range []string{"schema_version", "issue_key", "started_at", "jira_cloud_id", "jira_account_id"} {
		if _, ok := fields[name]; !ok {
			return Timer{}, fmt.Errorf("required field %q is missing", name)
		}
	}
	var version int
	if err := json.Unmarshal(fields["schema_version"], &version); err != nil || version != 1 {
		return Timer{}, fmt.Errorf("schema_version must be 1")
	}
	var rawIssue, rawStart, description, cloudID, accountID string
	for name, destination := range map[string]*string{
		"issue_key": &rawIssue, "started_at": &rawStart, "jira_cloud_id": &cloudID,
		"jira_account_id": &accountID,
	} {
		if err := json.Unmarshal(fields[name], destination); err != nil {
			return Timer{}, fmt.Errorf("field %q must be a string", name)
		}
	}
	if raw, ok := fields["description"]; ok {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return Timer{}, errors.New("field \"description\" must be a string")
		}
		if err := json.Unmarshal(raw, &description); err != nil {
			return Timer{}, errors.New("field \"description\" must be a string")
		}
	}
	issue, err := worklog.ParseIssueKey(rawIssue)
	if err != nil {
		return Timer{}, err
	}
	if issue.String() != rawIssue {
		return Timer{}, errors.New("issue_key must be normalized to uppercase")
	}
	startedAt, err := time.Parse(time.RFC3339Nano, rawStart)
	if err != nil {
		return Timer{}, errors.New("started_at must be an offset-bearing RFC 3339 timestamp")
	}
	timer := Timer{Issue: issue, StartedAt: startedAt, Description: description, CloudID: jiraidentity.CloudID(cloudID), AccountID: jiraidentity.AccountID(accountID)}
	if err := validateTimer(timer, now); err != nil {
		return Timer{}, err
	}
	return timer, nil
}

func strictObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, errors.New("JSON document must be an object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("parse JSON field: %w", err)
		}
		name, ok := token.(string)
		if !ok {
			return nil, errors.New("JSON field name must be a string")
		}
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate field %q", name)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, fmt.Errorf("parse field %q: %w", name, err)
		}
		fields[name] = raw
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("parse JSON object: %w", err)
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("parse trailing JSON: %w", err)
		}
		return nil, fmt.Errorf("unexpected trailing JSON token %v", token)
	}
	return fields, nil
}

func validateTimer(timer Timer, now time.Time) error {
	if !timer.StartedAt.IsZero() && timer.StartedAt.After(now) {
		return errors.New("started_at is in the future")
	}
	if timer.StartedAt.IsZero() {
		return errors.New("started_at must not be empty")
	}
	if parsed, err := worklog.ParseIssueKey(timer.Issue.String()); err != nil || parsed != timer.Issue {
		return errors.New("issue_key must be a valid normalized Jira issue key")
	}
	if strings.TrimSpace(string(timer.CloudID)) == "" {
		return errors.New("jira_cloud_id must not be empty")
	}
	if strings.TrimSpace(string(timer.AccountID)) == "" {
		return errors.New("jira_account_id must not be empty")
	}
	return nil
}

func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open Running timer directory %q: %w", path, err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync Running timer directory %q: %w", path, err)
	}
	return nil
}
