package odoo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientAgainstFakeOdoo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != "session_id=s3cr3t" {
			t.Errorf("expected session cookie, got %q", got)
		}
		switch {
		case r.URL.Path == "/web/dataset/call_kw":
			var body struct {
				Params struct {
					Model  string `json:"model"`
					Method string `json:"method"`
				} `json:"params"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			var result any
			switch body.Params.Model + "." + body.Params.Method {
			case "project.task.read":
				result = []map[string]any{{"id": 11309, "name": "Wrong cost calc", "description": "<p>desc</p>"}}
			case "mail.message.search_read":
				result = []map[string]any{{"id": 1, "author_id": []any{7, "Alice"}, "date": "2026-08-29 10:00:00", "body": "<p>a comment</p>", "message_type": "comment"}}
			case "ir.attachment.search_read":
				result = []map[string]any{{"id": 99, "name": "screenshot.png", "mimetype": "image/png", "file_size": 3}}
			default:
				t.Fatalf("unexpected call %s.%s", body.Params.Model, body.Params.Method)
			}
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "result": result})
		case r.URL.Path == "/web/content/99":
			w.Write([]byte("PNG"))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "s3cr3t")

	task, err := c.ReadTask(11309)
	if err != nil {
		t.Fatal(err)
	}
	if task.Name != "Wrong cost calc" || task.Description != "<p>desc</p>" {
		t.Errorf("unexpected task: %+v", task)
	}

	chatter, err := c.FetchChatter(11309)
	if err != nil {
		t.Fatal(err)
	}
	if len(chatter) != 1 || chatter[0].Author != "Alice" {
		t.Errorf("unexpected chatter: %+v", chatter)
	}

	attachments, err := c.FetchAttachments(11309)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || attachments[0].Name != "screenshot.png" {
		t.Errorf("unexpected attachments: %+v", attachments)
	}

	data, err := c.DownloadAttachment(99)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "PNG" {
		t.Errorf("unexpected attachment bytes: %q", data)
	}
}

func TestHasHiddenText(t *testing.T) {
	cases := map[string]bool{
		"":             false,
		"<p>hello</p>": false,
		`<span style="font-size:0px">secret</span>`: true,
		`<div style="display:none">secret</div>`:    true,
		`<span style="opacity:0;">secret</span>`:    true,
	}
	for html, want := range cases {
		if got := HasHiddenText(html); got != want {
			t.Errorf("HasHiddenText(%q) = %v, want %v", html, got, want)
		}
	}
}
