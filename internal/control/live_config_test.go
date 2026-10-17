package control

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type liveModelConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

// go test changes cwd to the package directory. Find this checkout's .env
// without consulting the user's home or any other project's configuration.
func liveEnvironmentPath() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !info.IsDir() {
			return filepath.Join(dir, ".env"), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("live tests require a repository-local .env")
		}
		dir = parent
	}
}

// Read literal dotenv assignments without exporting secrets into the process
// environment (and therefore child commands). Never evaluate shell expressions.
func readLiveModelConfig(path string) (liveModelConfig, error) {
	config := liveModelConfig{BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-flash"}
	file, err := os.Open(path)
	if err != nil {
		return config, fmt.Errorf("read live .env: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		name, value, ok := strings.Cut(strings.TrimPrefix(text, "export "), "=")
		if !ok {
			return config, fmt.Errorf("invalid live .env assignment at line %d", line)
		}
		name = strings.TrimSpace(name)
		if name != "DEEPSEEK_API_KEY" && name != "DEEPSEEK_BASE_URL" && name != "DEEPSEEK_MODEL" {
			continue
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "\"") {
			value, err = strconv.Unquote(value)
			if err != nil {
				return config, fmt.Errorf("invalid live .env quoting at line %d", line)
			}
		} else if strings.HasPrefix(value, "'") {
			if len(value) < 2 || !strings.HasSuffix(value, "'") {
				return config, fmt.Errorf("invalid live .env quoting at line %d", line)
			}
			value = value[1 : len(value)-1]
		}
		if strings.ContainsAny(value, "\r\n") {
			return config, fmt.Errorf("live .env values must be single-line literals (line %d)", line)
		}
		switch name {
		case "DEEPSEEK_API_KEY":
			config.APIKey = value
		case "DEEPSEEK_BASE_URL":
			config.BaseURL = value
		case "DEEPSEEK_MODEL":
			config.Model = value
		}
	}
	if scanner.Err() != nil {
		return config, fmt.Errorf("cannot scan live .env")
	}
	u, err := url.Parse(config.BaseURL)
	if config.APIKey == "" || config.Model == "" || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return config, fmt.Errorf("live .env requires DEEPSEEK_API_KEY, a model and an HTTPS base URL without credentials/query/fragment")
	}
	return config, nil
}

func TestLiveModelConfig(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "ambient-value-unchanged")
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"defaults", "DEEPSEEK_API_KEY=fake-test-key\n", true},
		{"quoted", "# local settings\nexport DEEPSEEK_API_KEY='fake-test-key'\nDEEPSEEK_MODEL=\"deepseek-v4-flash\"\nDEEPSEEK_BASE_URL=https://api.deepseek.com\n", true},
		{"literal", "DEEPSEEK_API_KEY='$(never-execute)'\n", true},
		{"missing-key", "DEEPSEEK_MODEL=deepseek-v4-flash\n", false},
		{"invalid-quote", "DEEPSEEK_API_KEY=\"fake-test-key\n", false},
		{"invalid-assignment", "fake-test-key\n", false},
		{"insecure-endpoint", "DEEPSEEK_API_KEY=fake-test-key\nDEEPSEEK_BASE_URL=http://example.com\n", false},
		{"credential-endpoint", "DEEPSEEK_API_KEY=fake-test-key\nDEEPSEEK_BASE_URL=https://fake-test-key@example.com\n", false},
		{"multiline", "DEEPSEEK_API_KEY=\"fake-test-key\\nsecond-line\"\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			config, err := readLiveModelConfig(path)
			if (err == nil) != tc.valid {
				t.Fatal("unexpected live configuration validity")
			}
			if err != nil && strings.Contains(err.Error(), "fake-test-key") {
				t.Fatal("configuration error exposed the credential")
			}
			if tc.name == "literal" && config.APIKey != "$(never-execute)" {
				t.Fatal("dotenv value was expanded")
			}
			if tc.valid && (config.BaseURL != "https://api.deepseek.com" || config.Model != "deepseek-v4-flash") {
				t.Fatal("unexpected model defaults")
			}
			if os.Getenv("DEEPSEEK_API_KEY") != "ambient-value-unchanged" {
				t.Fatal("dotenv credential leaked into the process environment")
			}
		})
	}
}
