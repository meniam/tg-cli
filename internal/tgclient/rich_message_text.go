package tgclient

import (
	"strings"

	"github.com/gotd/td/tg"
)

// richMessageText renders the visible text of a received rich message.
func richMessageText(message tg.RichMessage) string {
	return cleanRichLines(richBlocksText(message.Blocks))
}

func richTextText(text tg.RichTextClass) string {
	switch value := text.(type) {
	case nil, *tg.TextEmpty, *tg.TextImage:
		// TextImage only has a document ID and dimensions, with no alt text.
		return ""
	case *tg.TextPlain:
		if value == nil {
			return ""
		}
		return value.Text
	case *tg.TextConcat:
		if value == nil {
			return ""
		}
		var result strings.Builder
		for _, child := range value.Texts {
			result.WriteString(richTextText(child))
		}
		return result.String()
	case *tg.TextMath:
		if value == nil {
			return ""
		}
		return value.Source
	case *tg.TextCustomEmoji:
		if value == nil {
			return ""
		}
		return value.Alt
	case *tg.TextDiff:
		if value == nil {
			return ""
		}
		return joinRichParts(richTextText(value.OldText), richTextText(value.Text))
	case interface{ GetText() tg.RichTextClass }:
		// Every other text-bearing TL constructor wraps one RichText node.
		return richTextText(value.GetText())
	default:
		return ""
	}
}

func richBlocksText(blocks []tg.PageBlockClass) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		parts = append(parts, richBlockText(block))
	}
	return joinRichParts(parts...)
}

func richBlockText(block tg.PageBlockClass) string {
	switch value := block.(type) {
	case nil:
		return ""
	case *tg.PageBlockAuthorDate:
		return richTextText(value.Author)
	case *tg.PageBlockBlockquote:
		return joinRichParts(richTextText(value.Text), richTextText(value.Caption))
	case *tg.PageBlockPullquote:
		return joinRichParts(richTextText(value.Text), richTextText(value.Caption))
	case *tg.PageBlockBlockquoteBlocks:
		return joinRichParts(richBlocksText(value.Blocks), richTextText(value.Caption))
	case *tg.PageBlockList:
		parts := make([]string, 0, len(value.Items))
		for _, item := range value.Items {
			parts = append(parts, richListItemText(item))
		}
		return joinRichParts(parts...)
	case *tg.PageBlockOrderedList:
		parts := make([]string, 0, len(value.Items))
		for _, item := range value.Items {
			parts = append(parts, richOrderedItemText(item))
		}
		return joinRichParts(parts...)
	case *tg.PageBlockTable:
		parts := []string{richTextText(value.Title)}
		for _, row := range value.Rows {
			cells := make([]string, 0, len(row.Cells))
			for _, cell := range row.Cells {
				cells = append(cells, richTextText(cell.Text))
			}
			parts = append(parts, strings.Join(cells, "\t"))
		}
		return joinRichParts(parts...)
	case *tg.PageBlockDetails:
		return joinRichParts(richTextText(value.Title), richBlocksText(value.Blocks))
	case *tg.PageBlockCover:
		return richBlockText(value.Cover)
	case *tg.PageBlockCollage:
		return joinRichParts(richBlocksText(value.Items), richCaptionText(value.Caption))
	case *tg.PageBlockSlideshow:
		return joinRichParts(richBlocksText(value.Items), richCaptionText(value.Caption))
	case *tg.PageBlockEmbedPost:
		return joinRichParts(value.Author, richBlocksText(value.Blocks), richCaptionText(value.Caption))
	case *tg.PageBlockRelatedArticles:
		parts := []string{richTextText(value.Title)}
		for _, article := range value.Articles {
			parts = append(parts, joinRichParts(article.Title, article.Description, article.Author))
		}
		return joinRichParts(parts...)
	case *tg.PageBlockButtonRow:
		parts := make([]string, 0, len(value.Buttons))
		for _, button := range value.Buttons {
			parts = append(parts, richTextText(button.Text))
		}
		return joinRichParts(parts...)
	case *tg.PageBlockChannel:
		if value.Channel == nil {
			return ""
		}
		if titled, ok := value.Channel.(interface{ GetTitle() string }); ok {
			return titled.GetTitle()
		}
		return ""
	case *tg.PageBlockMath:
		return value.Source
	case *tg.PageBlockPhoto:
		return richCaptionText(value.Caption)
	case *tg.PageBlockVideo:
		return richCaptionText(value.Caption)
	case *tg.PageBlockAudio:
		return richCaptionText(value.Caption)
	case *tg.PageBlockDocument:
		return richCaptionText(value.Caption)
	case *tg.PageBlockEmbed:
		return richCaptionText(value.Caption)
	case *tg.PageBlockMap:
		return richCaptionText(value.Caption)
	case interface{ GetText() tg.RichTextClass }:
		return richTextText(value.GetText())
	default:
		// Unsupported, Divider and Anchor have no visible text. Media IDs, URLs,
		// embedded HTML and unknown future blocks are intentionally omitted.
		return ""
	}
}

func richListItemText(item tg.PageListItemClass) string {
	switch value := item.(type) {
	case *tg.PageListItemText:
		return richTextText(value.Text)
	case *tg.PageListItemBlocks:
		return richBlocksText(value.Blocks)
	default:
		return ""
	}
}

func richOrderedItemText(item tg.PageListOrderedItemClass) string {
	switch value := item.(type) {
	case *tg.PageListOrderedItemText:
		return richTextText(value.Text)
	case *tg.PageListOrderedItemBlocks:
		return richBlocksText(value.Blocks)
	default:
		return ""
	}
}

func richCaptionText(caption tg.PageCaption) string {
	return joinRichParts(richTextText(caption.Text), richTextText(caption.Credit))
}

func joinRichParts(parts ...string) string {
	var nonempty []string
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			nonempty = append(nonempty, part)
		}
	}
	return strings.Join(nonempty, "\n")
}

func cleanRichLines(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var result strings.Builder
	lastNewline := false
	for _, r := range text {
		if r == '\n' {
			if lastNewline {
				continue
			}
			lastNewline = true
		} else {
			lastNewline = false
		}
		result.WriteRune(r)
	}
	return strings.TrimSpace(result.String())
}
