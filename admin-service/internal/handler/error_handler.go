package handler

import (
	"net/http"
	"os"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

func CustomHTTPErrorHandler(err error, c echo.Context) {
	code := http.StatusInternalServerError
	if he, ok := err.(*echo.HTTPError); ok {
		code = he.Code
		if code == http.StatusNotFound {
			filePath := "/app/static/404.html"
			content, readErr := os.ReadFile(filePath)
			if readErr != nil {
				c.Logger().Error("Could not read custom 404 page", zap.Error(readErr), zap.String("path", filePath))
				c.String(http.StatusNotFound, "404 Not Found")
			} else {
				c.HTMLBlob(http.StatusNotFound, content)
			}
			return
		}
	}
	if code >= 500 {
		c.Logger().Error(err)
	}
	if !c.Response().Committed {
		if c.Request().Method == http.MethodHead {
			err = c.NoContent(code)
		} else {
			err = c.String(code, http.StatusText(code))
		}
		if err != nil {
			c.Logger().Error(err)
		}
	}
}
