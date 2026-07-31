package jiraidentity

type CloudID string

type AccountID string

type Reference struct {
	SiteURL   string
	CloudID   CloudID
	Email     string
	AccountID AccountID
}

type Identity struct {
	Reference
	DisplayName string
}
