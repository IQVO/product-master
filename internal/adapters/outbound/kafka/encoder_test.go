package kafka

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/claudioed/product-master/internal/domain/product"
)

const goldenID = "1b0c9a4e-2f7d-4a63-9d1e-5a7c3e2b9f10"

func fixedID() string { return goldenID }

func mustClassification(t *testing.T, tags []product.HandlingTag, tc product.TemperatureClass, dot product.DOTHazardClass) product.Classification {
	t.Helper()
	c, err := product.NewClassification(tags, tc, dot)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func declared() product.UnitDimensions { return product.RehydrateUnitDimensions(200, 120, 80, 1500) }

func measured() product.Measurement {
	return product.RehydrateMeasurement(product.RehydrateUnitDimensions(205, 121, 82, 1720), time.Date(2026, 10, 6, 14, 5, 0, 0, time.UTC), "CUBISCAN-03")
}

func header(version int64, minute int) product.Header {
	return product.Header{SKU: "SKU-1", Version: version, At: time.Date(2026, 10, 6, 21, minute, 0, 0, time.UTC)}
}

func envelope(eventName, timeStr, data string) string {
	return `{"specversion":"1.0","id":"` + goldenID + `","source":"/warehouse/product-master",` +
		`"type":"com.warehouse.wms.product-master.product.` + eventName + `","subject":"SKU-1",` +
		`"datacontenttype":"application/json","dataschema":"urn:warehouse:product-master:events:` + eventName + `:v1",` +
		`"time":"` + timeStr + `","data":` + data + `}`
}

// TestEncoder_GoldenWireFormat pins the exact bytes of every published type
// (all CloudEvents attributes + payload, as apis/asyncapi.yaml's examples)
// and the outbox row around them (content-type header, key, topic, full
// type, dataschema, id).
func TestEncoder_GoldenWireFormat(t *testing.T) {
	d, m := declared(), measured()
	cases := []struct {
		name  string
		event product.Event
		want  string
	}{
		{
			"ProductRegistered",
			product.ProductRegistered{Header: header(1, 0), Description: "Lithium battery pack 12V"},
			envelope("ProductRegistered", "2026-10-06T21:00:00Z", `{"sku":"SKU-1","description":"Lithium battery pack 12V","version":1}`),
		},
		{
			"ProductDescriptionChanged",
			product.ProductDescriptionChanged{Header: header(2, 1), Description: "Lithium battery pack 12V, 7Ah"},
			envelope("ProductDescriptionChanged", "2026-10-06T21:01:00Z", `{"sku":"SKU-1","description":"Lithium battery pack 12V, 7Ah","version":2}`),
		},
		{
			"ProductClassified",
			product.ProductClassified{Header: header(3, 2), Source: product.SourceNative,
				Classification: mustClassification(t, []product.HandlingTag{product.TemperatureSensitive, product.Hazmat}, product.Frozen, 3)},
			envelope("ProductClassified", "2026-10-06T21:02:00Z", `{"sku":"SKU-1","handling_tags":["Hazmat","TemperatureSensitive"],"temperature_class":"Frozen","dot_hazard_class":3,"classification_source":"native","version":3}`),
		},
		{
			"ProductDimensionsDeclared",
			product.ProductDimensionsDeclared{Header: header(4, 3), Profile: product.RehydratePhysicalProfile(&d, nil)},
			envelope("ProductDimensionsDeclared", "2026-10-06T21:03:00Z", `{"sku":"SKU-1",`+
				`"declared":{"length_mm":200,"width_mm":120,"height_mm":80,"weight_g":1500,"volume_mm3":1920000},`+
				`"effective":{"length_mm":200,"width_mm":120,"height_mm":80,"weight_g":1500,"volume_mm3":1920000},`+
				`"effective_source":"declared","discrepancy":false,"version":4}`),
		},
		{
			"ProductMeasured",
			product.ProductMeasured{Header: header(5, 4), Profile: product.RehydratePhysicalProfile(&d, &m)},
			envelope("ProductMeasured", "2026-10-06T21:04:00Z", `{"sku":"SKU-1",`+
				`"declared":{"length_mm":200,"width_mm":120,"height_mm":80,"weight_g":1500,"volume_mm3":1920000},`+
				`"measured":{"length_mm":205,"width_mm":121,"height_mm":82,"weight_g":1720,"volume_mm3":2034010,"measured_at":"2026-10-06T14:05:00Z","device_id":"CUBISCAN-03"},`+
				`"effective":{"length_mm":205,"width_mm":121,"height_mm":82,"weight_g":1720,"volume_mm3":2034010},`+
				`"effective_source":"measured","discrepancy":true,"version":5}`),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
			wantType := "com.warehouse.wms.product-master.product." + tc.name
			if msg.EventType != wantType || msg.EventID != goldenID || msg.Topic != "warehouse.product-master.events" ||
				string(msg.Key) != "SKU-1" || msg.Subject != "SKU-1" ||
				msg.DataSchema != "urn:warehouse:product-master:events:"+tc.name+":v1" {
				t.Fatalf("row = %+v", msg)
			}
			if len(msg.Headers) != 1 || msg.Headers[0].Key != "content-type" || msg.Headers[0].Value != "application/cloudevents+json; charset=UTF-8" {
				t.Fatalf("headers = %+v", msg.Headers)
			}
		})
	}
}

