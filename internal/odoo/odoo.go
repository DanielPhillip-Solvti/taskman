// Package odoo is a minimal client for Odoo's session-authenticated
// JSON-RPC web endpoint (/web/dataset/call_kw). It exists so the daemon can
// pull a ticket's title, description, chatter, and attachments directly
// from Odoo — the same data a human would see on the task form — rather
// than trusting whatever the browser extension's DOM scrape happened to
// pick up. That also lets the daemon control exactly how that (untrusted,
// user-submitted) content is framed for the coding agent.
//
// Auth is the browser's own session_id cookie, forwarded by the extension;
// the daemon never stores Odoo credentials of its own. This mirrors the
// pattern already used by the read-task/post-task-update Claude skills.
package odoo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Client talks to one Odoo instance (host) as one already-authenticated
// browser session (sessionID).
type Client struct {
	Host      string // e.g. "https://pergolux.odoo.com" (scheme included)
	SessionID string
	HTTP      *http.Client
}

// New builds a Client, defaulting the scheme to https if the caller (e.g.
// the extension's location.origin) didn't already include one.
func New(host, sessionID string) *Client {
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}
	return &Client{
		Host:      strings.TrimRight(host, "/"),
		SessionID: sessionID,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
	}
}

type rpcRequest struct {
	JSONRPC string    `json:"jsonrpc"`
	Method  string    `json:"method"`
	Params  rpcParams `json:"params"`
}

type rpcParams struct {
	Model  string         `json:"model"`
	Method string         `json:"method"`
	Args   []any          `json:"args"`
	Kwargs map[string]any `json:"kwargs"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Message string `json:"message"`
	Data    struct {
		Name    string `json:"name"`    // e.g. "odoo.exceptions.AccessError"
		Message string `json:"message"` // the actual exception text
		Debug   string `json:"debug"`   // full Python traceback
	} `json:"data"`
}

// detail renders the parts of an Odoo RPC error actually worth logging.
// The top-level "message" is almost always the useless generic "Odoo
// Server Error" — the real cause (exception type + message, and the
// traceback) is nested under "data", which the caller has to opt into
// showing since it can be long.
func (e *rpcError) detail() string {
	msg := e.Data.Message
	if msg == "" {
		msg = e.Message
	}
	if e.Data.Name != "" {
		msg = fmt.Sprintf("%s: %s", e.Data.Name, msg)
	}
	return msg
}

// callKW performs one model.method(args, **kwargs) call, matching Odoo's
// /web/dataset/call_kw JSON-RPC contract.
func (c *Client) callKW(model, method string, args []any, kwargs map[string]any) (json.RawMessage, error) {
	if kwargs == nil {
		// call_kw's "kwargs" argument is required server-side (not
		// optional-with-default), so a nil map has to still serialize as
		// "{}" rather than being omitted or encoded as JSON null.
		kwargs = map[string]any{}
	}
	reqBody, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  "call",
		Params:  rpcParams{Model: model, Method: method, Args: args, Kwargs: kwargs},
	})
	if err != nil {
		return nil, fmt.Errorf("odoo: encode request: %w", err)
	}

	req, err := http.NewRequest("POST", c.Host+"/web/dataset/call_kw", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("odoo: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "session_id="+c.SessionID)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("odoo: %s.%s: %w", model, method, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("odoo: %s.%s: read response: %w", model, method, err)
	}

	var parsed rpcResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("odoo: %s.%s: decode response (status %d): %w", model, method, resp.StatusCode, err)
	}
	if parsed.Error != nil {
		if parsed.Error.Data.Debug != "" {
			// The full Python traceback is invaluable for diagnosing a
			// server-side failure but too long to put in an error string
			// that ends up in the task log / UI — log it separately.
			log.Printf("odoo: %s.%s server traceback:\n%s", model, method, parsed.Error.Data.Debug)
		}
		return nil, fmt.Errorf("odoo: %s.%s: %s", model, method, parsed.Error.detail())
	}
	return parsed.Result, nil
}

// Task is the subset of project.task fields the agent needs.
type Task struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"` // HTML, as authored — untrusted
}

// many2one is Odoo's [id, display_name] shape for relational fields.
type rawTask struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description any    `json:"description"` // false when empty, else a string
}

