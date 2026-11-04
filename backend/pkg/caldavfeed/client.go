package caldavfeed

import (
	"context"
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"

	"github.com/aitjcize/esp32-photoframe-server/backend/pkg/gcalendar"
	"github.com/aitjcize/esp32-photoframe-server/backend/pkg/icalfeed"
)

const (
	defaultTimeout   = 15 * time.Second
	maxResponseBytes = 20 << 20
)

type Config struct {
	Endpoint      string
	Username      string
	Password      string
	SkipTLSVerify bool
}

type CalendarInfo struct {
	ID          string `json:"id"`
	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`
}

type Client struct { now func() time.Time }

func NewClient() *Client { return &Client{now: time.Now} }

func (c *Client) ListCalendars(cfg Config) ([]CalendarInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	base, err := validateEndpoint(cfg.Endpoint)
	if err != nil { return nil, err }

	principal := base
	if href, err := c.currentUserPrincipal(ctx, cfg, base); err == nil && href != "" {
		if principal, err = resolveSameOrigin(base, href); err != nil { return nil, err }
	}
	home := principal
	if href, err := c.calendarHomeSet(ctx, cfg, principal); err == nil && href != "" {
		if home, err = resolveSameOrigin(base, href); err != nil { return nil, err }
	}
	body := `<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:prop><d:resourcetype/><d:displayname/><c:calendar-description/><c:supported-calendar-component-set/></d:prop>
</d:propfind>`
	data, err := c.doXML(ctx, cfg, "PROPFIND", home, "1", body)
	if err != nil { return nil, fmt.Errorf("list CalDAV calendars: %w", err) }
	ms, err := parseMultiStatus(data)
	if err != nil { return nil, fmt.Errorf("parse CalDAV calendar list: %w", err) }
	out := make([]CalendarInfo,0)
	for _,resp:=range ms.Responses {
		prop,ok:=successfulProp(resp); if !ok || prop.ResourceType.Calendar==nil { continue }
		if len(prop.SupportedComponents.Components)>0 && !prop.SupportedComponents.supports("VEVENT"){continue}
		href:=strings.TrimSpace(resp.Href); if href==""{continue}
		resolved,err:=resolveSameOrigin(base,href); if err!=nil{continue}
		name:=strings.TrimSpace(prop.DisplayName); if name==""{name=fallbackCalendarName(resolved.Path)}
		out=append(out,CalendarInfo{ID:resolved.EscapedPath(),Summary:name,Description:strings.TrimSpace(prop.CalendarDescription)})
	}
	sort.SliceStable(out,func(i,j int)bool{return strings.ToLower(out[i].Summary)<strings.ToLower(out[j].Summary)})
	if len(out)==0{return nil,fmt.Errorf("no VEVENT calendars found at discovered CalDAV home set")}
	return out,nil
}

func (c *Client) GetUpcomingEvents(cfg Config, calendarPath, timezone string, lookaheadDays, limit int) ([]gcalendar.Event,error){
	base,err:=validateEndpoint(cfg.Endpoint); if err!=nil{return nil,err}
	if strings.TrimSpace(calendarPath)==""{return nil,fmt.Errorf("no CalDAV calendar selected")}
	target,err:=resolveSameOrigin(base,calendarPath); if err!=nil{return nil,fmt.Errorf("invalid CalDAV calendar path: %w",err)}
	loc:=c.now().Location(); if tz:=strings.TrimSpace(timezone);tz!=""{parsed,err:=time.LoadLocation(tz);if err!=nil{return nil,fmt.Errorf("invalid calendar timezone %q: %w",timezone,err)};loc=parsed}
	if lookaheadDays<1{lookaheadDays=7}; if limit<1{limit=5}
	now:=c.now().In(loc); windowStart:=time.Date(now.Year(),now.Month(),now.Day(),0,0,0,0,loc); windowEnd:=windowStart.AddDate(0,0,lookaheadDays)
	startUTC:=windowStart.UTC().Format("20060102T150405Z"); endUTC:=windowEnd.UTC().Format("20060102T150405Z")
	body:=fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:prop><d:getetag/><c:calendar-data/></d:prop>
  <c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VEVENT"><c:time-range start="%s" end="%s"/></c:comp-filter></c:comp-filter></c:filter>
</c:calendar-query>`,startUTC,endUTC)
	ctx,cancel:=context.WithTimeout(context.Background(),defaultTimeout);defer cancel()
	data,err:=c.doXML(ctx,cfg,"REPORT",target,"1",body);if err!=nil{return nil,fmt.Errorf("query CalDAV calendar: %w",err)}
	ms,err:=parseMultiStatus(data);if err!=nil{return nil,fmt.Errorf("parse CalDAV query response: %w",err)}
	calendars:=make([]*ics.Calendar,0,len(ms.Responses))
	for _,resp:=range ms.Responses{prop,ok:=successfulProp(resp);if !ok||strings.TrimSpace(prop.CalendarData)==""{continue};cal,err:=ics.ParseCalendar(strings.NewReader(prop.CalendarData));if err!=nil{continue};calendars=append(calendars,cal)}
	if len(calendars)==0{return []gcalendar.Event{},nil}
	return icalfeed.EventsForWindow(calendars,now,windowStart,windowEnd,limit)
}

