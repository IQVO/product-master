// Package mcp is the inbound Model Context Protocol adapter: it exposes this
// bounded context to the AI ecosystem as a second driving adapter over the
// same application-layer read use cases the REST adapter uses. It is built on
// the official MCP Go SDK and served over Streamable HTTP only.
//
// Per docs/adr/0005-mcp-server-adoption.md the surface is READ-ONLY: every
// tool reads product master data (get_product, list_products,
// get_product_classification, get_physical_profile); writes stay on REST,
// and governance_test.go fails the build on a write-verb tool name. This
// package depends inward on the application layer and the domain only --
// never on an outbound adapter or the REST adapter -- and nothing else may
// depend on it (internal/architecture's TestMCPAdapterDependencyRule). The
// composition root (cmd/mcp) wires concrete repositories into the use cases.
// There is no auth of any kind (fleet-wide revert 2026-09-11;
// TestNoAuthMiddlewareReintroduced).
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP initialize
// handshake.
const (
	serverName    = "product-master-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server at initialize.
const instructions = "Product master data of the warehouse (read-only): look up one SKU with get_product " +
	"(description, handling classification, physical profile, version), page through products with list_products " +
	"(optionally only those carrying a handling_tag, or only classified/unclassified ones), read just the handling " +
	"classification with get_product_classification (handling tags Hazmat, Fragile, TemperatureSensitive, Oversized, " +
	"HighValue; temperature class; DOT hazard class; whether it is native or legacy-import) or just the declared vs " +
	"measured dimensions with get_physical_profile. Nothing here changes data: registering, classifying and " +
	"measuring products are REST operations of product-master."

// NewServer builds the MCP server for this bounded context with every tool
// registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	return server
}

// Handler returns the Streamable HTTP handler for the MCP server.
func Handler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}