// ReadTask fetches a project.task's name + description.
func (c *Client) ReadTask(taskID int) (Task, error) {
	raw, err := c.callKW("project.task", "read", []any{[]int{taskID}, []string{"name", "description"}}, nil)
	if err != nil {
		return Task{}, err
	}
	var rows []rawTask
	if err := json.Unmarshal(raw, &rows); err != nil {
		return Task{}, fmt.Errorf("odoo: decode project.task read: %w", err)
	}
	if len(rows) == 0 {
		return Task{}, fmt.Errorf("odoo: task #%d not found (session expired, or wrong host?)", taskID)
	}
	desc, _ := rows[0].Description.(string) // false (bool) when empty, per Odoo's JSON encoding
	return Task{ID: rows[0].ID, Name: rows[0].Name, Description: desc}, nil
}

// Message is one chatter entry (an internal note or a customer-visible
// comment) on the ticket.
type Message struct {
	ID     int    `json:"id"`
	Author string `json:"author"`
	Date   string `json:"date"`
	Body   string `json:"body"` // HTML — untrusted, same as Description
	Type   string `json:"message_type"`
}

type rawMessage struct {
	ID          int    `json:"id"`
	AuthorID    any    `json:"author_id"` // [id, name] or false
	Date        string `json:"date"`
	Body        string `json:"body"`
	MessageType string `json:"message_type"`
}

// FetchChatter returns the ticket's mail.message thread (notes + comments),
// oldest first.
func (c *Client) FetchChatter(taskID int) ([]Message, error) {
	domain := []any{
		[]any{"res_id", "=", taskID},
		[]any{"model", "=", "project.task"},
		[]any{"message_type", "in", []string{"comment", "notification"}},
	}
	raw, err := c.callKW("mail.message", "search_read", []any{domain, []string{"author_id", "date", "body", "message_type"}}, map[string]any{"order": "date asc"})
	if err != nil {
		return nil, err
	}
	var rows []rawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("odoo: decode mail.message search_read: %w", err)
	}
	out := make([]Message, 0, len(rows))
	for _, r := range rows {
		author := ""
		if pair, ok := r.AuthorID.([]any); ok && len(pair) == 2 {
			author, _ = pair[1].(string)
		}
		out = append(out, Message{ID: r.ID, Author: author, Date: r.Date, Body: r.Body, Type: r.MessageType})
	}
	return out, nil
}

// Attachment describes one ir.attachment row without its content.
type Attachment struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Mimetype string `json:"mimetype"`
	FileSize int    `json:"file_size"`
}

// FetchAttachments lists the ticket's attachments (metadata only — use
// DownloadAttachment to fetch bytes).
func (c *Client) FetchAttachments(taskID int) ([]Attachment, error) {
	domain := []any{
		[]any{"res_id", "=", taskID},
		[]any{"res_model", "=", "project.task"},
	}
	raw, err := c.callKW("ir.attachment", "search_read", []any{domain, []string{"name", "mimetype", "file_size"}}, nil)
	if err != nil {
		return nil, err
	}
	var out []Attachment
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("odoo: decode ir.attachment search_read: %w", err)
	}
	return out, nil
}

// DownloadAttachment fetches one attachment's raw bytes over the same
// session-cookie auth, via Odoo's plain (non-RPC) content endpoint.
func (c *Client) DownloadAttachment(id int) ([]byte, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/web/content/%d?download=true", c.Host, id), nil)
	if err != nil {
		return nil, fmt.Errorf("odoo: build attachment request: %w", err)
	}
	req.Header.Set("Cookie", "session_id="+c.SessionID)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("odoo: download attachment %d: %w", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("odoo: download attachment %d: HTTP %d", id, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("odoo: read attachment %d: %w", id, err)
	}
	return data, nil
}

// hiddenTextRe flags CSS commonly used to hide prompt-injection text inside
// pasted HTML (near-zero font size, display:none, etc.) — ported from the
// read-task skill's same heuristic. It doesn't strip anything; callers use
// it to decide whether to warn the agent.
var hiddenTextRe = regexp.MustCompile(`(?i)font-size:\s*0*[01](?:\.\d+)?px|display:\s*none|visibility:\s*hidden|opacity:\s*0*(?:\.0+)?\s*[;"']`)

// HasHiddenText reports whether html contains style attributes commonly
// used to hide text from human readers while keeping it machine-readable —
// a known prompt-injection vector in pasted ticket content.
func HasHiddenText(html string) bool {
	return html != "" && hiddenTextRe.MatchString(html)
}
