package schedule

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	errs "github.com/vanyayudin26/omgmu_parser/errors"
	"github.com/vanyayudin26/omgmu_parser/model"
)

const (
	// BaseURL — корень сайта колледжа ОмГМУ.
	BaseURL   = "https://omsk-osma.ru"
	indexPath = "/shedule_kolledzh"
	groupPath = "/shedule_kolledzh/"
)

// Adapter — общий интерфейс источника расписания (группа/преподаватель).
type Adapter interface {
	GetSchedule(ctx context.Context, value, date string) ([]model.Schedule, error)
	GetOptions(ctx context.Context) ([]model.Option, error)
}

// Cell — одна пара в таблице (до раскрытия подгрупп).
type Cell struct {
	Time     string   `json:"time"`
	Subject  string   `json:"subject"`
	Teachers []string `json:"teachers"`
	Rooms    []string `json:"rooms"`
}

// Day — учебный день (промежуточное представление, кэшируется в Redis).
type Day struct {
	WD    int    `json:"wd"` // Пн=1 … Вс=7
	Name  string `json:"name"`
	Date  string `json:"date"` // "dd/mm"
	Cells []Cell `json:"cells"`
}

var (
	brRe    = regexp.MustCompile(`(?i)<br\s*/?>`)
	tagRe   = regexp.MustCompile(`<[^>]*>`)
	spaceRe = regexp.MustCompile(`\s+`)
	wdIndex = map[string]int{"Пн": 1, "Вт": 2, "Ср": 3, "Чт": 4, "Пт": 5, "Сб": 6, "Вс": 7}


	weekdayRU = map[time.Weekday]string{
		time.Monday: "Понедельник", time.Tuesday: "Вторник", time.Wednesday: "Среда",
		time.Thursday: "Четверг", time.Friday: "Пятница", time.Saturday: "Суббота", time.Sunday: "Воскресенье",
	}
	monthRU = []string{"", "января", "февраля", "марта", "апреля", "мая", "июня",
		"июля", "августа", "сентября", "октября", "ноября", "декабря"}
)

var client = &http.Client{Timeout: 15 * time.Second}

// IndexURL — адрес страницы со списком групп.
func IndexURL() string { return BaseURL + indexPath }

// GroupURL — адрес страницы расписания группы (с кодированием кириллицы).
func GroupURL(name string) string { return BaseURL + groupPath + url.PathEscape(name) }

// Fetch загружает страницу и возвращает разобранный документ.
func Fetch(ctx context.Context, rawurl string) (*goquery.Document, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; omgmu-schedule/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s", errs.ErrorBadResponse, resp.Status)
	}
	return goquery.NewDocumentFromReader(resp.Body)
}

// GroupName достаёт код группы из заголовка страницы ("Группа 402Ф" -> "402Ф").
func GroupName(doc *goquery.Document) string {
	return clean(strings.TrimPrefix(clean(doc.Find("h1").First().Text()), "Группа "))
}

