package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	appconfigure "github.com/JirakLu/clock/internal/app/configure"
	"golang.org/x/term"
)

type SecretReader interface {
	ReadSecret() (string, error)
}

type TerminalSecretReader struct {
	Input *os.File
}

func (r TerminalSecretReader) ReadSecret() (string, error) {
	if r.Input == nil || !term.IsTerminal(int(r.Input.Fd())) {
		return "", errors.New("API token entry requires an interactive terminal so input can be hidden")
	}
	value, err := term.ReadPassword(int(r.Input.Fd()))
	if err != nil {
		return "", fmt.Errorf("read hidden API token: %w", err)
	}
	return string(value), nil
}

type InteractivePrompter struct {
	input        *bufio.Reader
	output       io.Writer
	secretReader SecretReader
}

func NewInteractivePrompter(
	input io.Reader,
	output io.Writer,
	secretReader SecretReader,
) *InteractivePrompter {
	return &InteractivePrompter{
		input: bufio.NewReader(input), output: output, secretReader: secretReader,
	}
}

func (p *InteractivePrompter) ReadLine(label string) (string, error) {
	if _, err := fmt.Fprintf(p.output, "%s: ", label); err != nil {
		return "", err
	}
	value, err := p.input.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read %s: %w", label, err)
	}
	if errors.Is(err, io.EOF) && value == "" {
		return "", fmt.Errorf("read %s: input ended", label)
	}
	return strings.TrimSpace(value), nil
}

func (p *InteractivePrompter) ReadSecret(label string) (string, error) {
	if _, err := fmt.Fprintf(p.output, "%s: ", label); err != nil {
		return "", err
	}
	value, err := p.secretReader.ReadSecret()
	if _, newlineErr := fmt.Fprintln(p.output); err == nil && newlineErr != nil {
		err = newlineErr
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

func (p *InteractivePrompter) Confirm(
	ctx context.Context,
	proposal appconfigure.Proposal,
) (bool, error) {
	if _, err := fmt.Fprintf(
		p.output,
		"\nValidated Jira site and identity:\n  Site: %s\n  Name: %s\n  Email: %s\n  Account ID: %s\n  Hourly rate: %s CZK\n",
		proposal.Identity.SiteURL,
		proposal.Identity.DisplayName,
		proposal.Identity.Email,
		proposal.Identity.AccountID,
		proposal.HourlyRate.QuotedCZK(),
	); err != nil {
		return false, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		answer, err := p.ReadLine("Use this Jira identity? [y/N]")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "y", "yes":
			return true, nil
		case "", "n", "no":
			return false, nil
		default:
			if _, err := fmt.Fprintln(p.output, "Please answer yes or no."); err != nil {
				return false, err
			}
		}
	}
}
