package logging

import (
	"io"
	"log/slog"
)

func New(output io.Writer, level string) (*slog.Logger, error) {
	var logLevel slog.Level
	if err := logLevel.UnmarshalText([]byte(level)); err != nil {
		return nil, err
	}
	return slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{Level: logLevel})), nil
}
