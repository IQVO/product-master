package kafka

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/claudioed/product-master/internal/domain/product"
)

// analyticsEnvelope is envelope() on the analytics stream: the only
// difference from the integration bytes is the dataschema stream segment.
func analyticsEnvelope(eventName, timeStr, data string) string {
	return strings.Replace(envelope(eventName, timeStr, data),
		"urn:warehouse:product-master:events:", "urn:warehouse:product-master:analytics:", 1)
}

// goldenEvents is every published event with its expected `data` (the
// integration payload, which the analytics payload equals: ADR 0006).
func goldenEvents(t *testing.T) []struct {
	name, time, data string
	event            product.Event
} {
	t.Helper()
	d, m := declared(), measured()
	return []struct {
		name, time, data string
		event            product.Event
	}{
		{"ProductRegistered", "2026-10-06T21:00:00Z", `{"sku":"SKU-1","description":"Lithium battery pack 12V","version":1}`,
			product.ProductRegistered{Header: header(1, 0), Description: "Lithium battery pack 12V"}},
		{"ProductDescriptionChanged", "2026-10-06T21:01:00Z", `{"sku":"SKU-1","description":"Lithium battery pack 12V, 7Ah","version":2}`,
			product.ProductDescriptionChanged{Header: header(2, 1), Description: "Lithium battery pack 12V, 7Ah"}},
		{"ProductClassified", "2026-10-06T21:02:00Z", `{"sku":"SKU-1","handling_tags":["Hazmat","TemperatureSensitive"],"temperature_class":"Frozen","dot_hazard_class":3,"classification_source":"native","version":3}`,
			product.ProductClassified{Header: header(3, 2), Source: product.SourceNative,
				Classification: mustClassification(t, []product.HandlingTag{product.TemperatureSensitive, product.Hazmat}, product.Frozen, 3)}},
		{"ProductDimensionsDeclared", "2026-10-06T21:03:00Z", `{"sku":"SKU-1",` +
			`"declared":{"length_mm":200,"width_mm":120,"height_mm":80,"weight_g":1500,"volume_mm3":1920000},` +
			`"effective":{"length_mm":200,"width_mm":120,"height_mm":80,"weight_g":1500,"volume_mm3":1920000},` +
			`"effective_source":"declared","discrepancy":false,"version":4}`,
			product.ProductDimensionsDeclared{Header: header(4, 3), Profile: product.RehydratePhysicalProfile(&d, nil)}},
		{"ProductMeasured", "2026-10-06T21:04:00Z", `{"sku":"SKU-1",` +
			`"declared":{"length_mm":200,"width_mm":120,"height_mm":80,"weight_g":1500,"volume_mm3":1920000},` +
			`"measured":{"length_mm":205,"width_mm":121,"height_mm":82,"weight_g":1720,"volume_mm3":2034010,"measured_at":"2026-10-06T14:05:00Z","device_id":"CUBISCAN-03"},` +
			`"effective":{"length_mm":205,"width_mm":121,"height_mm":82,"weight_g":1720,"volume_mm3":2034010},` +
			`"effective_source":"measured","discrepancy":true,"version":5}`,
			product.ProductMeasured{Header: header(5, 4), Profile: product.RehydratePhysicalProfile(&d, &m)}},
	}
}

// TestAnalyticsEncoder_GoldenWireFormat pins the exact analytics bytes of
// every published type and the outbox row around them.
func TestAnalyticsEncoder_GoldenWireFormat(t *testing.T) {
	for _, tc := range goldenEvents(t) {
		t.Run(tc.name, func(t *testing.T) {
			msgs, err := (&AnalyticsEncoder{NewID: fixedID}).Encode(tc.event)
			if err != nil {
				t.Fatal(err)
			}
			if len(msgs) != 1 {
				t.Fatalf("got %d messages", len(msgs))
			}
			msg := msgs[0]
			if want := analyticsEnvelope(tc.name, tc.time, tc.data); string(msg.Value) != want {
				t.Fatalf("value:\n got %s\nwant %s", msg.Value, want)
			}
			wantType := "com.warehouse.wms.product-master.product." + tc.name
			if msg.EventType != wantType || msg.EventID != goldenID || msg.Topic != "warehouse.product-master.analytics" ||
				string(msg.Key) != "SKU-1" || msg.Subject != "SKU-1" ||
				msg.DataSchema != "urn:warehouse:product-master:analytics:"+tc.name+":v1" {
				t.Fatalf("row = %+v", msg)
			}
			if len(msg.Headers) != 1 || msg.Headers[0].Key != "content-type" || msg.Headers[0].Value != "application/cloudevents+json; charset=UTF-8" {
				t.Fatalf("headers = %+v", msg.Headers)
			}
		})
	}
}

