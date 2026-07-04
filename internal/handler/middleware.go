package handler

import (
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

func RequestIDMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			reqID := c.Request().Header.Get(echo.HeaderXRequestID)
			if reqID == "" {
				reqID = uuid.New().String()
			}
			c.Response().Header().Set(echo.HeaderXRequestID, reqID)
			return next(c)
		}
	}
}
