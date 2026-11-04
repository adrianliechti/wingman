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
	Model string `json:"model,omitzero"` // only inside countTokens.generateContentRequest
	Tier  string `json:"serviceTier,omitzero"`

	Contents          []*Content `json:"contents,omitempty"`
	SystemInstruction *Content   `json:"systemInstruction,omitzero"`

	Tools      []*Tool     `json:"tools,omitempty"`
	ToolConfig *ToolConfig `json:"toolConfig,omitzero"`

	GenerationConfig *GenerationConfig `json:"generationConfig,omitzero"`
	SafetySettings   []*SafetySetting  `json:"safetySettings,omitempty"`
}

type ThinkingConfig struct {
	ThinkingLevel   string `json:"thinkingLevel,omitzero"`
	ThinkingBudget  *int   `json:"thinkingBudget,omitzero"`
	IncludeThoughts bool   `json:"includeThoughts,omitzero"`
}

// Content represents a message with parts
type Content struct {
	Role  string  `json:"role,omitzero"`
	Parts []*Part `json:"parts,omitempty"`
}

// Part is a single piece of content
type Part struct {
	Text             string            `json:"text,omitzero"`
	Thought          bool              `json:"thought,omitzero"`
	ThoughtSignature []byte            `json:"thoughtSignature,omitempty"`
	InlineData       *Blob             `json:"inlineData,omitzero"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitzero"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitzero"`
	FileData         *FileData         `json:"fileData,omitzero"`
}

// Blob represents inline binary data
type Blob struct {
	MimeType string `json:"mimeType,omitzero"`
	Data     string `json:"data,omitzero"` // base64 encoded
}

// FileData represents a reference to uploaded file
type FileData struct {
	FileUri  string `json:"fileUri,omitzero"`
	MimeType string `json:"mimeType,omitzero"`
}

// FunctionCall represents a function call from the model
type FunctionCall struct {
	ID   string         `json:"id,omitzero"` // internal ID for tracking
	Name string         `json:"name,omitzero"`
	Args map[string]any `json:"args,omitempty"`
}

// FunctionResponse is the result of a function call
type FunctionResponse struct {
	ID       string                  `json:"id,omitzero"` // matches the FunctionCall ID
	Name     string                  `json:"name,omitzero"`
	Response map[string]any          `json:"response,omitempty"`
	Parts    []*FunctionResponsePart `json:"parts,omitempty"`
}

// FunctionResponsePart carries media alongside the function response's JSON
// payload. Only one of InlineData / FileData should be set.
type FunctionResponsePart struct {
	InlineData *FunctionResponseBlob     `json:"inlineData,omitzero"`
	FileData   *FunctionResponseFileData `json:"fileData,omitzero"`
}

type FunctionResponseBlob struct {
	MimeType    string `json:"mimeType,omitzero"`
	Data        string `json:"data,omitzero"` // base64 encoded
	DisplayName string `json:"displayName,omitzero"`
}

type FunctionResponseFileData struct {
	FileUri     string `json:"fileUri,omitzero"`
	MimeType    string `json:"mimeType,omitzero"`
	DisplayName string `json:"displayName,omitzero"`
}

// Tool represents a tool the model can use
type Tool struct {
	FunctionDeclarations []*FunctionDeclaration `json:"functionDeclarations,omitempty"`
}

// FunctionDeclaration describes a function
type FunctionDeclaration struct {
	Name                 string `json:"name,omitzero"`
	Description          string `json:"description,omitzero"`
	Parameters           any    `json:"parameters,omitzero"`           // JSON Schema (standard Gemini API)
	ParametersJsonSchema any    `json:"parametersJsonSchema,omitzero"` // JSON Schema (Gemini CLI format)
}

// ToolConfig configures tool behavior
type ToolConfig struct {
	FunctionCallingConfig *FunctionCallingConfig `json:"functionCallingConfig,omitzero"`
}

