// Package github implements GitHub's browser device authorization flow.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/integrations"
)

type Options struct {
	ClientID   string
	HTTPClient *http.Client
}
type Adapter struct {
	options Options
	client  *http.Client
}

func New(o Options) *Adapter {
	o.ClientID = strings.TrimSpace(o.ClientID)
	client := http.Client{Timeout: 20 * time.Second}
	if o.HTTPClient != nil {
		client = *o.HTTPClient
	}
	if client.Timeout == 0 {
		client.Timeout = 20 * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("authorization redirect refused") }
	return &Adapter{o, &client}
}
func (a *Adapter) Descriptor() integrations.Descriptor {
	return integrations.Descriptor{ID: "github", Name: "GitHub", LoginAvailable: strings.TrimSpace(a.options.ClientID) != ""}
}

type device struct {
	Code     string
	Interval time.Duration
}

func (a *Adapter) Begin(ctx context.Context) (integrations.Challenge, error) {
	if !a.Descriptor().LoginAvailable {
		return integrations.Challenge{}, errors.New("GitHub OAuth application is not configured")
	}
	var response struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	err := a.post(ctx, "https://github.com/login/device/code", url.Values{"client_id": {a.options.ClientID}, "scope": {"repo read:user"}}, &response)
	if err != nil {
		return integrations.Challenge{}, err
	}
	if response.DeviceCode == "" || response.UserCode == "" || response.VerificationURI != "https://github.com/login/device" || response.ExpiresIn <= 0 || response.ExpiresIn > 3600 || response.Interval < 0 || response.Interval > 3600 {
		return integrations.Challenge{}, errors.New("invalid GitHub authorization challenge")
	}
	interval := max(response.Interval, 5)
	return integrations.Challenge{URL: response.VerificationURI, UserCode: response.UserCode, ExpiresAt: time.Now().Add(time.Duration(response.ExpiresIn) * time.Second), Private: device{response.DeviceCode, time.Duration(interval) * time.Second}}, nil
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshExpiresIn int    `json:"refresh_token_expires_in"`
	Error            string `json:"error"`
}

func (r tokenResponse) credential() integrations.Credential {
	c := integrations.Credential{AccessToken: r.AccessToken, RefreshToken: r.RefreshToken}
	now := time.Now()
	if r.ExpiresIn > 0 {
		expiry := now.Add(time.Duration(r.ExpiresIn) * time.Second)
		c.ExpiresAt = &expiry
	}
	if r.RefreshExpiresIn > 0 {
		expiry := now.Add(time.Duration(r.RefreshExpiresIn) * time.Second)
		c.RefreshExpiresAt = &expiry
	}
	return c
}
func (a *Adapter) Authorize(ctx context.Context, challenge integrations.Challenge) (integrations.Grant, error) {
	state, ok := challenge.Private.(device)
	if !ok {
		return integrations.Grant{}, errors.New("invalid GitHub login session")
	}
	interval := state.Interval
	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return integrations.Grant{}, ctx.Err()
		case <-timer.C:
		}
		var token tokenResponse
		err := a.post(ctx, "https://github.com/login/oauth/access_token", url.Values{"client_id": {a.options.ClientID}, "device_code": {state.Code}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}, &token)
		if err != nil {
			return integrations.Grant{}, err
		}
		switch token.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "":
		default:
			return integrations.Grant{}, errors.New("GitHub authorization was denied or expired")
		}
		if token.AccessToken == "" || token.ExpiresIn < 0 || token.RefreshExpiresIn < 0 {
			return integrations.Grant{}, errors.New("GitHub returned no access token")
		}
		identity, err := a.identity(ctx, token.AccessToken)
		if err != nil {
			return integrations.Grant{}, err
		}
		return integrations.Grant{Identity: identity, Credential: token.credential()}, nil
	}
}
func (a *Adapter) Refresh(ctx context.Context, previous integrations.Credential) (integrations.Credential, error) {
	values := url.Values{"client_id": {a.options.ClientID}, "grant_type": {"refresh_token"}, "refresh_token": {previous.RefreshToken}}
	var token tokenResponse
	if err := a.post(ctx, "https://github.com/login/oauth/access_token", values, &token); err != nil {
		return integrations.Credential{}, err
	}
	if token.Error != "" || token.AccessToken == "" || token.ExpiresIn < 0 || token.RefreshExpiresIn < 0 {
		return integrations.Credential{}, errors.New("GitHub credential refresh failed")
	}
	c := token.credential()
	if c.RefreshToken == "" {
		c.RefreshToken = previous.RefreshToken
		c.RefreshExpiresAt = previous.RefreshExpiresAt
	}
	return c, nil
}
func (a *Adapter) identity(ctx context.Context, token string) (integrations.Identity, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	if err != nil {
		return integrations.Identity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err = a.do(req, &user); err != nil {
		return integrations.Identity{}, err
	}
	if user.ID <= 0 || user.Login == "" {
		return integrations.Identity{}, errors.New("GitHub returned no account identity")
	}
	return integrations.Identity{ID: strconv.FormatInt(user.ID, 10), Username: user.Login, Name: user.Name}, nil
}
func (a *Adapter) post(ctx context.Context, endpoint string, values url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return errors.New("invalid GitHub authorization request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return a.do(req, out)
}
func (a *Adapter) do(req *http.Request, out any) error {
	req.Header.Set("User-Agent", "Pot-GitHub-Integration")
	response, err := a.client.Do(req)
	if err != nil {
		return errors.New("could not reach GitHub authorization service")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("GitHub authorization request failed")
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(out); err != nil {
		return errors.New("invalid GitHub authorization response")
	}
	return nil
}
