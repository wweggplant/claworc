package llmgateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// --- openAICompletions (default / fallback) ---

type openAICompletions struct{}

func (openAICompletions) SetAuthHeader(req *http.Request, apiKey string, _ []byte) {
	req.Header.Set("Authorization", "Bearer "+apiKey)
}

func (openAICompletions) RewritePath(baseURL, requestPath, _ string, _ []byte) string {
	if strings.HasSuffix(baseURL, "/v1") && strings.HasPrefix(requestPath, "/v1/") {
		return requestPath[3:]
	}
	return requestPath
}

func (openAICompletions) ParseUsage(body []byte) (int, int, int) {
	return ParseUsageOpenAICompletions(body)
}

func (openAICompletions) ParseStreamingUsage(body []byte) (int, int, int) {
	return ParseUsageOpenAICompletionsStream(body)
}

func (openAICompletions) ProbeURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/v1/models"
}

func (openAICompletions) ProbeHeaders(*http.Request) {}

func (openAICompletions) IsEventStream() bool { return false }

// --- openAIResponses (embeds openAICompletions for shared auth/probe) ---

type openAIResponses struct {
	openAICompletions
}

func (openAIResponses) RewritePath(baseURL, requestPath, _ string, _ []byte) string {
	if strings.HasSuffix(baseURL, "/v1") && strings.HasPrefix(requestPath, "/v1/") {
		return requestPath[3:]
	}
	if !strings.HasSuffix(baseURL, "/v1") && !strings.HasPrefix(requestPath, "/v1/") {
		return "/v1" + requestPath
	}
	return requestPath
}

func (openAIResponses) ParseUsage(body []byte) (int, int, int) {
	return ParseUsageOpenAIResponses(body)
}

func (openAIResponses) ParseStreamingUsage(body []byte) (int, int, int) {
	return ParseUsageOpenAIResponsesStream(body)
}

// --- anthropicMessages ---

type anthropicMessages struct{}

func (anthropicMessages) SetAuthHeader(req *http.Request, apiKey string, _ []byte) {
	req.Header.Set("x-api-key", apiKey)
}

func (anthropicMessages) RewritePath(baseURL, requestPath, _ string, _ []byte) string {
	if strings.HasSuffix(baseURL, "/v1") && strings.HasPrefix(requestPath, "/v1/") {
		return requestPath[3:]
	}
	return requestPath
}

func (anthropicMessages) ParseUsage(body []byte) (int, int, int) {
	return ParseUsageAnthropicMessages(body)
}

func (anthropicMessages) ParseStreamingUsage(body []byte) (int, int, int) {
	return ParseUsageAnthropicMessagesStream(body)
}

func (anthropicMessages) ProbeURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/v1/models"
}

func (anthropicMessages) ProbeHeaders(req *http.Request) {
	req.Header.Set("anthropic-version", "2023-06-01")
}

func (anthropicMessages) IsEventStream() bool { return false }

// --- googleGenerativeAI ---

type googleGenerativeAI struct{}

func (googleGenerativeAI) SetAuthHeader(req *http.Request, apiKey string, _ []byte) {
	req.Header.Set("x-goog-api-key", apiKey)
}

func (googleGenerativeAI) RewritePath(baseURL, requestPath, _ string, _ []byte) string {
	if strings.HasSuffix(baseURL, "/v1") && strings.HasPrefix(requestPath, "/v1/") {
		return requestPath[3:]
	}
	return requestPath
}

func (googleGenerativeAI) ParseUsage(body []byte) (int, int, int) {
	return ParseUsageGoogleGenerativeAI(body)
}

func (googleGenerativeAI) ParseStreamingUsage(body []byte) (int, int, int) {
	return ParseUsageGoogleGenerativeAI(body)
}

func (googleGenerativeAI) ProbeURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/v1/models"
}

func (googleGenerativeAI) ProbeHeaders(*http.Request) {}

func (googleGenerativeAI) IsEventStream() bool { return false }

// --- ollamaAPI ---

type ollamaAPI struct{}

func (ollamaAPI) SetAuthHeader(req *http.Request, apiKey string, _ []byte) {
	req.Header.Set("Authorization", "Bearer "+apiKey)
}

func (ollamaAPI) RewritePath(baseURL, requestPath, _ string, _ []byte) string {
	if strings.HasSuffix(baseURL, "/v1") && strings.HasPrefix(requestPath, "/v1/") {
		return requestPath[3:]
	}
	return requestPath
}

func (ollamaAPI) ParseUsage(body []byte) (int, int, int) {
	return ParseUsageOllama(body)
}

func (ollamaAPI) ParseStreamingUsage(body []byte) (int, int, int) {
	return ParseUsageOllamaStream(body)
}

func (ollamaAPI) ProbeURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/api/tags"
}

func (ollamaAPI) ProbeHeaders(*http.Request) {}

func (ollamaAPI) IsEventStream() bool { return false }

// --- bedrockConverseIAM ---
// Implements direct AWS Bedrock access via SigV4 signing.
//
// API key format: ACCESS_KEY_ID:SECRET_ACCESS_KEY[:SESSION_TOKEN]
// Base URL:       https://bedrock-runtime.{region}.amazonaws.com
//
// Incoming Anthropic-format requests (/v1/messages) are rewritten to the
// Bedrock InvokeModel endpoint.  Streaming responses use AWS binary event
// stream encoding and are transcoded to SSE by the gateway.

type bedrockConverseIAM struct{}

// bedrockCreds holds parsed AWS credentials.
type bedrockCreds struct {
	accessKey    string
	secretKey    string
	sessionToken string
	region       string // optional; overrides region extracted from base URL
}

