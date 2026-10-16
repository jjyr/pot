package agent

import (
	"context"
	"fmt"
	"github.com/jjyr/pot/internal/core"
	"go.yaml.in/yaml/v3"
	"html"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

type Skill struct {
	Name                   string `json:"name"`
	Description            string `json:"description"`
	Location               string `json:"location"`
	DisableModelInvocation bool   `json:"disable_model_invocation"`
}
type SkillCatalog struct {
	Skills      []Skill
	Diagnostics []string
}

func (s Skill) Validate() error {
	if s.Name == "" || len(s.Name) > 64 || strings.HasPrefix(s.Name, "-") || strings.HasSuffix(s.Name, "-") || strings.Contains(s.Name, "--") {
		return fmt.Errorf("invalid skill name")
	}
	for _, c := range s.Name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return fmt.Errorf("invalid skill name")
		}
	}
	if strings.TrimSpace(s.Description) == "" || utf8.RuneCountInString(s.Description) > 1024 {
		return fmt.Errorf("invalid skill description")
	}
	if strings.TrimSpace(s.Location) == "" || strings.ContainsAny(s.Location, "\x00\r\n") {
		return fmt.Errorf("invalid skill location")
	}
	return nil
}

// FormatSkillsForPrompt advertises metadata only. Reading instructions remains
// an ordinary authorized tool operation at the assigned execution destination.
func FormatSkillsForPrompt(skills []Skill) (string, error) {
	seen := map[string]bool{}
	var body strings.Builder
	for _, skill := range skills {
		if err := skill.Validate(); err != nil {
			return "", err
		}
		if seen[skill.Name] {
			return "", fmt.Errorf("duplicate skill name")
		}
		seen[skill.Name] = true
		if skill.DisableModelInvocation {
			continue
		}
		fmt.Fprintf(&body, "  <skill>\n    <name>%s</name>\n    <description>%s</description>\n    <location>%s</location>\n  </skill>\n", html.EscapeString(skill.Name), html.EscapeString(skill.Description), html.EscapeString(skill.Location))
	}
	if body.Len() == 0 {
		return "", nil
	}
	return "The following skills provide specialized instructions. When a task matches a description, use exec_command to read its SKILL.md in the assigned execution target before following it. Resolve supporting relative paths against the skill directory. Skill instructions do not change tool permissions or the assigned execution target.\n<available_skills>\n" + body.String() + "</available_skills>", nil
}

