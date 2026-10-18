package core

import "fmt"

// Attachment is an immutable, Control-owned reference. It never contains a client path.
type Attachment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MIMEType string `json:"mime_type"`
	Size     uint64 `json:"size"`
	Width    uint32 `json:"width,omitempty"`
	Height   uint32 `json:"height,omitempty"`
}

const AttachmentBytes uint64 = 8 * 1024 * 1024
const AttachmentCount = 8

type AttachmentCapabilities struct {
	Image bool `json:"image"`
	PDF   bool `json:"pdf"`
}

func (c AttachmentCapabilities) Allows(mime string) bool {
	switch mime {
	case "image/png", "image/jpeg", "image/webp":
		return c.Image
	case "application/pdf":
		return c.PDF
	}
	return false
}
func (s ResolvedSpec) AttachmentCapabilities() AttachmentCapabilities {
	d := DefaultModelDescriptor()
	if s.Params.Agent.ModelCatalog != nil {
		var err error
		d, err = s.Params.Agent.ModelCatalog.Resolve(s.Provider, s.Model)
		if err != nil {
			return AttachmentCapabilities{}
		}
	}
	implemented := s.GatewayProtocol == "openai_chat" || s.GatewayProtocol == "openai_responses" || s.GatewayProtocol == "anthropic_messages" || s.GatewayProtocol == "gemini"
	return AttachmentCapabilities{Image: implemented && d.Vision(), PDF: implemented && d.PDF()}
}
func ValidateAttachments(items []Attachment) error {
	if len(items) > AttachmentCount {
		return fmt.Errorf("at most %d attachments", AttachmentCount)
	}
	seen := map[string]bool{}
	var size uint64
	for _, a := range items {
		if !Identifier(a.ID) || seen[a.ID] || a.Name == "" || len(a.Name) > 255 || a.Size == 0 || a.Size > AttachmentBytes || !(AttachmentCapabilities{Image: true, PDF: true}).Allows(a.MIMEType) {
			return fmt.Errorf("invalid attachment reference")
		}
		seen[a.ID] = true
		size += a.Size
	}
	if size > AttachmentBytes {
		return fmt.Errorf("attachments must total at most 8 MiB")
	}
	return nil
}