// parseBedrockCreds splits "ACCESS_KEY:SECRET_KEY[:SESSION_TOKEN[:REGION]]".
// To supply a region without a session token use an empty third segment:
//
//	ACCESS_KEY:SECRET_KEY::us-west-2
func parseBedrockCreds(apiKey string) bedrockCreds {
	parts := strings.SplitN(apiKey, ":", 4)
	c := bedrockCreds{}
	if len(parts) >= 1 {
		c.accessKey = parts[0]
	}
	if len(parts) >= 2 {
		c.secretKey = parts[1]
	}
	if len(parts) >= 3 {
		c.sessionToken = parts[2]
	}
	if len(parts) == 4 {
		c.region = parts[3]
	}
	return c
}

// extractBedrockRegion extracts the AWS region from a Bedrock runtime base URL.
// Supports: https://bedrock-runtime.us-east-1.amazonaws.com
// Falls back to "us-east-1" if the URL does not match the expected pattern.
func extractBedrockRegion(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "us-east-1"
	}
	parts := strings.Split(u.Hostname(), ".")
	// ["bedrock-runtime", "us-east-1", "amazonaws", "com"]
	if len(parts) >= 4 && strings.HasPrefix(parts[0], "bedrock") {
		return parts[1]
	}
	return "us-east-1"
}

// isBedrockStream checks whether the request body sets "stream": true.
func isBedrockStream(body []byte) bool {
	var b struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &b)
	return b.Stream
}

func (bedrockConverseIAM) RewritePath(baseURL, requestPath, model string, body []byte) string {
	// Only rewrite Anthropic-format paths.
	if !strings.HasSuffix(requestPath, "/messages") {
		return requestPath
	}
	if model == "" {
		return requestPath
	}
	// URL-encode the model ID (colons in e.g. "anthropic.claude-3-5-sonnet-...-v2:0").
	encodedModel := url.PathEscape(model)
	if isBedrockStream(body) {
		return "/model/" + encodedModel + "/invoke-with-response-stream"
	}
	return "/model/" + encodedModel + "/invoke"
}

func (bedrockConverseIAM) SetAuthHeader(req *http.Request, apiKey string, body []byte) {
	creds := parseBedrockCreds(apiKey)
	if creds.accessKey == "" || creds.secretKey == "" {
		return
	}
	region := creds.region
	if region == "" {
		region = extractBedrockRegion(req.URL.String())
	}
	if err := sigV4Sign(req, body, creds, region, "bedrock", time.Now().UTC()); err != nil {
		// log only; caller will handle upstream error
		_ = err
	}
}

func (bedrockConverseIAM) ParseUsage(body []byte) (int, int, int) {
	// Bedrock InvokeModel returns the Anthropic Messages response format for Claude.
	return ParseUsageAnthropicMessages(body)
}

func (bedrockConverseIAM) ParseStreamingUsage(body []byte) (int, int, int) {
	// After event-stream → SSE transcoding the captured buffer is Anthropic SSE.
	return ParseUsageAnthropicMessagesStream(body)
}

func (bedrockConverseIAM) ProbeURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/")
}

func (bedrockConverseIAM) ProbeHeaders(*http.Request) {}

func (bedrockConverseIAM) IsEventStream() bool { return true }

// --- AWS SigV4 implementation (no external dependencies) ---

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hexSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// sigV4Sign adds AWS Signature Version 4 headers to req in-place.
func sigV4Sign(req *http.Request, body []byte, creds bedrockCreds, region, service string, t time.Time) error {
	amzDate := t.Format("20060102T150405Z")
	dateStamp := t.Format("20060102")

	payloadHash := hexSHA256(body)

	// Ensure required headers are set before canonicalisation.
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if creds.sessionToken != "" {
		req.Header.Set("x-amz-security-token", creds.sessionToken)
	}

	// Canonical headers: sort header names, lowercase.
	signedHeaderNames := []string{"content-type", "host", "x-amz-content-sha256", "x-amz-date"}
	if creds.sessionToken != "" {
		signedHeaderNames = append(signedHeaderNames, "x-amz-security-token")
	}
	// Sort them (already sorted by hand above; keep sorted if you add more).
	// Canonical headers block.
	var canonHeadersBuf strings.Builder
	for _, h := range signedHeaderNames {
		val := req.Header.Get(h)
		if h == "host" {
			val = req.URL.Host
		}
		canonHeadersBuf.WriteString(h)
		canonHeadersBuf.WriteByte(':')
		canonHeadersBuf.WriteString(strings.TrimSpace(val))
		canonHeadersBuf.WriteByte('\n')
	}
	signedHeaders := strings.Join(signedHeaderNames, ";")

	// Canonical URI (path).
	canonURI := req.URL.EscapedPath()
	if canonURI == "" {
		canonURI = "/"
	}

	// Canonical query string (sorted).
	canonQuery := req.URL.Query().Encode()

	canonRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		req.Method,
		canonURI,
		canonQuery,
		canonHeadersBuf.String(),
		signedHeaders,
		payloadHash,
	)

	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, service)
	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
		amzDate,
		credentialScope,
		hexSHA256([]byte(canonRequest)),
	)

	signingKey := hmacSHA256(
		hmacSHA256(
			hmacSHA256(
				hmacSHA256(
					[]byte("AWS4"+creds.secretKey),
					[]byte(dateStamp),
				),
				[]byte(region),
			),
			[]byte(service),
		),
		[]byte("aws4_request"),
	)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	authHeader := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		creds.accessKey, credentialScope, signedHeaders, signature,
	)
	req.Header.Set("Authorization", authHeader)
	return nil
}
