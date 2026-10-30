package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/claudioed/product-master/internal/application/usecases"
)

// tracerName is the OTel instrumentation scope for A2A skill spans, mirroring
// the MCP adapter's mcp.tool spans.
const tracerName = "github.com/claudioed/product-master/internal/adapters/inbound/a2a"

// Skill ids advertised on the Agent Card (see Card).
const (
	skillProductProfile   = "product_profile"
	skillDeclarePackLevel = "declare_pack_level"
)

// skillRequest is the wire contract of every skill call: the first meaningful
// message part carries (as a Data part, or a Text part holding the same JSON)
// the skill id and its arguments. Strict on purpose -- an agent client is
// machine-to-machine, so unknown fields are rejected, never ignored: a
// misspelled name like "qtyOfChld" must not silently drop a required quantity.
// (Go's encoding/json matches field names case-insensitively, so "qtyofchild"
// still reaches qtyOfChild; only genuinely unknown names are refused.)
type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments"`
}

// productProfileArgs and declarePackLevelArgs are the per-skill argument
// shapes. The names are the camelCase ones the domain error texts use.
type productProfileArgs struct {
	SKU  string `json:"sku"`
	GTIN string `json:"gtin"`
}

type dimsArgs struct {
	LengthMm int64 `json:"lengthMm"`
	WidthMm  int64 `json:"widthMm"`
	HeightMm int64 `json:"heightMm"`
	WeightG  int64 `json:"weightG"`
}

type declarePackLevelArgs struct {
	SKU            string    `json:"sku"`
	Level          string    `json:"level"`
	GTIN           string    `json:"gtin"`
	ChildGTIN      string    `json:"childGtin"`
	QtyOfChild     int       `json:"qtyOfChild"`
	UnitsPerPack   int       `json:"unitsPerPack"`
	Dims           *dimsArgs `json:"dims"`
	IsOrderable    bool      `json:"isOrderable"`
	IsDespatchUnit bool      `json:"isDespatchUnit"`
}

// answer maps one incoming message to a skill and returns the agent's reply
// Message. It is the whole executor body: this is a message-only agent, so a
// skill run is exactly one request mapped to one answer (ADR 0012).
func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, problem := parseSkillRequest(ec)
	if problem != nil {
		return reply(ec, envelope{Skill: "", Problem: problem}, problem.summary())
	}

	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+req.Skill)
	defer span.End()
	span.SetAttributes(attribute.String("a2a.skill", req.Skill))

	dto, problem := run(ctx, deps, req)
	if problem != nil {
		span.SetStatus(codes.Error, problem.Detail)
		return reply(ec, envelope{Skill: req.Skill, Problem: problem}, problem.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: dto}, dto.Summary())
}

// run dispatches one validated request to its skill.
func run(ctx context.Context, deps Deps, req skillRequest) (skillDTO, *problem) {
	switch req.Skill {
	case skillProductProfile:
		data, problem := deps.productProfile(ctx, req.Arguments)
		if problem != nil {
			return skillDTO{}, problem
		}
		return skillDTO{ProductProfile: &data, summary: data.summary}, nil
	case skillDeclarePackLevel:
		data, problem := deps.declarePackLevel(ctx, req.Arguments)
		if problem != nil {
			return skillDTO{}, problem
		}
		return skillDTO{PackLevelDeclared: &data, summary: data.summary}, nil
	default:
		return skillDTO{}, problemUnknownSkill(req.Skill)
	}
}

// reply builds the agent Message for one skill run: a Data part carrying the
// structured envelope and a Text part carrying the one-line summary, bound to
// the caller's context (and task, when the client referenced one).
func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(env), a2a.NewTextPart(summary))
}

// parseSkillRequest extracts the skillRequest from the message's first
// meaningful part. A Data part (or a Text part holding the same JSON object)
// is accepted; anything else is a request-shape problem, answered -- not
// raised -- because a malformed delegation is a business answer for the
// calling agent, not a transport failure.
func parseSkillRequest(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	msg := ec.Message
	if msg == nil || len(msg.Parts) == 0 {
		return skillRequest{}, problemMalformedRequest("the message carries no parts")
	}
	raw, err := partJSON(msg.Parts[0])
	if err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the first message part is not a skill request: %v", err))
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformedRequest(`the skill request carries no "skill" field`)
	}
	return req, nil
}

