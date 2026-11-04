package icalfeed

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"
	"github.com/teambition/rrule-go"

	"github.com/aitjcize/esp32-photoframe-server/backend/pkg/gcalendar"
)

const (
	defaultTimeout = 10 * time.Second
	maxFeedBytes   = 5 << 20
	maxEvents      = 10
)

type Client struct {
	httpClient *http.Client
	now        func() time.Time
}

func NewClient() *Client {
	return &Client{httpClient: &http.Client{Timeout: defaultTimeout}, now: time.Now}
}

func (c *Client) GetTodayEvents(feedURL, timezone string) ([]gcalendar.Event, error) {
	return c.GetUpcomingEvents(feedURL, timezone, 1, maxEvents)
}

func (c *Client) GetUpcomingEvents(feedURL, timezone string, lookaheadDays, limit int) ([]gcalendar.Event, error) {
	loc := c.now().Location()
	if strings.TrimSpace(timezone) != "" {
		parsed, err := time.LoadLocation(strings.TrimSpace(timezone))
		if err != nil {
			return nil, fmt.Errorf("invalid iCal timezone %q: %w", timezone, err)
		}
		loc = parsed
	}
	if lookaheadDays < 1 { lookaheadDays = 1 }
	if limit < 1 { limit = maxEvents }
	now := c.now().In(loc)
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	windowEnd := startOfDay.AddDate(0, 0, lookaheadDays)
	cal, err := c.fetch(feedURL)
	if err != nil { return nil, err }
	return EventsForWindow([]*ics.Calendar{cal}, now, startOfDay, windowEnd, limit)
}

func EventsForWindow(calendars []*ics.Calendar, now, windowStart, windowEnd time.Time, limit int) ([]gcalendar.Event, error) {
	if limit < 1 { limit = maxEvents }
	out := make([]gcalendar.Event, 0)
	for _, cal := range calendars {
		if cal == nil { continue }
		events, err := expandCalendar(cal, now, windowStart, windowEnd)
		if err != nil { return nil, err }
		out = append(out, events...)
	}
	sort.SliceStable(out, func(i,j int) bool { return out[i].Start.Before(out[j].Start) })
	if len(out) > limit { out = out[:limit] }
	return out, nil
}

func (c *Client) fetch(feedURL string) (*ics.Calendar, error) {
	u, err := url.Parse(strings.TrimSpace(feedURL))
	if err != nil { return nil, fmt.Errorf("invalid iCal URL: %w", err) }
	if u.Scheme != "http" && u.Scheme != "https" { return nil, fmt.Errorf("iCal URL must use http or https") }
	if u.Host == "" { return nil, fmt.Errorf("iCal URL has no host") }
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil { return nil, fmt.Errorf("create iCal request: %w", err) }
	req.Header.Set("Accept", "text/calendar, text/plain;q=0.9, */*;q=0.1")
	req.Header.Set("User-Agent", "esp32-photoframe-server-ical/1")
	resp, err := c.httpClient.Do(req)
	if err != nil { return nil, fmt.Errorf("fetch iCal feed: %w", err) }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return nil, fmt.Errorf("iCal feed returned HTTP %d", resp.StatusCode) }
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil { return nil, fmt.Errorf("read iCal feed: %w", err) }
	if len(body) > maxFeedBytes { return nil, fmt.Errorf("iCal feed is larger than %d bytes", maxFeedBytes) }
	cal, err := ics.ParseCalendar(bytes.NewReader(body))
	if err != nil { return nil, fmt.Errorf("parse iCal feed: %w", err) }
	return cal, nil
}

type overrideKey struct { uid string; start int64 }
type parsedEvent struct {
	uid, summary string
	start, end time.Time
	allDay, cancel bool
	rid *time.Time
	rules, exRules []*ics.RecurrenceRule
	rdates, exdates []time.Time
}

func expandCalendar(cal *ics.Calendar, now, windowStart, windowEnd time.Time) ([]gcalendar.Event, error) {
	masters := make([]parsedEvent,0)
	overrides := map[overrideKey]parsedEvent{}
	for _, ve := range cal.Events() {
		p, err := parseVEvent(ve); if err != nil { continue }
		if p.rid != nil { overrides[overrideKey{p.uid,p.rid.Unix()}]=p; continue }
		masters=append(masters,p)
	}
	out:=make([]gcalendar.Event,0); seen:=map[string]struct{}{}
	for _, master := range masters {
		for _, occ := range occurrences(master,windowStart,windowEnd) {
			current:=master; occEnd:=occ.Add(master.end.Sub(master.start))
			if ov,ok:=overrides[overrideKey{master.uid,occ.Unix()}]; ok {
				if ov.cancel { continue }; current=ov; occ=ov.start; occEnd=ov.end
			}
			if current.cancel || !overlaps(occ,occEnd,windowStart,windowEnd) || (!current.allDay && !occEnd.After(now)) { continue }
			key:=fmt.Sprintf("%s|%d",current.uid,occ.UnixNano()); if _,ok:=seen[key];ok{continue}; seen[key]=struct{}{}
			out=append(out,gcalendar.Event{Summary:current.summary,Start:occ,End:occEnd,AllDay:current.allDay})
		}
	}
	masterUIDs:=map[string]struct{}{}; for _,m:=range masters{masterUIDs[m.uid]=struct{}{}}
	for _,ov:=range overrides{
		if _,ok:=masterUIDs[ov.uid];ok||ov.cancel{continue}
		if overlaps(ov.start,ov.end,windowStart,windowEnd)&&(ov.allDay||ov.end.After(now)){out=append(out,gcalendar.Event{Summary:ov.summary,Start:ov.start,End:ov.end,AllDay:ov.allDay})}
	}
	sort.SliceStable(out,func(i,j int)bool{return out[i].Start.Before(out[j].Start)})
	return out,nil
}

