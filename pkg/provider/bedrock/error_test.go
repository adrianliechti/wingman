package bedrock

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// Throttling clears by waiting; a service quota does not. Both are 429.
func TestQuotaIsNotThrottling(t *testing.T) {
	for _, tc := range []struct {
		err  error
		kind provider.ErrorKind
	}{
		{&types.ServiceQuotaExceededException{Message: aws.String("quota")}, provider.ErrorKindQuotaExceeded},
		{&types.ThrottlingException{Message: aws.String("slow down")}, provider.ErrorKindRateLimited},
		{&types.ModelNotReadyException{Message: aws.String("warming up")}, provider.ErrorKindUnavailable},
	} {
		converted := convertError(tc.err)
		_, ok := converted.(*provider.ProviderError)
		if !ok || provider.KindFromError(converted) != tc.kind {
			t.Errorf("%T: kind %q, error %#v", tc.err, provider.KindFromError(converted), converted)
		}
	}
}
