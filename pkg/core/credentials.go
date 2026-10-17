package core

import (
	"errors"
	"strings"
)

// CredentialRef names the single connected account for an external service.
type CredentialRef struct {
	Service string `json:"service"`
}

func (r CredentialRef) Validate() error {
	if !Identifier(r.Service) || len(r.Service) > 64 {
		return errors.New("invalid credential service")
	}
	return nil
}

type CredentialBindings map[string]CredentialRef

func (b CredentialBindings) Validate() error {
	if len(b) > 16 {
		return errors.New("too many credential bindings")
	}
	for name, ref := range b {
		if !CredentialEnvironmentName(name) {
			return errors.New("invalid credential environment variable")
		}
		if err := ref.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func CredentialEnvironmentName(name string) bool {
	if name == "" || len(name) > 128 || strings.ContainsRune(name, '=') {
		return false
	}
	for i, c := range name {
		if !(c == '_' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
