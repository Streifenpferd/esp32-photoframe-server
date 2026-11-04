package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aitjcize/esp32-photoframe-server/backend/pkg/caldavfeed"
	"github.com/aitjcize/esp32-photoframe-server/backend/pkg/gcalendar"
	"github.com/aitjcize/esp32-photoframe-server/backend/pkg/googlephotos"
	"github.com/aitjcize/esp32-photoframe-server/backend/pkg/icalfeed"
)

const (
	defaultCalendarLookaheadDays = 7
	defaultCalendarMaxEvents     = 5
)

// CalendarEventService normalizes all supported calendar sources to the
// renderer's existing []gcalendar.Event type. It is deliberately read-only.
type CalendarEventService struct {
	settings   *SettingsService
	google     *gcalendar.Client
	googleAuth *googlephotos.Client
	ical       *icalfeed.Client
	caldav     *caldavfeed.Client
}

func NewCalendarEventService(
	settings *SettingsService,
	google *gcalendar.Client,
	googleAuth *googlephotos.Client,
	ical *icalfeed.Client,
	caldav *caldavfeed.Client,
) *CalendarEventService {
	return &CalendarEventService{
		settings:   settings,
		google:     google,
		googleAuth: googleAuth,
		ical:       ical,
		caldav:     caldav,
	}
}

// GetEvents fetches events for the currently configured calendar source.
// calendarID is the per-device Google calendar id or CalDAV collection path.
// For a single iCal feed it is ignored.
func (s *CalendarEventService) GetEvents(calendarID, deviceTimezone string) ([]gcalendar.Event, error) {
	if s == nil || s.settings == nil {
		return nil, nil
	}

	source := strings.ToLower(strings.TrimSpace(s.setting("calendar_source")))
	icalURL := strings.TrimSpace(s.setting("ical_url"))
	caldavURL := strings.TrimSpace(s.setting("caldav_url"))

	if source == "" {
		switch {
		case caldavURL != "":
			source = "caldav"
		case icalURL != "":
			source = "ical"
		default:
			source = "google"
		}
	}

	tz := strings.TrimSpace(s.setting("ical_timezone"))
	if tz == "" {
		tz = strings.TrimSpace(deviceTimezone)
	}
	lookahead := s.intSetting("calendar_lookahead_days", defaultCalendarLookaheadDays, 1, 31)
	maxEvents := s.intSetting("calendar_max_events", defaultCalendarMaxEvents, 1, 20)

	switch source {
	case "ical":
		if icalURL == "" {
			return nil, fmt.Errorf("calendar source is iCal but no iCal URL is configured")
		}
		if s.ical == nil {
			return nil, fmt.Errorf("iCal client is not available")
		}
		return s.ical.GetUpcomingEvents(icalURL, tz, lookahead, maxEvents)

	case "caldav":
		if caldavURL == "" {
			return nil, fmt.Errorf("calendar source is CalDAV but no CalDAV URL is configured")
		}
		if strings.TrimSpace(calendarID) == "" {
			return nil, fmt.Errorf("no CalDAV calendar selected for this device")
		}
		if s.caldav == nil {
			return nil, fmt.Errorf("CalDAV client is not available")
		}
		cfg := caldavfeed.Config{
			Endpoint:      caldavURL,
			Username:      s.setting("caldav_username"),
			Password:      s.setting("caldav_password"),
			SkipTLSVerify: strings.EqualFold(strings.TrimSpace(s.setting("caldav_skip_cert")), "true"),
		}
		return s.caldav.GetUpcomingEvents(cfg, calendarID, tz, lookahead, maxEvents)

	case "google":
		if s.google == nil || s.googleAuth == nil {
			return nil, nil
		}
		httpClient, err := s.googleAuth.GetClient()
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(calendarID) == "" {
			calendarID = "primary"
		}
		events, err := s.google.GetTodayEvents(httpClient, calendarID, tz)
		if err != nil {
			return nil, err
		}
		if len(events) > maxEvents {
			events = events[:maxEvents]
		}
		return events, nil

	default:
		return nil, fmt.Errorf("unsupported calendar source %q", source)
	}
}

func (s *CalendarEventService) setting(key string) string {
	v, err := s.settings.Get(key)
	if err != nil {
		return ""
	}
	return v
}

func (s *CalendarEventService) intSetting(key string, fallback, min, max int) int {
	raw := strings.TrimSpace(s.setting(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return fallback
	}
	return n
}
