package caldavfeed

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDiscoveryAndUpcomingEvents(t *testing.T) {
	loc,err:=time.LoadLocation("Europe/Berlin");require.NoError(t,err);now:=time.Date(2026,9,14,12,0,0,0,loc)
	ts:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		user,pass,ok:=r.BasicAuth();require.True(t,ok);require.Equal(t,"alice",user);require.Equal(t,"secret",pass)
		w.Header().Set("Content-Type","application/xml; charset=utf-8")
		switch{
		case r.Method=="PROPFIND"&&r.URL.Path=="/caldav/":_,_=fmt.Fprint(w,`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:"><d:response><d:href>/caldav/</d:href><d:propstat><d:prop><d:current-user-principal><d:href>/principals/alice/</d:href></d:current-user-principal></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`)
		case r.Method=="PROPFIND"&&r.URL.Path=="/principals/alice/":_,_=fmt.Fprint(w,`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:response><d:href>/principals/alice/</d:href><d:propstat><d:prop><c:calendar-home-set><d:href>/calendars/alice/</d:href></c:calendar-home-set></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`)
		case r.Method=="PROPFIND"&&r.URL.Path=="/calendars/alice/":_,_=fmt.Fprint(w,`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:response><d:href>/calendars/alice/family/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/><c:calendar/></d:resourcetype><d:displayname>Family</d:displayname><c:calendar-description>Family calendar</c:calendar-description><c:supported-calendar-component-set><c:comp name="VEVENT"/></c:supported-calendar-component-set></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`)
		case r.Method=="REPORT"&&r.URL.Path=="/calendars/alice/family/":
			body:=`BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//PhotoFrame Test//EN
BEGIN:VEVENT
UID:evening@example
DTSTART:20260914T160000Z
DTEND:20260914T170000Z
SUMMARY:Evening appointment
END:VEVENT
BEGIN:VEVENT
UID:tomorrow@example
DTSTART:20260915T060000Z
DTEND:20260915T070000Z
SUMMARY:Tomorrow appointment
END:VEVENT
END:VCALENDAR
`
			xmlBody:=strings.NewReplacer("&","&amp;","<","&lt;",">","&gt;").Replace(body)
			_,_=fmt.Fprintf(w,`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:response><d:href>/calendars/alice/family/event.ics</d:href><d:propstat><d:prop><c:calendar-data>%s</c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`,xmlBody)
		default:http.Error(w,"unexpected request "+r.Method+" "+r.URL.Path,http.StatusBadRequest)
		}
	}));defer ts.Close()
	c:=NewClient();c.now=func()time.Time{return now};cfg:=Config{Endpoint:ts.URL+"/caldav/",Username:"alice",Password:"secret"}
	cals,err:=c.ListCalendars(cfg);require.NoError(t,err);require.Len(t,cals,1);require.Equal(t,"Family",cals[0].Summary);require.Equal(t,"/calendars/alice/family/",cals[0].ID)
	events,err:=c.GetUpcomingEvents(cfg,cals[0].ID,"Europe/Berlin",7,5);require.NoError(t,err);require.Len(t,events,2);require.Equal(t,"Evening appointment",events[0].Summary);require.Equal(t,18,events[0].Start.In(loc).Hour());require.Equal(t,"Tomorrow appointment",events[1].Summary)
}

func TestResolveSameOriginRejectsCredentialLeak(t *testing.T){base,err:=validateEndpoint("https://calendar.example.test/caldav/");require.NoError(t,err);_,err=resolveSameOrigin(base,"https://evil.example.test/calendar/");require.Error(t,err)}
