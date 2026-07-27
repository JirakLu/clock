package secret_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/JirakLu/clock/internal/secret"
)

func TestTokenFormattingIsAlwaysRedacted(t *testing.T) {
	t.Parallel()

	const raw = "never-format-this"
	token, _ := secret.NewToken(raw)
	for _, format := range []string{"%s", "%v", "%+v", "%#v", "%q"} {
		formatted := fmt.Sprintf(format, token)
		if strings.Contains(formatted, raw) {
			t.Errorf("format %q exposed token as %q", format, formatted)
		}
	}
}