// FunctionCallingConfig controls function calling
type FunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitzero"` // AUTO, ANY, NONE
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// GenerationConfig contains generation parameters
type GenerationConfig struct {
	MaxOutputTokens *int     `json:"maxOutputTokens,omitzero"`
	StopSequences   []string `json:"stopSequences,omitempty"`
	CandidateCount  *int     `json:"candidateCount,omitzero"`

	ThinkingConfig *ThinkingConfig `json:"thinkingConfig,omitzero"`

	ResponseMimeType   string `json:"responseMimeType,omitzero"`
	ResponseSchema     any    `json:"responseSchema,omitzero"`
	ResponseJsonSchema any    `json:"responseJsonSchema,omitzero"`

	ResponseModalities []string `json:"responseModalities,omitempty"`
	MediaResolution    string   `json:"mediaResolution,omitzero"`

	Seed             *int     `json:"seed,omitzero"`
	PresencePenalty  *float32 `json:"presencePenalty,omitzero"`
	FrequencyPenalty *float32 `json:"frequencyPenalty,omitzero"`

	ResponseLogprobs bool `json:"responseLogprobs,omitzero"`
	Logprobs         *int `json:"logprobs,omitzero"`
}

// SafetySetting configures safety thresholds
type SafetySetting struct {
	Category  string `json:"category,omitzero"`
	Threshold string `json:"threshold,omitzero"`
}

// GenerateContentResponse is the response from generateContent
type GenerateContentResponse struct {
	ResponseId   string `json:"responseId,omitzero"`
	ModelVersion string `json:"modelVersion,omitzero"`

	Candidates []*Candidate `json:"candidates,omitempty"`

	PromptFeedback *PromptFeedback `json:"promptFeedback,omitzero"`
	Error          *APIError       `json:"error,omitzero"`

	UsageMetadata *UsageMetadata `json:"usageMetadata,omitzero"`
}

// Candidate is a single response candidate
type Candidate struct {
	Content       *Content        `json:"content,omitzero"`
	FinishReason  string          `json:"finishReason,omitzero"`
	Index         int             `json:"index,omitzero"`
	SafetyRatings []*SafetyRating `json:"safetyRatings,omitempty"`
	TokenCount    int             `json:"tokenCount,omitzero"`
}

// SafetyRating represents a safety evaluation
type SafetyRating struct {
	Category    string `json:"category,omitzero"`
	Probability string `json:"probability,omitzero"`
	Blocked     bool   `json:"blocked,omitzero"`
}

// UsageMetadata contains token usage information
type UsageMetadata struct {
	PromptTokenCount        int  `json:"promptTokenCount,omitzero"`
	CachedContentTokenCount int  `json:"cachedContentTokenCount,omitzero"`
	CandidatesTokenCount    int  `json:"candidatesTokenCount,omitzero"`
	ToolUsePromptTokenCount int  `json:"toolUsePromptTokenCount,omitzero"`
	ThoughtsTokenCount      *int `json:"thoughtsTokenCount,omitzero"`
	TotalTokenCount         int  `json:"totalTokenCount,omitzero"`

	PromptTokensDetails        []*ModalityTokenCount `json:"promptTokensDetails,omitempty"`
	CacheTokensDetails         []*ModalityTokenCount `json:"cacheTokensDetails,omitempty"`
	CandidatesTokensDetails    []*ModalityTokenCount `json:"candidatesTokensDetails,omitempty"`
	ToolUsePromptTokensDetails []*ModalityTokenCount `json:"toolUsePromptTokensDetails,omitempty"`

	Tier string `json:"serviceTier,omitzero"`
}

// ModalityTokenCount reports tokens broken down by modality.
type ModalityTokenCount struct {
	Modality   string `json:"modality,omitzero"`
	TokenCount int    `json:"tokenCount,omitzero"`
}

// PromptFeedback contains feedback about the prompt
type PromptFeedback struct {
	BlockReason   string          `json:"blockReason,omitzero"`
	SafetyRatings []*SafetyRating `json:"safetyRatings,omitempty"`
}

// CountTokensRequest is the request for countTokens. The official body is
// either `contents` or a nested `generateContentRequest`; the flat
// systemInstruction/tools form is a Wingman extension kept for older clients.
type CountTokensRequest struct {
	Contents               []*Content              `json:"contents,omitempty"`
	GenerateContentRequest *GenerateContentRequest `json:"generateContentRequest,omitzero"`

	SystemInstruction *Content `json:"systemInstruction,omitzero"`
	Tools             []*Tool  `json:"tools,omitempty"`
}

// CountTokensResponse is the response from countTokens
type CountTokensResponse struct {
	TotalTokens         int                   `json:"totalTokens"`
	PromptTokensDetails []*ModalityTokenCount `json:"promptTokensDetails,omitempty"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error *APIError `json:"error,omitzero"`
}

// APIError contains error details
type APIError struct {
	Code    int              `json:"code,omitzero"`
	Message string           `json:"message,omitzero"`
	Status  string           `json:"status,omitzero"`
	Details []map[string]any `json:"details,omitempty"`
}
