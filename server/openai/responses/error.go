package responses

import (
	"github.com/adrianliechti/wingman/pkg/provider"
)

// A failed Response has a closed set of codes, unlike the standalone error
// event. Preserve recognized codes and translate foreign provider failures.
// OpenAI also sends the undocumented insufficient_quota and
// context_length_exceeded, which clients use to stop retrying or to compact.
// https://developers.openai.com/api/reference/resources/responses/streaming-events
func responseErrorCode(err error) string {
	code := provider.NativeType(err, provider.VendorOpenAI)
	switch code {
	case "server_error", "rate_limit_exceeded", "invalid_prompt",
		"data_residency_mismatch", "bio_policy", "misalignment_policy_violation",
		"vector_store_timeout", "invalid_image", "invalid_image_format",
		"invalid_base64_image", "invalid_image_url", "image_too_large",
		"image_too_small", "image_parse_error", "image_content_policy_violation",
		"invalid_image_mode", "image_file_too_large", "unsupported_image_media_type",
		"empty_image_file", "failed_to_download_image", "image_file_not_found",
		"insufficient_quota", "context_length_exceeded":
		return code
	case "credit_balance_exhausted", "organization_spend_limit_exceeded",
		"project_spend_limit_exceeded", "organization_usage_limit_exceeded":
		// Spend limits do not clear by waiting, so never report them as rate limits.
		return "insufficient_quota"
	}
	switch provider.KindFromError(err) {
	case provider.ErrorKindQuotaExceeded, provider.ErrorKindBilling:
		return "insufficient_quota"
	case provider.ErrorKindRateLimited:
		return "rate_limit_exceeded"
	}
	return "server_error"
}