func (c *Client) currentUserPrincipal(ctx context.Context,cfg Config,target *url.URL)(string,error){
	body:=`<?xml version="1.0" encoding="utf-8"?><d:propfind xmlns:d="DAV:"><d:prop><d:current-user-principal/></d:prop></d:propfind>`
	data,err:=c.doXML(ctx,cfg,"PROPFIND",target,"0",body);if err!=nil{return "",err};ms,err:=parseMultiStatus(data);if err!=nil{return "",err}
	for _,resp:=range ms.Responses{if prop,ok:=successfulProp(resp);ok&&strings.TrimSpace(prop.CurrentUserPrincipal.Href)!=""{return strings.TrimSpace(prop.CurrentUserPrincipal.Href),nil}}
	return "",fmt.Errorf("current-user-principal not advertised")
}

func (c *Client) calendarHomeSet(ctx context.Context,cfg Config,target *url.URL)(string,error){
	body:=`<?xml version="1.0" encoding="utf-8"?><d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><c:calendar-home-set/></d:prop></d:propfind>`
	data,err:=c.doXML(ctx,cfg,"PROPFIND",target,"0",body);if err!=nil{return "",err};ms,err:=parseMultiStatus(data);if err!=nil{return "",err}
	for _,resp:=range ms.Responses{if prop,ok:=successfulProp(resp);ok&&strings.TrimSpace(prop.CalendarHomeSet.Href)!=""{return strings.TrimSpace(prop.CalendarHomeSet.Href),nil}}
	return "",fmt.Errorf("calendar-home-set not advertised")
}

func (c *Client) doXML(ctx context.Context,cfg Config,method string,target *url.URL,depth,body string)([]byte,error){
	httpClient:=&http.Client{Timeout:defaultTimeout}
	if target.Scheme=="https"&&cfg.SkipTLSVerify{transport:=http.DefaultTransport.(*http.Transport).Clone();transport.TLSClientConfig=&tls.Config{InsecureSkipVerify:true};httpClient.Transport=transport}
	req,err:=http.NewRequestWithContext(ctx,method,target.String(),strings.NewReader(body));if err!=nil{return nil,err}
	req.Header.Set("Content-Type","application/xml; charset=utf-8");req.Header.Set("Accept","application/xml, text/xml;q=0.9, */*;q=0.1");req.Header.Set("User-Agent","esp32-photoframe-server-caldav/1.4")
	if depth!=""{req.Header.Set("Depth",depth)};if cfg.Username!=""||cfg.Password!=""{req.SetBasicAuth(cfg.Username,cfg.Password)}
	resp,err:=httpClient.Do(req);if err!=nil{return nil,err};defer resp.Body.Close()
	data,err:=io.ReadAll(io.LimitReader(resp.Body,maxResponseBytes+1));if err!=nil{return nil,err};if len(data)>maxResponseBytes{return nil,fmt.Errorf("CalDAV response is larger than %d bytes",maxResponseBytes)}
	if resp.StatusCode<200||resp.StatusCode>=300{msg:=strings.TrimSpace(string(data));if len(msg)>500{msg=msg[:500]+"…"};return nil,fmt.Errorf("HTTP %d%s",resp.StatusCode,formatServerMessage(msg))}
	return data,nil
}

