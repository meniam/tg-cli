package tgclient

import (
	"testing"

	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/tg"
)

func richPlain(text string) tg.RichTextClass { return &tg.TextPlain{Text: text} }

func TestRichTextTextWrappers(t *testing.T) {
	inner := richPlain("text")
	wrappers := []tg.RichTextClass{
		&tg.TextBold{Text: inner}, &tg.TextItalic{Text: inner},
		&tg.TextUnderline{Text: inner}, &tg.TextStrike{Text: inner},
		&tg.TextFixed{Text: inner}, &tg.TextURL{Text: inner},
		&tg.TextEmail{Text: inner}, &tg.TextSubscript{Text: inner},
		&tg.TextSuperscript{Text: inner}, &tg.TextMarked{Text: inner},
		&tg.TextPhone{Text: inner}, &tg.TextAnchor{Text: inner},
		&tg.TextSpoiler{Text: inner}, &tg.TextMention{Text: inner},
		&tg.TextHashtag{Text: inner}, &tg.TextBotCommand{Text: inner},
		&tg.TextCashtag{Text: inner}, &tg.TextAutoURL{Text: inner},
		&tg.TextAutoEmail{Text: inner}, &tg.TextAutoPhone{Text: inner},
		&tg.TextBankCard{Text: inner}, &tg.TextMentionName{Text: inner},
		&tg.TextDate{Text: inner}, &tg.TextButton{Text: inner},
		&tg.TextTonAddress{Text: inner},
	}
	for _, wrapper := range wrappers {
		if got := richTextText(wrapper); got != "text" {
			t.Errorf("%T: %q", wrapper, got)
		}
	}
	if got := richTextText(&tg.TextConcat{Texts: []tg.RichTextClass{&tg.TextBold{Text: richPlain("A")}, &tg.TextUnderline{Text: richPlain("B")}}}); got != "AB" {
		t.Errorf("nested concat=%q", got)
	}
	if got := richTextText(&tg.TextMath{Source: "x^2"}); got != "x^2" {
		t.Errorf("math=%q", got)
	}
	if got := richTextText(&tg.TextCustomEmoji{Alt: "✨"}); got != "✨" {
		t.Errorf("emoji=%q", got)
	}
	if got := richTextText(&tg.TextImage{DocumentID: 1}); got != "" {
		t.Errorf("image=%q", got)
	}
	if got := richTextText(&tg.TextDiff{OldText: richPlain("old"), Text: richPlain("new")}); got != "old\nnew" {
		t.Errorf("diff=%q", got)
	}
}

func TestRichMessageTextBlocks(t *testing.T) {
	msg := tg.RichMessage{Blocks: []tg.PageBlockClass{
		&tg.PageBlockHeading1{Text: richPlain("Heading")},
		&tg.PageBlockParagraph{Text: &tg.TextConcat{Texts: []tg.RichTextClass{&tg.TextURL{Text: richPlain("link")}, richPlain(" "), &tg.TextUnderline{Text: richPlain("under")}}}},
		&tg.PageBlockList{Items: []tg.PageListItemClass{
			&tg.PageListItemText{Text: richPlain("first")},
			&tg.PageListItemBlocks{Blocks: []tg.PageBlockClass{
				&tg.PageBlockParagraph{Text: richPlain("parent")},
				&tg.PageBlockOrderedList{Items: []tg.PageListOrderedItemClass{&tg.PageListOrderedItemText{Text: richPlain("child")}}},
			}},
		}},
		&tg.PageBlockTable{Title: richPlain("Table"), Rows: []tg.PageTableRow{{Cells: []tg.PageTableCell{{Text: richPlain("A")}, {Text: richPlain("B")}}}}},
		&tg.PageBlockBlockquote{Text: richPlain("quote"), Caption: richPlain("source")},
		&tg.PageBlockPreformatted{Text: richPlain("code"), Language: "go"},
		&tg.PageBlockMath{Source: "E=mc²"},
		&tg.PageBlockParagraph{Text: &tg.TextCustomEmoji{Alt: "✨"}},
		&tg.PageBlockUnsupported{},
		&tg.PageBlockParagraph{Text: richPlain("\n\nlast\n\n")},
	}}
	want := "Heading\nlink under\nfirst\nparent\nchild\nTable\nA\tB\nquote\nsource\ncode\nE=mc²\n✨\nlast"
	if got := richMessageText(msg); got != want {
		t.Fatalf("rich text:\n%q\nwant:\n%q", got, want)
	}
}

