package cli

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"tg-cli/internal/buildinfo"
	"tg-cli/internal/config"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/output"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

// schemaVersion is the version of the document `tg schema` prints. It grows
// when a field changes meaning, so an agent tells an old schema from a new one.
const schemaVersion = 3

// schemaDoc is the machine description of the command line.
type schemaDoc struct {
	SchemaVersion int             `json:"schema_version"`
	Config        []configKeyDoc  `json:"config"`
	Settings      []settingKeyDoc `json:"settings"`
	Tool          toolInfo        `json:"tool"`
	Output        outputDoc       `json:"output"`
	ExitCodes     []exitCodeDoc   `json:"exit_codes"`
	GlobalFlags   []flagDoc       `json:"global_flags"`
	Commands      []commandDoc    `json:"commands"`
	Subscription  subscriptionDoc `json:"subscription"`
}

// configKeyDoc describes one supported TOML key and its overrides.
type configKeyDoc struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Default     string `json:"default"`
	Environment string `json:"environment"`
	Flag        string `json:"flag"`
	// Description explains a key whose plain default does not say where its
	// value lands. It is absent for a key that needs no explanation.
	Description string `json:"description,omitempty"`
}

func configKeyDocs() []configKeyDoc {
	return []configKeyDoc{
		{"home", "string", "~/.config/tg-cli", "TG_CLI_HOME", "", "on macOS the data of the previous default, ~/Library/Application Support/tg-cli, moves here at the first command; on Linux with XDG_CONFIG_HOME set the default is tg-cli inside it; a home set here or in TG_CLI_HOME is never moved"},
		{"default_profile", "string", "default", "", "--profile", ""},
		{"files.database", "string", "store.db", "", "", "the file inside db/<profile>/ next to the configuration file"},
		{"files.session", "string", "session.json", "", "", ""},
		{"files.log", "string", "daemon.log", "", "", ""},
		{"files.deletions", "string", "deletions.jsonl", "", "", ""},
		{"files.access", "string", "access.json", "", "", ""},
	}
}

type settingKeyDoc struct {
	Key         string          `json:"key"`
	Type        string          `json:"type"`
	Default     json.RawMessage `json:"default"`
	Environment string          `json:"environment"`
	Flag        string          `json:"flag"`
	Apply       string          `json:"apply"`
	Protected   bool            `json:"protected"`
	Secret      bool            `json:"secret"`
}

func settingKeyDocs() []settingKeyDoc {
	keys := config.SettingKeys()
	out := make([]settingKeyDoc, 0, len(keys))
	for _, k := range keys {
		out = append(out, settingKeyDoc{k.Name, k.Type, k.Default, k.Env, k.Flag, k.Apply, k.Protected, k.Secret})
	}
	return out
}

// subscriptionDoc describes the raw RPC stream before a CLI watch command is
// added. Event is the shape of notification params, not a command result.
type subscriptionDoc struct {
	Method       string         `json:"method"`
	Result       []schema.Field `json:"result"`
	Notification string         `json:"notification"`
	Event        []schema.Field `json:"event"`
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
	Result         string             `json:"result"`
	ResultVariants []resultVariantDoc `json:"result_variants,omitempty"`
	// ChatResult and ChatFields describe the object returned with a chat argument.
	ChatResult string `json:"chat_result,omitempty"`
	// Formats are the --format values this command accepts: JSONL is left out
	// for a command that returns no list.
	Formats []string `json:"formats"`
	// Fields are the paths of the result, for a command that returns an object.
	Fields     []schema.Field `json:"fields,omitempty"`
	ChatFields []schema.Field `json:"chat_fields,omitempty"`
	// Item is the list a JSONL line is taken from, for a command that prints
	// JSONL; its Fields are the paths inside one line.
	Item               *itemDoc  `json:"item,omitempty"`
	PublicItem         *itemDoc  `json:"public_item,omitempty"`
	SubscriptionMethod string    `json:"subscription_method,omitempty"`
	Args               []argDoc  `json:"args,omitempty"`
	Flags              []flagDoc `json:"flags,omitempty"`
}

