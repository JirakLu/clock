package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/JirakLu/clock/internal/earnings"
	"github.com/JirakLu/clock/internal/jiraidentity"
	"github.com/pelletier/go-toml/v2"
)

type Configuration struct {
	JiraIdentity jiraidentity.Reference
	HourlyRate   earnings.HourlyRate
}

type Store struct {
	path string
}

func NewStore(userConfigDir string) *Store {
	return &Store{path: filepath.Join(userConfigDir, "clock", "config.toml")}
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) Load() (Configuration, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return Configuration{}, fmt.Errorf("read configuration %q: %w", s.path, err)
	}

	var document struct {
		Jira struct {
			SiteURL   string `toml:"site_url"`
			CloudID   string `toml:"cloud_id"`
			Email     string `toml:"email"`
			AccountID string `toml:"account_id"`
		} `toml:"jira"`
		Earnings struct {
			HourlyRate string `toml:"hourly_rate_czk"`
		} `toml:"earnings"`
	}
	decoder := toml.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return Configuration{}, fmt.Errorf("parse configuration %q: %w", s.path, err)
	}

	rate, err := earnings.ParseHourlyRate(document.Earnings.HourlyRate)
	if err != nil {
		return Configuration{}, fmt.Errorf("validate configuration %q: %w", s.path, err)
	}
	configuration := Configuration{
		JiraIdentity: jiraidentity.Reference{
			SiteURL: document.Jira.SiteURL, CloudID: jiraidentity.CloudID(document.Jira.CloudID),
			Email: document.Jira.Email, AccountID: jiraidentity.AccountID(document.Jira.AccountID),
		},
		HourlyRate: rate,
	}
	if err := validate(configuration); err != nil {
		return Configuration{}, fmt.Errorf("validate configuration %q: %w", s.path, err)
	}
	return configuration, nil
}

func (s *Store) Save(configuration Configuration) error {
	if err := validate(configuration); err != nil {
		return fmt.Errorf("validate configuration %q: %w", s.path, err)
	}

	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create configuration directory %q: %w", directory, err)
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("inspect configuration directory %q: %w", directory, err)
	}
	if permissions := directoryInfo.Mode().Perm(); permissions != 0o700 {
		return fmt.Errorf(
			"configuration directory %q has permissions %o; set them to 700 before retrying",
			directory,
			permissions,
		)
	}

	data := encode(configuration)
	temporary, err := os.CreateTemp(directory, ".config.toml-*")
	if err != nil {
		return fmt.Errorf("create temporary configuration beside %q: %w", s.path, err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = os.Remove(temporaryPath)
	}()

	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure temporary configuration for %q: %w", s.path, err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary configuration for %q: %w", s.path, err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary configuration for %q: %w", s.path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary configuration for %q: %w", s.path, err)
	}

	directoryHandle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open configuration directory %q before replacement: %w", directory, err)
	}
	defer directoryHandle.Close()
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("atomically replace configuration %q: %w", s.path, err)
	}
	// Rename is the commit point: the credential transaction must not roll back
	// after the new configuration is visible. The temp file was already synced
	// and secured; this best-effort directory sync improves crash durability
	// without misreporting a committed replacement as a non-mutating failure.
	_ = directoryHandle.Sync()
	return nil
}

func validate(configuration Configuration) error {
	canonical, err := CanonicalSiteURL(configuration.JiraIdentity.SiteURL)
	if err != nil {
		return err
	}
	if canonical != configuration.JiraIdentity.SiteURL {
		return fmt.Errorf("site URL must be canonical; use %q", canonical)
	}
	if strings.TrimSpace(string(configuration.JiraIdentity.CloudID)) == "" {
		return errors.New("Jira Cloud ID must not be empty")
	}
	if containsControl(string(configuration.JiraIdentity.CloudID)) {
		return errors.New("Jira Cloud ID contains invalid control characters")
	}
	if strings.TrimSpace(configuration.JiraIdentity.Email) == "" {
		return errors.New("Atlassian email must not be empty")
	}
	if containsControl(configuration.JiraIdentity.Email) {
		return errors.New("Atlassian email contains invalid control characters")
	}
	if strings.TrimSpace(string(configuration.JiraIdentity.AccountID)) == "" {
		return errors.New("Jira account ID must not be empty")
	}
	if containsControl(string(configuration.JiraIdentity.AccountID)) {
		return errors.New("Jira account ID contains invalid control characters")
	}
	if !configuration.HourlyRate.Valid() {
		return errors.New("Hourly rate must be a valid quoted CZK amount")
	}
	return nil
}

func containsControl(value string) bool {
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return true
		}
	}
	return false
}

func CanonicalSiteURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid Jira site URL %q: %w", raw, err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("Jira site URL %q must be an HTTPS origin without a path, query, credentials, or fragment", raw)
	}
	return "https://" + strings.ToLower(parsed.Host), nil
}

func encode(configuration Configuration) []byte {
	return []byte(fmt.Sprintf(`[jira]
site_url = %s
cloud_id = %s
email = %s
account_id = %s

[earnings]
hourly_rate_czk = %s
`,
		strconv.Quote(configuration.JiraIdentity.SiteURL),
		strconv.Quote(string(configuration.JiraIdentity.CloudID)),
		strconv.Quote(configuration.JiraIdentity.Email),
		strconv.Quote(string(configuration.JiraIdentity.AccountID)),
		strconv.Quote(configuration.HourlyRate.QuotedCZK()),
	))
}
