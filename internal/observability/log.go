package observability

import (
	"log/slog"
	"os"
)

func Configure() { slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil))) }
