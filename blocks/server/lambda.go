// blocks/server/lambda.go
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/raywall/go-code-blocks/core"
)

// ── LambdaBlock ───────────────────────────────────────────────────────────────

// LambdaBlock is a Lambda integration block that normalizes API Gateway (v1/v2)
// and ALB events into the universal *Request type and translates *Response back
// to the wire format expected by each event source.
//
//	router := server.NewRouter()
//	router.GET("/users/:id", getUserHandler)
//	router.POST("/users",    createUserHandler)
//
//	fn := server.NewLambda("api",
//	    server.WithSource(server.SourceAPIGatewayV2),
//	    server.WithRouter(router),
//	    server.WithMiddleware(server.Logging(), server.Recovery()),
//	)
//
//	app.MustRegister(fn)
//	app.InitAll(ctx)
//	fn.Start() // blocks — calls lambda.Start() internally
type LambdaBlock struct {
	name    string
	cfg     blockConfig
	handler Handler
}

// NewLambda creates a new Lambda block.
// WithSource is required — it determines how events are decoded and
// how responses are encoded.
func NewLambda(name string, opts ...Option) *LambdaBlock {
	cfg := buildConfig(opts)
	return &LambdaBlock{name: name, cfg: cfg}
}

// Name implements core.Block.
func (b *LambdaBlock) Name() string { return b.name }

// Init implements core.Block. It resolves the effective handler and validates
// that a source type has been configured.
func (b *LambdaBlock) Init(_ context.Context) error {
	if b.cfg.source == "" {
		return fmt.Errorf("server/lambda %q: source not configured; use WithSource", b.name)
	}

	h, err := b.cfg.effectiveHandler()
	if err != nil {
		return fmt.Errorf("server/lambda %q: %w", b.name, err)
	}
	b.handler = h
	return nil
}

// Shutdown implements core.Block. Lambda manages its own lifecycle;
// this is a no-op but satisfies the interface.
func (b *LambdaBlock) Shutdown(_ context.Context) error { return nil }

// Start registers the Lambda handler and begins polling the Lambda runtime.
// This call blocks until the Lambda execution environment shuts down.
// It must be called after app.InitAll.
//
//	app.InitAll(ctx)
//	defer app.ShutdownAll(ctx)
//	fn.Start() // hand control to the Lambda runtime
func (b *LambdaBlock) Start() {
	switch b.cfg.source {
	case SourceAPIGatewayV1:
		lambda.Start(b.handleAPIGatewayV1)
	case SourceAPIGatewayV2:
		lambda.Start(b.handleAPIGatewayV2)
	case SourceALB:
		lambda.Start(b.handleALB)
	default:
		// Unreachable after Init validation, but kept for safety.
		panic(fmt.Sprintf("server/lambda %q: unsupported source %q", b.name, b.cfg.source))
	}
}

// ── API Gateway V1 (REST API) ─────────────────────────────────────────────────

func (b *LambdaBlock) handleAPIGatewayV1(
	ctx context.Context,
	event events.APIGatewayProxyRequest,
) (events.APIGatewayProxyResponse, error) {
	req := requestFromAPIGatewayV1(event)
	resp, err := b.handler(ctx, req)
	if err != nil {
		resp = Error(http.StatusInternalServerError, err.Error())
	}
	if resp == nil {
		resp = Error(http.StatusInternalServerError, "handler returned nil response")
	}
	return toAPIGatewayV1Response(resp)
}

func requestFromAPIGatewayV1(e events.APIGatewayProxyRequest) *Request {
	query := make(map[string][]string)
	for k, v := range e.QueryStringParameters {
		query[k] = []string{v}
	}
	for k, vals := range e.MultiValueQueryStringParameters {
		query[k] = vals
	}

	headers := make(map[string]string, len(e.Headers))
	for k, v := range e.Headers {
		headers[http.CanonicalHeaderKey(k)] = v
	}

	var body []byte
	if e.IsBase64Encoded {
		decoded, _ := base64.StdEncoding.DecodeString(e.Body)
		body = decoded
	} else {
		body = []byte(e.Body)
	}

	return &Request{
		Method:     e.HTTPMethod,
		Path:       e.Path,
		PathParams: e.PathParameters,
		Query:      query,
		Headers:    headers,
		Body:       body,
		SourceIP:   e.RequestContext.Identity.SourceIP,
		RequestID:  e.RequestContext.RequestID,
		Stage:      e.RequestContext.Stage,
		Source:     SourceAPIGatewayV1,
		Raw:        e,
	}
}

func toAPIGatewayV1Response(resp *Response) (events.APIGatewayProxyResponse, error) {
	body, isBase64, err := encodeBodyForLambda(resp)
	if err != nil {
		return events.APIGatewayProxyResponse{StatusCode: 500}, err
	}
	headers := normalizeHeaders(resp.Headers)
	return events.APIGatewayProxyResponse{
		StatusCode:      resp.StatusCode,
		Headers:         headers,
		Body:            body,
		IsBase64Encoded: isBase64,
	}, nil
}

// ── API Gateway V2 (HTTP API) ─────────────────────────────────────────────────

