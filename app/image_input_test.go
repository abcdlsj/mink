package app

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareImageInputDirective(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.png")
	data := []byte("\x89PNG\r\n\x1a\npng")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	text, attachments, err := prepareImageInput("看图 [[image:"+path+"]]", nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != "看图\n\nAttached images:\n[image_1: a.png]" {
		t.Fatalf("text = %q", text)
	}
	if len(attachments) != 1 {
		t.Fatalf("attachments = %#v", attachments)
	}
	if attachments[0].MIME != "image/png" {
		t.Fatalf("mime = %q", attachments[0].MIME)
	}
	if attachments[0].Data != base64.StdEncoding.EncodeToString(data) {
		t.Fatalf("data = %q", attachments[0].Data)
	}
	if attachments[0].Label != "image_1" {
		t.Fatalf("label = %q", attachments[0].Label)
	}
}

func TestPrepareImageInputPastedPathLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.jpg")
	if err := os.WriteFile(path, []byte("\xff\xd8\xffjpg"), 0o644); err != nil {
		t.Fatal(err)
	}

	text, attachments, err := prepareImageInput("这是什么\n"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != "这是什么\n\nAttached images:\n[image_1: a.jpg]" {
		t.Fatalf("text = %q", text)
	}
	if len(attachments) != 1 || attachments[0].MIME != "image/jpeg" {
		t.Fatalf("attachments = %#v", attachments)
	}
}

func TestPrepareImageInputURL(t *testing.T) {
	text, attachments, err := prepareImageInput("![x](https://example.com/a.webp)", nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != "请看这张图片。\n\nAttached images:\n[image_1: a.webp]" {
		t.Fatalf("text = %q", text)
	}
	if len(attachments) != 1 || attachments[0].URL != "https://example.com/a.webp" {
		t.Fatalf("attachments = %#v", attachments)
	}
}

func TestPrepareImageInputPlainWebURL(t *testing.T) {
	input := "https://bazaar.mrmao.life/\n这个是网页，你可以去网页自己抓接口自己查往届的数据"
	text, attachments, err := prepareImageInput(input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != input {
		t.Fatalf("text = %q", text)
	}
	if len(attachments) != 0 {
		t.Fatalf("attachments = %#v", attachments)
	}
}

func TestPrepareImageInputRejectsWebPageImageURL(t *testing.T) {
	_, _, err := prepareImageInput("[[image:https://bazaar.mrmao.life/]]", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "unsupported image type: https://bazaar.mrmao.life/" {
		t.Fatalf("err = %v", err)
	}
}

func TestPrepareImageInputLabelsMultipleImages(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.png")
	b := filepath.Join(dir, "b.png")
	for _, path := range []string{a, b} {
		if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\npng"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	text, attachments, err := prepareImageInput("对比\n"+a+"\n"+b, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "对比\n\nAttached images:\n[image_1: a.png]\n[image_2: b.png]"
	if text != want {
		t.Fatalf("text = %q", text)
	}
	if len(attachments) != 2 || attachments[0].Label != "image_1" || attachments[1].Label != "image_2" {
		t.Fatalf("attachments = %#v", attachments)
	}
}

func TestPrepareImageInputFileURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.png")
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\npng"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, attachments, err := prepareImageInput("[[image:file://"+path+"]]", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || attachments[0].Name != "a.png" {
		t.Fatalf("attachments = %#v", attachments)
	}
}
