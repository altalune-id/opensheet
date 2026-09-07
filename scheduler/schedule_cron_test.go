package scheduler_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/template/scheduler"
)

func TestCronExpr_AcceptedForms(t *testing.T) {
	for _, expr := range []string{
		"0 */6 * * *",    // 5-field standard
		"30 0 */6 * * *", // 6-field with seconds
		"@every 30s",     // descriptor
		"@daily",
	} {
		t.Run(expr, func(t *testing.T) {
			s, err := scheduler.CronExpr(expr, time.UTC)
			require.NoError(t, err)
			require.NotNil(t, s)
		})
	}
}

func TestCronExpr_Rejects(t *testing.T) {
	for _, expr := range []string{"", "not a cron", "* * *"} {
		t.Run(expr, func(t *testing.T) {
			_, err := scheduler.CronExpr(expr, time.UTC)
			require.Error(t, err)
		})
	}
}

func TestCronExpr_NextEverySixHours(t *testing.T) {
	s := scheduler.MustCronExpr("0 */6 * * *", time.UTC)
	got := s.Next(time.Date(2026, 1, 1, 7, 15, 0, 0, time.UTC))
	require.Equal(t, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), got)
}

func TestCronExpr_HonoursLocation(t *testing.T) {
	jkt, err := time.LoadLocation("Asia/Jakarta") // UTC+7, no DST
	require.NoError(t, err)
	s := scheduler.MustCronExpr("0 0 * * *", jkt)
	got := s.Next(time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC))
	require.Equal(t, time.Date(2026, 1, 2, 0, 0, 0, 0, jkt), got.In(jkt))
}

func TestMustCronExpr_Panics(t *testing.T) {
	require.Panics(t, func() { scheduler.MustCronExpr("nope", time.UTC) })
}

func TestCronExpr_String(t *testing.T) {
	require.Equal(t, `cron "0 */6 * * *" UTC`, scheduler.MustCronExpr("0 */6 * * *", time.UTC).String())
}
