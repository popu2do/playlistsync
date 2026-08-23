package auth

import (
	"context"
	"fmt"
	"time"
)

type cdpEvalResult struct {
	Result struct {
		Type  string `json:"type"`
		Value any    `json:"value"`
	} `json:"result"`
}

// EvaluateCDPResult evaluates a JS expression in the target page and returns the string result
func EvaluateCDPResult(port int, pageID string, expression string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return EvaluateCDPResultWithContext(ctx, port, pageID, expression)
}

// EvaluateCDPResultWithContext evaluates a JS expression in the target page using a context
func EvaluateCDPResultWithContext(ctx context.Context, port int, pageID string, expression string) (string, error) {
	params := map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  true,
	}
	res, err := cdpCall[cdpEvalResult](ctx, port, pageID, 2, "Runtime.evaluate", params)
	if err != nil {
		return "", err
	}
	val := res.Result.Value
	if valStr, ok := val.(string); ok {
		return valStr, nil
	}
	if val != nil {
		return fmt.Sprintf("%v", val), nil
	}
	return "", nil
}
