package logs

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var log *zap.Logger

func init() {
	cfg := zap.NewProductionConfig()
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	l, err := cfg.Build()
	if err != nil {
		panic(err)
	}
	log = l
}

func Info(args ...interface{}) {
	log.Sugar().Info(args...)
}

func Debug(args ...interface{}) {
	log.Sugar().Debug(args...)
}

func Error(args ...interface{}) {
	log.Sugar().Error(args...)
}
