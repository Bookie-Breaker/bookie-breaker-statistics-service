package handler

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type Meta struct {
	Timestamp  string          `json:"timestamp"`
	RequestID  string          `json:"request_id"`
	Pagination *PaginationMeta `json:"pagination,omitempty"`
}

type PaginationMeta struct {
	Limit      int    `json:"limit"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func SuccessResponse(c echo.Context, data any) error {
	return c.JSON(http.StatusOK, map[string]any{
		"data": data,
		"meta": newMeta(c),
	})
}

func CreatedResponse(c echo.Context, data any) error {
	return c.JSON(http.StatusCreated, map[string]any{
		"data": data,
		"meta": newMeta(c),
	})
}

func AcceptedResponse(c echo.Context, data any) error {
	return c.JSON(http.StatusAccepted, map[string]any{
		"data": data,
		"meta": newMeta(c),
	})
}

func PaginatedResponse(c echo.Context, data any, limit int, hasMore bool, nextCursor string) error {
	meta := newMeta(c)
	meta.Pagination = &PaginationMeta{
		Limit:      limit,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}
	return c.JSON(http.StatusOK, map[string]any{
		"data": data,
		"meta": meta,
	})
}

func ErrorResponse(c echo.Context, status int, code, message string) error {
	return c.JSON(status, map[string]any{
		"error": ErrorDetail{Code: code, Message: message},
		"meta":  newMeta(c),
	})
}

func newMeta(c echo.Context) Meta {
	reqID := c.Response().Header().Get(echo.HeaderXRequestID)
	if reqID == "" {
		reqID = uuid.New().String()
	}
	return Meta{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		RequestID: reqID,
	}
}
