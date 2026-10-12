package boot

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	rootmcp "altalune.id/opensheet/mcp"
)

func decodeFailure(t *testing.T, input string) error {
	t.Helper()
	err := protojson.Unmarshal([]byte(input), &apperrorv1.ErrorDetail{})
	require.Error(t, err, "protojson accepted %s", input)
	return err
}

func TestMCPErrorPayloadMapsUndecodableArgumentsToAValidationError(t *testing.T) {
	tests := []struct {
		name  string
		input string
		meta  map[string]string
	}{
		{name: "a wrong JSON type", input: `{"code":{}}`, meta: map[string]string{"tool": "todo_create", "field": "code"}},
		{name: "an unknown field", input: `{"bogus":1}`, meta: map[string]string{"tool": "todo_create", "field": "bogus"}},
		{name: "a string where an object belongs", input: `{"meta":"x"}`, meta: map[string]string{"tool": "todo_create"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cause := rootmcp.NewInvalidArgumentsError("todo_create", decodeFailure(t, tc.input))

			payload := mcpErrorPayload(t.Context(), cause)

			require.Equal(t, apperror.CodeValidation, payload.Code)
			require.Equal(t, tc.meta, payload.Meta)
			require.Contains(t, payload.Message, cause.Reason)
		})
	}
}
