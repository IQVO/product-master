package main

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	inbounda2a "github.com/claudioed/product-master/internal/adapters/inbound/a2a"
	"github.com/claudioed/product-master/internal/adapters/outbound/clock"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/memory"
	"github.com/claudioed/product-master/internal/application/usecases"
)

// newWiredServer wires the binary's router and A2A handler over the in-memory
// repositories, TEST DOUBLES: the binary itself has no in-memory path (depsFor
// takes any usecases.Writer). It returns the in-memory outbox and a Writer
// to seed with.
func newWiredServer(t *testing.T) (*httptest.Server, *memory.OutboxRepo, usecases.Writer) {
	t.Helper()
	products, ob := memory.NewProductRepo(), memory.NewOutboxRepo()
	w := usecases.Writer{
		Products: products, Outbox: ob, Encoder: outboundkafka.NewFanoutEncoder(),
		UoW: memory.NewUnitOfWork(products, ob), Clock: clock.System{},
	}
	card := inbounda2a.Card(inbounda2a.Config{AgentURL: testAgentURL, Version: "test"})
	srv := httptest.NewServer(newRouter(inbounda2a.Handler(inbounda2a.NewExecutor(depsFor(w))), card))
	t.Cleanup(srv.Close)
	return srv, ob, w
}

func sendSkill(t *testing.T, endpoint, skill string, args map[string]any) map[string]any {
	t.Helper()
	ctx := context.Background()
	client, err := a2aclient.NewFromCard(ctx, inbounda2a.Card(inbounda2a.Config{AgentURL: endpoint, Version: "test"}))
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}
	resp, err := client.SendMessage(ctx, &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))})
	if err != nil {
		t.Fatalf("SendMessage %s: %v", skill, err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) == 0 {
		t.Fatalf("reply = %T, want a message", resp)
	}
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", msg.Parts[0].Content)
	}
	env, _ := data.Value.(map[string]any)
	return env
}

// The write skill reaches the use cases, the outbox and the encoder this
// binary wires, over BOTH mount points: a declaration enqueues the pack level
// event (the relay in cmd/api publishes it), a rejected one enqueues nothing.
func TestDepsFor_DeclarePackLevelEnqueuesThroughTheWiredWriter(t *testing.T) {
	srv, ob, w := newWiredServer(t)
	if _, err := (&usecases.RegisterProduct{Writer: w}).Handle(context.Background(), usecases.RegisterProductCommand{SKU: "SKU-1", Description: "Frozen peas"}); err != nil {
		t.Fatal(err)
	}
	base := len(ob.Messages())

	for _, path := range []string{"/", "/a2a"} {
		env := sendSkill(t, srv.URL+path, "declare_pack_level", map[string]any{"sku": "SKU-404", "level": "EACH", "gtin": "4006381333931"})
		if env["problem"] == nil {
			t.Fatalf("%s: declare on an unknown SKU = %v, want a problem", path, env)
		}
	}
	if got := len(ob.Messages()); got != base {
		t.Fatalf("outbox rows %d -> %d after rejected declarations", base, got)
	}

	env := sendSkill(t, srv.URL+"/a2a", "declare_pack_level", map[string]any{"sku": "SKU-1", "level": "EACH", "gtin": "4006381333931"})
	if env["problem"] != nil || env["data"] == nil {
		t.Fatalf("declare = %v, want data", env)
	}
	if got := len(ob.Messages()); got != base+1 {
		t.Fatalf("outbox rows %d -> %d, want exactly one new row from the delegated write", base, got)
	}
	read := sendSkill(t, srv.URL+"/", "product_profile", map[string]any{"gtin": "4006381333931"})
	if read["problem"] != nil {
		t.Fatalf("product_profile by the declared GTIN = %v", read)
	}
}

// depsFor offers exactly the curated surface and shares one Writer.
func TestDepsFor_WiresEveryUseCase(t *testing.T) {
	d := depsFor(usecases.Writer{Products: memory.NewProductRepo()})
	if d.GetProduct == nil || d.ResolveGTIN == nil || d.DeclarePackLevel == nil {
		t.Fatalf("deps = %+v, want every use case wired", d)
	}
}
