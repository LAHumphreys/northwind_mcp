package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/afb/mcp-northwind-server/internal/db"
)

// TestServerSmoke drives the MCP server end to end over an in-memory transport
// against a live Northwind database. It skips when no database is reachable
// unless NORTHWIND_TEST_REQUIRE_DB is set (CI and the session-start hook set it).
func TestServerSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx)
	if err != nil {
		if os.Getenv("NORTHWIND_TEST_REQUIRE_DB") != "" {
			t.Fatalf("Northwind database required but unavailable: %v", err)
		}
		t.Skipf("Northwind database unavailable: %v", err)
	}
	defer pool.Close()

	server := newServer(pool)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "smoke-test", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer session.Close()

	t.Run("tools/list advertises every tool", func(t *testing.T) {
		res, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		got := make(map[string]bool, len(res.Tools))
		for _, tool := range res.Tools {
			got[tool.Name] = true
		}
		for _, name := range []string{"get_products", "get_customers", "get_customer_info", "get_top_performing_products"} {
			if !got[name] {
				t.Errorf("tool %q not advertised; got %v", name, res.Tools)
			}
		}
	})

	t.Run("tools/call get_customer_info", func(t *testing.T) {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "get_customer_info",
			Arguments: map[string]any{"customer_id": "ALFKI"},
		})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if res.IsError {
			t.Fatalf("tool returned error: %+v", res.Content)
		}
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatalf("marshal structured content: %v", err)
		}
		var info db.CustomerInfo
		if err := json.Unmarshal(raw, &info); err != nil {
			t.Fatalf("unmarshal CustomerInfo: %v", err)
		}
		if info.CustomerID != "ALFKI" || info.CompanyName != "Alfreds Futterkiste" {
			t.Errorf("unexpected customer: %+v", info)
		}
		if info.TotalOrders != 6 {
			t.Errorf("total_orders = %d, want 6", info.TotalOrders)
		}
	})

	t.Run("tools/call unknown customer is a tool error", func(t *testing.T) {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "get_customer_info",
			Arguments: map[string]any{"customer_id": "NOPE"},
		})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if !res.IsError {
			t.Errorf("expected IsError for unknown customer, got %+v", res.StructuredContent)
		}
	})

	t.Run("resources/read products", func(t *testing.T) {
		res, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: productsURI})
		if err != nil {
			t.Fatalf("ReadResource: %v", err)
		}
		if len(res.Contents) != 1 {
			t.Fatalf("contents len = %d, want 1", len(res.Contents))
		}
		var products []db.Product
		if err := json.Unmarshal([]byte(res.Contents[0].Text), &products); err != nil {
			t.Fatalf("unmarshal products: %v", err)
		}
		if len(products) != 77 {
			t.Errorf("products len = %d, want 77", len(products))
		}
	})
}