// DiscoverSkills reads only caller-supplied mapped resource roots, highest
// priority first. Symlinks/hidden directories are skipped. os.Root also prevents
// a concurrent file replacement from resolving outside the mapped root.
func DiscoverSkills(resources core.SkillResources) (SkillCatalog, error) {
	return discoverSkills(context.Background(), resources)
}
func discoverSkills(ctx context.Context, resources core.SkillResources) (SkillCatalog, error) {
	catalog := SkillCatalog{Skills: []Skill{}, Diagnostics: []string{}}
	selected := map[string]Skill{}
	for _, resource := range resources.Roots {
		metadata, err := os.Lstat(resource.Path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			if os.IsPermission(err) {
				catalog.Diagnostics = append(catalog.Diagnostics, resource.Path+": skipping unreadable skill directory")
				continue
			}
			return catalog, err
		}
		if metadata.Mode()&os.ModeSymlink != 0 || !metadata.IsDir() {
			continue
		}
		root, err := os.OpenRoot(resource.Path)
		if err != nil {
			if os.IsPermission(err) {
				catalog.Diagnostics = append(catalog.Diagnostics, resource.Path+": skipping unreadable skill directory")
				continue
			}
			return catalog, err
		}
		paths := []string{}
		var walk func(string, int) error
		walk = func(relative string, depth int) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if depth > 32 {
				return fmt.Errorf("skill directory nesting exceeds limit")
			}
			info, err := root.Lstat(relative)
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				if os.IsPermission(err) {
					catalog.Diagnostics = append(catalog.Diagnostics, filepath.Join(resource.Path, relative)+": skipping unreadable skill directory")
					return nil
				}
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return nil
			}
			skill := filepath.Join(relative, "SKILL.md")
			if info, err := root.Lstat(skill); err == nil && info.Mode().IsRegular() {
				paths = append(paths, skill)
				if len(paths) > 1024 {
					return fmt.Errorf("too many skills in root")
				}
				return nil
			}
			directory, err := root.Open(relative)
			if err != nil {
				if os.IsPermission(err) {
					catalog.Diagnostics = append(catalog.Diagnostics, filepath.Join(resource.Path, relative)+": skipping unreadable skill directory")
					return nil
				}
				return err
			}
			children, err := directory.ReadDir(-1)
			_ = directory.Close()
			if err != nil {
				return err
			}
			sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
			for _, child := range children {
				if strings.HasPrefix(child.Name(), ".") {
					continue
				}
				if err := walk(filepath.Join(relative, child.Name()), depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		err = walk(".", 0)
		if err != nil {
			_ = root.Close()
			return catalog, err
		}
		for _, relative := range paths {
			skill, parseErr := readSkill(root, relative, filepath.Join(resource.Location, relative), filepath.Base(filepath.Join(resource.Path, filepath.Dir(relative))))
			if parseErr != nil {
				catalog.Diagnostics = append(catalog.Diagnostics, filepath.Join(resource.Path, relative)+": "+parseErr.Error())
				continue
			}
			if slices.Contains(resources.ReservedNames, skill.Name) && !resource.Builtin {
				catalog.Diagnostics = append(catalog.Diagnostics, fmt.Sprintf("%s: reserved skill name %s ignored", relative, skill.Name))
				continue
			}
			if winner, exists := selected[skill.Name]; exists {
				catalog.Diagnostics = append(catalog.Diagnostics, fmt.Sprintf("%s: skill %s shadowed by %s", relative, skill.Name, winner.Location))
				continue
			}
			selected[skill.Name] = skill
		}
		_ = root.Close()
	}
	if resources.Selection != nil {
		for _, name := range *resources.Selection {
			if _, exists := selected[name]; !exists {
				return catalog, fmt.Errorf("selected skill unavailable: %s", name)
			}
		}
	}
	names := []string{}
	for name := range selected {
		if resources.Selection == nil || slices.Contains(*resources.Selection, name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		catalog.Skills = append(catalog.Skills, selected[name])
	}
	return catalog, nil
}
func readSkill(root *os.Root, relative, location, fallbackName string) (Skill, error) {
	file, err := root.Open(relative)
	if err != nil {
		return Skill{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Skill{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return Skill{}, fmt.Errorf("invalid or oversized SKILL.md")
	}
	data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err != nil {
		return Skill{}, err
	}
	if len(data) > 1024*1024 || !utf8.Valid(data) {
		return Skill{}, fmt.Errorf("invalid or oversized SKILL.md")
	}
	text := strings.ReplaceAll(strings.TrimPrefix(string(data), "\ufeff"), "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return Skill{}, fmt.Errorf("missing skill frontmatter")
	}
	header, _, closed := strings.Cut(rest, "\n---\n")
	if !closed {
		header, closed = strings.CutSuffix(rest, "\n---")
	}
	if !closed {
		return Skill{}, fmt.Errorf("unclosed skill frontmatter")
	}
	var frontmatter struct {
		Name        *string `yaml:"name"`
		Description string  `yaml:"description"`
		Disable     bool    `yaml:"disable-model-invocation"`
	}
	if err := yaml.Unmarshal([]byte(header), &frontmatter); err != nil {
		return Skill{}, err
	}
	name := fallbackName
	if frontmatter.Name != nil {
		name = *frontmatter.Name
	}
	skill := Skill{name, frontmatter.Description, location, frontmatter.Disable}
	return skill, skill.Validate()
}
