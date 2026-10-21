package control

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/core"
)

type githubFilters struct {
	AccountID    string   `json:"account_id"`
	Repositories []string `json:"repositories"`
	Reasons      []string `json:"reasons"`
	SubjectTypes []string `json:"subject_types"`
	Threads      []string `json:"threads"`
}

var githubReasons = []string{"assign", "author", "comment", "ci_activity", "invitation", "manual", "mention", "review_requested", "security_alert", "state_change", "subscribed", "team_mention"}
var githubSubjectTypes = []string{"Issue", "PullRequest", "Commit", "Release", "Discussion", "CheckSuite", "RepositoryVulnerabilityAlert"}

func defaultGitHubFilters(account string) json.RawMessage {
	raw, _ := json.Marshal(githubFilters{AccountID: account, Reasons: []string{"mention"}})
	return raw
}

// Strict decoding retains null as unrestricted and [] as match-none.
func decodeGitHubFilters(raw json.RawMessage, config *githubFilters) error {
	var wire struct {
		AccountID    string    `json:"account_id"`
		Repositories *[]string `json:"repositories"`
		Reasons      *[]string `json:"reasons"`
		SubjectTypes *[]string `json:"subject_types"`
		Threads      *[]string `json:"threads"`
	}
	if err := core.DecodeJSON(raw, &wire); err != nil {
		return err
	}
	return json.Unmarshal(raw, config)
}
func validateGitHubNotificationConfig(raw json.RawMessage) error {
	var keys map[string]json.RawMessage
	if err := core.DecodeJSON(raw, &keys); err != nil {
		return err
	}
	for _, key := range []string{"account_id", "repositories", "reasons", "subject_types", "threads"} {
		if _, ok := keys[key]; !ok {
			return errors.New("missing_notification_" + key)
		}
	}
	var config githubFilters
	if string(raw) == "null" {
		return errors.New("invalid_notification_filters")
	}
	if err := decodeGitHubFilters(raw, &config); err != nil {
		return err
	}
	repo := regexp.MustCompile(`^[-\w.]+/[-\w.]+$`)
	thread := regexp.MustCompile(`^[1-9][0-9]*$`)
	for key, values := range map[string][]string{"repositories": config.Repositories, "reasons": config.Reasons, "subject_types": config.SubjectTypes, "threads": config.Threads} {
		if len(values) > 100 {
			return errors.New("too_many_notification_filters")
		}
		for _, v := range values {
			if len(v) > 200 || key == "repositories" && !repo.MatchString(v) || key == "threads" && !thread.MatchString(v) {
				return errors.New("invalid_notification_" + key)
			}
			if key == "reasons" && !slices.Contains(githubReasons, v) {
				return errors.New("unsupported_notification_reason")
			}
			if key == "subject_types" && !slices.Contains(githubSubjectTypes, v) {
				return errors.New("unsupported_subject_type")
			}
		}
	}
	return nil
}
func (s *Service) githubAccount(ctx context.Context) (string, error) {
	catalog, err := s.Integrations.List(ctx)
	if err != nil {
		return "", err
	}
	for _, item := range catalog {
		if item.ID == "github" && item.Account != nil {
			return item.Account.Identity.ID, nil
		}
	}
	return "", errors.New("github_account_not_connected")
}
func (s *Service) githubBinding(ctx context.Context) (assistant.BackgroundBinding, error) {
	if err := s.ensureGitHubNotifications(ctx); err != nil {
		return assistant.BackgroundBinding{}, err
	}
	bindings, err := s.Mailbox.Backgrounds(ctx)
	if err != nil {
		return assistant.BackgroundBinding{}, err
	}
	for _, b := range bindings {
		if b.ID == "github" {
			return b, nil
		}
	}
	return assistant.BackgroundBinding{}, errors.New("github_notification_binding_unavailable")
}
