package handler

import (
	"bytes"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

const maxJSONBodySize int64 = 1 << 20

// LimitBody bounds request-body reads, including chunked bodies without a Content-Length.
func LimitBody(max int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body == nil {
			c.Next()
			return
		}
		originalBody := c.Request.Body
		if c.Request.ContentLength > max {
			if err := originalBody.Close(); err != nil {
				_ = c.Error(err)
			}
			c.AbortWithStatus(http.StatusRequestEntityTooLarge)
			return
		}

		body, readErr := io.ReadAll(io.LimitReader(originalBody, max+1))
		closeErr := originalBody.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			_ = c.Error(err)
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		if int64(len(body)) > max {
			c.AbortWithStatus(http.StatusRequestEntityTooLarge)
			return
		}

		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		c.Request.ContentLength = int64(len(body))
		c.Next()
	}
}
