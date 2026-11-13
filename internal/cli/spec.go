package cli

import (
	"reflect"
	"strings"

	"github.com/spf13/cobra"
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

// formatChoices are the allowed values of the --format flag of a list command.
func formatChoices() []flagChoices {
	return []flagChoices{{Flag: "format", Values: outputFormats()}}
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

// commandSpec is what every runnable command declares about itself: the part of
// its contract an agent needs before calling it. A runnable command without a
// spec fails TestEveryCommandIsAnnotated, so a new command cannot ship
// undescribed.
type commandSpec struct {
	// Safety is the class of changes the command makes.
	Safety safetyClass
	// Dangerous is set by hand, never derived from Safety: a command can be
	// local_write and still lose data, like `note delete --author ai --yes`.
	// A command that is not dangerous leaves this out.
	Dangerous bool
	// Result is the Go type of data in the success envelope.
	Result reflect.Type
	// Args are the positional arguments, in the order they are given.
	Args []argSpec
	// Choices are the allowed values of the flags whose check is a list.
	Choices []flagChoices
}

// annotate records the spec of a runnable command and returns the command.
func (a *app) annotate(cmd *cobra.Command, spec commandSpec) *cobra.Command {
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
