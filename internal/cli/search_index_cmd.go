package cli

import (
	"github.com/spf13/cobra"

	"tg-cli/internal/rpc"
)

// searchWithIndexCommand is `search` with its `index` group. The group is added
// here, not inside searchCommand, so the two files change independently.
func (a *app) searchWithIndexCommand() *cobra.Command {
	cmd := a.searchCommand()
	cmd.AddCommand(a.searchIndexCommand())
	return cmd
}

// searchIndexCommand drives the semantic indexer of the Daemon: the background
// job that cuts the local messages into fragments and embeds them through the
// local Ollama service. None of the three commands calls Telegram. `start` sends
// the text of fragments to Ollama, so it needs [semantic_search] enabled = true;
// `status` answers "disabled" without it.
func (a *app) searchIndexCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Build and inspect the semantic search index",
		Long: "The Daemon cuts the local messages into fragments and embeds them through a local\n" +
			"Ollama service. The group needs [semantic_search] enabled = true, except status.\n" +
			"To search for the word \"index\" itself, put -- before it: tg search -- index.",
	}
	start := a.annotate(&cobra.Command{
		Use:   "start",
		Short: "Run the indexer now",
		Long: "Queue an indexing run without waiting for the next interval. The command returns at\n" +
			"once with the state; a run that is already in progress is reported unchanged.\n" +
			"Check progress with search index status. The text of message fragments goes to the\n" +
			"local Ollama service only. It fails with exit code 2 while the feature is off.",
		Args: cobra.NoArgs, RunE: a.searchIndexStart,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.SemanticIndexStatus]()})
	status := a.annotate(&cobra.Command{
		Use:   "status",
		Short: "Show the semantic indexer state",
		Long: "Report whether the feature is on, the state of the indexer (disabled, idle, indexing,\n" +
			"unavailable, model_missing, error), the model, the fragments indexed out of the\n" +
			"indexable ones, and the time of the next run. It answers \"disabled\" while the\n" +
			"feature is off and never asks Ollama.",
		Args: cobra.NoArgs, RunE: a.searchIndexStatus,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[rpc.SemanticIndexStatus]()})
	cancel := a.annotate(&cobra.Command{
		Use:   "cancel",
		Short: "Stop the indexing run",
		Long: "Stop the run in progress. Fragments already stored stay; the next run continues from\n" +
			"there. It fails with exit code 2 while the feature is off.",
		Args: cobra.NoArgs, RunE: a.searchIndexCancel,
	}, noRisk, commandSpec{Safety: safetyLocalWrite, Result: typeOf[rpc.SemanticIndexStatus]()})
	cmd.AddCommand(start, status, cancel)
	return cmd
}

func (a *app) searchIndexStart(cmd *cobra.Command, _ []string) error {
	return a.searchIndexCall(cmd, rpc.MethodSearchIndexStart)
}

func (a *app) searchIndexStatus(cmd *cobra.Command, _ []string) error {
	return a.searchIndexCall(cmd, rpc.MethodSearchIndexStatus)
}

func (a *app) searchIndexCancel(cmd *cobra.Command, _ []string) error {
	return a.searchIndexCall(cmd, rpc.MethodSearchIndexCancel)
}

func (a *app) searchIndexCall(cmd *cobra.Command, method string) error {
	var status rpc.SemanticIndexStatus
	if err := a.callOne(cmd, method, rpc.SearchIndexParams{}, &status); err != nil {
		return err
	}
	return a.success(cmd, status)
}
