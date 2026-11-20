package cli

import (
	"slices"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

// localSearchResult describes both result shapes of search in the schema:
// hits for the ordinary search, fragments for --semantic. The command prints
// the RPC result itself, which carries one of the two.
type localSearchResult struct {
	Hits          []rpc.SearchHit        `json:"hits,omitempty"`
	Fragments     []rpc.SemanticFragment `json:"fragments,omitempty"`
	VectorSkipped string                 `json:"vector_skipped,omitempty"`
}

// searchSemantic runs `tg search --semantic`: the hybrid search over
// conversation fragments. The flags that do not apply to it are refused before
// the Daemon is asked.
func (a *app) searchSemantic(cmd *cobra.Command, query string, scopes []string, author string, settings outputSettings) error {
	if len(scopes) > 0 && slices.ContainsFunc(scopes, func(scope string) bool { return scope != "messages" }) {
		return fail(exitcode.Usage, "--semantic searches messages only: use --scope messages or no --scope")
	}
	if author != "" {
		return fail(exitcode.Usage, "--author does not apply to --semantic")
	}
	if deleted, _ := cmd.Flags().GetBool("deleted"); deleted {
		return fail(exitcode.Usage, "--deleted does not apply to --semantic: deleted messages are not searched")
	}
	p := rpc.SemanticSearchParams{Query: query}
	p.Chat, _ = cmd.Flags().GetString("chat")
	p.Limit, _ = cmd.Flags().GetInt("limit")
	if p.Limit < 1 || p.Limit > 100 {
		return fail(exitcode.Usage, "--limit must be between 1 and 100")
	}
	p.Prefix, _ = cmd.Flags().GetBool("prefix")
	var result rpc.SemanticSearchResult
	if err := a.callOne(cmd, rpc.MethodSearchSemantic, p, &result); err != nil {
		return err
	}
	return emitListAtPath(a, cmd, settings, "fragments", result, result.Fragments, "")
}
