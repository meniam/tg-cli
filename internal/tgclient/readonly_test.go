package tgclient

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"testing"
)

// writeVerbs start the name of anything that changes data of the account.
const writeVerbs = `Send|Edit|Delete|Forward|Join|Leave|Invite|Kick|Ban|Block|Unblock|Report|Create|Pin|Unpin|` +
	`Mark|Read|Set|Save|Toggle|Import|Upload|Install|Register|Revoke|Terminate|Update|Reorder|Hide|Clear`

// writeCall matches a call name that writes: either a plain verb (peers.User.Block,
// peers.Channel.Leave) or a raw API method (MessagesSendMessage, ChannelsEditTitle).
var writeCall = regexp.MustCompile(`^(` + writeVerbs + `)|^(Messages|Channels|Contacts|Account|Users|Auth|Phone|Stories|Payments|Bots|Folders|Chatlists)(` + writeVerbs + `)|^MessagesGet(BotCallbackAnswer|InlineBotResults)$`)

// readFiles are the files that read chats and messages. They must stay
// read-only: login code lives in other files and is allowed to use sign-in
// methods. The same rule is stated in the security section of AGENTS.md.
var readFiles = []string{"download.go", "history.go", "peers.go", "message.go", "convert.go", "cursor.go", "client.go", "errors.go", "members.go", "topics_common.go", "users_read.go", "buttons_read.go", "stickers_read.go", "channels_profile_read.go", "privacy_read.go", "chatlist_read.go", "saved_read.go", "transcribe.go", "stats.go", "phone_read.go", "translate.go"}

func TestReadFilesCallNothingThatWrites(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range readFiles {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var bad []string
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name != "UploadGetFile" && writeCall.MatchString(sel.Sel.Name) {
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "strings" && sel.Sel.Name == "Join" {
					return true
				}
				bad = append(bad, sel.Sel.Name+" at "+fset.Position(sel.Pos()).String())
			}
			return true
		})
		sort.Strings(bad)
		for _, b := range bad {
			t.Errorf("%s calls a method that may write to the account: %s", name, b)
		}
	}
}

func TestWriteCallPatternCatchesWriters(t *testing.T) {
	for _, name := range []string{
		"MessagesSendMessage", "MessagesDeleteMessages", "MessagesReadHistory", "MessagesEditMessage",
		"ChannelsJoinChannel", "ChannelsLeaveChannel", "ContactsBlock", "AccountUpdateProfile",
		"Block", "Leave", "Join", "ImportInvite", "SendCode", "ReportSpam", "MessagesSetTyping",
		"MessagesForwardMessages", "MessagesSaveDraft", "MessagesUnpinAllMessages",
		"MessagesGetBotCallbackAnswer", "MessagesGetInlineBotResults",
	} {
		if !writeCall.MatchString(name) {
			t.Errorf("%s must be recognised as a write", name)
		}
	}
	for _, name := range []string{
		"Resolve", "ResolveDeeplinkJoin", "GetHistory", "GetReplies", "Search", "GetMessagesWithEntities",
		"FullRaw", "Iter", "Next", "Value", "Err", "BatchSize", "OffsetID", "Apply", "Init", "Self",
		"ParticipantsCount", "MessagesGetDialogs", "Raw", "InputPeer", "VisibleName",
	} {
		if writeCall.MatchString(name) {
			t.Errorf("%s is a read and must not be flagged", name)
		}
	}
}