// ParseOptions разбирает список групп с индексной страницы.
func ParseOptions(doc *goquery.Document) []model.Option {
	seen := map[string]bool{}
	var out []model.Option
	doc.Find("a[href*='" + groupPath + "']").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		if idx := strings.LastIndex(href, "/"); idx < 0 || idx == len(href)-1 {
			return
		}
		val := clean(a.Text())
		if val == "" || seen[val] {
			return
		}
		seen[val] = true
		out = append(out, model.Option{Label: val, Value: val})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

// ParseDays разбирает таблицу расписания в список дней (все недели подряд).
func ParseDays(doc *goquery.Document) []Day {
	var days []Day
	cur := -1
	doc.Find("table.rasp_table tr").Each(func(_ int, tr *goquery.Selection) {
		hour := tr.Find("td.hour")
		if hour.Length() == 0 {
			return
		}
		if !tr.ChildrenFiltered("td").First().HasClass("hour") {
			td := tr.ChildrenFiltered("td").First()
			html, _ := td.Html()
			parts := brRe.Split(html, 2)
			name := clean(stripTags(parts[0]))
			date := ""
			if len(parts) > 1 {
				date = clean(stripTags(parts[1]))
			}
			days = append(days, Day{WD: wdIndex[name], Name: name, Date: date})
			cur = len(days) - 1
		}
		if cur < 0 {
			return
		}
		box := tr.Find("div.cell").First()
		divs := box.ChildrenFiltered("div")
		days[cur].Cells = append(days[cur].Cells, Cell{
			Time:     splitTime(hour.Text()),
			Subject:  clean(divs.Eq(0).Text()),
			Teachers: splitBR(divs.Eq(1)),
			Rooms:    splitBR(divs.Eq(2)),
		})
	})
	return days
}

// ExpandCell раскрывает пару в один или несколько Lesson (по числу подгрупп).
func ExpandCell(c Cell, num int, group string) []model.Lesson {
	n := strconv.Itoa(num)
	if len(c.Teachers) <= 1 {
		return []model.Lesson{{
			Num: n, Time: c.Time, Name: c.Subject,
			Room: At(c.Rooms, 0), Group: group, Teacher: At(c.Teachers, 0),
		}}
	}
	out := make([]model.Lesson, 0, len(c.Teachers))
	for i := range c.Teachers {
		out = append(out, model.Lesson{
			Num: n, Time: c.Time, Name: c.Subject,
			Room: At(c.Rooms, i), Group: group,
			Subgroup: strconv.Itoa(i + 1), Teacher: c.Teachers[i],
		})
	}
	return out
}

// Assemble строит ровно 7 дней (Пн..Вс) недели, содержащей date,
// подставляя пары из byDay (ключ "dd/mm"). Пустой день -> Lessons=nil ("lesson":null).
// Формат вывода повторяет оригинал: фиксированная неделя, человекочитаемая дата, href на источник.
func Assemble(date string, byDay map[string][]model.Lesson, href string) []model.Schedule {
	monday := mondayOf(parseDate(date))
	out := make([]model.Schedule, 0, 7)
	for i := 0; i < 7; i++ {
		d := monday.AddDate(0, 0, i)
		out = append(out, model.Schedule{
			Date:    humanDate(d),
			Href:    href,
			Lessons: byDay[d.Format("02/01")],
		})
	}
	return out
}

// At безопасно возвращает элемент среза по индексу.
func At(s []string, i int) string {
	if i >= 0 && i < len(s) {
		return s[i]
	}
	return ""
}

func parseDate(date string) time.Time {
	if t, err := time.Parse("02.01.2006", strings.TrimSpace(date)); err == nil {
		return t
	}
	return time.Now()
}

func mondayOf(t time.Time) time.Time {
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7 // воскресенье
	}
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location()).AddDate(0, 0, -(wd - 1))
}

func humanDate(t time.Time) string {
	return fmt.Sprintf("%s, %d %s %d", weekdayRU[t.Weekday()], t.Day(), monthRU[int(t.Month())], t.Year())
}

func clean(s string) string     { return strings.TrimSpace(spaceRe.ReplaceAllString(s, " ")) }
func stripTags(s string) string { return tagRe.ReplaceAllString(s, "") }

func splitBR(s *goquery.Selection) []string {
	html, _ := s.Html()
	var out []string
	for _, part := range brRe.Split(html, -1) {
		if p := clean(stripTags(part)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitTime приводит "08:00-11:20" к формату оригинала "08:00\n11:20":
// на сайте ХМТПК время лежало в ячейке двумя строками, фронт рисует его как есть.
func splitTime(s string) string {
	s = clean(s)
	if i := strings.Index(s, "-"); i >= 0 {
		return strings.TrimSpace(s[:i]) + "\n" + strings.TrimSpace(s[i+1:])
	}
	return s
}
