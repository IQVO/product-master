package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/claudioed/product-master/internal/adapters/outbound/clock"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/memory"
	"github.com/claudioed/product-master/internal/application/ports"
	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

// Valid GTINs (mod-10 check digit verified): each1 and case1 belong to SKU-1,
// each2 to SKU-2; variableMeasure has indicator digit 9.
const (
	each1           = "4006381333931"
	case1           = "24006381333935"
	each2           = "5901234123457"
	variableMeasure = "94006381333934"
)

// harness builds Deps over in-memory repositories, TEST DOUBLES: the binary
// itself has no in-memory path (cmd/a2a takes the Postgres ones). Seeding
// goes through the real use cases, the way the REST adapter would write.
type harness struct {
	deps     Deps
	outbox   *memory.OutboxRepo
	register *usecases.RegisterProduct
	classify *usecases.ClassifyProduct
	declare  *usecases.DeclareDimensions
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return harnessOver(t, memory.NewProductRepo())
}

func harnessOver(t *testing.T, products ports.ProductRepository) *harness {
	t.Helper()
	ob := memory.NewOutboxRepo()
	w := usecases.Writer{
		Products: products, Outbox: ob, Encoder: outboundkafka.NewFanoutEncoder(),
		UoW: memory.NewUnitOfWork(products.(memory.Snapshotter), ob), Clock: clock.System{},
	}
	return &harness{
		deps: Deps{
			GetProduct:       &usecases.GetProduct{Products: products},
			ResolveGTIN:      &usecases.ResolveGTIN{Products: products},
			DeclarePackLevel: &usecases.DeclarePackLevel{Writer: w},
		},
		outbox:   ob,
		register: &usecases.RegisterProduct{Writer: w},
		classify: &usecases.ClassifyProduct{Writer: w},
		declare:  &usecases.DeclareDimensions{Writer: w},
	}
}

func (h *harness) seed(t *testing.T, sku, description string) {
	t.Helper()
	if _, err := h.register.Handle(context.Background(), usecases.RegisterProductCommand{SKU: sku, Description: description}); err != nil {
		t.Fatalf("register %s: %v", sku, err)
	}
}

func (h *harness) seedClassified(t *testing.T, sku string) {
	t.Helper()
	h.seed(t, sku, "Frozen peas")
	if _, err := h.classify.Handle(context.Background(), usecases.ClassifyProductCommand{
		SKU: sku, HandlingTags: []string{"TemperatureSensitive", "Hazmat"}, TemperatureClass: "Frozen", DOTHazardClass: 3,
	}); err != nil {
		t.Fatalf("classify %s: %v", sku, err)
	}
	if _, err := h.declare.Handle(context.Background(), usecases.DeclareDimensionsCommand{
		SKU: sku, DimensionsInput: usecases.DimensionsInput{LengthMm: 200, WidthMm: 100, HeightMm: 50, WeightG: 500},
	}); err != nil {
		t.Fatalf("declare dimensions %s: %v", sku, err)
	}
}

// ---- the wire: JSON-RPC over HTTP, driven by the SDK's own client ----------

type wireDims struct {
	LengthMm  int64 `json:"lengthMm"`
	WidthMm   int64 `json:"widthMm"`
	HeightMm  int64 `json:"heightMm"`
	WeightG   int64 `json:"weightG"`
	VolumeMm3 int64 `json:"volumeMm3"`
}

type wirePackLevel struct {
	Level          string    `json:"level"`
	GTIN           string    `json:"gtin"`
	ChildGTIN      string    `json:"childGtin"`
	QtyOfChild     int       `json:"qtyOfChild"`
	UnitsPerPack   int       `json:"unitsPerPack"`
	Dims           *wireDims `json:"dims"`
	IsOrderable    bool      `json:"isOrderable"`
	IsDespatchUnit bool      `json:"isDespatchUnit"`
	Active         bool      `json:"active"`
	EffectiveFrom  string    `json:"effectiveFrom"`
}