func (b *LambdaBlock) handleAPIGatewayV2(
	ctx context.Context,
	event events.APIGatewayV2HTTPRequest,
) (events.APIGatewayV2HTTPResponse, error) {
	req := requestFromAPIGatewayV2(event)
	resp, err := b.handler(ctx, req)
	if err != nil {
		resp = Error(http.StatusInternalServerError, err.Error())
	}
	if resp == nil {
		resp = Error(http.StatusInternalServerError, "handler returned nil response")
	}
	return toAPIGatewayV2Response(resp)
}

func requestFromAPIGatewayV2(e events.APIGatewayV2HTTPRequest) *Request {
	query := make(map[string][]string)
	for k, v := range e.QueryStringParameters {
		query[k] = strings.Split(v, ",")
	}

	headers := make(map[string]string, len(e.Headers))
	for k, v := range e.Headers {
		headers[http.CanonicalHeaderKey(k)] = v
	}

	var body []byte
	if e.IsBase64Encoded {
		decoded, _ := base64.StdEncoding.DecodeString(e.Body)
		body = decoded
	} else {
		body = []byte(e.Body)
	}

	return &Request{
		Method:     e.RequestContext.HTTP.Method,
		Path:       e.RawPath,
		PathParams: e.PathParameters,
		Query:      query,
		Headers:    headers,
		Body:       body,
		SourceIP:   e.RequestContext.HTTP.SourceIP,
		RequestID:  e.RequestContext.RequestID,
		Stage:      e.RequestContext.Stage,
		Source:     SourceAPIGatewayV2,
		Raw:        e,
	}
}

func toAPIGatewayV2Response(resp *Response) (events.APIGatewayV2HTTPResponse, error) {
	body, isBase64, err := encodeBodyForLambda(resp)
	if err != nil {
		return events.APIGatewayV2HTTPResponse{StatusCode: 500}, err
	}
	headers := normalizeHeaders(resp.Headers)
	return events.APIGatewayV2HTTPResponse{
		StatusCode:      resp.StatusCode,
		Headers:         headers,
		Body:            body,
		IsBase64Encoded: isBase64,
	}, nil
}

// ── ALB ───────────────────────────────────────────────────────────────────────

func (b *LambdaBlock) handleALB(
	ctx context.Context,
	event events.ALBTargetGroupRequest,
) (events.ALBTargetGroupResponse, error) {
	req := requestFromALB(event)
	resp, err := b.handler(ctx, req)
	if err != nil {
		resp = Error(http.StatusInternalServerError, err.Error())
	}
	if resp == nil {
		resp = Error(http.StatusInternalServerError, "handler returned nil response")
	}
	return toALBResponse(resp)
}

func requestFromALB(e events.ALBTargetGroupRequest) *Request {
	query := make(map[string][]string)
	for k, v := range e.QueryStringParameters {
		query[k] = []string{v}
	}
	for k, vals := range e.MultiValueQueryStringParameters {
		query[k] = vals
	}

	headers := make(map[string]string, len(e.Headers))
	for k, v := range e.Headers {
		headers[http.CanonicalHeaderKey(k)] = v
	}

	var body []byte
	if e.IsBase64Encoded {
		decoded, _ := base64.StdEncoding.DecodeString(e.Body)
		body = decoded
	} else {
		body = []byte(e.Body)
	}

	return &Request{
		Method:    e.HTTPMethod,
		Path:      e.Path,
		Query:     query,
		Headers:   headers,
		Body:      body,
		RequestID: headers["X-Amzn-Trace-Id"],
		Source:    SourceALB,
		Raw:       e,
	}
}

func toALBResponse(resp *Response) (events.ALBTargetGroupResponse, error) {
	body, isBase64, err := encodeBodyForLambda(resp)
	if err != nil {
		return events.ALBTargetGroupResponse{StatusCode: 500}, err
	}
	headers := normalizeHeaders(resp.Headers)
	statusDesc := fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	return events.ALBTargetGroupResponse{
		StatusCode:        resp.StatusCode,
		StatusDescription: statusDesc,
		Headers:           headers,
		Body:              body,
		IsBase64Encoded:   isBase64,
	}, nil
}

// ── shared helpers ────────────────────────────────────────────────────────────

// encodeBodyForLambda converts resp.Body into the string form that Lambda
// event responses expect, handling base64 for binary payloads.
func encodeBodyForLambda(resp *Response) (string, bool, error) {
	if resp.Body == nil {
		return "", false, nil
	}

	if resp.IsBase64 {
		switch v := resp.Body.(type) {
		case []byte:
			return base64.StdEncoding.EncodeToString(v), true, nil
		case string:
			return base64.StdEncoding.EncodeToString([]byte(v)), true, nil
		}
	}

	switch v := resp.Body.(type) {
	case string:
		return v, false, nil
	case []byte:
		return string(v), false, nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", false, fmt.Errorf("encode lambda response body: %w", err)
		}
		return string(b), false, nil
	}
}

// normalizeHeaders converts map[string]string to the map[string]string form
// expected by Lambda response structs (already the right type, kept for clarity).
func normalizeHeaders(h map[string]string) map[string]string {
	if h == nil {
		return map[string]string{}
	}
	return h
}

// Ensure LambdaBlock implements core.Block.
var _ core.Block = (*LambdaBlock)(nil)
