package content

// Role represents the role of a content creator
type Role string

const (
	RoleUser  Role = "user"
	RoleModel Role = "model"
)

// FinishReason represents why the model stopped generating tokens
type FinishReason string

const (
	FinishReasonUnspecified           FinishReason = "FINISH_REASON_UNSPECIFIED"
	FinishReasonStop                  FinishReason = "STOP"
	FinishReasonMaxTokens             FinishReason = "MAX_TOKENS"
	FinishReasonSafety                FinishReason = "SAFETY"
	FinishReasonRecitation            FinishReason = "RECITATION"
	FinishReasonOther                 FinishReason = "OTHER"
	FinishReasonMalformedFunctionCall FinishReason = "MALFORMED_FUNCTION_CALL"
)

// HarmCategory represents a category of harm
type HarmCategory string

const (
	HarmCategoryUnspecified      HarmCategory = "HARM_CATEGORY_UNSPECIFIED"
	HarmCategoryHateSpeech       HarmCategory = "HARM_CATEGORY_HATE_SPEECH"
	HarmCategorySexuallyExplicit HarmCategory = "HARM_CATEGORY_SEXUALLY_EXPLICIT"
	HarmCategoryDangerousContent HarmCategory = "HARM_CATEGORY_DANGEROUS_CONTENT"
	HarmCategoryHarassment       HarmCategory = "HARM_CATEGORY_HARASSMENT"
	HarmCategoryCivicIntegrity   HarmCategory = "HARM_CATEGORY_CIVIC_INTEGRITY"
)

// HarmProbability represents the probability of harm
type HarmProbability string

const (
	HarmProbabilityUnspecified HarmProbability = "HARM_PROBABILITY_UNSPECIFIED"
	HarmProbabilityNegligible  HarmProbability = "NEGLIGIBLE"
	HarmProbabilityLow         HarmProbability = "LOW"
	HarmProbabilityMedium      HarmProbability = "MEDIUM"
	HarmProbabilityHigh        HarmProbability = "HIGH"
)

// HarmBlockThreshold represents the threshold for blocking harm
type HarmBlockThreshold string

const (
	HarmBlockThresholdUnspecified HarmBlockThreshold = "HARM_BLOCK_THRESHOLD_UNSPECIFIED"
	HarmBlockLowAndAbove          HarmBlockThreshold = "BLOCK_LOW_AND_ABOVE"
	HarmBlockMediumAndAbove       HarmBlockThreshold = "BLOCK_MEDIUM_AND_ABOVE"
	HarmBlockOnlyHigh             HarmBlockThreshold = "BLOCK_ONLY_HIGH"
	HarmBlockNone                 HarmBlockThreshold = "BLOCK_NONE"
)

// GenerateContentRequest represents a request to generate content
// https://ai.google.dev/api/generate-content
type GenerateContentRequest struct {
	Contents          []Content         `json:"contents"`
	Tools             []Tool            `json:"tools,omitempty"`
	ToolConfig        *ToolConfig       `json:"toolConfig,omitempty"`
	SafetySettings    []SafetySetting   `json:"safetySettings,omitempty"`
	SystemInstruction *Content          `json:"systemInstruction,omitempty"`
	GenerationConfig  *GenerationConfig `json:"generationConfig,omitempty"`
	CachedContent     string            `json:"cachedContent,omitempty"`
}

// Content represents the content of a message
type Content struct {
	Parts []Part `json:"parts"`
	Role  Role   `json:"role,omitempty"`
}

// Part represents a part of the content
type Part struct {
	Text             string            `json:"text,omitempty"`
	InlineData       *Blob             `json:"inlineData,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
	FileData         *FileData         `json:"fileData,omitempty"`
}

// Blob represents inline data
type Blob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64 encoded
}

// FileData represents file data reference
type FileData struct {
	MimeType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri"`
}

// FunctionCall represents a function call from the model
type FunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