type resultVariantDoc struct {
	Flag   string         `json:"flag"`
	Result string         `json:"result"`
	Fields []schema.Field `json:"fields,omitempty"`
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
		Config:        configKeyDocs(),
		Settings:      settingKeyDocs(),
		Tool:          toolInfo{Name: root.Name(), Version: info.Version, Revision: info.Revision},
		Output: outputDoc{
			Formats: formatNames(output.Formats()),
			Success: `{"ok":true,"data":...,"throttled_ms":<milliseconds> (optional)}`,
			Failure: `{"ok":false,"error":{"code":"<name>","message":"..."},"throttled_ms":<milliseconds> (optional)}`,
		},
		GlobalFlags: flagDocs(root.PersistentFlags(), []flagChoices{{Flag: "format", Values: formatNames(output.Formats())}}),
		Subscription: subscriptionDoc{
			Method:       rpc.MethodSubscribe,
			Result:       schema.Fields(reflect.TypeOf(rpc.SubscribeResult{})),
			Notification: rpc.MethodEvent,
			Event:        schema.Fields(reflect.TypeOf(rpc.Event{})),
		},
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
		Path:               path,
		Summary:            cmd.Short,
		Safety:             string(spec.Safety),
		Dangerous:          spec.Risk == dangerous,
		Result:             resultName(spec.Result),
		ChatResult:         resultName(spec.ChatResult),
		Formats:            commandFormats(spec),
		Fields:             schema.Fields(spec.Result),
		ChatFields:         schema.Fields(spec.ChatResult),
		Item:               itemDocFor(spec),
		PublicItem:         itemDocFor(commandSpec{Result: spec.Result, ItemPath: spec.PublicItemPath}),
		Flags:              flagDocs(cmd.LocalFlags(), spec.Choices),
		Description:        cmd.Long,
		SubscriptionMethod: spec.SubscriptionMethod,
	}
	if doc.Description == cmd.Short {
		doc.Description = ""
	}
	for _, variant := range spec.ResultVariants {
		doc.ResultVariants = append(doc.ResultVariants, resultVariantDoc{Flag: variant.Flag, Result: resultName(variant.Result), Fields: schema.Fields(variant.Result)})
	}
	for _, arg := range spec.Args {
		doc.Args = append(doc.Args, argDoc{Name: arg.Name, Required: arg.Required, Variadic: arg.Variadic})
	}
	return doc
}

// commandFormats are the --format values a command accepts.
func commandFormats(spec commandSpec) []string {
	if len(spec.Formats) > 0 {
		return formatNames(spec.Formats)
	}
	if spec.ObjectJSONL {
		return formatNames(output.Formats())
	}
	if spec.ItemPath == "" {
		return formatNames(output.FormatsWithoutJSONL())
	}
	return formatNames(output.Formats())
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
		if f.Hidden || (f.Name == "help" && f.Shorthand == "h") {
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.success(cmd, buildinfo.Get())
		},
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[buildinfo.Info]()})
}

// schemaCommand prints the description of every command. It reads the command
// tree only: no daemon, no network, no database.
func (a *app) schemaCommand(root *cobra.Command) *cobra.Command {
	return a.annotate(&cobra.Command{
		Use:   "schema [command...]",
		Short: "Print the machine description of every command, argument, flag and exit code (read only, no daemon needed)",
		RunE: func(cmd *cobra.Command, args []string) error {
			doc := schemaDocFor(root, a.specs)
			if len(args) != 0 {
				path := strings.Join(args, " ")
				filtered := doc.Commands[:0]
				for _, item := range doc.Commands {
					if item.Path == path || strings.HasPrefix(item.Path, path+" ") {
						filtered = append(filtered, item)
					}
				}
				if len(filtered) == 0 {
					return fail(exitcode.Usage, "unknown command path %q", path)
				}
				doc.Commands = filtered
			}
			return a.success(cmd, doc)
		},
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[schemaDoc](), Args: []argSpec{{Name: "command", Variadic: true}}})
}
