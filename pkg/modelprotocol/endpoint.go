package modelprotocol

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// EndpointResolution describes an address without contacting a provider.
// Unknown paths are complete custom endpoints, not guessed base paths.
type EndpointResolution struct {
	Endpoint         string   `json:"endpoint"`
	ModelsEndpoint   string   `json:"models_endpoint,omitempty"`
	DetectedProtocol Protocol `json:"detected_protocol,omitempty"`
}

var versionPath = regexp.MustCompile(`^v[0-9]+(?:(?:alpha|beta)[0-9]*)?$`)

func ResolveEndpoint(address string, protocol Protocol) (EndpointResolution, error) {
	var result EndpointResolution
	if err := protocol.Validate(); err != nil || protocol == Unknown {
		return result, errors.New("unsupported provider protocol")
	}
	u, err := url.Parse(strings.TrimSpace(address))
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return result, errors.New("invalid provider endpoint")
	}
	for name := range u.Query() {
		if strings.Contains(strings.ToLower(name), "key") || strings.Contains(strings.ToLower(name), "token") {
			return result, errors.New("provider credentials must use the API key field")
		}
	}
	path := strings.TrimRight(u.Path, "/")
	originalPath, escapedPath := path, strings.TrimRight(u.EscapedPath(), "/")
	base := ""
	for p, suffix := range map[Protocol]string{OpenAIChat: "/chat/completions", OpenAIResponses: "/responses", AnthropicMessages: "/messages"} {
		if strings.HasSuffix(path, suffix) {
			result.DetectedProtocol = p
			base = strings.TrimSuffix(path, suffix)
			break
		}
	}
	if prefix, tail, ok := strings.Cut(path, "/models/"); ok && strings.HasSuffix(tail, ":streamGenerateContent") {
		result.DetectedProtocol, base = Gemini, prefix
	}
	if result.DetectedProtocol == "" && (path == "" || versionPath.MatchString(path[strings.LastIndex(path, "/")+1:])) {
		if path == "" {
			path = "/v1"
			if protocol == Gemini {
				path = "/v1beta"
			}
		}
		base = path
		path += map[Protocol]string{OpenAIChat: "/chat/completions", OpenAIResponses: "/responses", AnthropicMessages: "/messages", Gemini: "/models/{model}:streamGenerateContent"}[protocol]
		result.DetectedProtocol = protocol
	}
	// Preserve complete custom paths (including trailing slash and query).
	if result.DetectedProtocol != "" {
		suffix := &url.URL{Path: strings.TrimPrefix(path, originalPath)}
		u.Path, u.RawPath = path, escapedPath+suffix.EscapedPath()
	}
	result.Endpoint = strings.ReplaceAll(u.String(), "%7Bmodel%7D", "{model}")
	if result.DetectedProtocol != "" && u.RawQuery == "" {
		suffix := &url.URL{Path: strings.TrimPrefix(path, base)}
		escapedBase := strings.TrimSuffix(u.EscapedPath(), suffix.EscapedPath())
		u.Path, u.RawPath = base+"/models", escapedBase+"/models"
		result.ModelsEndpoint = u.String()
	}
	return result, nil
}

// BindEndpointModel substitutes only the Gemini path placeholder. Credentials
// and query values never participate in model interpolation.
func BindEndpointModel(endpoint, model string) string {
	u, err := url.Parse(endpoint)
	if err != nil || !strings.Contains(u.Path, "{model}") {
		return endpoint
	}
	u.RawPath = strings.ReplaceAll(u.EscapedPath(), "%7Bmodel%7D", url.PathEscape(model))
	u.Path, _ = url.PathUnescape(u.RawPath)
	return u.String()
}
