package db

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
)

func TestIsRowSecurityViolation(t *testing.T) {
	using := `new row violates row-level security policy (USING expression) for table "webhook_endpoints"`
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"the conflict refusal, as Postgres 17.12 reports it": {&pgconn.PgError{Code: "42501", Routine: "ExecWithCheckOptions", Message: using}, true},
		"wrapped":                                              {fmt.Errorf("save: %w", &pgconn.PgError{Code: "42501", Routine: "ExecWithCheckOptions", Message: using}), true},
		"a translated message fails closed":                    {&pgconn.PgError{Code: "42501", Routine: "ExecWithCheckOptions", Message: "la nueva fila viola la política de seguridad de registros"}, false},
		"a WITH CHECK refusal from the same routine":           {&pgconn.PgError{Code: "42501", Routine: "ExecWithCheckOptions", Message: `new row violates row-level security policy for table "webhook_endpoints"`}, false},
		"no routine: the English conflict text":                {&pgconn.PgError{Code: "42501", Message: using}, true},
		"no routine: a WITH CHECK refusal is not the conflict": {&pgconn.PgError{Code: "42501", Message: `new row violates row-level security policy for table "webhook_endpoints"`}, false},
		"another insufficient-privilege error":                 {&pgconn.PgError{Code: "42501", Routine: "aclcheck_error", Message: "permission denied for table webhook_endpoints"}, false},
		"another code":                                         {&pgconn.PgError{Code: "23505", Routine: "ExecWithCheckOptions", Message: using}, false},
		"not a Postgres error":                                 {errors.New(using), false},
		"nil":                                                  {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsRowSecurityViolation(tc.err))
		})
	}
}
