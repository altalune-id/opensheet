package authl

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoopbackOptions(t *testing.T) {
	o := loopbackConfig{host: "127.0.0.1", openBrowser: true, callbackTTL: 5 * time.Minute}

	WithLoopbackHost("::1")(&o)
	assert.Equal(t, "::1", o.host)

	WithLoopbackPort(4242)(&o)
	assert.Equal(t, 4242, o.port)

	WithOpenBrowser(false)(&o)
	assert.False(t, o.openBrowser)

	WithCallbackTTL(30 * time.Second)(&o)
	assert.Equal(t, 30*time.Second, o.callbackTTL)
}

func TestWriteLoopbackHTML_Success(t *testing.T) {
	rec := httptest.NewRecorder()
	writeLoopbackHTML(rec, true, "")

	res := rec.Result()
	defer res.Body.Close()
	assert.Equal(t, 200, res.StatusCode)
	assert.Contains(t, res.Header.Get("Content-Type"), "text/html")
	assert.Contains(t, rec.Body.String(), "signed in")
}

func TestWriteLoopbackHTML_Failure(t *testing.T) {
	rec := httptest.NewRecorder()
	writeLoopbackHTML(rec, false, "<script>alert(1)</script>")

	res := rec.Result()
	defer res.Body.Close()
	assert.Equal(t, 400, res.StatusCode)
	body := rec.Body.String()
	assert.Contains(t, body, "failed")
	assert.False(t, strings.Contains(body, "<script>"), "must escape html")
	assert.Contains(t, body, "&lt;script&gt;")
}
