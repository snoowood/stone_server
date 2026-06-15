package logger

import (
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Init configures zerolog output and the global level.
// logLevel 가 비어 있으면 AppEnv 기준 기본값(development=debug, 그 외=info)을 쓴다.
func Init(appEnv, logLevel string) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	if appEnv == "development" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}
	// production: JSON to stderr (default zerolog behavior)

	zerolog.SetGlobalLevel(resolveLevel(appEnv, logLevel))
}

func resolveLevel(appEnv, logLevel string) zerolog.Level {
	if logLevel != "" {
		if lvl, err := zerolog.ParseLevel(strings.ToLower(logLevel)); err == nil {
			return lvl
		}
		// 잘못된 값이면 기본값으로 폴백(부팅은 막지 않는다).
	}
	if appEnv == "development" {
		return zerolog.DebugLevel
	}
	return zerolog.InfoLevel
}