func TestRichMessageTextOtherTextBearingBlocks(t *testing.T) {
	caption := tg.PageCaption{Text: richPlain("caption"), Credit: richPlain("credit")}
	msg := tg.RichMessage{Blocks: []tg.PageBlockClass{
		&tg.PageBlockDetails{Title: richPlain("details"), Blocks: []tg.PageBlockClass{&tg.PageBlockParagraph{Text: richPlain("inside")}}},
		&tg.PageBlockCollage{Items: []tg.PageBlockClass{&tg.PageBlockPhoto{Caption: tg.PageCaption{Text: richPlain("photo")}}}, Caption: caption},
		&tg.PageBlockSlideshow{Items: []tg.PageBlockClass{&tg.PageBlockVideo{Caption: tg.PageCaption{Text: richPlain("video")}}}},
		&tg.PageBlockEmbedPost{Author: "author", Blocks: []tg.PageBlockClass{&tg.PageBlockParagraph{Text: richPlain("post")}}, Caption: tg.PageCaption{Text: richPlain("post caption")}},
		&tg.PageBlockRelatedArticles{Title: richPlain("related"), Articles: []tg.PageRelatedArticle{{Title: "article", Description: "description", Author: "writer"}}},
		&tg.PageBlockButtonRow{Buttons: []tg.PageButton{{Text: richPlain("button")}}},
		&tg.PageBlockBlockquoteBlocks{Blocks: []tg.PageBlockClass{&tg.PageBlockParagraph{Text: richPlain("nested quote")}}, Caption: richPlain("quote caption")},
		&tg.PageBlockCover{Cover: &tg.PageBlockParagraph{Text: richPlain("cover")}},
		&tg.PageBlockDocument{Caption: tg.PageCaption{Text: richPlain("document")}},
		&tg.PageBlockMap{Caption: tg.PageCaption{Text: richPlain("map")}},
		&tg.PageBlockAudio{Caption: tg.PageCaption{Text: richPlain("audio")}},
		&tg.PageBlockEmbed{Caption: tg.PageCaption{Text: richPlain("embed")}},
		&tg.PageBlockDivider{}, &tg.PageBlockAnchor{Name: "not visible"},
	}}
	want := "details\ninside\nphoto\ncaption\ncredit\nvideo\nauthor\npost\npost caption\nrelated\narticle\ndescription\nwriter\nbutton\nnested quote\nquote caption\ncover\ndocument\nmap\naudio\nembed"
	if got := richMessageText(msg); got != want {
		t.Fatalf("other blocks:\n%q\nwant:\n%q", got, want)
	}
	if got := richMessageText(tg.RichMessage{Blocks: []tg.PageBlockClass{&tg.PageBlockDivider{}, &tg.PageBlockParagraph{Text: richPlain("\n\n")}}}); got != "" {
		t.Errorf("empty blocks=%q", got)
	}
}

func TestToMessageRichTextFallback(t *testing.T) {
	rich := tg.RichMessage{Blocks: []tg.PageBlockClass{&tg.PageBlockParagraph{Text: richPlain("from blocks")}}}
	for _, tc := range []struct{ name, original, want string }{
		{"empty", "", "from blocks"},
		{"whitespace", " \n ", "from blocks"},
		{"plain priority", "plain", "plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &tg.Message{ID: 7, Date: 1700000000, Message: tc.original, Media: &tg.MessageMediaPhoto{}}
			m.SetRichMessage(rich)
			got, ok := toMessage(42, m, peer.Entities{}, nil)
			if !ok || got.Text != tc.want || got.MediaType != "photo" {
				t.Fatalf("message=%+v ok=%v", got, ok)
			}
		})
	}
	m := &tg.Message{ID: 8, Date: 1700000000}
	m.SetRichMessage(tg.RichMessage{})
	if got, _ := toMessage(42, m, peer.Entities{}, nil); got.Text != "" {
		t.Errorf("empty rich=%q", got.Text)
	}
}