func TestEncoder_OptionalFieldsAreOmitted(t *testing.T) {
	c := mustClassification(t, []product.HandlingTag{product.Fragile}, "", 0)
	manual := product.RehydrateMeasurement(product.RehydrateUnitDimensions(1, 2, 3, 4), time.Date(2026, 10, 6, 14, 5, 0, 500000000, time.UTC), "")
	msgs, err := (&Encoder{NewID: fixedID}).Encode(
		product.ProductClassified{Header: header(2, 0), Classification: c, Source: product.SourceLegacyImport},
		product.ProductMeasured{Header: header(3, 0), Profile: product.RehydratePhysicalProfile(nil, &manual)},
		product.ProductDimensionsDeclared{Header: header(4, 0), Profile: product.PhysicalProfile{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantData := []string{
		`"data":{"sku":"SKU-1","handling_tags":["Fragile"],"classification_source":"legacy-import","version":2}}`,
		`"data":{"sku":"SKU-1","measured":{"length_mm":1,"width_mm":2,"height_mm":3,"weight_g":4,"volume_mm3":6,"measured_at":"2026-10-06T14:05:00.5Z"},` +
			`"effective":{"length_mm":1,"width_mm":2,"height_mm":3,"weight_g":4,"volume_mm3":6},"effective_source":"measured","discrepancy":false,"version":3}}`,
		`"data":{"sku":"SKU-1","effective_source":"none","discrepancy":false,"version":4}}`,
	}
	for i, want := range wantData {
		if !strings.HasSuffix(string(msgs[i].Value), want) {
			t.Errorf("message %d = %s\nwant suffix %s", i, msgs[i].Value, want)
		}
	}
}

type unknownEvent struct{ product.Header }

func (unknownEvent) EventName() string { return "Unknown" }

func TestEncoder_RejectsAnUnpublishedEvent(t *testing.T) {
	msgs, err := NewEncoder().Encode(product.ProductRegistered{Header: header(1, 0)}, unknownEvent{header(1, 0)})
	if err == nil || msgs != nil {
		t.Fatalf("got %v, %v; want an error and no messages", msgs, err)
	}
}

func TestEncoder_DefaultsAndTopicOverride(t *testing.T) {
	msgs, err := (&Encoder{Topic: "itest-topic"}).Encode(product.ProductRegistered{Header: header(1, 0)}, product.ProductRegistered{Header: header(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if msgs[0].Topic != "itest-topic" {
		t.Fatalf("topic = %q", msgs[0].Topic)
	}
	if _, err := uuid.Parse(msgs[0].EventID); err != nil || msgs[0].EventID == msgs[1].EventID {
		t.Fatalf("ids %q %q must be distinct UUIDs (%v)", msgs[0].EventID, msgs[1].EventID, err)
	}
	if !strings.Contains(string(msgs[0].Value), `"id":"`+msgs[0].EventID+`"`) {
		t.Fatal("the row id must be the CloudEvents id")
	}
}
