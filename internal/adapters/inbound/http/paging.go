package http

import (
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/claudioed/product-master/internal/adapters/inbound/http/page"
	"github.com/claudioed/product-master/internal/domain/product"
)

// The 400 problems of the paging contract (REST API guideline 3.4 and 4.2).
var (
	errInvalidLimit          = errors.New("limit must be an integer between 1 and 500")
	errInvalidCursor         = errors.New("cursor is not a valid page token")
	errUnknownQueryParameter = errors.New("unknown query parameter")
)

// pageParams reads limit and cursor of a collection GET and rejects any query
// parameter the operation does not declare (filters lists the declared ones).
func pageParams(r *http.Request, filters ...string) (int, *page.Position, error) {
	if name := page.Unknown(r, filters...); name != "" {
		return 0, nil, fmt.Errorf("%w: %q is not a parameter of this operation", errUnknownQueryParameter, sanitizeLog(name))
	}
	limit, err := page.Limit(r)
	if err != nil {
		return 0, nil, errInvalidLimit
	}
	after, err := page.After(r)
	if err != nil {
		return 0, nil, errInvalidCursor
	}
	return limit, after, nil
}

// packLevelRank mirrors the domain order EACH < INNER < CASE < PALLET.
var packLevelRank = map[product.PackLevel]int{"EACH": 1, "INNER": 2, "CASE": 3, "PALLET": 4}

// packLevelPosition is the keyset of a definition: (rank, effectiveFrom) then
// the GTIN, which is unique across every definition ever (a GTIN is never
// reassigned), so ties never skip or repeat a row.
func packLevelPosition(d product.PackLevelDef) page.Position {
	return page.Position{
		Sort: fmt.Sprintf("%d|%020d", packLevelRank[d.Level()], d.EffectiveFrom().UTC().UnixNano()),
		ID:   d.GTIN().String(),
	}
}

func afterPosition(cur, after page.Position) bool {
	return cur.Sort > after.Sort || (cur.Sort == after.Sort && cur.ID > after.ID)
}

// toPackLevelList pages the product's pack levels (lowest level first, oldest
// first within a level) after the cursor position.
func toPackLevelList(p *product.Product, limit int, after *page.Position) packLevelListResponse {
	var rows []product.PackLevelDef
	for _, d := range p.PackLevels().Levels() {
		if after == nil || afterPosition(packLevelPosition(d), *after) {
			rows = append(rows, d)
		}
	}
	sortPackLevels(rows)
	pg := page.Of(rows, limit, packLevelPosition)
	out := packLevelListResponse{SKU: string(p.SKU()), Version: p.Version(), NextCursor: pg.NextCursor, Items: make([]packLevelResponse, len(pg.Items))}
	for i, d := range pg.Items {
		out.Items[i] = toPackLevel(d)
	}
	return out
}

func sortPackLevels(rows []product.PackLevelDef) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := packLevelPosition(rows[i]), packLevelPosition(rows[j])
		return afterPosition(b, a)
	})
}