// wireEnvelope is a TEST-LOCAL copy of the answer contract. If the adapter's
// envelope drifts, this suite fails instead of silently following it.
type wireEnvelope struct {
	Skill string `json:"skill"`
	Data  struct {
		ProductProfile    *wireProfile `json:"productProfile"`
		PackLevelDeclared *struct {
			SKU       string        `json:"sku"`
			Version   int64         `json:"version"`
			Created   bool          `json:"created"`
			PackLevel wirePackLevel `json:"packLevel"`
		} `json:"packLevelDeclared"`
	} `json:"data"`
	Problem *struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Status int    `json:"status"`
	} `json:"problem"`
}

type wire struct {
	client *a2aclient.Client
}

func newWire(t *testing.T, deps Deps) *wire {
	t.Helper()
	srv := httptest.NewServer(Handler(NewExecutor(deps)))
	t.Cleanup(srv.Close)
	card := Card(Config{AgentURL: srv.URL + "/", Version: "test"})
	client, err := a2aclient.NewFromCard(context.Background(), card)
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}
	return &wire{client: client}
}

// call sends one message part and returns the decoded envelope and the
// one-line text summary of the agent's reply.
func (w *wire) call(t *testing.T, part *a2a.Part) (wireEnvelope, string) {
	t.Helper()
	resp, err := w.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) < 2 {
		t.Fatalf("reply = %T, want a message with a data and a text part", resp)
	}
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", msg.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	var env wireEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("reply is not the skill envelope: %v (%s)", err, raw)
	}
	text, _ := msg.Parts[1].Content.(a2a.Text)
	return env, string(text)
}