func validateEndpoint(raw string)(*url.URL,error){u,err:=url.Parse(strings.TrimSpace(raw));if err!=nil{return nil,fmt.Errorf("invalid CalDAV URL: %w",err)};if u.Scheme!="http"&&u.Scheme!="https"{return nil,fmt.Errorf("CalDAV URL must use http or https")};if u.Host==""{return nil,fmt.Errorf("CalDAV URL has no host")};u.Fragment="";return u,nil}
func resolveSameOrigin(base *url.URL,href string)(*url.URL,error){ref,err:=url.Parse(strings.TrimSpace(href));if err!=nil{return nil,err};resolved:=base.ResolveReference(ref);if !strings.EqualFold(resolved.Host,base.Host){return nil,fmt.Errorf("CalDAV server returned cross-origin href %q",href)};if base.Scheme=="https"&&resolved.Scheme!="https"{return nil,fmt.Errorf("CalDAV server attempted HTTPS downgrade")};resolved.Fragment="";return resolved,nil}
func fallbackCalendarName(p string)string{clean:=strings.TrimSuffix(p,"/");name:=path.Base(clean);if decoded,err:=url.PathUnescape(name);err==nil{name=decoded};if strings.TrimSpace(name)==""||name=="."||name=="/"{return "Calendar"};return name}
func formatServerMessage(msg string)string{if msg==""{return ""};return ": "+msg}

type multiStatus struct{Responses []davResponse `xml:"response"`}
type davResponse struct{Href string `xml:"href"`;PropStats []davPropStat `xml:"propstat"`}
type davPropStat struct{Prop davProp `xml:"prop"`;Status string `xml:"status"`}
type hrefProp struct{Href string `xml:"href"`}
type resourceType struct{Calendar *struct{} `xml:"calendar"`;Collection *struct{} `xml:"collection"`}
type supportedComponentSet struct{Components []componentName `xml:"comp"`}
type componentName struct{Name string `xml:"name,attr"`}
func (s supportedComponentSet)supports(name string)bool{for _,comp:=range s.Components{if strings.EqualFold(strings.TrimSpace(comp.Name),name){return true}};return false}
type davProp struct{CurrentUserPrincipal hrefProp `xml:"current-user-principal"`;CalendarHomeSet hrefProp `xml:"calendar-home-set"`;ResourceType resourceType `xml:"resourcetype"`;DisplayName string `xml:"displayname"`;CalendarDescription string `xml:"calendar-description"`;SupportedComponents supportedComponentSet `xml:"supported-calendar-component-set"`;CalendarData string `xml:"calendar-data"`}
func parseMultiStatus(data []byte)(*multiStatus,error){var ms multiStatus;if err:=xml.Unmarshal(data,&ms);err!=nil{return nil,err};return &ms,nil}
func successfulProp(resp davResponse)(davProp,bool){for _,ps:=range resp.PropStats{status:=strings.ToUpper(strings.TrimSpace(ps.Status));if strings.Contains(status," 200 ")||strings.HasSuffix(status," 200 OK"){return ps.Prop,true}};return davProp{},false}
