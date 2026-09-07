package mailer

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

type Console struct {
	From string
	W    io.Writer
}

func (c *Console) Send(_ context.Context, m Message) error {
	from := m.From
	if from == "" {
		from = c.From
	}
	if err := (Message{From: from, To: m.To, Subject: m.Subject}).Validate(); err != nil {
		return err
	}
	w := c.W
	if w == nil {
		w = os.Stdout
	}
	_, _ = fmt.Fprintf(w, "\n--- mailer.console %s ---\nFrom: %s\nTo: %s\nSubject: %s\n\n%s\n------------------------------\n",
		time.Now().UTC().Format(time.RFC3339), from, m.To, m.Subject, m.TextBody)
	return nil
}
