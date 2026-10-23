package assistant

import (
	"errors"
)

// ProfileUpdate changes only supplied fields. Avatar is a normalized PNG data URL;
// an empty value restores the assigned kitchen character.
type ProfileUpdate struct {
	Name   *string `json:"name,omitempty"`
	Avatar *string `json:"avatar,omitempty"`
}

func (p ProfileUpdate) Validate() error {
	if p.Name == nil && p.Avatar == nil {
		return errors.New("profile update requires at least one field")
	}
	if p.Name != nil {
		if err := ValidateName(*p.Name); err != nil {
			return err
		}
	}
	if p.Avatar != nil && *p.Avatar != "" {
		return ValidateAvatar(*p.Avatar)
	}
	return nil
}
