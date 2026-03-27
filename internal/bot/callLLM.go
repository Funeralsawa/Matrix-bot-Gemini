package bot

import (
	"context"
	"time"

	"google.golang.org/genai"
)

func Call(history []*genai.Content, reqConfig *genai.GenerateContentConfig) (*genai.GenerateContentResponse, time.Duration, error) {
	now := time.Now()
	timeoutCtx, cancel := context.WithTimeout(ctx, botConfig.Model.TimeOutWhen)
	defer cancel()
	result, err := gclient.Models.GenerateContent(
		timeoutCtx,
		botConfig.Model.Model,
		history,
		reqConfig,
	)
	costTime := time.Since(now)
	return result, costTime, err
}
