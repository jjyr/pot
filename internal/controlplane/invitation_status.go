package controlplane

import (
	"context"
	"fmt"
	"time"
)

type InvitationStatus struct {
	State     string `json:"state"`
	RuntimeID string `json:"runtime_id,omitempty"`
}

func (d *Directory) invitationStatus(id string) InvitationStatus {
	i, ok := d.state.Invites[id]
	if !ok {
		return InvitationStatus{State: "revoked"}
	}
	if i.Request != nil && i.Enrollment != nil {
		return InvitationStatus{State: "joined", RuntimeID: i.Enrollment.RuntimeID}
	}
	if i.Expires <= time.Now().UnixMilli() {
		return InvitationStatus{State: "expired"}
	}
	return InvitationStatus{State: "pending"}
}
func (d *Directory) InvitationStatus(_ context.Context, id string) (InvitationStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.options.Embedded || d.state.Role != "control" || len(id) != 64 {
		return InvitationStatus{}, ErrUnauthorized
	}
	return d.invitationStatus(id), nil
}
func (d *Directory) RevokeInvitation(_ context.Context, id string) (InvitationStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.options.Embedded || d.state.Role != "control" || len(id) != 64 {
		return InvitationStatus{}, ErrUnauthorized
	}
	state := d.invitationStatus(id)
	if state.State == "joined" {
		return state, nil
	}
	err := d.update(func(b *book) error { delete(b.Invites, id); return nil })
	return InvitationStatus{State: "revoked"}, err
}
func (d *Directory) ReplaceInvitation(_ context.Context, id string, ttl time.Duration) (*Invitation, InvitationStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.options.Embedded || d.state.Role != "control" || len(id) != 64 {
		return nil, InvitationStatus{}, ErrUnauthorized
	}
	if ttl <= 0 || ttl > 24*time.Hour {
		return nil, InvitationStatus{}, fmt.Errorf("invalid invitation lifetime")
	}
	state := d.invitationStatus(id)
	if state.State == "joined" {
		return nil, state, nil
	}
	i := Invitation{Code: secret(), ExpiresAtMS: time.Now().Add(ttl).UnixMilli(), Identity: d.identity}
	err := d.update(func(b *book) error {
		delete(b.Invites, id)
		if len(b.Invites) >= 1024 {
			return fmt.Errorf("invitation capacity reached")
		}
		b.Invites[tokenHash(i.Code)] = invitation{Expires: i.ExpiresAtMS}
		return nil
	})
	return &i, InvitationStatus{State: "pending"}, err
}

// InvitationID is a non-authorizing receipt for owner-side status and revocation.
func InvitationID(code string) string { return tokenHash(code) }
