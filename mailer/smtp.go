package mailer

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"
)

// SMTP sends via net/smtp, honouring cfg.SMTP for auth/host.
type SMTP struct {
	Cfg Config
}

// Send delivers m via SMTP.
func (s *SMTP) Send(_ context.Context, m Message) error {
	from := m.From
	if from == "" {
		from = s.Cfg.From
	}
	// SECURITY: reject CR/LF in header fields (RFC 5322 sec 2.2.3) so callers can't inject extra headers or split the body.
	if err := (Message{From: from, To: m.To, Subject: m.Subject}).Validate(); err != nil {
		return err
	}
	addr := fmt.Sprintf("%s:%d", s.Cfg.SMTP.Host, s.Cfg.SMTP.Port)
	var auth smtp.Auth
	if s.Cfg.SMTP.User != "" {
		auth = smtp.PlainAuth("", s.Cfg.SMTP.User, s.Cfg.SMTP.Pass, s.Cfg.SMTP.Host)
	}
	body := buildMIME(from, m)
	return smtp.SendMail(addr, auth, from, []string{m.To}, []byte(body))
}

// stripCRLF removes any CR or LF from v.
func stripCRLF(v string) string {
	if !strings.ContainsAny(v, "\r\n") {
		return v
	}
	return strings.NewReplacer("\r", "", "\n", "").Replace(v)
}

func buildMIME(from string, m Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", stripCRLF(from))
	fmt.Fprintf(&b, "To: %s\r\n", stripCRLF(m.To))
	fmt.Fprintf(&b, "Subject: %s\r\n", stripCRLF(m.Subject))
	if m.HTMLBody != "" {
		fmt.Fprintf(&b, "MIME-Version: 1.0\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s", m.HTMLBody)
	} else {
		fmt.Fprintf(&b, "MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s", m.TextBody)
	}
	return b.String()
}