func (w *wire) skill(t *testing.T, skill string, args map[string]any) (wireEnvelope, string) {
	t.Helper()
	return w.call(t, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
}

func wantProblem(t *testing.T, env wireEnvelope, status int, slug string) {
	t.Helper()
	if env.Problem == nil {
		t.Fatalf("expected a %d problem, got data: %+v", status, env.Data)
	}
	if env.Problem.Status != status || env.Problem.Type != problemTypeBase+slug {
		t.Fatalf("problem = %+v, want %d %s", env.Problem, status, slug)
	}
}

func declareEach1(sku string) map[string]any {
	return map[string]any{"sku": sku, "level": "EACH", "gtin": each1}
}

func declareCase1(sku string) map[string]any {
	return map[string]any{"sku": sku, "level": "CASE", "gtin": case1, "childGtin": each1, "qtyOfChild": 12}
}

// wireProfile is the product_profile payload of the test-local envelope.
type wireProfile = struct {
	SKU            string `json:"sku"`
	Description    string `json:"description"`
	Version        int64  `json:"version"`
	Classification *struct {
		HandlingTags         []string `json:"handlingTags"`
		TemperatureClass     string   `json:"temperatureClass"`
		DOTHazardClass       *int     `json:"dotHazardClass"`
		ClassificationSource string   `json:"classificationSource"`
	} `json:"classification"`
	PhysicalProfile struct {
		Effective       *wireDims `json:"effective"`
		EffectiveSource string    `json:"effectiveSource"`
		Discrepancy     bool      `json:"discrepancy"`
	} `json:"physicalProfile"`
	PackLevels   []wirePackLevel `json:"packLevels"`
	IdentifiedBy *wirePackLevel  `json:"identifiedBy"`
}

func assertFrozenPeasClassification(t *testing.T, p *wireProfile) {
	t.Helper()
	c := p.Classification
	if c == nil {
		t.Fatal("classification is missing")
	}
	if strings.Join(c.HandlingTags, ",") != "Hazmat,TemperatureSensitive" || c.TemperatureClass != "Frozen" || c.ClassificationSource != "native" {
		t.Fatalf("classification = %+v", c)
	}
	if c.DOTHazardClass == nil || *c.DOTHazardClass != 3 {
		t.Fatalf("dot hazard class = %v, want 3", c.DOTHazardClass)
	}
}

func assertEachThenCase(t *testing.T, p *wireProfile) {
	t.Helper()
	if len(p.PackLevels) != 2 || p.PackLevels[0].Level != "EACH" || p.PackLevels[1].Level != "CASE" || p.PackLevels[1].UnitsPerPack != 12 {
		t.Fatalf("pack levels = %+v, want EACH then CASE (12 units)", p.PackLevels)
	}
	if p.IdentifiedBy != nil {
		t.Fatalf("identifiedBy = %+v, want none for a SKU lookup", p.IdentifiedBy)
	}
}

func TestWireProductProfileBySKUComposesTheReadModels(t *testing.T) {
	h := newHarness(t)
	h.seedClassified(t, "SKU-1")
	w := newWire(t, h.deps)
	for _, args := range []map[string]any{declareEach1("SKU-1"), declareCase1("SKU-1")} {
		if env, _ := w.skill(t, skillDeclarePackLevel, args); env.Problem != nil {
			t.Fatalf("seeding a pack level: %+v", env.Problem)
		}
	}

	env, text := w.skill(t, skillProductProfile, map[string]any{"sku": "SKU-1"})
	p := env.Data.ProductProfile
	if env.Problem != nil || p == nil {
		t.Fatalf("envelope = %+v", env)
	}
	if p.SKU != "SKU-1" || p.Description != "Frozen peas" || p.Version != 5 {
		t.Fatalf("profile = %+v, want SKU-1 \"Frozen peas\" at version 5 (register, classify, dimensions, 2 pack levels)", p)
	}
	assertFrozenPeasClassification(t, p)
	if e := p.PhysicalProfile.Effective; p.PhysicalProfile.EffectiveSource != "declared" || e == nil || e.VolumeMm3 != 1_000_000 {
		t.Fatalf("physical profile = %+v", p.PhysicalProfile)
	}
	assertEachThenCase(t, p)
	if want := `SKU-1 "Frozen peas" v5: Hazmat+TemperatureSensitive (Frozen); 2 pack level(s)`; text != want {
		t.Fatalf("summary = %q, want %q", text, want)
	}
}

func TestWireProductProfileByGTINNamesTheIdentifiedLevel(t *testing.T) {
	h := newHarness(t)
	h.seedClassified(t, "SKU-1")
	w := newWire(t, h.deps)
	w.skill(t, skillDeclarePackLevel, declareEach1("SKU-1"))
	w.skill(t, skillDeclarePackLevel, declareCase1("SKU-1"))

	env, text := w.skill(t, skillProductProfile, map[string]any{"gtin": case1})
	p := env.Data.ProductProfile
	if env.Problem != nil || p == nil || p.SKU != "SKU-1" || p.IdentifiedBy == nil || p.IdentifiedBy.Level != "CASE" || p.IdentifiedBy.GTIN != case1 || p.IdentifiedBy.UnitsPerPack != 12 {
		t.Fatalf("envelope = %+v", env)
	}
	if !strings.HasSuffix(text, "; GTIN "+case1+" is the CASE level") {
		t.Fatalf("summary = %q", text)
	}

	// the EACH printed as a 13-digit barcode resolves, normalised to 14 digits
	env, _ = w.skill(t, skillProductProfile, map[string]any{"gtin": each1})
	if p := env.Data.ProductProfile; p == nil || p.IdentifiedBy == nil || p.IdentifiedBy.Level != "EACH" || p.IdentifiedBy.GTIN != "0"+each1 {
		t.Fatalf("EACH lookup = %+v", env)
	}
}

func TestWireProductProfileOfAnUnclassifiedProductOmitsClassification(t *testing.T) {
	h := newHarness(t)
	h.seed(t, "SKU-9", "Mug")
	env, text := newWire(t, h.deps).skill(t, skillProductProfile, map[string]any{"sku": "SKU-9"})
	p := env.Data.ProductProfile
	if env.Problem != nil || p == nil || p.Classification != nil || len(p.PackLevels) != 0 || p.PhysicalProfile.EffectiveSource != "none" {
		t.Fatalf("envelope = %+v", env)
	}
	if text != `SKU-9 "Mug" v1: unclassified; 0 pack level(s)` {
		t.Fatalf("summary = %q", text)
	}
}

func assertDeclaredEach(t *testing.T, env wireEnvelope) {
	t.Helper()
	d := env.Data.PackLevelDeclared
	if env.Problem != nil || d == nil {
		t.Fatalf("envelope = %+v", env)
	}
	if !d.Created || d.SKU != "SKU-1" || d.Version != 2 {
		t.Fatalf("declared = %+v, want a new level on SKU-1 at version 2", d)
	}
	l := d.PackLevel
	if l.Level != "EACH" || l.GTIN != "0"+each1 || l.UnitsPerPack != 1 || !l.Active || !l.IsOrderable {
		t.Fatalf("pack level = %+v", l)
	}
	if l.Dims == nil || l.Dims.VolumeMm3 != 480_000 {
		t.Fatalf("dims = %+v, want the declared 100x80x60 mm", l.Dims)
	}
}

func TestWireDeclarePackLevelIsDelegatedThroughTheOutbox(t *testing.T) {
	h := newHarness(t)
	h.seed(t, "SKU-1", "Frozen peas")
	w := newWire(t, h.deps)
	base := len(h.outbox.Messages())

	args := declareEach1("SKU-1")
	args["dims"] = map[string]any{"lengthMm": 100, "widthMm": 80, "heightMm": 60, "weightG": 400}
	args["isOrderable"] = true
	env, text := w.skill(t, skillDeclarePackLevel, args)
	assertDeclaredEach(t, env)
	if text != "declared EACH level (GTIN 0"+each1+") on SKU-1, version 2" {
		t.Fatalf("summary = %q", text)
	}
	if got := len(h.outbox.Messages()); got != base+1 {
		t.Fatalf("outbox rows = %d, want %d: the delegated write must leave exactly one event row through the transactional outbox", got, base+1)
	}

	// the same declaration again is idempotent: nothing new is enqueued
	env, text = w.skill(t, skillDeclarePackLevel, args)
	if d := env.Data.PackLevelDeclared; env.Problem != nil || d == nil || d.Created || d.Version != 2 || !strings.HasPrefix(text, "re-declared EACH level") {
		t.Fatalf("repeat = %+v / %q", env, text)
	}
	if got := len(h.outbox.Messages()); got != base+1 {
		t.Fatalf("outbox rows after a repeat = %d, want still %d", got, base+1)
	}

	// and a read sees the write
	env, _ = w.skill(t, skillProductProfile, map[string]any{"sku": "SKU-1"})
	if p := env.Data.ProductProfile; p == nil || len(p.PackLevels) != 1 || p.Version != 2 {
		t.Fatalf("after declaring, profile = %+v: the delegated write did not land", p)
	}
}

func TestWireDomainRejectionsAreAnsweredNotRaised(t *testing.T) {
	h := newHarness(t)
	h.seed(t, "SKU-1", "Frozen peas")
	h.seed(t, "SKU-2", "Mug")
	w := newWire(t, h.deps)
	w.skill(t, skillDeclarePackLevel, declareEach1("SKU-1"))
	w.skill(t, skillDeclarePackLevel, declareCase1("SKU-1"))
	base := len(h.outbox.Messages())

	two := func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	cases := map[string]struct {
		skill  string
		args   map[string]any
		status int
		slug   string
	}{
		"profile: unknown sku":            {skillProductProfile, two("sku", "NOPE"), 404, "product-not-found"},
		"profile: unknown gtin":           {skillProductProfile, two("gtin", each2), 404, "gtin-not-found"},
		"profile: malformed sku":          {skillProductProfile, two("sku", "a/b"), 400, "invalid-sku"},
		"profile: bad check digit":        {skillProductProfile, two("gtin", "4006381333932"), 400, "invalid-gtin"},
		"profile: variable measure":       {skillProductProfile, two("gtin", variableMeasure), 400, "variable-measure-unsupported"},
		"declare: unknown sku":            {skillDeclarePackLevel, two("sku", "NOPE", "level", "EACH", "gtin", each2), 404, "product-not-found"},
		"declare: malformed sku":          {skillDeclarePackLevel, two("sku", "a/b", "level", "EACH", "gtin", each2), 400, "invalid-sku"},
		"declare: bad level":              {skillDeclarePackLevel, two("sku", "SKU-2", "level", "BOX", "gtin", each2), 400, "invalid-pack-level"},
		"declare: bad gtin":               {skillDeclarePackLevel, two("sku", "SKU-2", "level", "EACH", "gtin", "123"), 400, "invalid-gtin"},
		"declare: variable measure":       {skillDeclarePackLevel, two("sku", "SKU-2", "level", "EACH", "gtin", variableMeasure), 400, "variable-measure-unsupported"},
		"declare: each with a quantity":   {skillDeclarePackLevel, two("sku", "SKU-2", "level", "EACH", "gtin", each2, "unitsPerPack", 5), 400, "invalid-pack-quantity"},
		"declare: dimension out of range": {skillDeclarePackLevel, two("sku", "SKU-2", "level", "EACH", "gtin", each2, "dims", two("lengthMm", 0, "widthMm", 1, "heightMm", 1, "weightG", 1)), 400, "invalid-dimension"},
		"declare: gtin held by sku-1":     {skillDeclarePackLevel, two("sku", "SKU-2", "level", "EACH", "gtin", each1), 409, "gtin-already-assigned"},
		"declare: case without child":     {skillDeclarePackLevel, two("sku", "SKU-2", "level", "CASE", "gtin", each2), 400, "invalid-pack-quantity"},
		"declare: child is not its own":   {skillDeclarePackLevel, two("sku", "SKU-2", "level", "CASE", "gtin", "14006381333938", "childGtin", each1, "qtyOfChild", 2), 409, "pack-level-order"},
		"declare: quantity is immutable":  {skillDeclarePackLevel, two("sku", "SKU-1", "level", "CASE", "gtin", case1, "childGtin", each1, "qtyOfChild", 6), 409, "pack-quantity-immutable"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env, text := w.skill(t, tc.skill, tc.args)
			wantProblem(t, env, tc.status, tc.slug)
			if !strings.Contains(text, env.Problem.Detail) || !strings.Contains(text, env.Problem.Title) {
				t.Fatalf("summary %q must carry the problem's title and detail", text)
			}
		})
	}
	if got := len(h.outbox.Messages()); got != base {
		t.Fatalf("outbox rows %d -> %d: a rejected delegation must write nothing", base, got)
	}
}

