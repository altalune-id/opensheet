package logger_test

import (
	"testing"

	"altalune.id/template/logger"
)

func TestNew_JSONHandlerByDefault(t *testing.T) {
	log := logger.New(logger.Config{Level: "info", Format: "json"})
	if log == nil {
		t.Fatal("New returned nil")
	}
}

func TestConfig_ValidateRejectsUnknownLevel(t *testing.T) {
	c := logger.Config{Level: "trace"}
	if err := c.Validate(); err == nil {
		t.Error("Validate accepted unknown level")
	}
}
