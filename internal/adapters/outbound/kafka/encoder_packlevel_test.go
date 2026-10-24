package kafka

import (
	"strings"
	"testing"
	"time"

	"github.com/claudioed/product-master/internal/domain/product"
	"github.com/claudioed/product-master/internal/domain/shared/gs1"
)

func gtin(t *testing.T, raw string) gs1.GTIN {
	t.Helper()
	g, err := gs1.ParseGTIN(raw)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

var effectiveFrom = time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)

func caseDef(t *testing.T, withDims bool) product.PackLevelDef {
	t.Helper()
	spec := product.PackLevelSpec{
		Level: product.PackLevelCase, GTIN: gtin(t, "24006381333935"), ChildGTIN: gtin(t, "4006381333931"),
		QtyOfChild: 12, UnitsPerPack: 12, IsOrderable: true, IsDespatchUnit: true,
	}
	if withDims {
		d := product.RehydrateUnitDimensions(400, 300, 200, 6500)
		spec.Dims = &d
	}
	return product.RehydratePackLevelDef(spec, nil, effectiveFrom, nil)
}

func eachDef(t *testing.T) product.PackLevelDef {
	t.Helper()
	return product.RehydratePackLevelDef(product.PackLevelSpec{Level: product.PackLevelEach, GTIN: gtin(t, "4006381333931"), UnitsPerPack: 1}, nil, effectiveFrom, nil)
}

// TestEncoder_PackLevelGoldenWireFormat pins the exact bytes of the three
// pack level events (snake_case, as every payload of this service) and the
// outbox row around them: the FULL CloudEvents type on the integration topic.
func TestEncoder_PackLevelGoldenWireFormat(t *testing.T) {
	measuredAt := time.Date(2026, 10, 6, 14, 5, 0, 0, time.UTC)
	m := product.RehydrateMeasurement(product.RehydrateUnitDimensions(410, 310, 210, 6900), measuredAt, "CUBISCAN-03")
	manual := product.RehydrateMeasurement(product.RehydrateUnitDimensions(1, 2, 3, 4), measuredAt, "")
	cases := []struct {
		name  string
		event product.Event
		want  string
	}{
		{
			"ProductPackLevelDeclared",
			product.ProductPackLevelDeclared{Header: header(3, 0), Def: caseDef(t, true)},
			envelope("ProductPackLevelDeclared", "2026-10-06T21:00:00Z", `{"sku":"SKU-1","level":"CASE","gtin":"24006381333935","child_gtin":"04006381333931","qty_of_child":12,"units_per_pack":12,`+
				`"dims":{"length_mm":400,"width_mm":300,"height_mm":200,"weight_g":6500},"is_orderable":true,"is_despatch_unit":true,"effective_from":"2026-10-06T20:00:00Z","version":3}`),
		},
		{
			"ProductPackLevelDeclared",
			product.ProductPackLevelDeclared{Header: header(2, 0), Def: eachDef(t)},
			envelope("ProductPackLevelDeclared", "2026-10-06T21:00:00Z", `{"sku":"SKU-1","level":"EACH","gtin":"04006381333931","units_per_pack":1,`+
				`"is_orderable":false,"is_despatch_unit":false,"effective_from":"2026-10-06T20:00:00Z","version":2}`),
		},
		{
			"ProductPackLevelRetired",
			product.ProductPackLevelRetired{Header: header(4, 1), Level: product.PackLevelCase, GTIN: gtin(t, "24006381333935"), EffectiveTo: time.Date(2026, 10, 6, 21, 1, 0, 0, time.UTC), Reason: "superseded"},
			envelope("ProductPackLevelRetired", "2026-10-06T21:01:00Z", `{"sku":"SKU-1","level":"CASE","gtin":"24006381333935","effective_to":"2026-10-06T21:01:00Z","reason":"superseded","version":4}`),
		},
		{
			"ProductPackLevelMeasured",
			product.ProductPackLevelMeasured{Header: header(5, 2), Level: product.PackLevelCase, GTIN: gtin(t, "24006381333935"), Measurement: m},
			envelope("ProductPackLevelMeasured", "2026-10-06T21:02:00Z", `{"sku":"SKU-1","level":"CASE","gtin":"24006381333935",`+
				`"dims":{"length_mm":410,"width_mm":310,"height_mm":210,"weight_g":6900},"measured_at":"2026-10-06T14:05:00Z","device_id":"CUBISCAN-03","version":5}`),
		},
		{
			"ProductPackLevelMeasured",
			product.ProductPackLevelMeasured{Header: header(6, 3), Level: product.PackLevelEach, GTIN: gtin(t, "4006381333931"), Measurement: manual},
			envelope("ProductPackLevelMeasured", "2026-10-06T21:03:00Z", `{"sku":"SKU-1","level":"EACH","gtin":"04006381333931",`+
				`"dims":{"length_mm":1,"width_mm":2,"height_mm":3,"weight_g":4},"measured_at":"2026-10-06T14:05:00Z","version":6}`),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/"+strings.Split(strings.Split(tc.want, `"level":"`)[1], `"`)[0], func(t *testing.T) {
			msgs, err := (&Encoder{NewID: fixedID}).Encode(tc.event)
			if err != nil {
				t.Fatal(err)
			}
			if len(msgs) != 1 {
				t.Fatalf("got %d messages", len(msgs))
			}
			msg := msgs[0]
			if string(msg.Value) != tc.want {
				t.Fatalf("value:\n got %s\nwant %s", msg.Value, tc.want)
			}
			if msg.EventType != "com.warehouse.wms.product-master.product."+tc.name || msg.EventID != goldenID || msg.Topic != "warehouse.product-master.events" ||
				string(msg.Key) != "SKU-1" || msg.Subject != "SKU-1" ||
				msg.DataSchema != "urn:warehouse:product-master:events:"+tc.name+":v1" {
				t.Fatalf("row = %+v", msg)
			}
			if len(msg.Headers) != 1 || msg.Headers[0].Key != "content-type" {
				t.Fatalf("headers = %+v", msg.Headers)
			}
		})
	}
}

