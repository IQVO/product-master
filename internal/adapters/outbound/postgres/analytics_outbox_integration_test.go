//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/product-master/internal/adapters/outbound/clock"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/application/outbox"
	"github.com/claudioed/product-master/internal/application/ports"
	"github.com/claudioed/product-master/internal/application/usecases"
)

// ADR 0006: every domain event is enqueued TWICE in the same unit of work, on
// the integration topic and on the analytics topic, under ONE CloudEvents id.

type analyticsOutboxRow struct {
	eventID, topic, eventType, dataschema, value string
}

func analyticsOutboxRows(t *testing.T, pool *pgxpool.Pool) []analyticsOutboxRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT event_id, topic, event_type, dataschema, convert_from(value, 'UTF8') FROM outbox_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []analyticsOutboxRow
	for rows.Next() {
		var r analyticsOutboxRow
		if err := rows.Scan(&r.eventID, &r.topic, &r.eventType, &r.dataschema, &r.value); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// failingOutbox inserts for real, then fails: the surrounding unit of work
// must roll BOTH rows back.
type failingOutbox struct {
	inner ports.OutboxRepository
	err   error
}

func (f failingOutbox) Insert(ctx context.Context, msgs ...outbox.Message) error {
	if err := f.inner.Insert(ctx, msgs...); err != nil {
		return err
	}
	return f.err
}

func fanoutWriter(pool *pgxpool.Pool) usecases.Writer {
	return usecases.Writer{
		Products: postgres.NewProductRepo(pool), Outbox: postgres.NewOutboxRepo(pool),
		Encoder: outboundkafka.NewFanoutEncoder(), UoW: postgres.NewUnitOfWork(pool), Clock: clock.System{},
	}
}

func TestFanout_Postgres_EachEventLandsOnBothTopicsUnderOneIDInOneTransaction(t *testing.T) {
	ctx := context.Background()
	pool := startPostgresPool(t)
	w := fanoutWriter(pool)

	if _, err := (&usecases.RegisterProduct{Writer: w}).Handle(ctx, usecases.RegisterProductCommand{SKU: "SKU-1", Description: "d"}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&usecases.ClassifyProduct{Writer: w}).Handle(ctx, usecases.ClassifyProductCommand{SKU: "SKU-1", HandlingTags: []string{"Hazmat"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&usecases.DeclareDimensions{Writer: w}).Handle(ctx, usecases.DeclareDimensionsCommand{SKU: "SKU-1",
		DimensionsInput: usecases.DimensionsInput{LengthMm: 200, WidthMm: 120, HeightMm: 80, WeightG: 1500}}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-time.Minute)
	if _, err := (&usecases.RecordMeasurement{Writer: w}).Handle(ctx, usecases.RecordMeasurementCommand{SKU: "SKU-1",
		DimensionsInput: usecases.DimensionsInput{LengthMm: 300, WidthMm: 120, HeightMm: 80, WeightG: 1500}, MeasuredAt: at}); err != nil {
		t.Fatal(err)
	}

	rows := analyticsOutboxRows(t, pool)
	events := []string{"ProductRegistered", "ProductClassified", "ProductDimensionsDeclared", "ProductMeasured"}
	if len(rows) != 2*len(events) {
		t.Fatalf("outbox rows = %d, want %d (four events x two topics)", len(rows), 2*len(events))
	}
	seen := map[string]bool{}
	for i, ev := range events {
		in, an := rows[2*i], rows[2*i+1]
		if in.topic != outboundkafka.Topic || an.topic != outboundkafka.AnalyticsTopic {
			t.Fatalf("%s topics = %q, %q", ev, in.topic, an.topic)
		}
		if in.eventID == "" || in.eventID != an.eventID {
			t.Errorf("%s: ids %q vs %q must be the SAME per occurrence", ev, in.eventID, an.eventID)
		}
		if seen[in.eventID] {
			t.Errorf("%s: occurrence id %q reused", ev, in.eventID)
		}
		seen[in.eventID] = true
		if in.eventType != "com.warehouse.wms.product-master.product."+ev || an.eventType != in.eventType {
			t.Errorf("%s: types %q / %q", ev, in.eventType, an.eventType)
		}
		if in.dataschema != "urn:warehouse:product-master:events:"+ev+":v1" || an.dataschema != "urn:warehouse:product-master:analytics:"+ev+":v1" {
			t.Errorf("%s: dataschemas %q / %q", ev, in.dataschema, an.dataschema)
		}
		if !strings.Contains(an.value, `"id":"`+an.eventID+`"`) || !strings.Contains(in.value, `"id":"`+in.eventID+`"`) {
			t.Errorf("%s: the persisted bytes do not carry the row's CloudEvents id", ev)
		}
		// Payloads are equal (ADR 0006 section 2): the bytes differ only in the dataschema.
		if strings.Replace(in.value, ":events:", ":analytics:", 1) != an.value {
			t.Errorf("%s: analytics bytes differ from the integration bytes beyond the dataschema:\n%s\n%s", ev, in.value, an.value)
		}
	}
}

func TestFanout_Postgres_AFailureWritesNeitherTopicsRows(t *testing.T) {
	ctx := context.Background()
	pool := startPostgresPool(t)
	w := fanoutWriter(pool)
	if _, err := (&usecases.RegisterProduct{Writer: w}).Handle(ctx, usecases.RegisterProductCommand{SKU: "SKU-1", Description: "d"}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("injected failure after the real outbox write")
	failing := w
	failing.Outbox = failingOutbox{inner: w.Outbox, err: boom}
	if _, err := (&usecases.ClassifyProduct{Writer: failing}).Handle(ctx, usecases.ClassifyProductCommand{SKU: "SKU-1", HandlingTags: []string{"Fragile"}}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the injected failure", err)
	}
	rows := analyticsOutboxRows(t, pool)
	if len(rows) != 2 {
		t.Fatalf("outbox rows = %d, want 2 (only the ProductRegistered pair survives)", len(rows))
	}
	if rows[1].topic != outboundkafka.AnalyticsTopic || !strings.HasSuffix(rows[1].eventType, "ProductRegistered") {
		t.Fatalf("surviving rows = %+v", rows)
	}
}