// TestFanoutEncoder_TwoRowsOneID: integration row first (byte-identical to
// Encoder's), analytics row second, the same id and type on both.
func TestFanoutEncoder_TwoRowsOneID(t *testing.T) {
	ids := []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}
	n := 0
	f := &FanoutEncoder{Integration: &Encoder{}, Analytics: &AnalyticsEncoder{}, NewID: func() string { n++; return ids[n-1] }}
	reg := product.ProductRegistered{Header: header(1, 0), Description: "d"}
	cls := product.ProductClassified{Header: header(2, 1), Source: product.SourceNative,
		Classification: mustClassification(t, []product.HandlingTag{product.Fragile}, "", 0)}
	msgs, err := f.Encode(reg, cls)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4", len(msgs))
	}
	for i, ev := range []product.Event{reg, cls} {
		in, an := msgs[2*i], msgs[2*i+1]
		if in.EventID != ids[i] || an.EventID != ids[i] {
			t.Fatalf("event %d ids = %s / %s, want %s on both", i, in.EventID, an.EventID, ids[i])
		}
		if in.Topic != Topic || an.Topic != AnalyticsTopic {
			t.Fatalf("event %d topics = %s / %s", i, in.Topic, an.Topic)
		}
		if in.EventType != an.EventType || in.Subject != an.Subject || string(in.Key) != string(an.Key) {
			t.Fatalf("event %d rows differ beyond topic/dataschema: %+v / %+v", i, in, an)
		}
		single, err := (&Encoder{NewID: func() string { return ids[i] }}).Encode(ev)
		if err != nil {
			t.Fatal(err)
		}
		if string(in.Value) != string(single[0].Value) {
			t.Fatalf("event %d integration bytes changed by the fan-out:\n got %s\nwant %s", i, in.Value, single[0].Value)
		}
		wantAnalytics := strings.Replace(string(single[0].Value), ":events:", ":analytics:", 1)
		if string(an.Value) != wantAnalytics {
			t.Fatalf("event %d analytics bytes:\n got %s\nwant %s", i, an.Value, wantAnalytics)
		}
	}
}

func TestFanoutEncoder_RejectsAnUnpublishedEvent(t *testing.T) {
	msgs, err := NewFanoutEncoder().Encode(product.ProductRegistered{Header: header(1, 0)}, unknownEvent{header(1, 0)})
	if err == nil || msgs != nil {
		t.Fatalf("got %v, %v; want an error and no messages", msgs, err)
	}
}

func TestFanoutEncoder_ZeroValueDefaults(t *testing.T) {
	msgs, err := (&FanoutEncoder{}).Encode(product.ProductRegistered{Header: header(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Topic != Topic || msgs[1].Topic != AnalyticsTopic || msgs[0].EventID != msgs[1].EventID {
		t.Fatalf("got %+v", msgs)
	}
	if _, err := uuid.Parse(msgs[0].EventID); err != nil {
		t.Fatalf("id %q is not a UUID: %v", msgs[0].EventID, err)
	}
}

func TestAnalyticsEncoder_DefaultsAndTopicOverride(t *testing.T) {
	msgs, err := (&AnalyticsEncoder{Topic: "itest-analytics"}).Encode(
		product.ProductRegistered{Header: header(1, 0)}, product.ProductMeasured{Header: header(2, 0), Profile: product.PhysicalProfile{}})
	if err != nil {
		t.Fatal(err)
	}
	if msgs[0].Topic != "itest-analytics" || msgs[0].EventID == msgs[1].EventID {
		t.Fatalf("got %+v", msgs)
	}
	if _, err := uuid.Parse(msgs[0].EventID); err != nil {
		t.Fatalf("id %q is not a UUID: %v", msgs[0].EventID, err)
	}
	if _, err := (&AnalyticsEncoder{}).Encode(unknownEvent{header(1, 0)}); err == nil {
		t.Fatal("an unpublished event must be rejected")
	}
}
