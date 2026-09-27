// Package db provides access to the Northwind Postgres database.
package db

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Product mirrors a row of the Northwind "products" table.
type Product struct {
	ProductID       int32    `json:"product_id"`
	ProductName     string   `json:"product_name"`
	SupplierID      *int32   `json:"supplier_id,omitempty"`
	CategoryID      *int32   `json:"category_id,omitempty"`
	QuantityPerUnit *string  `json:"quantity_per_unit,omitempty"`
	UnitPrice       *float32 `json:"unit_price,omitempty"`
	UnitsInStock    *int32   `json:"units_in_stock,omitempty"`
	UnitsOnOrder    *int32   `json:"units_on_order,omitempty"`
	ReorderLevel    *int32   `json:"reorder_level,omitempty"`
	Discontinued    int32    `json:"discontinued"`
}

// CustomerInfo combines customer contact details with order activity.
type CustomerInfo struct {
	CustomerID    string     `json:"customer_id"`
	CompanyName   string     `json:"company_name"`
	ContactName   *string    `json:"contact_name"`
	ContactTitle  *string    `json:"contact_title"`
	Phone         *string    `json:"phone"`
	City          *string    `json:"city"`
	Country       *string    `json:"country"`
	TotalOrders   int64      `json:"total_orders"`
	LifetimeValue float64    `json:"lifetime_value"`
	LastOrderDate *time.Time `json:"last_order_date"`
}

// Customer mirrors a row of the Northwind customers table.
type Customer struct {
	CustomerID   string  `json:"customer_id" jsonschema:"Unique Northwind customer code, such as ALFKI."`
	CompanyName  string  `json:"company_name" jsonschema:"Name of the customer's company."`
	ContactName  *string `json:"contact_name" jsonschema:"Primary contact person; null if not recorded."`
	ContactTitle *string `json:"contact_title" jsonschema:"Contact person's job title; null if not recorded."`
	Address      *string `json:"address" jsonschema:"Street or mailing address; null if not recorded."`
	City         *string `json:"city" jsonschema:"Customer's city; null if not recorded."`
	Region       *string `json:"region" jsonschema:"State, province, or other region; null if not recorded."`
	PostalCode   *string `json:"postal_code" jsonschema:"Postal or ZIP code; null if not recorded."`
	Country      *string `json:"country" jsonschema:"Customer's country; null if not recorded."`
	Phone        *string `json:"phone" jsonschema:"Customer's phone number; null if not recorded."`
	Fax          *string `json:"fax" jsonschema:"Customer's fax number; null if not recorded."`
}

// TopPerformingProduct summarizes a product's sales performance.
type TopPerformingProduct struct {
	ProductID      int32   `json:"product_id"`
	ProductName    string  `json:"product_name"`
	CategoryName   string  `json:"category_name"`
	TotalUnitsSold int64   `json:"total_units_sold"`
	NetSales       float64 `json:"net_sales"`
}

// NewPool builds a pgx connection pool from NORTHWIND_DB_* environment variables.
func NewPool(ctx context.Context) (*pgxpool.Pool, error) {
	host := envOrDefault("NORTHWIND_DB_HOST", "localhost")
	port := envOrDefault("NORTHWIND_DB_PORT", "5432")
	user := envOrDefault("NORTHWIND_DB_USER", "northwind")
	password := envOrDefault("NORTHWIND_DB_PASSWORD", "northwind")
	dbname := envOrDefault("NORTHWIND_DB_NAME", "northwind")

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", user, password, host, port, dbname)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("creating pgx pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	return pool, nil
}

// ListProducts returns every row in the products table, ordered by product_id.
func ListProducts(ctx context.Context, pool *pgxpool.Pool) ([]Product, error) {
	rows, err := pool.Query(ctx, `
		SELECT product_id, product_name, supplier_id, category_id,
		       quantity_per_unit, unit_price, units_in_stock,
		       units_on_order, reorder_level, discontinued
		FROM products
		ORDER BY product_id
	`)
	if err != nil {
		return nil, fmt.Errorf("querying products: %w", err)
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(
			&p.ProductID, &p.ProductName, &p.SupplierID, &p.CategoryID,
			&p.QuantityPerUnit, &p.UnitPrice, &p.UnitsInStock,
			&p.UnitsOnOrder, &p.ReorderLevel, &p.Discontinued,
		); err != nil {
			return nil, fmt.Errorf("scanning product row: %w", err)
		}
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating product rows: %w", err)
	}
	return products, nil
}

