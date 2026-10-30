package sandbox

import (
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestDefaultExecutablePathUsesPlatformDirectories(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			paths := strings.Split(defaultExecutablePath(goos), ":")
			for _, directory := range []string{"/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"} {
				if !slices.Contains(paths, directory) {
					t.Errorf("missing system executable directory %s", directory)
				}
			}
			if slices.Contains(paths, "/opt/homebrew/bin") != (goos == "darwin") {
				t.Fatal("Homebrew executable directory must be macOS-only")
			}
			if slices.Contains(paths, "") || slices.Contains(paths, ".") {
				t.Fatal("default PATH must not search the working directory")
			}
		})
	}
}

func TestEnvironmentPathFallbackAndOverride(t *testing.T) {
	for _, configured := range []string{"", "/custom/toolchain/bin:/usr/bin"} {
		t.Setenv("PATH", configured)
		want := configured
		if want == "" {
			want = defaultExecutablePath(runtime.GOOS)
		}
		if !slices.Contains(Environment(), "PATH="+want) {
			t.Fatalf("PATH=%q did not produce expected PATH %q", configured, want)
		}
	}
}

func TestEnvironmentInheritsHomeWithoutToolchainOverrides(t *testing.T) {
	t.Setenv("HOME", "/account/home")
	t.Setenv("POT_TEST_AUTH_SECRET", "private")
	env := Environment()
	if !slices.Contains(env, "HOME=/account/home") {
		t.Fatal("account HOME was not inherited", env)
	}
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if slices.Contains([]string{"GOCACHE", "GOPATH", "CARGO_HOME", "RUSTUP_HOME", "npm_config_cache", "XDG_CACHE_HOME", "POT_TEST_AUTH_SECRET"}, key) {
			t.Fatalf("unexpected toolchain override or ambient secret: %s", key)
		}
	}
}