func parseVEvent(ve *ics.VEvent)(parsedEvent,error){
	start,err:=ve.GetStartAt(); if err!=nil{return parsedEvent{},fmt.Errorf("VEVENT %q has no valid DTSTART: %w",ve.Id(),err)}
	allDay:=false; if prop:=ve.GetProperty(ics.ComponentPropertyDtStart);prop!=nil{allDay=prop.GetValueType()==ics.ValueDataTypeDate}
	end,err:=ve.GetEndAt(); if err!=nil||!end.After(start){end=defaultEnd(ve,start,allDay)}
	summary:="(untitled)"; if prop:=ve.GetProperty(ics.ComponentPropertySummary);prop!=nil&&strings.TrimSpace(prop.Value)!=""{summary=ics.ToText(prop.Value)}
	cancel:=false; if prop:=ve.GetProperty(ics.ComponentPropertyStatus);prop!=nil{cancel=strings.EqualFold(strings.TrimSpace(prop.Value),"CANCELLED")}
	var rid *time.Time; if ve.HasProperty(ics.ComponentPropertyRecurrenceId){if t,err:=ve.GetRecurrenceID();err==nil{rid=&t}}
	rules,_:=ve.GetRRules(); exRules,_:=ve.GetExRules(); rdates,_:=ve.GetRDates(); exdates,_:=ve.GetExDates()
	return parsedEvent{ve.Id(),summary,start,end,allDay,cancel,rid,rules,exRules,rdates,exdates},nil
}

func defaultEnd(ve *ics.VEvent,start time.Time,allDay bool)time.Time{
	if p:=ve.GetProperty(ics.ComponentPropertyDuration);p!=nil{if d,err:=parseDuration(strings.TrimSpace(p.Value));err==nil&&d>0{return start.Add(d)}}
	if allDay{return start.AddDate(0,0,1)}; return start.Add(time.Hour)
}

func occurrences(ev parsedEvent,windowStart,windowEnd time.Time)[]time.Time{
	duration:=ev.end.Sub(ev.start); searchStart:=windowStart.Add(-duration); if duration<=0{searchStart=windowStart}
	if len(ev.rules)==0&&len(ev.exRules)==0&&len(ev.rdates)==0&&len(ev.exdates)==0{return []time.Time{ev.start}}
	set:=&rrule.Set{}; if len(ev.rules)==0{set.RDate(ev.start)}
	for _,rule:=range ev.rules{rr,err:=rrule.StrToRRule(rule.String());if err!=nil{continue};rr.DTStart(ev.start);set.RRule(rr)}
	for _,rd:=range ev.rdates{set.RDate(rd)}; for _,ex:=range ev.exdates{set.ExDate(ex)}
	occs:=set.Between(searchStart,windowEnd,true); if len(ev.exRules)==0{return occs}
	excluded:=map[int64]struct{}{}; for _,rule:=range ev.exRules{rr,err:=rrule.StrToRRule(rule.String());if err!=nil{continue};rr.DTStart(ev.start);for _,x:=range rr.Between(searchStart,windowEnd,true){excluded[x.Unix()]=struct{}{}}}
	filtered:=occs[:0]; for _,o:=range occs{if _,skip:=excluded[o.Unix()];skip{continue};filtered=append(filtered,o)}; return filtered
}

func overlaps(start,end,windowStart,windowEnd time.Time)bool{return start.Before(windowEnd)&&end.After(windowStart)}

func parseDuration(s string)(time.Duration,error){
	if s==""{return 0,fmt.Errorf("empty duration")}; sign:=time.Duration(1); if s[0]=='-'{sign=-1;s=s[1:]}else if s[0]=='+'{s=s[1:]}
	if len(s)<2||s[0]!='P'{return 0,fmt.Errorf("invalid duration")}; s=s[1:]; var total time.Duration; var num int64; var have bool; inTime:=false
	for _,r:=range s{if r>='0'&&r<='9'{num=num*10+int64(r-'0');have=true;continue};if r=='T'{inTime=true;continue};if !have{return 0,fmt.Errorf("invalid duration")}
		switch r{case 'W':total+=time.Duration(num)*7*24*time.Hour;case 'D':total+=time.Duration(num)*24*time.Hour;case 'H':if !inTime{return 0,fmt.Errorf("invalid duration")};total+=time.Duration(num)*time.Hour;case 'M':if !inTime{return 0,fmt.Errorf("invalid duration")};total+=time.Duration(num)*time.Minute;case 'S':if !inTime{return 0,fmt.Errorf("invalid duration")};total+=time.Duration(num)*time.Second;default:return 0,fmt.Errorf("invalid duration unit %q",r)};num=0;have=false}
	if have{return 0,fmt.Errorf("invalid duration")}; return sign*total,nil
}
