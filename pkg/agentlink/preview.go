package agentlink

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/jjyr/pot/pkg/peer"
)

type previewRequest struct {
	Invitation string   `json:"invitation"`
	Code       string   `json:"code,omitempty"`
	Target     Identity `json:"target"`
}

// Preview proves the endpoint possesses the invitation's host key and reads its
// current target identity without claiming the invitation or creating a link.
func (s *Service) Preview(ctx context.Context, raw string) (Preview, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	address, err := parseAddress(ctx, raw)
	if err != nil {
		return Preview{}, err
	}
	if address.Host.NodeID == s.options.HostIdentity.NodeID {
		return Preview{}, ErrInvalid
	}
	// Preview admission carries no capability. Reveal the code only through
	// the encrypted session after TLS has verified the pinned server key.
	auth := sessionAuth{Preview: &previewRequest{Invitation: address.Invitation, Target: address.Identity}}
	data, _ := json.Marshal(auth)
	session, err := peer.Dial(ctx, address.Endpoint, peer.DialOptions{Identity: s.options.HostIdentity, PrivateKey: s.options.HostKey, Expected: &address.Host, Service: ServiceName, Auth: data, Context: ctx})
	if err != nil {
		return Preview{}, transportError(err)
	}
	defer session.Close()
	params, _ := json.Marshal(struct {
		Code string `json:"code"`
	}{Code: address.Code})
	response, err := session.Call(ctx, peer.Request{ID: newID(), Method: "preview", Params: params})
	if err != nil {
		return Preview{}, transportError(err)
	}
	var result Preview
	if len(response) > 8192 || decodeJSON(response, &result) != nil || !validNodeIdentity(result.Identity) || !equalIdentity(result.Identity, address.Identity) || result.Host != address.Host || result.Host != session.Remote() || result.Endpoint != address.Endpoint || result.Fingerprint != hostFingerprint(address.Host) {
		return Preview{}, ErrConflict
	}
	if !result.ExpiresAt.After(time.Now()) {
		return Preview{}, ErrExpired
	}
	return result, nil
}

func hostFingerprint(identity peer.Identity) string {
	key, _ := base64.RawURLEncoding.DecodeString(identity.PublicKey)
	hash := sha256.Sum256(key)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(hash[:])
}

func (s *Service) previewInvitation(ctx context.Context, request previewRequest) (Preview, error) {
	if !validID(request.Invitation) || !validToken(request.Code) || !validNodeIdentity(request.Target) || request.Target.Address.NodeID != s.options.HostIdentity.NodeID {
		return Preview{}, ErrInvalid
	}
	var raw []byte
	var endpoint, ref, claim string
	var expiry int64
	if err := s.options.DB.QueryRowContext(ctx, `SELECT identity,endpoint,expires_at,secret_ref,claim_hash FROM agent_link_invitations WHERE id=?`, request.Invitation).Scan(&raw, &endpoint, &expiry, &ref, &claim); err != nil {
		return Preview{}, ErrUnauthorized
	}
	var identity Identity
	if decodeJSON(raw, &identity) != nil || !equalIdentity(identity, request.Target) {
		return Preview{}, ErrConflict
	}
	secret, err := s.options.Secrets.Get(ctx, ref)
	if err != nil {
		return Preview{}, err
	}
	defer clear(secret)
	if subtle.ConstantTimeCompare(secret, []byte(request.Code)) != 1 {
		return Preview{}, ErrUnauthorized
	}
	if claim != "" {
		return Preview{}, ErrUsed
	}
	if expiry <= time.Now().UnixMilli() {
		return Preview{}, ErrExpired
	}
	current, err := s.localIdentity(ctx)
	if err != nil || current.Address.NodeID != s.options.HostIdentity.NodeID {
		return Preview{}, ErrConflict
	}
	return Preview{Identity: current, Host: s.options.HostIdentity, Endpoint: endpoint, Fingerprint: hostFingerprint(s.options.HostIdentity), ExpiresAt: time.UnixMilli(expiry).UTC()}, nil
}

func (s *Service) previewHandler(request previewRequest) peer.Handler {
	return func(ctx context.Context, input peer.Request) (json.RawMessage, error) {
		if input.Method != "preview" {
			return nil, ErrUnauthorized
		}
		var params struct {
			Code string `json:"code"`
		}
		if len(input.Params) > 128 || decodeJSON(input.Params, &params) != nil || !validToken(params.Code) {
			return nil, ErrInvalid
		}
		if !s.admit("preview:" + request.Invitation) {
			return nil, ErrUnavailable
		}
		defer s.release("preview:" + request.Invitation)
		value := request
		value.Code = params.Code
		result, err := s.previewInvitation(ctx, value)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
}
