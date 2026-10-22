package utils

import "go.uber.org/zap"

func Log() *zap.SugaredLogger {
	return zap.L().Sugar()
}