func TestWireArgumentValidation(t *testing.T) {
	w := newWire(t, newHarness(t).deps)
	cases := map[string]struct {
		skill string
		args  map[string]any
	}{
		"profile: neither sku nor gtin":    {skillProductProfile, map[string]any{}},
		"profile: both sku and gtin":       {skillProductProfile, map[string]any{"sku": "SKU-1", "gtin": each1}},
		"profile: blank sku":               {skillProductProfile, map[string]any{"sku": "   "}},
		"profile: unknown argument":        {skillProductProfile, map[string]any{"sku": "SKU-1", "includeRetired": true}},
		"profile: snake_case is not a key": {skillProductProfile, map[string]any{"sku": "SKU-1", "handling_tag": "Hazmat"}},
		"profile: wrong type":              {skillProductProfile, map[string]any{"sku": 7}},
		"declare: missing sku":             {skillDeclarePackLevel, map[string]any{"level": "EACH", "gtin": each1}},
		"declare: missing level":           {skillDeclarePackLevel, map[string]any{"sku": "SKU-1", "gtin": each1}},
		"declare: missing gtin":            {skillDeclarePackLevel, map[string]any{"sku": "SKU-1", "level": "EACH"}},
		"declare: unknown argument":        {skillDeclarePackLevel, map[string]any{"sku": "SKU-1", "level": "EACH", "gtin": each1, "force": true}},
		"declare: typo'd quantity":         {skillDeclarePackLevel, map[string]any{"sku": "SKU-1", "level": "CASE", "gtin": case1, "childGtin": each1, "qtyOfChld": 12}},
		"declare: snake_case key":          {skillDeclarePackLevel, map[string]any{"sku": "SKU-1", "level": "CASE", "gtin": case1, "child_gtin": each1, "qty_of_child": 12}},
		"declare: fractional quantity":     {skillDeclarePackLevel, map[string]any{"sku": "SKU-1", "level": "CASE", "gtin": case1, "childGtin": each1, "qtyOfChild": 1.5}},
		"declare: unknown dims field":      {skillDeclarePackLevel, map[string]any{"sku": "SKU-1", "level": "EACH", "gtin": each1, "dims": map[string]any{"length": 1}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env, _ := w.skill(t, tc.skill, tc.args)
			wantProblem(t, env, 400, "invalid-argument")
		})
	}
}