// ListCustomers returns every customer, ordered by customer_id.
func ListCustomers(ctx context.Context, pool *pgxpool.Pool) ([]Customer, error) {
	rows, err := pool.Query(ctx, `
		SELECT customer_id, company_name, contact_name, contact_title, address,
		       city, region, postal_code, country, phone, fax
		FROM customers
		ORDER BY customer_id
	`)
	if err != nil {
		return nil, fmt.Errorf("querying customers: %w", err)
	}
	defer rows.Close()

	var customers []Customer
	for rows.Next() {
		var customer Customer
		if err := rows.Scan(
			&customer.CustomerID, &customer.CompanyName, &customer.ContactName,
			&customer.ContactTitle, &customer.Address, &customer.City,
			&customer.Region, &customer.PostalCode, &customer.Country,
			&customer.Phone, &customer.Fax,
		); err != nil {
			return nil, fmt.Errorf("scanning customer row: %w", err)
		}
		customers = append(customers, customer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating customer rows: %w", err)
	}
	return customers, nil
}

// GetCustomerInfo returns contact and order summary data for one customer.
func GetCustomerInfo(ctx context.Context, pool *pgxpool.Pool, customerID string) (CustomerInfo, error) {
	var customer CustomerInfo
	var lastOrderDate *time.Time
	err := pool.QueryRow(ctx, `
		SELECT c.customer_id, c.company_name, c.contact_name, c.contact_title,
		       c.phone, c.city, c.country,
		       COUNT(DISTINCT o.order_id) AS total_orders,
		       COALESCE(SUM(od.unit_price * od.quantity * (1 - od.discount)), 0)::double precision AS lifetime_value,
		       MAX(o.order_date) AS last_order_date
		FROM customers c
		LEFT JOIN orders o ON c.customer_id = o.customer_id
		LEFT JOIN order_details od ON o.order_id = od.order_id
		WHERE c.customer_id = $1
		GROUP BY c.customer_id
	`, customerID).Scan(
		&customer.CustomerID, &customer.CompanyName, &customer.ContactName,
		&customer.ContactTitle, &customer.Phone, &customer.City, &customer.Country,
		&customer.TotalOrders, &customer.LifetimeValue, &lastOrderDate,
	)
	if err != nil {
		return CustomerInfo{}, fmt.Errorf("querying customer info: %w", err)
	}
	customer.LastOrderDate = lastOrderDate
	return customer, nil
}

// GetTopPerformingProducts returns the top products by net sales, limited to the given count.
func GetTopPerformingProducts(ctx context.Context, pool *pgxpool.Pool, limit int32) ([]TopPerformingProduct, error) {
	rows, err := pool.Query(ctx, `
		SELECT
		    p.product_id,
		    p.product_name,
		    cat.category_name,
		    SUM(od.quantity) AS total_units_sold,
		    ROUND(SUM(od.unit_price * od.quantity * (1 - od.discount))::numeric, 2) AS net_sales
		FROM order_details od
		JOIN products p ON od.product_id = p.product_id
		JOIN categories cat ON p.category_id = cat.category_id
		GROUP BY p.product_id, p.product_name, cat.category_name
		ORDER BY net_sales DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("querying top performing products: %w", err)
	}
	defer rows.Close()

	var products []TopPerformingProduct
	for rows.Next() {
		var p TopPerformingProduct
		if err := rows.Scan(
			&p.ProductID, &p.ProductName, &p.CategoryName,
			&p.TotalUnitsSold, &p.NetSales,
		); err != nil {
			return nil, fmt.Errorf("scanning top performing product row: %w", err)
		}
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating top performing product rows: %w", err)
	}
	return products, nil
}

func envOrDefault(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
