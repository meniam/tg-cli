package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	docsHeading  = regexp.MustCompile(`(?m)^(#{1,6})[ \t]+([^\r\n]+?)[ \t]*#*[ \t]*$`)
	markdownLink = regexp.MustCompile(`\[[^]\n]*\]\(([^)\s]+)(?:\s+[^)]*)?\)`)
)

type docsSection struct {
	file  string
	level int
	body  string
}

func docsRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate docs test source")
	}
	for dir := filepath.Dir(source); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if filepath.Dir(dir) == dir {
			t.Fatal("cannot find repository root from docs test source")
		}
	}
}

// stripMarkdownCode preserves offsets while excluding fenced and inline code.
func stripMarkdownCode(source string) string {
	lines := strings.SplitAfter(source, "\n")
	var out strings.Builder
	fence := 0
	inline := 0
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		marker := 0
		for marker < len(trimmed) && trimmed[marker] == '`' {
			marker++
		}
		if marker >= 3 && (fence == 0 || marker >= fence) {
			if fence == 0 {
				fence = marker
			} else {
				fence = 0
			}
			out.WriteString(strings.Repeat(" ", len(line)))
			continue
		}
		if fence != 0 {
			out.WriteString(strings.Repeat(" ", len(line)))
			continue
		}
		for i := 0; i < len(line); {
			if line[i] == '`' {
				j := i + 1
				for j < len(line) && line[j] == '`' {
					j++
				}
				count := j - i
				if inline == 0 {
					inline = count
				} else if count == inline {
					inline = 0
				}
				out.WriteString(strings.Repeat(" ", count))
				i = j
				continue
			}
			if inline != 0 {
				out.WriteByte(' ')
			} else {
				out.WriteByte(line[i])
			}
			i++
		}
	}
	return out.String()
}

func readDocsSections(t *testing.T, root string) map[string][]docsSection {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, "docs", "cli", "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("docs/cli/*.md: no documentation files: %v", err)
	}
	sections := make(map[string][]docsSection)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		clean := stripMarkdownCode(string(data))
		headings := docsHeading.FindAllStringSubmatchIndex(clean, -1)
		for i, match := range headings {
			level := match[3] - match[2]
			name := strings.TrimSpace(clean[match[4]:match[5]])
			if !strings.HasPrefix(name, "tg ") {
				continue
			}
			end := len(clean)
			for _, next := range headings[i+1:] {
				if next[3]-next[2] <= level {
					end = next[0]
					break
				}
			}
			sections[name] = append(sections[name], docsSection{
				file:  filepath.ToSlash(strings.TrimPrefix(file, root+string(filepath.Separator))),
				level: level, body: string(data[match[1]:end]),
			})
		}
	}
	return sections
}

func TestDocsCommandsAndFlags(t *testing.T) {
	rootDir := docsRoot(t)
	sections := readDocsSections(t, rootDir)
	_, root := annotatedApp()
	commands := make(map[string]*cobra.Command)
	walkCommands(root, func(cmd *cobra.Command, path string) { commands["tg "+path] = cmd })

	t.Run("command sections", func(t *testing.T) {
		var missing []string
		for name := range commands {
			if len(sections[name]) == 0 || (sections[name][0].level != 2 && sections[name][0].level != 3) {
				missing = append(missing, name)
			}
		}
		slices.Sort(missing)
		if len(missing) > 0 {
			t.Errorf("docs/cli/*.md: missing command sections: %s", strings.Join(missing, ", "))
		}
		for name, found := range sections {
			if _, ok := commands[name]; !ok {
				for _, section := range found {
					t.Errorf("%s: obsolete command section %s", section.file, name)
				}
			} else if len(found) > 1 {
				t.Errorf("%s: duplicate command section %s", found[1].file, name)
			} else if found[0].level != 2 && found[0].level != 3 {
				t.Errorf("%s: %s: command heading must be level 2 or 3", found[0].file, name)
			}
		}
	})

	t.Run("global flags", func(t *testing.T) {
		file := filepath.Join(rootDir, "docs", "cli", "overview.md")
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		body := string(data)
		root.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
			if flag.Name != "help" && !flag.Hidden && !strings.Contains(body, "`--"+flag.Name+"`") {
				t.Errorf("docs/cli/overview.md: missing global flag --%s", flag.Name)
			}
		})
	})

	t.Run("command flags", func(t *testing.T) {
		for name, cmd := range commands {
			found := sections[name]
			if len(found) == 0 {
				continue
			}
			section := found[0]
			cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
				// Hidden flags are internal and are not part of the documented contract.
				if flag.Name != "help" && !flag.Hidden && !strings.Contains(section.body, "`--"+flag.Name+"`") {
					t.Errorf("%s: %s: missing flag --%s", section.file, name, flag.Name)
				}
			})
		}
	})
}

func TestDocsRelativeLinks(t *testing.T) {
	root := docsRoot(t)
	files := []string{"AGENTS.md", "README.md", "INTENT.md"}
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			files = append(files, strings.TrimPrefix(path, root+string(filepath.Separator)))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range files {
		t.Run(relative, func(t *testing.T) {
			file := filepath.Join(root, relative)
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			for _, match := range markdownLink.FindAllStringSubmatch(stripMarkdownCode(string(data)), -1) {
				target := match[1]
				if target == "" || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "tg:") {
					continue
				}
				path, _, _ := strings.Cut(target, "#")
				if _, err := os.Stat(filepath.Join(filepath.Dir(file), path)); err != nil {
					t.Errorf("%s: link %q: %v", relative, target, err)
				}
			}
		})
	}
}
