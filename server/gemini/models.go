package gemini

// FinishReason is the reason generation stopped
type FinishReason = string

// FinishReason constants for Gemini API
const (
	FinishReasonUnspecified           FinishReason = "FINISH_REASON_UNSPECIFIED"
	FinishReasonStop                  FinishReason = "STOP"
	FinishReasonMaxTokens             FinishReason = "MAX_TOKENS"
	FinishReasonSafety                FinishReason = "SAFETY"
	FinishReasonRecitation            FinishReason = "RECITATION"
	FinishReasonMalformedFunctionCall FinishReason = "MALFORMED_FUNCTION_CALL"
	FinishReasonOther                 FinishReason = "OTHER"
)

// GenerateContentRequest is the request body for generateContent
type GenerateContentRequest struct {
	Contents          []*Content        `json:"contents,omitempty"`
	SystemInstruction *Content          `json:"systemInstruction,omitempty"`
	Tools             []*Tool           `json:"tools,omitempty"`
	ToolConfig        *ToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *GenerationConfig `json:"generationConfig,omitempty"`
	SafetySettings    []*SafetySetting  `json:"safetySettings,omitempty"`
}

type ThinkingConfig struct {
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
	ThinkingBudget  *int   `json:"thinkingBudget,omitempty"`
	IncludeThoughts bool   `json:"includeThoughts,omitempty"`
}

// Content represents a message with parts
type Content struct {
	Role  string  `json:"role,omitempty"`
	Parts []*Part `json:"parts,omitempty"`
}

// Part is a single piece of content
type Part struct {
	Text             string            `json:"text,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
	InlineData       *Blob             `json:"inlineData,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
	FileData         *FileData         `json:"fileData,omitempty"`
}

// Blob represents inline binary data
type Blob struct {
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"` // base64 encoded
}