// TestWireAnswersOnlyNameArgumentsTheSurfaceOffers: the shared domain error
// texts spell their argument names in camelCase (unitsPerPack, qtyOfChild,
// ...), which are exactly the keys this surface offers -- unlike MCP's
// snake_case. No answer may teach a key the strict decoder would then refuse:
// every camelCase token in a problem answer must be an accepted argument
// name, and no answer may carry a snake_case identifier.
func TestWireAnswersOnlyNameArgumentsTheSurfaceOffers(t *testing.T) {
	h := newHarness(t)
	h.seed(t, "SKU-1", "Frozen peas")
	w := newWire(t, h.deps)
	w.skill(t, skillDeclarePackLevel, declareEach1("SKU-1"))
	w.skill(t, skillDeclarePackLevel, declareCase1("SKU-1"))

	offered := map[string]bool{}
	for _, typ := range []any{productProfileArgs{}, declarePackLevelArgs{}, dimsArgs{}} {
		raw, _ := json.Marshal(typ)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		for k := range m {
			offered[k] = true
		}
	}
	camel, snake := regexp.MustCompile(`\b[a-z]+[A-Z][A-Za-z]*\b`), regexp.MustCompile(`[a-z]_[a-z]`)
	answers := []map[string]any{
		{"sku": "SKU-1", "level": "EACH", "gtin": each2, "unitsPerPack": 5},                                                                // invalid-pack-quantity
		{"sku": "SKU-1", "level": "INNER", "gtin": each2, "childGtin": each1, "qtyOfChild": 1},                                             // invalid-pack-quantity
		{"sku": "SKU-1", "level": "INNER", "gtin": each2, "childGtin": each1, "qtyOfChild": 3, "unitsPerPack": 9},                          // invalid-pack-quantity
		{"sku": "SKU-1", "level": "CASE", "gtin": each2},                                                                                   // pack-level-order
		{"sku": "SKU-1", "level": "EACH", "gtin": each2, "dims": map[string]any{"lengthMm": 1, "widthMm": 1, "heightMm": 1, "weightG": 0}}, // invalid-weight
	}
	for _, args := range answers {
		env, text := w.skill(t, skillDeclarePackLevel, args)
		if env.Problem == nil {
			t.Fatalf("args %v: expected a problem", args)
		}
		for _, s := range []string{env.Problem.Detail, env.Problem.Title, text} {
			if snake.MatchString(s) {
				t.Errorf("an answer carries a snake_case identifier (the MCP spelling): %q", s)
			}
			for _, tok := range camel.FindAllString(s, -1) {
				if !offered[tok] {
					t.Errorf("an answer names %q, which this surface does not accept as an argument: %q", tok, s)
				}
			}
		}
	}
}

