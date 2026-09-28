// Package trace records MCP traffic and the SQL it triggers as JSON Lines.
//
// It is wired in only when the server is started with NORTHWIND_TRACE_FILE
// set. A Recorder is attached to the request context by the MCP middleware;
// the pgx QueryTracer and Rows helper pick it up from there, so every SQL
// event carries the id of the MCP request that caused it.
package trace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Event is one JSONL record. Kind is one of:
//
//	mcp.request   an inbound JSON-RPC request or notification (Method, Params)
//	mcp.response  the result or error returned for it (Method, Result | Error)
//	sql.query     a statement sent to Postgres (SQL, Args)
//	sql.rows      the scanned rows a db function returned (Label, Rows)
//	sql.result    the command tag Postgres reported (CommandTag, DurationMS)
//
// RequestID ties SQL events to the MCP request that triggered them.
type Event struct {
	Seq        int64           `json:"seq"`
	Time       time.Time       `json:"ts"`
	Kind       string          `json:"kind"`
	RequestID  int64           `json:"request_id,omitempty"`
	Method     string          `json:"method,omitempty"`
	Params     json.RawMessage `json:"params,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	SQL        string          `json:"sql,omitempty"`
	Args       []any           `json:"args,omitempty"`
	CommandTag string          `json:"command_tag,omitempty"`
	Label      string          `json:"label,omitempty"`
	Rows       any             `json:"rows,omitempty"`
	DurationMS float64         `json:"duration_ms,omitempty"`
}

// Recorder appends events to a writer. It is safe for concurrent use.
type Recorder struct {
	mu     sync.Mutex
	w      io.WriteCloser
	seq    atomic.Int64
	reqSeq atomic.Int64
}

// Open creates or appends to the JSONL file at path.
func Open(path string) (*Recorder, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening trace file: %w", err)
	}
	return &Recorder{w: f}, nil
}

// New returns a Recorder writing to w.
func New(w io.WriteCloser) *Recorder { return &Recorder{w: w} }

// Close flushes and closes the underlying writer.
func (r *Recorder) Close() error { return r.w.Close() }

func (r *Recorder) emit(ev Event) {
	ev.Seq = r.seq.Add(1)
	ev.Time = time.Now().UTC()
	line, err := json.Marshal(ev)
	if err != nil {
		line = []byte(fmt.Sprintf(`{"seq":%d,"kind":"trace.error","error":%q}`, ev.Seq, err.Error()))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = r.w.Write(append(line, '\n'))
}

type ctxKey int

const (
	recorderKey ctxKey = iota
	requestIDKey
	queryStartKey
)

func fromContext(ctx context.Context) (*Recorder, int64, bool) {
	r, ok := ctx.Value(recorderKey).(*Recorder)
	if !ok || r == nil {
		return nil, 0, false
	}
	id, _ := ctx.Value(requestIDKey).(int64)
	return r, id, true
}

func marshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(map[string]string{"marshal_error": err.Error()})
	}
	return b
}

// Middleware returns MCP receiving middleware that records every inbound
// request and its response, and tags the request context so SQL activity
// performed while handling it is attributed to it.
func (r *Recorder) Middleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			id := r.reqSeq.Add(1)
			ctx = context.WithValue(ctx, recorderKey, r)
			ctx = context.WithValue(ctx, requestIDKey, id)

			r.emit(Event{Kind: "mcp.request", RequestID: id, Method: method, Params: marshal(req.GetParams())})
			start := time.Now()
			res, err := next(ctx, method, req)

			ev := Event{Kind: "mcp.response", RequestID: id, Method: method, DurationMS: ms(start)}
			if err != nil {
				ev.Error = err.Error()
			} else {
				ev.Result = marshal(res)
			}
			r.emit(ev)
			return res, err
		}
	}
}

// QueryTracer is a pgx.QueryTracer that records statements and their outcome
// against the MCP request in the context. It is a no-op for contexts without
// a Recorder (pool pings, health checks, untraced servers).
type QueryTracer struct{}

var _ pgx.QueryTracer = QueryTracer{}

// TraceQueryStart implements pgx.QueryTracer.
func (QueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	r, id, ok := fromContext(ctx)
	if !ok {
		return ctx
	}
	r.emit(Event{Kind: "sql.query", RequestID: id, SQL: strings.TrimSpace(data.SQL), Args: data.Args})
	return context.WithValue(ctx, queryStartKey, time.Now())
}

// TraceQueryEnd implements pgx.QueryTracer.
func (QueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	r, id, ok := fromContext(ctx)
	if !ok {
		return
	}
	ev := Event{Kind: "sql.result", RequestID: id, CommandTag: data.CommandTag.String()}
	if start, ok := ctx.Value(queryStartKey).(time.Time); ok {
		ev.DurationMS = ms(start)
	}
	if data.Err != nil {
		ev.Error = data.Err.Error()
	}
	r.emit(ev)
}

// Rows records the scanned result set a db function is about to return.
// It is a no-op when the context carries no Recorder.
func Rows(ctx context.Context, label string, rows any) {
	r, id, ok := fromContext(ctx)
	if !ok {
		return
	}
	r.emit(Event{Kind: "sql.rows", RequestID: id, Label: label, Rows: rows})
}

func ms(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}
