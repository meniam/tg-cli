package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var updateDocs = flag.Bool("update-docs", false, "rewrite generated CLI documentation tables")

const docsUpdateCommand = "run: go test ./internal/cli -run TestDocsGenerated -update-docs"

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", `\|`)
	return strings.Join(strings.Fields(value), " ")
}

func flagTable(flags *pflag.FlagSet) string {
	var rows []string
	flags.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		defaultValue := f.DefValue
		if defaultValue == "" {
			defaultValue = "none"
		}
		rows = append(rows, fmt.Sprintf("| `--%s` | %s | `%s` | %s |",
			markdownCell(f.Name), markdownCell(f.Value.Type()), markdownCell(defaultValue), markdownCell(f.Usage)))
	})
	if len(rows) == 0 {
		return "No command flags."
	}
	return "| Flag | Type | Default | Description |\n| --- | --- | --- | --- |\n" + strings.Join(rows, "\n")
}

func exitCodeTable(root *cobra.Command, specs map[*cobra.Command]commandSpec) string {
	var rows []string
	for _, code := range schemaDocFor(root, specs).ExitCodes {
		rows = append(rows, fmt.Sprintf("| %d | `%s` | %s |",
			code.Code, markdownCell(code.Name), markdownCell(code.Description)))
	}
	return "| Code | Name | Meaning |\n| --- | --- | --- |\n" + strings.Join(rows, "\n")
}

func generatedBlock(content, open, close, table string) (string, error) {
	start := strings.Index(content, open)
	if start < 0 {
		return "", fmt.Errorf("missing %s", open)
	}
	if strings.Count(content, open) != 1 {
		return "", fmt.Errorf("duplicate marker %s", open)
	}
	end := strings.Index(content[start+len(open):], close)
	if end < 0 {
		return "", fmt.Errorf("missing %s after %s", close, open)
	}
	end += start + len(open)
	return content[:start+len(open)] + "\n" + table + "\n" + content[end:], nil
}

func TestDocsGenerated(t *testing.T) {
	dir := docsRoot(t)
	sections := readDocsSections(t, dir)
	a, root := annotatedApp()
	type block struct {
		command string
		open    string
		close   string
		table   string
	}
	blocks := map[string][]block{}
	walkCommands(root, func(cmd *cobra.Command, path string) {
		name := "tg " + path
		found := sections[name]
		if len(found) != 1 {
			t.Errorf("docs/cli/*.md: %s: expected one command section, found %d", name, len(found))
			return
		}
		entry := block{name, "<!-- schema:flags " + name + " -->", "<!-- /schema:flags -->", flagTable(cmd.LocalFlags())}
		if !strings.Contains(found[0].body, entry.open) || !strings.Contains(found[0].body, entry.close) {
			t.Errorf("%s: %s: missing flags block; %s", found[0].file, name, docsUpdateCommand)
			return
		}
		blocks[found[0].file] = append(blocks[found[0].file], entry)
	})
	blocks["docs/cli/overview.md"] = append(blocks["docs/cli/overview.md"],
		block{"global flags", "<!-- schema:global-flags -->", "<!-- /schema:global-flags -->", flagTable(root.PersistentFlags())},
		block{"exit codes", "<!-- schema:exit-codes -->", "<!-- /schema:exit-codes -->", exitCodeTable(root, a.specs)},
	)
	var files []string
	for file := range blocks {
		files = append(files, file)
	}
	slices.Sort(files)
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join(dir, file)
			old, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			current := string(old)
			expected := current
			valid := true
			for _, entry := range blocks[file] {
				next, err := generatedBlock(expected, entry.open, entry.close, entry.table)
				if err != nil {
					t.Errorf("%s: %s: %v; %s", file, entry.command, err, docsUpdateCommand)
					valid = false
					continue
				}
				expected = next
			}
			if !valid {
				return
			}
			if *updateDocs {
				if expected != current {
					info, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(expected), info.Mode().Perm()); err != nil {
						t.Fatal(err)
					}
				}
			} else if expected != current {
				t.Errorf("%s: generated tables differ; %s", file, docsUpdateCommand)
			}
		})
	}
}

func TestDocsGeneratedFormatting(t *testing.T) {
	flags := pflag.NewFlagSet("sample", pflag.ContinueOnError)
	flags.String("visible", "", "first | second\nline")
	flags.String("hidden", "", "internal")
	if err := flags.MarkHidden("hidden"); err != nil {
		t.Fatal(err)
	}
	want := "| Flag | Type | Default | Description |\n| --- | --- | --- | --- |\n| `--visible` | string | `none` | first \\| second line |"
	if got := flagTable(flags); got != want {
		t.Errorf("flag table = %q, want %q", got, want)
	}
	if got := flagTable(pflag.NewFlagSet("empty", pflag.ContinueOnError)); got != "No command flags." {
		t.Errorf("empty flag table = %q", got)
	}
}
