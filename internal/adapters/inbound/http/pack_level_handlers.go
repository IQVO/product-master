package http

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

// The packaging hierarchy routes (ADR 0011).

// pathParam returns a path segment, unescaped.
func pathParam(r *http.Request, name string) (string, error) {
	raw, err := url.PathUnescape(chi.URLParam(r, name))
	if err != nil {
		return "", errMalformedRequest
	}
	return raw, nil
}

// handleResolveGTIN backs GET /gtins/{gtin}: the path GTIN is normalised to
// 14 digits first; a retired level answers with active=false.
func (s *Server) handleResolveGTIN(w http.ResponseWriter, r *http.Request) {
	raw, err := pathParam(r, "gtin")
	if err != nil {
		writeError(w, r, err)
		return
	}
	res, err := s.ResolveGTIN.Handle(r.Context(), raw)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toGTIN(res))
}

// handleListPackLevels backs GET /products/{sku}/pack-levels (retired levels included).
func (s *Server) handleListPackLevels(w http.ResponseWriter, r *http.Request) {
	p, err := s.loadProduct(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toPackLevelList(p))
}

// handleDeclarePackLevel backs PUT /products/{sku}/pack-levels/{level}: 201
// when the product had no level there, else 200.
func (s *Server) handleDeclarePackLevel(w http.ResponseWriter, r *http.Request) {
	sku, err := skuParam(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	level, err := pathParam(r, "level")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req packLevelRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	in := usecases.PackLevelInput{
		Level: level, GTIN: req.GTIN, ChildGTIN: req.ChildGTIN, QtyOfChild: req.QtyOfChild, UnitsPerPack: req.UnitsPerPack,
		IsOrderable: req.IsOrderable, IsDespatchUnit: req.IsDespatchUnit,
	}
	if req.Dims != nil {
		dims, err := req.Dims.dimensions()
		if err != nil {
			writeError(w, r, err)
			return
		}
		in.Dims = &dims
	}
	res, err := s.DeclarePackLevel.Handle(r.Context(), usecases.DeclarePackLevelCommand{SKU: sku, PackLevelInput: in})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, createdOr200(res.Created), toPackLevelResource(res.Product, res.Def))
}

// handleRetirePackLevel backs POST /products/{sku}/pack-levels/{level}:retire.
// The body is optional ({"reason": "..."}); retiring a retired level answers
// 200 with that level and changes nothing.
func (s *Server) handleRetirePackLevel(w http.ResponseWriter, r *http.Request) {
	sku, err := skuParam(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	level, err := pathParam(r, "level")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req retirePackLevelRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := s.RetirePackLevel.Handle(r.Context(), usecases.RetirePackLevelCommand{SKU: sku, Level: level, Reason: req.Reason})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toPackLevelResource(p, latestAt(p, product.PackLevel(level))))
}

// handleRecordPackLevelMeasurement backs
// PUT /products/{sku}/pack-levels/{level}/measured: 409 stale-measurement for
// an older reading.
func (s *Server) handleRecordPackLevelMeasurement(w http.ResponseWriter, r *http.Request) {
	sku, err := skuParam(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	level, err := pathParam(r, "level")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req measurementRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	dims, err := req.dimensions()
	if err != nil {
		writeError(w, r, err)
		return
	}
	p, err := s.RecordPackLevelMeasurement.Handle(r.Context(), usecases.RecordPackLevelMeasurementCommand{
		SKU: sku, Level: level, DimensionsInput: dims, MeasuredAt: req.MeasuredAt, DeviceID: req.DeviceID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toPackLevelResource(p, latestAt(p, product.PackLevel(level))))
}

// latestAt is the most recent definition at level (the active one, else the
// last retired one).
func latestAt(p *product.Product, level product.PackLevel) product.PackLevelDef {
	var out product.PackLevelDef
	for _, d := range p.PackLevels().Levels() {
		if d.Level() == level {
			out = d
		}
	}
	return out
}

// decodeOptionalJSON is decodeJSON for a body that may be absent or empty.
func decodeOptionalJSON(r *http.Request, dest any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		return errors.Join(errMalformedRequest, err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return decodeJSON(r, dest)
}