// FunctionResponse represents a response to a function call
type FunctionResponse struct {
	Name     string `json:"name"`
	Response any    `json:"response"`
}

// Tool represents a tool that can be used by the model
type Tool struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations,omitempty"`
	CodeExecution        *CodeExecution        `json:"codeExecution,omitempty"`
}

// FunctionDeclaration represents a function declaration
type FunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// CodeExecution enables code execution capability
type CodeExecution struct{}

// ToolConfig configures tool behavior
type ToolConfig struct {
	FunctionCallingConfig *FunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

// FunctionCallingConfig configures function calling behavior
type FunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"` // AUTO, ANY, NONE
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// SafetySetting configures safety thresholds
type SafetySetting struct {
	Category  HarmCategory       `json:"category"`
	Threshold HarmBlockThreshold `json:"threshold"`
}

// GenerationConfig configures generation parameters
type GenerationConfig struct {
	StopSequences    []string        `json:"stopSequences,omitempty"`
	ResponseMimeType string          `json:"responseMimeType,omitempty"`
	ResponseSchema   any             `json:"responseSchema,omitempty"`
	CandidateCount   *int            `json:"candidateCount,omitempty"`
	MaxOutputTokens  *int            `json:"maxOutputTokens,omitempty"`
	Temperature      *float32        `json:"temperature,omitempty"`
	TopP             *float32        `json:"topP,omitempty"`
	TopK             *int            `json:"topK,omitempty"`
	Seed             *int            `json:"seed,omitempty"`
	PresencePenalty  *float32        `json:"presencePenalty,omitempty"`
	FrequencyPenalty *float32        `json:"frequencyPenalty,omitempty"`
	ThinkingConfig   *ThinkingConfig `json:"thinkingConfig,omitempty"`
}

// ThinkingConfig configures thinking/reasoning behavior
type ThinkingConfig struct {
	IncludeThoughts *bool `json:"includeThoughts,omitempty"`
	ThinkingBudget  *int  `json:"thinkingBudget,omitempty"`
}

// GenerateContentResponse represents a response from generateContent
type GenerateContentResponse struct {
	Candidates     []Candidate     `json:"candidates,omitempty"`
	PromptFeedback *PromptFeedback `json:"promptFeedback,omitempty"`
	UsageMetadata  *UsageMetadata  `json:"usageMetadata,omitempty"`
	ModelVersion   string          `json:"modelVersion,omitempty"`
}

// Candidate represents a response candidate
type Candidate struct {
	Content          *Content          `json:"content,omitempty"`
	FinishReason     FinishReason      `json:"finishReason,omitempty"`
	SafetyRatings    []SafetyRating    `json:"safetyRatings,omitempty"`
	CitationMetadata *CitationMetadata `json:"citationMetadata,omitempty"`
	TokenCount       int               `json:"tokenCount,omitempty"`
	Index            int               `json:"index,omitempty"`
}

// SafetyRating represents a safety rating
type SafetyRating struct {
	Category    HarmCategory    `json:"category"`
	Probability HarmProbability `json:"probability"`
	Blocked     bool            `json:"blocked,omitempty"`
}

// CitationMetadata contains citation information
type CitationMetadata struct {
	CitationSources []CitationSource `json:"citationSources,omitempty"`
}

// CitationSource represents a citation source
type CitationSource struct {
	StartIndex int    `json:"startIndex,omitempty"`
	EndIndex   int    `json:"endIndex,omitempty"`
	URI        string `json:"uri,omitempty"`
	License    string `json:"license,omitempty"`
}

// PromptFeedback contains feedback about the prompt
type PromptFeedback struct {
	BlockReason   string         `json:"blockReason,omitempty"`
	SafetyRatings []SafetyRating `json:"safetyRatings,omitempty"`
}

// UsageMetadata contains token usage information
type UsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount      int `json:"totalTokenCount,omitempty"`
	ThoughtsTokenCount   int `json:"thoughtsTokenCount,omitempty"`
}
