package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/product-master/internal/domain/product"
)

// mimeTypeJSON is the MIME type of every resource this server serves.
const mimeTypeJSON = "application/json"

// packLevelsTemplateURI is the ONE resource this server offers (Agentic
// Standard v2, AS-3; ADR 0014): a scoped read model this context owns — the
// packaging hierarchy of a single SKU, retired levels included, bounded to
// that one product and built from the same GetProduct read use case the
// get_pack_levels tool and REST use. It is not a dump of any table.
const packLevelsTemplateURI = "warehouse://product-master/skus/{sku}/pack-levels"

// registerResources adds the pack-levels resource template. Resources are L1
// by nature (facts, no side effects), so they carry no tool Meta.
func (d Deps) registerResources(server *mcp.Server) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: packLevelsTemplateURI,
		Name:        "pack levels of one SKU",
		Description: "The packaging hierarchy of one product: every pack level it ever declared (EACH, INNER, CASE, PALLET), retired ones included, with GTINs, content quantities, dimensions and the effective dates. Bounded to the one SKU; not a catalogue dump.",
		MIMEType:    mimeTypeJSON,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		sku, err := skuOfPackLevelsURI(req.Params.URI)
		if err != nil {
			return nil, err
		}
		p, err := d.GetProduct.Handle(ctx, sku)
		if err != nil {
			return nil, mapError(err)
		}
		body, err := json.Marshal(packLevelsOutputFor(p))
		if err != nil {
			return nil, fmt.Errorf("render pack levels resource: %w", err)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      req.Params.URI,
				MIMEType: mimeTypeJSON,
				Text:     string(body),
			}},
		}, nil
	})
}

// packLevelsOutputFor renders a product's packaging hierarchy as the view the
// get_pack_levels tool returns (same shape on both surfaces).
func packLevelsOutputFor(p *product.Product) packLevelsOutput {
	levels := p.PackLevels().Levels()
	out := packLevelsOutput{SKU: string(p.SKU()), Version: p.Version(), Items: make([]packLevelView, len(levels))}
	for i, l := range levels {
		out.Items[i] = toPackLevel(l)
	}
	return out
}

// skuOfPackLevelsURI extracts the {sku} segment of a
// warehouse://product-master/skus/{sku}/pack-levels URI. The value is
// model-supplied and therefore untrusted; a malformed URI is rejected rather
// than silently defaulted (the SKU's own charset rules are enforced by the
// use case).
func skuOfPackLevelsURI(uri string) (string, error) {
	const (
		scheme = "warehouse://product-master/skus/"
		suffix = "/pack-levels"
	)
	if !strings.HasPrefix(uri, scheme) {
		return "", fmt.Errorf("resource uri %q must start with %q", uri, scheme)
	}
	rest := strings.TrimPrefix(uri, scheme)
	sku, ok := strings.CutSuffix(rest, suffix)
	if !ok {
		return "", fmt.Errorf("resource uri %q must end with %q", uri, suffix)
	}
	if parts := strings.Split(rest, "/"); len(parts) != 2 || parts[0] == "" {
		return "", fmt.Errorf("resource uri %q must be warehouse://product-master/skus/{sku}/pack-levels with a non-empty sku", uri)
	}
	return sku, nil
}
