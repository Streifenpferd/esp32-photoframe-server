package handler

import (
	"net/http"
	"strings"

	"github.com/aitjcize/esp32-photoframe-server/backend/pkg/caldavfeed"
	"github.com/labstack/echo/v4"
)

// CalDAVHandler exposes only read-only discovery for the settings UI.
// Calendar event fetching itself happens server-side during image rendering.
type CalDAVHandler struct {
	client *caldavfeed.Client
}

func NewCalDAVHandler(client *caldavfeed.Client) *CalDAVHandler {
	return &CalDAVHandler{client: client}
}

type calDAVCalendarsRequest struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
	SkipCert bool   `json:"skip_cert"`
}

func (h *CalDAVHandler) ListCalendars(c echo.Context) error {
	if h == nil || h.client == nil {
		return respondError(c, http.StatusServiceUnavailable, "CalDAV client is not available")
	}
	var req calDAVCalendarsRequest
	if err := c.Bind(&req); err != nil {
		return respondError(c, http.StatusBadRequest, "invalid CalDAV request")
	}
	if strings.TrimSpace(req.URL) == "" {
		return respondError(c, http.StatusBadRequest, "CalDAV URL is required")
	}

	calendars, err := h.client.ListCalendars(caldavfeed.Config{
		Endpoint:      strings.TrimSpace(req.URL),
		Username:      req.Username,
		Password:      req.Password,
		SkipTLSVerify: req.SkipCert,
	})
	if err != nil {
		return respondError(c, http.StatusBadGateway, "CalDAV discovery failed: "+err.Error())
	}
	return c.JSON(http.StatusOK, calendars)
}
