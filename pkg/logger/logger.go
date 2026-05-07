package logger

import (
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func Init(appEnv string) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	if appEnv == "development" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}
	// production: JSON to stderr (default zerolog behavior)
}
