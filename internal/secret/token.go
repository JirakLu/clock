package secret

import "fmt"

// Token deliberately has no String or formatting methods.
type Token struct {
	value string
}

func NewToken(value string) (Token, bool) {
	if value == "" {
		return Token{}, false
	}
	return Token{value: value}, true
}

func (t Token) Value() string {
	return t.value
}

func (t Token) Empty() bool {
	return t.value == ""
}

func (t Token) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED]"))
}