// partJSON renders one part's content as the JSON bytes of a skill request.
func partJSON(part *a2a.Part) ([]byte, error) {
	switch content := part.Content.(type) {
	case a2a.Data:
		return json.Marshal(content.Value)
	case a2a.Text:
		return []byte(string(content)), nil
	default:
		return nil, errors.New("expected a data part (or a text part holding the skill JSON)")
	}
}

// decodeStrict decodes one JSON document into dst, rejecting unknown fields
// and trailing data. Absent arguments decode as the empty object so each
// skill's own required-field checks produce the specific problem.
func decodeStrict(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON document")
	}
	return nil
}

// productProfile is the composed read skill: the product (found by sku, or by
// the GTIN of one of its pack levels) with its classification, physical
// profile and packaging hierarchy, from this context's own state through the
// same use cases REST and MCP use. A read: it writes nothing and raises no
// event.
func (d Deps) productProfile(ctx context.Context, rawArgs json.RawMessage) (productProfile, *problem) {
	var args productProfileArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return productProfile{}, problemInvalidArgument(fmt.Errorf("arguments: %w", err))
	}
	sku, gtin := strings.TrimSpace(args.SKU), strings.TrimSpace(args.GTIN)
	if (sku == "") == (gtin == "") {
		return productProfile{}, problemInvalidArgument(errors.New("exactly one of sku or gtin is required"))
	}
	if gtin != "" {
		res, err := d.ResolveGTIN.Handle(ctx, gtin)
		if err != nil {
			return productProfile{}, problemFromUseCase(err)
		}
		p, err := d.GetProduct.Handle(ctx, string(res.SKU))
		if err != nil {
			return productProfile{}, problemFromUseCase(err)
		}
		return toProductProfile(p, &res.Def), nil
	}
	p, err := d.GetProduct.Handle(ctx, args.SKU)
	if err != nil {
		return productProfile{}, problemFromUseCase(err)
	}
	return toProductProfile(p, nil), nil
}

// declarePackLevel is the single state-changing delegation: it declares a
// pack level through the same use case REST and MCP use, so the GTIN
// uniqueness, hierarchy and quantity-immutability invariants and the
// transactional outbox behave identically regardless of caller.
func (d Deps) declarePackLevel(ctx context.Context, rawArgs json.RawMessage) (packLevelDeclared, *problem) {
	var args declarePackLevelArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return packLevelDeclared{}, problemInvalidArgument(fmt.Errorf("arguments: %w", err))
	}
	if strings.TrimSpace(args.SKU) == "" || strings.TrimSpace(args.Level) == "" || strings.TrimSpace(args.GTIN) == "" {
		return packLevelDeclared{}, problemInvalidArgument(errors.New("sku, level and gtin are required"))
	}
	res, err := d.DeclarePackLevel.Handle(ctx, args.command())
	if err != nil {
		return packLevelDeclared{}, problemFromUseCase(err)
	}
	return toPackLevelDeclared(res), nil
}

// command maps the skill arguments to the use case command.
func (a declarePackLevelArgs) command() usecases.DeclarePackLevelCommand {
	cmd := usecases.DeclarePackLevelCommand{SKU: a.SKU, PackLevelInput: usecases.PackLevelInput{
		Level: a.Level, GTIN: a.GTIN, ChildGTIN: a.ChildGTIN, QtyOfChild: a.QtyOfChild, UnitsPerPack: a.UnitsPerPack,
		IsOrderable: a.IsOrderable, IsDespatchUnit: a.IsDespatchUnit,
	}}
	if a.Dims != nil {
		cmd.Dims = &usecases.DimensionsInput{LengthMm: a.Dims.LengthMm, WidthMm: a.Dims.WidthMm, HeightMm: a.Dims.HeightMm, WeightG: a.Dims.WeightG}
	}
	return cmd
}
