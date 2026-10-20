package controlplane

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestInvitationStatusReplacementAndCancellationPreserveMembership(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "machines.sqlite")
	d, err := Open(path, Options{RuntimeID: "control"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { d.Close() }()
	if err = d.EnableControl(); err != nil {
		t.Fatal(err)
	}
	inv, err := d.Invitation(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	id := InvitationID(inv.Code)
	state, err := d.InvitationStatus(ctx, id)
	if err != nil || state.State != "pending" {
		t.Fatal(state, err)
	}
	replacement, state, err := d.ReplaceInvitation(ctx, id, time.Hour)
	if err != nil || replacement == nil || state.State != "pending" {
		t.Fatal(state, err)
	}
	if state, _ = d.InvitationStatus(ctx, id); state.State != "revoked" {
		t.Fatal(state)
	}
	worker := directory(t, "runner")
	signed, err := worker.prepareJoin("tcp://localhost:12345", d.Identity(), replacement.Code)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Join(ctx, signed); err != nil {
		t.Fatal(err)
	}
	id = InvitationID(replacement.Code)
	state, err = d.RevokeInvitation(ctx, id)
	if err != nil || state.State != "joined" || state.RuntimeID != "runner" {
		t.Fatal(state, err)
	}
	replacement, state, err = d.ReplaceInvitation(ctx, id, time.Hour)
	if err != nil || replacement != nil || state.State != "joined" {
		t.Fatal(state, err)
	}
	d.Close()
	d, err = Open(path, Options{RuntimeID: "control"})
	if err != nil {
		t.Fatal(err)
	}
	state, err = d.InvitationStatus(ctx, id)
	if err != nil || state.State != "joined" {
		t.Fatal(state, err)
	}
	members, err := d.Machines(ctx)
	if err != nil || len(members) != 1 || members[0].Identity.RuntimeID != "runner" {
		t.Fatal(members, err)
	}
}

func TestInvitationCancellationFencesUnusedCode(t *testing.T) {
	ctx := context.Background()
	d := directory(t, "control")
	d.EnableControl()
	inv, err := d.Invitation(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := d.RevokeInvitation(ctx, InvitationID(inv.Code)); err != nil || state.State != "revoked" {
		t.Fatal(state, err)
	}
	worker := directory(t, "runner")
	signed, err := worker.prepareJoin("tcp://localhost:12345", d.Identity(), inv.Code)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Join(ctx, signed); err == nil {
		t.Fatal("cancelled invitation enrolled Runner")
	}
}
