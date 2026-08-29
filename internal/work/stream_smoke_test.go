package work

import (
	"bytes"
	"strings"
	"testing"
)

func TestClaudeStreamWriterSmoke(t *testing.T) {
	sample := `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"a\nb\nc"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Here's the output."}]}}
{"type":"result","subtype":"success","result":"Here's the output."}
`
	var out bytes.Buffer
	var result string
	sw := &claudeStreamWriter{out: &out, result: &result}

	// Feed it in two chunks, split mid-line, to exercise buffering.
	n := len(sample) / 2
	if _, err := sw.Write([]byte(sample[:n])); err != nil {
		t.Fatal(err)
	}
	if _, err := sw.Write([]byte(sample[n:])); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	t.Logf("formatted:\n%s", got)
	if !strings.Contains(got, "→ Bash") {
		t.Errorf("expected tool_use line, got: %s", got)
	}
	if !strings.Contains(got, "a\nb\nc") {
		t.Errorf("expected tool_result content, got: %s", got)
	}
	if !strings.Contains(got, "Here's the output.") {
		t.Errorf("expected assistant text, got: %s", got)
	}
	if result != "Here's the output." {
		t.Errorf("expected captured result, got: %q", result)
	}
}