func TestWireRequestShapeProblems(t *testing.T) {
	w := newWire(t, newHarness(t).deps)

	env, _ := w.call(t, a2a.NewTextPart("not json at all"))
	wantProblem(t, env, 400, "malformed-skill-request")

	env, _ = w.call(t, a2a.NewDataPart(map[string]any{"arguments": map[string]any{}}))
	wantProblem(t, env, 400, "malformed-skill-request")

	env, _ = w.call(t, a2a.NewDataPart(map[string]any{"skill": skillProductProfile, "arguments": map[string]any{}, "extra": 1}))
	wantProblem(t, env, 400, "malformed-skill-request")

	env, _ = w.call(t, a2a.NewTextPart(`{"skill":"product_profile","arguments":{"sku":"SKU-1"}} {"trailing":true}`))
	wantProblem(t, env, 400, "malformed-skill-request")

	env, _ = w.skill(t, "retire_pack_level", map[string]any{"sku": "SKU-1", "level": "EACH"})
	wantProblem(t, env, 400, "unknown-skill")

	env, _ = w.skill(t, "launch_rocket", map[string]any{})
	wantProblem(t, env, 400, "unknown-skill")

	// A text part holding the same JSON is accepted (documented contract).
	h := newHarness(t)
	h.seed(t, "SKU-1", "Frozen peas")
	env, _ = newWire(t, h.deps).call(t, a2a.NewTextPart(`{"skill":"product_profile","arguments":{"sku":"SKU-1"}}`))
	if env.Problem != nil || env.Data.ProductProfile == nil {
		t.Fatalf("a text part holding the skill JSON must be accepted, got %+v", env)
	}
}

