package cli

import (
	"reflect"
	"strings"

	"github.com/spf13/cobra"

	"tg-cli/internal/output"
)

// safetyClass is what a command does outside its own process. An agent reads it
// before running a command it has never used.
type safetyClass string

const (
	// safetyRead only reads: from Telegram, from the local database, or both.
	safetyRead safetyClass = "read"
	// safetyLocalWrite changes local state: the database or the daemon.
	// It never changes the Telegram account.
	safetyLocalWrite safetyClass = "local_write"
	// safetyAccountWrite changes the Telegram account or its login state.
	safetyAccountWrite safetyClass = "account_write"
)

// argSpec describes one positional argument of a command.
type argSpec struct {
	// Name is how the command usage calls the argument, without the brackets.
	Name string `json:"name"`
	// Required is false when the command works without the argument.
	Required bool `json:"required"`
	// Variadic is true when the command takes any number of the arguments.
	Variadic bool `json:"variadic,omitempty"`
}

// flagChoices are the allowed values of a flag whose check is a list, so that
// `schema` shows them and the check is not the only place that knows them.
type flagChoices struct {
	Flag   string
	Values []string
}

// formatNames turns a list of formats into the strings an agent types.
func formatNames(formats []output.Format) []string {
	names := make([]string, len(formats))
	for i, f := range formats {
		names[i] = string(f)
	}
	return names
}

// authorChoices are the allowed values of a --author flag that takes both.
func authorChoices() []flagChoices {
	return []flagChoices{{Flag: "author", Values: []string{"owner", "ai"}}}
}

// chatTypeChoices are the allowed values of the --type flag of `chat list`.
// The daemon checks them, so the list is repeated here for the schema only.
func chatTypeChoices() []flagChoices {
	return []flagChoices{{Flag: "type", Values: []string{"user", "bot", "group", "supergroup", "channel"}}}
}

// scopeChoices are the allowed values of the --scope flag of `search`.
func scopeChoices() []flagChoices {
	return []flagChoices{{Flag: "scope", Values: searchScopes()}}
}

// risk is whether a command can lose data or do something the agent cannot
// undo. It is its own type and a required argument of annotate, so that every
// command records the choice: a bool field would have let a new command fall
// back to the zero value "not dangerous" without anyone deciding it.
type risk string

const (
	// noRisk marks a command whose success loses nothing: it reads, or it
	// writes something that can be written again.
	noRisk risk = "no"
	// dangerous marks a command that can lose data or change the account
	// beyond an ordinary write, like `note delete --author ai --yes`.
	dangerous risk = "dangerous"
)

// known reports whether r was chosen from the constants above.
func (r risk) known() bool { return r == noRisk || r == dangerous }

// commandSpec is what every runnable command declares about itself: the part of
// its contract an agent needs before calling it. A runnable command without a
// spec fails TestEveryCommandIsAnnotated, so a new command cannot ship
// undescribed.
type commandSpec struct {
	// Safety is the class of changes the command makes.
	Safety safetyClass
	// Risk is the hand-made dangerous flag; it is set through annotate, never
	// derived from Safety: a local_write command can still lose data.
	Risk risk
	// Result is the Go type of data in the success envelope.
	Result reflect.Type
	// Args are the positional arguments, in the order they are given.
	Args []argSpec
	// Choices are the allowed values of the flags whose check is a list.
	Choices []flagChoices
	// ItemPath is the json path of the list inside the result that a JSONL
	// line is taken from, empty when the command does not print JSONL.
	ItemPath string
	// Formats overrides the ordinary command formats for a streaming command.
	Formats []output.Format
	// SubscriptionMethod links a stream command to the raw RPC contract.
	SubscriptionMethod string
}

// annotate records the spec of a runnable command and returns the command. The
// risk of the command is a required argument, so the choice cannot be skipped.
func (a *app) annotate(cmd *cobra.Command, r risk, spec commandSpec) *cobra.Command {
	spec.Risk = r
	a.specs[cmd] = spec
	return cmd
}

// typeOf names the result type of a command in an annotation.
func typeOf[T any]() reflect.Type { return reflect.TypeOf((*T)(nil)).Elem() }

// walkCommands visits every runnable command an agent can use, with its path
// like "chat list". Groups, hidden and deprecated commands and the help command
// cobra generates are left out.
func walkCommands(root *cobra.Command, visit func(cmd *cobra.Command, path string)) {
	var walk func(c *cobra.Command, prefix string)
	walk = func(c *cobra.Command, prefix string) {
		for _, child := range c.Commands() {
			path := strings.TrimSpace(prefix + " " + child.Name())
			if child.IsAvailableCommand() && child.Runnable() {
				visit(child, path)
			}
			walk(child, path)
		}
	}
	walk(root, "")
}
