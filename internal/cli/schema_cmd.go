package cli

import (
	"reflect"
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"tg-cli/internal/buildinfo"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/schema"
)

// schemaVersion is the version of the document `tg-cli schema` prints. It grows
// when a field changes meaning, so an agent tells an old schema from a new one.
const schemaVersion = 1

// schemaDoc is the machine description of the command line.
type schemaDoc struct {
	SchemaVersion int           `json:"schema_version"`
	Tool          toolInfo      `json:"tool"`
	Output        outputDoc     `json:"output"`
	ExitCodes     []exitCodeDoc `json:"exit_codes"`
	GlobalFlags   []flagDoc     `json:"global_flags"`
	Commands      []commandDoc  `json:"commands"`
}

// toolInfo names the tool and the build it was made from.
type toolInfo struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Revision string `json:"revision,omitempty"`
}

// outputDoc lists the output formats and the envelope every command shares.
type outputDoc struct {
	// Formats are the accepted --format values. jsonl applies to the commands
	// that return a list, json to all of them.
	Formats []string `json:"formats"`
	// Success is the shape of a success envelope, Failure of a failure one.
	Success string `json:"success"`
	Failure string `json:"failure"`
}

// exitCodeDoc is one exit code with the machine name an agent matches on.
type exitCodeDoc struct {
	Code        int    `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// commandDoc describes one runnable command.
type commandDoc struct {
	// Path is the command as typed, like "chat list".
	Path string `json:"path"`
	// Summary is the one-line description, Description the long one.
	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`
	// Safety is "read", "local_write" or "account_write".
	Safety string `json:"safety"`
	// Dangerous is set by hand for a command that can lose data or change the
	// account beyond an ordinary write.
	Dangerous bool `json:"dangerous"`
	// Result is the Go type of data in the success envelope.
	Result string `json:"result"`
	// Fields are the paths of the result, for a command that returns an object.
	Fields []schema.Field `json:"fields,omitempty"`
	// Item is the list a JSONL line is taken from, for a command that prints
	// JSONL; its Fields are the paths inside one line.
	Item  *itemDoc  `json:"item,omitempty"`
	Args  []argDoc  `json:"args,omitempty"`
	Flags []flagDoc `json:"flags,omitempty"`
}

// itemDoc names the list inside a list result that --format jsonl prints.
type itemDoc struct {
	// Path is the json path of the list, like "chats".
	Path   string         `json:"path"`
	Fields []schema.Field `json:"fields"`
}

// argDoc describes one positional argument.
type argDoc struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Variadic bool   `json:"variadic,omitempty"`
}

// flagDoc describes one flag.
type flagDoc struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	// Type is the pflag type: string, int, int64, bool, stringSlice, stringArray.
	Type string `json:"type"`
	// Default is the value used when the flag is not given, in its text form.
	Default string `json:"default,omitempty"`
	// Values are the accepted values, when the command accepts a fixed list.
	Values []string `json:"values,omitempty"`
	// Repeatable is true for a flag that may be given more than once.
	Repeatable bool   `json:"repeatable,omitempty"`
	Usage      string `json:"usage"`
}

// schemaDocFor builds the description of the command tree. The order is fixed,
// so the same build always prints the same document.
func schemaDocFor(root *cobra.Command, specs map[*cobra.Command]commandSpec) schemaDoc {
	info := buildinfo.Get()
	doc := schemaDoc{
		SchemaVersion: schemaVersion,
		Tool:          toolInfo{Name: root.Name(), Version: info.Version, Revision: info.Revision},
		Output: outputDoc{
			Formats: outputFormats(),
			Success: `{"ok":true,"data":...}`,
			Failure: `{"ok":false,"error":{"code":"<name>","message":"..."}}`,
		},
		GlobalFlags: flagDocs(root.PersistentFlags(), nil),
	}
	for _, code := range exitcode.All() {
		doc.ExitCodes = append(doc.ExitCodes, exitCodeDoc{Code: code, Name: exitcode.Name(code), Description: exitcode.Note(code)})
	}
	walkCommands(root, func(cmd *cobra.Command, path string) {
		doc.Commands = append(doc.Commands, commandDocFor(cmd, path, specs[cmd]))
	})
	sort.Slice(doc.Commands, func(i, j int) bool { return doc.Commands[i].Path < doc.Commands[j].Path })
	return doc
}

func commandDocFor(cmd *cobra.Command, path string, spec commandSpec) commandDoc {
	doc := commandDoc{
		Path:        path,
		Summary:     cmd.Short,
		Safety:      string(spec.Safety),
		Dangerous:   spec.Risk == dangerous,
		Result:      resultName(spec.Result),
		Fields:      schema.Fields(spec.Result),
		Item:        itemDocFor(spec),
		Flags:       flagDocs(cmd.LocalFlags(), spec.Choices),
		Description: cmd.Long,
	}
	if doc.Description == cmd.Short {
		doc.Description = ""
	}
	for _, arg := range spec.Args {
		doc.Args = append(doc.Args, argDoc{Name: arg.Name, Required: arg.Required, Variadic: arg.Variadic})
	}
	return doc
}

// itemDocFor describes the JSONL line of a list command: the path of the list in
// the result and the fields of one element of it.
func itemDocFor(spec commandSpec) *itemDoc {
	t := spec.Result
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if spec.ItemPath == "" || t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	elem, ok := schema.FieldType(t, spec.ItemPath)
	if !ok || elem.Kind() != reflect.Slice {
		return nil
	}
	return &itemDoc{Path: spec.ItemPath, Fields: schema.Fields(elem.Elem())}
}

// resultName is the type of a result as written in Go, empty for an unknown type.
func resultName(t reflect.Type) string {
	if t == nil {
		return ""
	}
	return t.String()
}

// flagDocs describes a set of flags. When choices name a flag, its Values are
// the allowed ones.
func flagDocs(flags *pflag.FlagSet, choices []flagChoices) []flagDoc {
	allowed := map[string][]string{}
	for _, c := range choices {
		allowed[c.Flag] = c.Values
	}
	docs := []flagDoc{}
	flags.VisitAll(func(f *pflag.Flag) {
		// cobra adds -h/--help to every command when it runs; it is the same
		// for all of them and not part of a command's own contract.
		if f.Name == "help" && f.Shorthand == "h" {
			return
		}
		repeated := false
		if _, ok := f.Value.(pflag.SliceValue); ok {
			repeated = true
		}
		docs = append(docs, flagDoc{
			Name:       f.Name,
			Shorthand:  f.Shorthand,
			Type:       f.Value.Type(),
			Default:    f.DefValue,
			Values:     allowed[f.Name],
			Repeatable: repeated,
			Usage:      f.Usage,
		})
	})
	return docs
}

// versionCommand prints the build the binary was made from.
func (a *app) versionCommand() *cobra.Command {
	return a.annotate(&cobra.Command{
		Use:   "version",
		Short: "Print the version of the tool and the revision it was built from (read only, no daemon needed)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.success(buildinfo.Get())
		},
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[buildinfo.Info]()})
}

// schemaCommand prints the description of every command. It reads the command
// tree only: no daemon, no network, no database.
func (a *app) schemaCommand(root *cobra.Command) *cobra.Command {
	return a.annotate(&cobra.Command{
		Use:   "schema",
		Short: "Print the machine description of every command, argument, flag and exit code (read only, no daemon needed)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.success(schemaDocFor(root, a.specs))
		},
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[schemaDoc]()})
}
