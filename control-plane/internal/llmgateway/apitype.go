package llmgateway

import "net/http"

// APIType encapsulates all per-provider behavior: auth headers, URL rewriting,
// usage parsing, and probe endpoints. Implementations are stateless value types.
type APIType interface {
	// SetAuthHeader sets the outgoing auth header(s). body is the raw request body,
	// needed by AWS SigV4 signing which must hash the payload.
	SetAuthHeader(req *http.Request, apiKey string, body []byte)
	// RewritePath transforms the request path for the upstream provider.
	// model is the model ID from the request body (needed for Bedrock URL construction).
	// body is the raw request body (needed to detect stream:true for Bedrock endpoint selection).
	RewritePath(baseURL, requestPath, model string, body []byte) string
	ParseUsage(body []byte) (inputTokens, outputTokens, cachedInputTokens int)
	ParseStreamingUsage(body []byte) (inputTokens, outputTokens, cachedInputTokens int)
	ProbeURL(baseURL string) string
	ProbeHeaders(req *http.Request)
	// IsEventStream returns true when the upstream uses AWS binary event stream encoding
	// instead of SSE. The gateway transcodes these responses to SSE for clients.
	IsEventStream() bool
}

// GetAPIType returns the APIType implementation for the given api type string.
func GetAPIType(apiType string) APIType {
	switch apiType {
	case "openai-responses":
		return openAIResponses{}
	case "anthropic-messages":
		return anthropicMessages{}
	case "google-generative-ai":
		return googleGenerativeAI{}
	case "ollama":
		return ollamaAPI{}
	case "bedrock-converse", "bedrock-converse-stream":
		return bedrockConverseIAM{}
	default:
		return openAICompletions{}
	}
}