// failingRepo is a product repository whose every read fails with an
// infrastructure-looking error, to prove nothing of it reaches the caller.
type failingRepo struct{ *memory.ProductRepo }

var errInfra = errors.New("dial tcp 10.0.0.7:5432: connection refused (password=hunter2)")

func (failingRepo) Get(context.Context, product.SKU) (*product.Product, error) { return nil, errInfra }

func TestWireUnexpectedFailureIsAnInternalProblemWithoutDetails(t *testing.T) {
	h := harnessOver(t, failingRepo{memory.NewProductRepo()})
	env, text := newWire(t, h.deps).skill(t, skillProductProfile, map[string]any{"sku": "SKU-1"})
	wantProblem(t, env, 500, "internal-error")
	for _, s := range []string{env.Problem.Detail, env.Problem.Title, text} {
		if strings.Contains(s, "hunter2") || strings.Contains(s, "5432") {
			t.Fatalf("an infrastructure detail leaked into the answer: %q", s)
		}
	}
}

// TestProblemCatalogue pins every row of the classification the REST adapter
// owns (internal/adapters/inbound/http/errors.go): same status and slug.
func TestProblemCatalogue(t *testing.T) {
	cases := []struct {
		err    error
		status int
		slug   string
	}{
		{product.ErrInvalidSKU, 400, "invalid-sku"},
		{product.ErrInvalidDimension, 400, "invalid-dimension"},
		{product.ErrInvalidWeight, 400, "invalid-weight"},
		{product.ErrInvalidPackLevel, 400, "invalid-pack-level"},
		{product.ErrVariableMeasureUnsupported, 400, "variable-measure-unsupported"},
		{product.ErrInvalidGTIN, 400, "invalid-gtin"},
		{product.ErrInvalidPackQuantity, 400, "invalid-pack-quantity"},
		{product.ErrInvalidReason, 400, "invalid-reason"},
		{repository.ErrProductNotFound, 404, "product-not-found"},
		{repository.ErrGtinNotFound, 404, "gtin-not-found"},
		{product.ErrPackLevelNotFound, 404, "pack-level-not-found"},
		{product.ErrGtinAlreadyAssigned, 409, "gtin-already-assigned"},
		{product.ErrPackQuantityImmutable, 409, "pack-quantity-immutable"},
		{product.ErrPackLevelOrder, 409, "pack-level-order"},
		{product.ErrPackDimensionsBelowContents, 409, "pack-dimensions-below-contents"},
		{repository.ErrConcurrentModification, 409, "concurrent-modification"},
		{errors.New("boom"), 500, "internal-error"},
	}
	for _, tc := range cases {
		p := problemFromUseCase(tc.err)
		if p.Status != tc.status || p.Type != problemTypeBase+tc.slug {
			t.Errorf("%v -> %d %s, want %d %s", tc.err, p.Status, p.Type, tc.status, tc.slug)
		}
	}
}

// TestCardIsServedAndDialable proves discovery end to end: the card handler
// serves the card at the well-known path and a client built from the FETCHED
// card reaches the agent.
func TestCardIsServedAndDialable(t *testing.T) {
	h := newHarness(t)
	h.seed(t, "SKU-1", "Frozen peas")
	mux := http.NewServeMux()
	mux.Handle("/", Handler(NewExecutor(h.deps)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(Card(Config{AgentURL: srv.URL + "/", Version: "test"})))

	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	w := &wire{client: client}
	env, _ := w.skill(t, skillProductProfile, map[string]any{"sku": "SKU-1"})
	if env.Data.ProductProfile == nil {
		t.Fatalf("a client built from the served card could not call the agent: %+v", env)
	}
}
