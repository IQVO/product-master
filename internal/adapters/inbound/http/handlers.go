package http

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

// skuParam returns the {sku} path segment, unescaped (an encoded '/' or
// space reaches the use case and is rejected there as invalid-sku).
func skuParam(r *http.Request) (string, error) {
	raw := chi.URLParam(r, "sku")
	sku, err := url.PathUnescape(raw)
	if err != nil {
		return "", product.ErrInvalidSKU
	}
	return sku, nil
}

func createdOr200(created bool) int {
	if created {
		return http.StatusCreated
	}
	return http.StatusOK
}

// handleRegisterProduct backs PUT /products/{sku}: 201 registered, 200
// already registered (description replaced or unchanged).
func (s *Server) handleRegisterProduct(w http.ResponseWriter, r *http.Request) {
	sku, err := skuParam(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req registerProductRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	res, err := s.RegisterProduct.Handle(r.Context(), usecases.RegisterProductCommand{SKU: sku, Description: req.Description})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, createdOr200(res.Created), toProduct(res.Product))
}

// handleGetProduct backs GET /products/{sku}.
func (s *Server) handleGetProduct(w http.ResponseWriter, r *http.Request) {
	p, err := s.loadProduct(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toProduct(p))
}

func (s *Server) loadProduct(r *http.Request) (*product.Product, error) {
	sku, err := skuParam(r)
	if err != nil {
		return nil, err
	}
	return s.GetProduct.Handle(r.Context(), sku)
}

// handleClassifyProduct backs PUT /products/{sku}/classification: 201 when
// the product had no classification, else 200.
func (s *Server) handleClassifyProduct(w http.ResponseWriter, r *http.Request) {
	sku, err := skuParam(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req classifyProductRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	dot := 0
	if req.DOTHazardClass != nil {
		if *req.DOTHazardClass == 0 {
			// 0 is the domain's "not recorded"; an explicit 0 is out of range.
			writeError(w, r, product.ErrInvalidDOTHazardClass)
			return
		}
		dot = *req.DOTHazardClass
	}
	res, err := s.ClassifyProduct.Handle(r.Context(), usecases.ClassifyProductCommand{
		SKU: sku, HandlingTags: req.HandlingTags, TemperatureClass: req.TemperatureClass, DOTHazardClass: dot,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	body, err := toProductClassification(res.Product)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, createdOr200(res.Created), body)
}

// handleGetClassification backs GET /products/{sku}/classification: 404
// product-not-found or product-classification-not-found.
func (s *Server) handleGetClassification(w http.ResponseWriter, r *http.Request) {
	p, err := s.loadProduct(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	body, err := toProductClassification(p)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// dimensions converts the request into the use-case input, rejecting a
// missing field as malformed-request.
func (d dimensionsRequest) dimensions() (usecases.DimensionsInput, error) {
	fields := []struct {
		name string
		v    *int64
	}{{"lengthMm", d.LengthMm}, {"widthMm", d.WidthMm}, {"heightMm", d.HeightMm}, {"weightG", d.WeightG}}
	for _, f := range fields {
		if f.v == nil {
			return usecases.DimensionsInput{}, fmt.Errorf("%w: %s is required", errMalformedRequest, f.name)
		}
	}
	return usecases.DimensionsInput{LengthMm: *d.LengthMm, WidthMm: *d.WidthMm, HeightMm: *d.HeightMm, WeightG: *d.WeightG}, nil
}

// handleDeclareDimensions backs PUT /products/{sku}/dimensions/declared.
func (s *Server) handleDeclareDimensions(w http.ResponseWriter, r *http.Request) {
	sku, err := skuParam(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req dimensionsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	dims, err := req.dimensions()
	if err != nil {
		writeError(w, r, err)
		return
	}
	p, err := s.DeclareDimensions.Handle(r.Context(), usecases.DeclareDimensionsCommand{SKU: sku, DimensionsInput: dims})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toPhysicalProfile(p.PhysicalProfile(), p.Version()))
}

// handleRecordMeasurement backs PUT /products/{sku}/dimensions/measured:
// 409 stale-measurement for an older reading.
func (s *Server) handleRecordMeasurement(w http.ResponseWriter, r *http.Request) {
	sku, err := skuParam(r)
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
	p, err := s.RecordMeasurement.Handle(r.Context(), usecases.RecordMeasurementCommand{
		SKU: sku, DimensionsInput: dims, MeasuredAt: req.MeasuredAt, DeviceID: req.DeviceID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toPhysicalProfile(p.PhysicalProfile(), p.Version()))
}

// handleGetPhysicalProfile backs GET /products/{sku}/physical-profile.
func (s *Server) handleGetPhysicalProfile(w http.ResponseWriter, r *http.Request) {
	p, err := s.loadProduct(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toPhysicalProfile(p.PhysicalProfile(), p.Version()))
}

// handleListProducts backs GET /products?limit=&cursor=&handlingTag=&classified=.
func (s *Server) handleListProducts(w http.ResponseWriter, r *http.Request) {
	q, err := parseListQuery(r.URL.Query())
	if err != nil {
		writeError(w, r, err)
		return
	}
	page, err := s.ListProducts.Handle(r.Context(), q)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := productPageResponse{Items: make([]productResponse, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, p := range page.Items {
		out.Items = append(out.Items, toProduct(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func parseListQuery(values url.Values) (usecases.ListProductsQuery, error) {
	q := usecases.ListProductsQuery{Cursor: values.Get("cursor"), HandlingTag: values.Get("handlingTag")}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit == 0 {
			return q, fmt.Errorf("%w: limit must be an integer 1..%d", usecases.ErrInvalidListQuery, usecases.MaxListLimit)
		}
		q.Limit = limit
	}
	if raw := values.Get("classified"); raw != "" {
		switch raw {
		case "true", "false":
			v := raw == "true"
			q.Classified = &v
		default:
			return q, fmt.Errorf("%w: classified must be true or false", usecases.ErrInvalidListQuery)
		}
	}
	return q, nil
}
