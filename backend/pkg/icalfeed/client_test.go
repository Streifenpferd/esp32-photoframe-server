package icalfeed

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGetTodayEventsExpandsDailyRuleAndExDate(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Berlin")
	require.NoError(t, err)
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, loc)
	icsBody := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//PhotoFrame Test//EN
BEGIN:VEVENT
UID:daily@example
DTSTART:20260913T070000Z
DTEND:20260913T073000Z
RRULE:FREQ=DAILY;COUNT=5
SUMMARY:Morning recurring
END:VEVENT
BEGIN:VEVENT
UID:evening@example
DTSTART:20260914T160000Z
DTEND:20260914T170000Z
SUMMARY:Evening appointment
END:VEVENT
END:VCALENDAR
`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Content-Type","text/calendar"); _,_=fmt.Fprint(w,icsBody) }))
	defer ts.Close()
	c:=NewClient(); c.now=func()time.Time{return now}
	events,err:=c.GetTodayEvents(ts.URL,"Europe/Berlin"); require.NoError(t,err); require.Len(t,events,1); require.Equal(t,"Evening appointment",events[0].Summary); require.Equal(t,18,events[0].Start.In(loc).Hour())
}

func TestGetTodayEventsAllDay(t *testing.T) {
	loc,err:=time.LoadLocation("Europe/Berlin"); require.NoError(t,err); now:=time.Date(2026,9,14,12,0,0,0,loc)
	icsBody:=`BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//PhotoFrame Test//EN
BEGIN:VEVENT
UID:holiday@example
DTSTART;VALUE=DATE:20260914
DTEND;VALUE=DATE:20260915
SUMMARY:School holiday
END:VEVENT
END:VCALENDAR
`
	ts:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){_,_=fmt.Fprint(w,icsBody)})); defer ts.Close()
	c:=NewClient(); c.now=func()time.Time{return now}; events,err:=c.GetTodayEvents(ts.URL,"Europe/Berlin"); require.NoError(t,err); require.Len(t,events,1); require.True(t,events[0].AllDay); require.Equal(t,"School holiday",events[0].Summary)
}

func TestParseDuration(t *testing.T){d,err:=parseDuration("P1DT2H30M");require.NoError(t,err);require.Equal(t,26*time.Hour+30*time.Minute,d)}

func TestGetUpcomingEventsIncludesFutureDays(t *testing.T){
	loc,err:=time.LoadLocation("Europe/Berlin");require.NoError(t,err);now:=time.Date(2026,9,14,12,0,0,0,loc)
	icsBody:=`BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//PhotoFrame Test//EN
BEGIN:VEVENT
UID:today@example
DTSTART:20260914T160000Z
DTEND:20260914T170000Z
SUMMARY:Today
END:VEVENT
BEGIN:VEVENT
UID:tomorrow@example
DTSTART:20260915T160000Z
DTEND:20260915T170000Z
SUMMARY:Tomorrow
END:VEVENT
END:VCALENDAR
`
	ts:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){_,_=fmt.Fprint(w,icsBody)}));defer ts.Close()
	c:=NewClient();c.now=func()time.Time{return now};events,err:=c.GetUpcomingEvents(ts.URL,"Europe/Berlin",7,5);require.NoError(t,err);require.Len(t,events,2);require.Equal(t,"Today",events[0].Summary);require.Equal(t,"Tomorrow",events[1].Summary)
}
