package mcp

import (
	"context"

	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

func (d Deps) getProduct(ctx context.Context, in skuInput) (productView, error) {
	p, err := d.GetProduct.Handle(ctx, in.SKU)
	if err != nil {
		return productView{}, mapError(err)
	}
	return toProduct(p), nil
}

func (d Deps) listProducts(ctx context.Context, in listProductsInput) (productPageView, error) {
	page, err := d.ListProducts.Handle(ctx, usecases.ListProductsQuery{
		Limit: in.Limit, Cursor: in.Cursor, HandlingTag: in.HandlingTag, Classified: in.Classified,
	})
	if err != nil {
		return productPageView{}, mapError(err)
	}
	out := productPageView{Items: make([]productView, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, p := range page.Items {
		out.Items = append(out.Items, toProduct(p))
	}
	return out, nil
}

func (d Deps) getProductClassification(ctx context.Context, in skuInput) (productClassificationView, error) {
	p, err := d.GetProduct.Handle(ctx, in.SKU)
	if err != nil {
		return productClassificationView{}, mapError(err)
	}
	c, source, err := p.Classification()
	if err != nil {
		return productClassificationView{}, mapError(err)
	}
	cv := toClassification(c, source)
	return productClassificationView{
		SKU: string(p.SKU()), HandlingTags: cv.HandlingTags, TemperatureClass: cv.TemperatureClass,
		DOTHazardClass: cv.DOTHazardClass, ClassificationSource: cv.ClassificationSource, Version: p.Version(),
	}, nil
}

func (d Deps) getPhysicalProfile(ctx context.Context, in skuInput) (physicalProfileOutput, error) {
	p, err := d.GetProduct.Handle(ctx, in.SKU)
	if err != nil {
		return physicalProfileOutput{}, mapError(err)
	}
	pv := toPhysicalProfile(p.PhysicalProfile())
	return physicalProfileOutput{
		SKU: string(p.SKU()), Declared: pv.Declared, Measured: pv.Measured, Effective: pv.Effective,
		EffectiveSource: pv.EffectiveSource, Discrepancy: pv.Discrepancy, Version: p.Version(),
	}, nil
}

func (d Deps) resolveGTIN(ctx context.Context, in gtinInput) (gtinView, error) {
	res, err := d.ResolveGTIN.Handle(ctx, in.GTIN)
	if err != nil {
		return gtinView{}, mapError(err)
	}
	pl := toPackLevel(res.Def)
	return gtinView{
		GTIN: pl.GTIN, SKU: string(res.SKU), Level: pl.Level, UnitsPerPack: pl.UnitsPerPack, ChildGTIN: pl.ChildGTIN,
		Active: pl.Active, EffectiveFrom: pl.EffectiveFrom, EffectiveTo: pl.EffectiveTo,
	}, nil
}

func (d Deps) getPackLevels(ctx context.Context, in skuInput) (packLevelsOutput, error) {
	p, err := d.GetProduct.Handle(ctx, in.SKU)
	if err != nil {
		return packLevelsOutput{}, mapError(err)
	}
	levels := p.PackLevels().Levels()
	out := packLevelsOutput{SKU: string(p.SKU()), Version: p.Version(), Items: make([]packLevelView, len(levels))}
	for i, l := range levels {
		out.Items[i] = toPackLevel(l)
	}
	return out, nil
}

func (d Deps) declarePackLevel(ctx context.Context, in declarePackLevelInput) (packLevelWriteOutput, error) {
	cmd := usecases.DeclarePackLevelCommand{SKU: in.SKU, PackLevelInput: usecases.PackLevelInput{
		Level: in.Level, GTIN: in.GTIN, ChildGTIN: in.ChildGTIN, QtyOfChild: in.QtyOfChild, UnitsPerPack: in.UnitsPerPack,
		IsOrderable: in.IsOrderable, IsDespatchUnit: in.IsDespatchUnit,
	}}
	if in.Dims != nil {
		cmd.Dims = &usecases.DimensionsInput{LengthMm: in.Dims.LengthMm, WidthMm: in.Dims.WidthMm, HeightMm: in.Dims.HeightMm, WeightG: in.Dims.WeightG}
	}
	res, err := d.DeclarePackLevel.Handle(ctx, cmd)
	if err != nil {
		return packLevelWriteOutput{}, mapError(err)
	}
	return packLevelWriteOutput{SKU: string(res.Product.SKU()), Version: res.Product.Version(), PackLevel: toPackLevel(res.Def)}, nil
}

func (d Deps) retirePackLevel(ctx context.Context, in retirePackLevelInput) (packLevelWriteOutput, error) {
	p, err := d.RetirePackLevel.Handle(ctx, usecases.RetirePackLevelCommand{SKU: in.SKU, Level: in.Level, Reason: in.Reason})
	if err != nil {
		return packLevelWriteOutput{}, mapError(err)
	}
	var latest product.PackLevelDef
	for _, l := range p.PackLevels().Levels() {
		if string(l.Level()) == in.Level {
			latest = l
		}
	}
	return packLevelWriteOutput{SKU: string(p.SKU()), Version: p.Version(), PackLevel: toPackLevel(latest)}, nil
}