// Pack level events go to the integration topic ONLY: the analytics stream is
// the data product of the five unit events (ADR 0006) and its projector would
// dead-letter a type it does not know.
func TestPackLevelEventsAreIntegrationOnly(t *testing.T) {
	events := []product.Event{
		product.ProductPackLevelDeclared{Header: header(2, 0), Def: eachDef(t)},
		product.ProductPackLevelRetired{Header: header(3, 0), Level: product.PackLevelEach, GTIN: gtin(t, "4006381333931"), EffectiveTo: effectiveFrom, Reason: "x"},
		product.ProductPackLevelMeasured{Header: header(4, 0), Level: product.PackLevelEach, GTIN: gtin(t, "4006381333931"), Measurement: measured()},
	}
	for _, ev := range events {
		t.Run(ev.EventName(), func(t *testing.T) {
			if _, err := (&AnalyticsEncoder{NewID: fixedID}).Encode(ev); err == nil || !strings.Contains(err.Error(), "not published") {
				t.Fatalf("analytics encoder must refuse loudly, got %v", err)
			}
			msgs, err := (&FanoutEncoder{NewID: fixedID}).Encode(ev)
			if err != nil || len(msgs) != 1 || msgs[0].Topic != Topic {
				t.Fatalf("fanout = %+v, %v; want exactly the integration message", msgs, err)
			}
		})
	}
	// mixed with a unit event: that one still fans out to both streams, in order
	msgs, err := (&FanoutEncoder{NewID: fixedID}).Encode(
		product.ProductPackLevelDeclared{Header: header(2, 0), Def: eachDef(t)},
		product.ProductRegistered{Header: header(1, 0), Description: "d"},
	)
	if err != nil || len(msgs) != 3 {
		t.Fatalf("msgs = %d, %v", len(msgs), err)
	}
	topics := []string{msgs[0].Topic, msgs[1].Topic, msgs[2].Topic}
	if topics[0] != Topic || topics[1] != Topic || topics[2] != AnalyticsTopic {
		t.Fatalf("topics = %v", topics)
	}
}
