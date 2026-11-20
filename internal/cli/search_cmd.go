package cli

import (
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

// searchScopes are the scopes `search` accepts, in the order results are grouped.
func searchScopes() []string { return []string{"chats", "people", "messages", "notes"} }

func (a *app) searchCommand() *cobra.Command {
	cmd := a.annotate(&cobra.Command{
		Use: "search [flags] <query>", Short: "Search the local database",
		Long: "Search the local database without a Telegram connection. Results include only\n" +
			"chats, people, messages, and notes that the daemon has already loaded or saved.\n" +
			"Quote a multiword query in the shell. Use -- before a query starting with -.\n" +
			"--scope accepts repeated values or a comma-separated list. JSONL prints one hit\n" +
			"per line in scope order.\n" +
			"--semantic searches messages only and returns conversation fragments (result\n" +
			"fields fragments, vector_skipped and vector_dropped) found by exact words,\n" +
			"stemmed words and vector similarity; JSONL then prints one fragment per line.\n" +
			"It works without Ollama: the fragments then come from words alone and\n" +
			"vector_skipped says why. Vector hits with a cosine similarity below\n" +
			"[semantic_search] min_similarity (default 0.5) are dropped before the merge and\n" +
			"counted in vector_dropped; --min-similarity X (0 to 1) overrides the setting\n" +
			"for one query and 0 turns the threshold off.",
		Args: cobra.ExactArgs(1), RunE: a.search,
	}, noRisk, commandSpec{Safety: safetyRead, Result: typeOf[localSearchResult](), Args: []argSpec{{Name: "query", Required: true}}, Choices: append(authorChoices(), scopeChoices()...), ItemPath: "hits"})
	cmd.SetFlagErrorFunc(func(*cobra.Command, error) error {
		return fail(exitcode.Usage, "invalid search flags")
	})
	cmd.Flags().StringSlice("scope", nil, "scopes: chats, people, messages, notes; repeat or separate with commas")
	cmd.Flags().String("author", "", "filter note hits by owner or ai")
	cmd.Flags().String("chat", "", "restrict message and note hits to this chat")
	cmd.Flags().Int("limit", 20, "hits per scope, 1 to 100")
	cmd.Flags().Bool("prefix", false, "match the last query term as a prefix")
	cmd.Flags().Bool("deleted", false, "include messages marked deleted")
	cmd.Flags().Bool("semantic", false, "search conversation fragments by words and meaning (messages only; --limit counts fragments)")
	cmd.Flags().Float64("min-similarity", 0, "with --semantic: lowest cosine similarity of a vector hit, 0 to 1 (0 turns the threshold off; default from [semantic_search] min_similarity)")
	return cmd
}

func (a *app) search(cmd *cobra.Command, args []string) error {
	if strings.TrimSpace(args[0]) == "" {
		return fail(exitcode.Usage, "search query must contain text")
	}
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	scopes, _ := cmd.Flags().GetStringSlice("scope")
	for i := range scopes {
		scopes[i] = strings.TrimSpace(scopes[i])
		if !slices.Contains(searchScopes(), scopes[i]) {
			return fail(exitcode.Usage, "--scope must be chats, people, messages, or notes")
		}
	}
	author, _ := cmd.Flags().GetString("author")
	if author != "" && author != "owner" && author != "ai" {
		return fail(exitcode.Usage, "--author must be owner or ai")
	}
	if semantic, _ := cmd.Flags().GetBool("semantic"); semantic {
		return a.searchSemantic(cmd, args[0], scopes, author, settings)
	}
	if cmd.Flags().Changed("min-similarity") {
		return fail(exitcode.Usage, "--min-similarity applies to --semantic only")
	}
	p := rpc.SearchParams{Query: args[0], Scopes: scopes, Author: author}
	p.Chat, _ = cmd.Flags().GetString("chat")
	p.Limit, _ = cmd.Flags().GetInt("limit")
	p.Prefix, _ = cmd.Flags().GetBool("prefix")
	p.IncludeDeleted, _ = cmd.Flags().GetBool("deleted")
	var result rpc.SearchResult
	if err := a.callOne(cmd, rpc.MethodSearch, p, &result); err != nil {
		return err
	}
	return emitList(a, cmd, settings, result, result.Hits, "")
}
