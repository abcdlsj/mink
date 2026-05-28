package llm

import (
	"testing"

	"github.com/abcdlsj/sumi/msg"
	"github.com/sashabaranov/go-openai"
)

func TestOpenAIMessageImagesUseMultipartContent(t *testing.T) {
	m := msg.Message{
		Role:    "user",
		Content: "看图",
		Attachments: []msg.Attachment{{
			Kind: "image",
			MIME: "image/png",
			Data: "abcd",
		}},
	}
	got := openAIMessage(m)
	if got.Content != "" {
		t.Fatalf("content = %q", got.Content)
	}
	if len(got.MultiContent) != 2 {
		t.Fatalf("parts = %#v", got.MultiContent)
	}
	if got.MultiContent[0].Type != openai.ChatMessagePartTypeText || got.MultiContent[0].Text != "看图" {
		t.Fatalf("text part = %#v", got.MultiContent[0])
	}
	if got.MultiContent[1].Type != openai.ChatMessagePartTypeImageURL {
		t.Fatalf("image part = %#v", got.MultiContent[1])
	}
	if got.MultiContent[1].ImageURL == nil || got.MultiContent[1].ImageURL.URL != "data:image/png;base64,abcd" {
		t.Fatalf("image url = %#v", got.MultiContent[1].ImageURL)
	}
}

func TestOpenRouterMessageImagesUseMultipartContent(t *testing.T) {
	m := msg.Message{
		Role:    "user",
		Content: "看图",
		Attachments: []msg.Attachment{{
			Kind: "image",
			URL:  "https://example.com/a.png",
		}},
	}
	got := openRouterMessage(m)
	if got.Content.Text != "" {
		t.Fatalf("content = %q", got.Content.Text)
	}
	if len(got.Content.Multi) != 2 {
		t.Fatalf("parts = %#v", got.Content.Multi)
	}
	if got.Content.Multi[1].ImageURL == nil || got.Content.Multi[1].ImageURL.URL != "https://example.com/a.png" {
		t.Fatalf("image url = %#v", got.Content.Multi[1].ImageURL)
	}
}

func TestAnthropicUserBlocksIncludeImages(t *testing.T) {
	blocks := anthropicUserBlocks(msg.Message{
		Role:    "user",
		Content: "看图",
		Attachments: []msg.Attachment{{
			Kind: "image",
			MIME: "image/png",
			Data: "abcd",
		}},
	})
	if len(blocks) != 2 {
		t.Fatalf("blocks = %#v", blocks)
	}
	if blocks[1].OfImage == nil || blocks[1].OfImage.Source.OfBase64 == nil {
		t.Fatalf("image block = %#v", blocks[1])
	}
}
