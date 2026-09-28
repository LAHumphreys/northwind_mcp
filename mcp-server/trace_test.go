package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/afb/mcp-northwind-server/internal/trace"
)

// TestTraceAttributesSQLToMCPRequest checks that, with tracing on, a tool
// call is recorded along with the SQL it ran and the rows it returned, all
// sharing one request id.
func TestTraceAttributesSQLToMCPRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := requirePool(t, ctx, trace.QueryTracer{})

	path := filepath.Join(t.TempDir(), "trace.jsonl")
	rec, err := trace.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	server := newServer(pool)
	server.AddReceivingMiddleware(rec.Middleware())

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "trace-test", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}

	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_customer_info",
		Arguments: map[string]any{"customer_id": "ALFKI"},
	}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	session.Close()
	serverSession.Close()
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	events := readEvents(t, path)

	var callID int64
	for _, ev := range events {
		if ev.Kind == "mcp.request" && ev.Method == "tools/call" {
			callID = ev.RequestID
		}
	}
	if callID == 0 {
		t.Fatalf("no mcp.request for tools/call in %d events", len(events))
	}

	got := map[string]trace.Event{}
	for _, ev := range events {
		if ev.RequestID == callID {
			got[ev.Kind] = ev
		}
	}
	for _, kind := range []string{"mcp.request", "sql.query", "sql.rows", "sql.result", "mcp.response"} {
		if _, ok := got[kind]; !ok {
			t.Errorf("no %s event for request %d", kind, callID)
		}
	}
	if q := got["sql.query"]; len(q.Args) != 1 || q.Args[0] != "ALFKI" {
		t.Errorf("sql.query args = %v, want [ALFKI]", q.Args)
	}
	if r := got["sql.rows"]; r.Label != "customer_info" {
		t.Errorf("sql.rows label = %q, want customer_info", r.Label)
	}
	if res := got["mcp.response"]; res.Error != "" || len(res.Result) == 0 {
		t.Errorf("mcp.response error=%q result_len=%d", res.Error, len(res.Result))
	}
}

func readEvents(t *testing.T, path string) []trace.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var events []trace.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var ev trace.Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("bad trace line %q: %v", sc.Text(), err)
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}