// FileData represents a reference to uploaded file
type FileData struct {
	FileUri  string `json:"fileUri,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// FunctionCall represents a function call from the model
type FunctionCall struct {
	ID   string         `json:"id,omitempty"` // internal ID for tracking
	Name string         `json:"name,omitempty"`
	Args map[string]any `json:"args,omitempty"`
}

// FunctionResponse is the result of a function call
type FunctionResponse struct {
	ID       string                  `json:"id,omitempty"` // matches the FunctionCall ID
	Name     string                  `json:"name,omitempty"`
	Response map[string]any          `json:"response,omitempty"`
	Parts    []*FunctionResponsePart `json:"parts,omitempty"`
}

// FunctionResponsePart carries media alongside the function response's JSON
// payload. Only one of InlineData / FileData should be set.
type FunctionResponsePart struct {
	InlineData *FunctionResponseBlob     `json:"inlineData,omitempty"`
	FileData   *FunctionResponseFileData `json:"fileData,omitempty"`
}

type FunctionResponseBlob struct {
	MimeType    string `json:"mimeType,omitempty"`
	Data        string `json:"data,omitempty"` // base64 encoded
	DisplayName string `json:"displayName,omitempty"`
}

type FunctionResponseFileData struct {
	FileUri     string `json:"fileUri,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

// Tool represents a tool the model can use
type Tool struct {
	FunctionDeclarations []*FunctionDeclaration `json:"functionDeclarations,omitempty"`
}

// FunctionDeclaration describes a function
type FunctionDeclaration struct {
	Name                 string `json:"name,omitempty"`
	Description          string `json:"description,omitempty"`
	Parameters           any    `json:"parameters,omitempty"`           // JSON Schema (standard Gemini API)
	ParametersJsonSchema any    `json:"parametersJsonSchema,omitempty"` // JSON Schema (Gemini CLI format)
}

// ToolConfig configures tool behavior
type ToolConfig struct {
	FunctionCallingConfig *FunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

// FunctionCallingConfig controls function calling
type FunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"` // AUTO, ANY, NONE
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// GenerationConfig contains generation parameters
type GenerationConfig struct {
	StopSequences      []string        `json:"stopSequences,omitempty"`
	Temperature        *float32        `json:"temperature,omitempty"`
	TopP               *float32        `json:"topP,omitempty"`
	TopK               *int            `json:"topK,omitempty"`
	MaxOutputTokens    *int            `json:"maxOutputTokens,omitempty"`
	CandidateCount     *int            `json:"candidateCount,omitempty"`
	Seed               *int            `json:"seed,omitempty"`
	PresencePenalty    *float32        `json:"presencePenalty,omitempty"`
	FrequencyPenalty   *float32        `json:"frequencyPenalty,omitempty"`
	ResponseLogprobs   bool            `json:"responseLogprobs,omitempty"`
	Logprobs           *int            `json:"logprobs,omitempty"`
	ResponseMimeType   string          `json:"responseMimeType,omitempty"`
	ResponseSchema     any             `json:"responseSchema,omitempty"`
	ResponseJsonSchema any             `json:"responseJsonSchema,omitempty"`
	ResponseModalities []string        `json:"responseModalities,omitempty"`
	MediaResolution    string          `json:"mediaResolution,omitempty"`
	ThinkingConfig     *ThinkingConfig `json:"thinkingConfig,omitempty"`
}

// SafetySetting configures safety thresholds
type SafetySetting struct {
	Category  string `json:"category,omitempty"`
	Threshold string `json:"threshold,omitempty"`
}

// GenerateContentResponse is the response from generateContent
type GenerateContentResponse struct {
	ResponseId     string          `json:"responseId,omitempty"`
	Candidates     []*Candidate    `json:"candidates,omitempty"`
	UsageMetadata  *UsageMetadata  `json:"usageMetadata,omitempty"`
	ModelVersion   string          `json:"modelVersion,omitempty"`
	PromptFeedback *PromptFeedback `json:"promptFeedback,omitempty"`
	Error          *APIError       `json:"error,omitempty"`
}

// Candidate is a single response candidate
type Candidate struct {
	Content       *Content        `json:"content,omitempty"`
	FinishReason  string          `json:"finishReason,omitempty"`
	Index         int             `json:"index,omitempty"`
	SafetyRatings []*SafetyRating `json:"safetyRatings,omitempty"`
	TokenCount    int             `json:"tokenCount,omitempty"`
}

// SafetyRating represents a safety evaluation
type SafetyRating struct {
	Category    string `json:"category,omitempty"`
	Probability string `json:"probability,omitempty"`
	Blocked     bool   `json:"blocked,omitempty"`
}

// UsageMetadata contains token usage information
type UsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount,omitempty"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
	CandidatesTokenCount    int `json:"candidatesTokenCount,omitempty"`
	ToolUsePromptTokenCount int `json:"toolUsePromptTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
	TotalTokenCount         int `json:"totalTokenCount,omitempty"`

	PromptTokensDetails        []*ModalityTokenCount `json:"promptTokensDetails,omitempty"`
	CacheTokensDetails         []*ModalityTokenCount `json:"cacheTokensDetails,omitempty"`
	CandidatesTokensDetails    []*ModalityTokenCount `json:"candidatesTokensDetails,omitempty"`
	ToolUsePromptTokensDetails []*ModalityTokenCount `json:"toolUsePromptTokensDetails,omitempty"`
}

// ModalityTokenCount reports tokens broken down by modality.
type ModalityTokenCount struct {
	Modality   string `json:"modality,omitempty"`
	TokenCount int    `json:"tokenCount,omitempty"`
}

// PromptFeedback contains feedback about the prompt
type PromptFeedback struct {
	BlockReason   string          `json:"blockReason,omitempty"`
	SafetyRatings []*SafetyRating `json:"safetyRatings,omitempty"`
}

// CountTokensRequest is the request for countTokens
type CountTokensRequest struct {
	Contents          []*Content `json:"contents,omitempty"`
	SystemInstruction *Content   `json:"systemInstruction,omitempty"`
	Tools             []*Tool    `json:"tools,omitempty"`
}

// CountTokensResponse is the response from countTokens
type CountTokensResponse struct {
	TotalTokens int `json:"totalTokens,omitempty"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error *APIError `json:"error,omitempty"`
}

// APIError contains error details
type APIError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Status  string `json:"status,omitempty"`
}
