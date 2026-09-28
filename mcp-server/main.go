package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/afb/mcp-northwind-server/internal/db"
	"github.com/afb/mcp-northwind-server/internal/trace"
)

const productsURI = "northwind://products"
const customersURI = "northwind://customers"
const customerInfoURITemplate = "northwind://customers/{customer_id}"

type customerInfoToolArgs struct {
	CustomerID string `json:"customer_id" jsonschema:"Customer ID, such as ALFKI."`
}

type topPerformingProductsToolArgs struct {
	Limit int32 `json:"limit" jsonschema:"Maximum number of products to return."`
}

func main() {
	log.Println("Starting MCP server")

	ctx := context.Background()

	// NORTHWIND_TRACE_FILE turns on JSONL tracing of every MCP request and
	// response plus the SQL (statements, args and rows) each one triggers.
	var (
		recorder *trace.Recorder
		tracer   pgx.QueryTracer
	)
	if path := envOrDefault("NORTHWIND_TRACE_FILE", ""); path != "" {
		rec, err := trace.Open(path)
		if err != nil {
			log.Fatalf("opening trace file: %v", err)
		}
		defer rec.Close()
		recorder, tracer = rec, trace.QueryTracer{}
		log.Printf("tracing MCP and SQL activity to %s", path)
	}

	pool, err := db.NewPoolWithTracer(ctx, tracer)
	if err != nil {
		log.Fatalf("connecting to Northwind database: %v", err)
	}
	defer pool.Close()

	server := newServer(pool)
	if recorder != nil {
		server.AddReceivingMiddleware(recorder.Middleware())
	}

	addr := envOrDefault("MCP_HTTP_ADDR", ":8080")
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true})

	log.Printf("northwind-mcp-server listening at %s/mcp", addr)
	if err := http.ListenAndServe(addr, http.StripPrefix("", mux(handler))); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

// newServer builds the MCP server and registers every resource, tool and
// prompt against the given connection pool. Kept separate from main so tests
// can drive the server over an in-memory transport.
func newServer(pool *pgxpool.Pool) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "northwind-mcp-server",
		Version: "0.1.0",
	}, nil)

	server.AddResource(&mcp.Resource{
		Name:        "get_products",
		Description: "All products listed in the Northwind database",
		URI:         productsURI,
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		products, err := db.ListProducts(ctx, pool)
		if err != nil {
			return nil, fmt.Errorf("listing products: %w", err)
		}
		data, err := json.Marshal(products)
		if err != nil {
			return nil, fmt.Errorf("marshaling products: %w", err)
		}

		log.Printf("Products found %d", len(products))

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      productsURI,
				MIMEType: "application/json",
				Text:     string(data),
			}},
		}, nil
	})

	server.AddResource(&mcp.Resource{
		Name:        "get_customers",
		Description: "All customers listed in the Northwind database",
		URI:         customersURI,
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		customers, err := db.ListCustomers(ctx, pool)
		if err != nil {
			return nil, fmt.Errorf("listing customers: %w", err)
		}
		data, err := json.Marshal(customers)
		if err != nil {
			return nil, fmt.Errorf("marshaling customers: %w", err)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      customersURI,
				MIMEType: "application/json",
				Text:     string(data),
			}},
		}, nil
	})

	server.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "get_customer_info",
		Description: "Customer contact details and order summary",
		URITemplate: customerInfoURITemplate,
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		resourceURI := req.Params.URI
		parsedURI, err := url.Parse(resourceURI)
		if err != nil || parsedURI.Scheme != "northwind" || parsedURI.Host != "customers" {
			return nil, mcp.ResourceNotFoundError(resourceURI)
		}
		customerID, err := url.PathUnescape(strings.TrimPrefix(parsedURI.EscapedPath(), "/"))
		if err != nil || customerID == "" || strings.Contains(customerID, "/") {
			return nil, mcp.ResourceNotFoundError(resourceURI)
		}

		customer, err := db.GetCustomerInfo(ctx, pool, customerID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, mcp.ResourceNotFoundError(resourceURI)
		}
		if err != nil {
			return nil, fmt.Errorf("getting customer info: %w", err)
		}
		data, err := json.Marshal(customer)
		if err != nil {
			return nil, fmt.Errorf("marshaling customer info: %w", err)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      resourceURI,
				MIMEType: "application/json",
				Text:     string(data),
			}},
		}, nil
	})

	mcp.AddTool[any, []db.Product](server, &mcp.Tool{
		Name:        "get_products",
		Description: "List all products in the Northwind database",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, []db.Product, error) {
		products, err := db.ListProducts(ctx, pool)
		if err != nil {
			return nil, nil, fmt.Errorf("listing products: %w", err)
		}
		return nil, products, nil
	})

	mcp.AddTool[any, []db.Customer](server, &mcp.Tool{
		Name:        "get_customers",
		Description: "List all customers in the Northwind database",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, []db.Customer, error) {
		customers, err := db.ListCustomers(ctx, pool)
		if err != nil {
			return nil, nil, fmt.Errorf("listing customers: %w", err)
		}
		return nil, customers, nil
	})

	mcp.AddTool[customerInfoToolArgs, db.CustomerInfo](server, &mcp.Tool{
		Name:        "get_customer_info",
		Description: "Get a customer's contact details and order summary",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args customerInfoToolArgs) (*mcp.CallToolResult, db.CustomerInfo, error) {
		customer, err := db.GetCustomerInfo(ctx, pool, args.CustomerID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, db.CustomerInfo{}, fmt.Errorf("customer %q not found", args.CustomerID)
		}
		if err != nil {
			return nil, db.CustomerInfo{}, fmt.Errorf("getting customer info: %w", err)
		}
		return nil, customer, nil
	})

	mcp.AddTool[topPerformingProductsToolArgs, []db.TopPerformingProduct](server, &mcp.Tool{
		Name:        "get_top_performing_products",
		Description: "List the top performing products by net sales",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args topPerformingProductsToolArgs) (*mcp.CallToolResult, []db.TopPerformingProduct, error) {
		products, err := db.GetTopPerformingProducts(ctx, pool, args.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("getting top performing products: %w", err)
		}
		return nil, products, nil
	})

	server.AddPrompt(&mcp.Prompt{
		Name:        "get_summary",
		Description: "Summarize product activity, stock levels, and value",
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{{
				Role: "user",
				Content: &mcp.TextContent{
					Text: `Return a product summary using the northwind://get_products resource. List
					- The total number of active items (not discontinued)
					- The 5 items with the most stock
					- The 5 lowest-stocked items
					- The 5 items with the highest unit price`,
				},
			}},
		}, nil
	})

	return server
}

// mux routes the /mcp path to the MCP handler.
func mux(handler http.Handler) http.Handler {
	m := http.NewServeMux()
	m.Handle("/mcp", handler)
	return m
}

func envOrDefault(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
