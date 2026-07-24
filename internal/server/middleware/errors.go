package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type ErrorBody struct {
	Error     string `json:"error"`
	RequestID string `json:"request_id,omitempty"`
}

func AbortWithError(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, ErrorBody{
		Error:     message,
		RequestID: GetRequestID(c),
	})
}

func JSONError(c *gin.Context, status int, message string) {
	c.JSON(status, ErrorBody{
		Error:     message,
		RequestID: GetRequestID(c),
	})
}

func BadRequest(c *gin.Context, message string) {
	JSONError(c, http.StatusBadRequest, message)
}

func NotFound(c *gin.Context, message string) {
	JSONError(c, http.StatusNotFound, message)
}

func InternalError(c *gin.Context, message string) {
	JSONError(c, http.StatusInternalServerError, message)
}
